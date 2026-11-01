// Package plan builds the trading plan for the next session: the stocks to buy, how many and at
// what prices. oracle recommend prints it and the API serves it, from the same functions.
package plan

import (
	"context"
	"errors"
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
)

// Options size the plan.
type Options struct {
	Capital float64 // BRL available
	Top     int     // stocks in the plan, each getting up to Capital/Top
	Risk    float64 // max loss per trade, in percent of Capital
	Fill    bool    // go down the tiers until the plan has Top stocks
}

// Row is one stock of the plan.
type Row struct {
	swing.Pick
	Name string
	Tier string // label of the tier that picked it
}

// Summary is how one tier performed across every stock on the latest session, before any selection.
type Summary struct {
	Tier       swing.Tier
	Candidates int // eligible stocks on the latest session
	Trades     int
	Wins       int
	SumReturn  float64
}

func (s Summary) WinRate() float64 {
	if s.Trades == 0 {
		return 0
	}
	return float64(s.Wins) / float64(s.Trades)
}

func (s Summary) AvgReturn() float64 {
	if s.Trades == 0 {
		return 0
	}
	return s.SumReturn / float64(s.Trades)
}

// Build tries the tiers from strongest to weakest. It stops at the first tier,
// unless Fill is set and the plan is still short.
func Build(ctx context.Context, history map[string][]quote.Quote, latest time.Time, params swing.Params, estimates *Estimates, opts Options) ([]Row, []Summary) {
	var rows []Row
	var summaries []Summary
	taken := make(map[string]bool)

	for _, tier := range swing.Tiers {
		params.Oversold = tier.Oversold
		analyses, s := analyzeTier(history, latest, params, tier)
		summaries = append(summaries, s)

		estimates.Fetch(ctx, eligibleTickers(analyses, params))
		for _, pick := range swing.Rank(analyses, estimates.All(), params, opts.Capital, opts.Risk, opts.Top) {
			if len(rows) == opts.Top {
				break
			}
			if taken[pick.Ticker] {
				continue
			}
			taken[pick.Ticker] = true
			bars := history[pick.Ticker]
			rows = append(rows, Row{Pick: pick, Name: bars[len(bars)-1].Name, Tier: tier.Label})
		}
		if len(rows) == opts.Top || !opts.Fill {
			break
		}
	}
	return rows, summaries
}

// Tiers summarizes every tier on the latest session, without building a plan.
func Tiers(history map[string][]quote.Quote, latest time.Time, params swing.Params) []Summary {
	summaries := make([]Summary, 0, len(swing.Tiers))
	for _, tier := range swing.Tiers {
		params.Oversold = tier.Oversold
		_, s := analyzeTier(history, latest, params, tier)
		summaries = append(summaries, s)
	}
	return summaries
}

// analyzeTier runs the rule on every stock traded on the latest session.
func analyzeTier(history map[string][]quote.Quote, latest time.Time, params swing.Params, tier swing.Tier) ([]swing.Analysis, Summary) {
	s := Summary{Tier: tier}
	analyses := swing.Scan(history, latest, params)
	for _, a := range analyses {
		s.Trades += a.Trades
		s.Wins += a.Wins
		s.SumReturn += a.SumReturn
		if a.Eligible(params) {
			s.Candidates++
		}
	}
	return analyses, s
}

func eligibleTickers(analyses []swing.Analysis, params swing.Params) []string {
	var tickers []string
	for _, a := range analyses {
		if a.Eligible(params) {
			tickers = append(tickers, a.Ticker)
		}
	}
	slices.Sort(tickers)
	return tickers
}

// Estimator gives analyst targets; *yahoo.Client in production.
type Estimator interface {
	Estimate(ctx context.Context, ticker string) (yahoo.Estimate, error)
}

// Estimates caches the analyst targets of one session, asking for each ticker once.
// Without a client it has no estimates and the plan ranks by history only.
// It is safe for concurrent use; the client is called outside the lock.
type Estimates struct {
	client Estimator

	mu       sync.Mutex
	byTicker map[string]yahoo.Estimate
	failed   map[string]error // yahoo.ErrNoCoverage or why the request failed
	tried    map[string]bool  // fetched or being fetched
}

var (
	// ErrDisabled is returned by Get when there is no client to ask.
	ErrDisabled = errors.New("analyst estimates are disabled")
	// ErrPending is returned by Get while another request is still fetching the ticker.
	ErrPending = errors.New("estimate still being fetched")
)

func NewEstimates(client Estimator) *Estimates {
	return &Estimates{
		client:   client,
		byTicker: make(map[string]yahoo.Estimate),
		failed:   make(map[string]error),
		tried:    make(map[string]bool),
	}
}

// Enabled reports whether there is a client to ask.
func (e *Estimates) Enabled() bool { return e.client != nil }

// Fetch asks the client for the tickers not tried yet. A ticker without coverage or whose request
// failed is not asked again, except when ctx ended first: then it is tried on the next Fetch.
func (e *Estimates) Fetch(ctx context.Context, tickers []string) {
	if e.client == nil {
		return
	}
	for _, ticker := range tickers {
		if !e.claim(ticker) {
			continue
		}
		estimate, err := e.client.Estimate(ctx, ticker)
		if err != nil && ctx.Err() != nil {
			e.release(ticker)
			return
		}
		if err != nil && !errors.Is(err, yahoo.ErrNoCoverage) {
			log.Printf("%s: estimate skipped: %v", ticker, err)
		}
		e.mu.Lock()
		if err == nil {
			e.byTicker[ticker] = estimate
		} else {
			e.failed[ticker] = err
		}
		e.mu.Unlock()
	}
}

// Get is the estimate of one ticker, fetching it if it was not tried yet. The error is
// ErrDisabled, yahoo.ErrNoCoverage, why the request failed, ErrPending, or ctx's error when it ended first.
func (e *Estimates) Get(ctx context.Context, ticker string) (yahoo.Estimate, error) {
	if e.client == nil {
		return yahoo.Estimate{}, ErrDisabled
	}
	e.Fetch(ctx, []string{ticker})
	if err := ctx.Err(); err != nil {
		return yahoo.Estimate{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if estimate, ok := e.byTicker[ticker]; ok {
		return estimate, nil
	}
	if err, ok := e.failed[ticker]; ok {
		return yahoo.Estimate{}, err
	}
	return yahoo.Estimate{}, ErrPending
}

// All is a copy of the estimates fetched so far.
func (e *Estimates) All() map[string]yahoo.Estimate {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.byTicker)
}

func (e *Estimates) claim(ticker string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.tried[ticker] {
		return false
	}
	e.tried[ticker] = true
	return true
}

func (e *Estimates) release(ticker string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.tried, ticker)
}
