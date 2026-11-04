// Package plan builds the trading plan for the next session, for oracle recommend and the API.
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

type Options struct {
	Capital float64 // BRL available
	Top     int     // each stock gets up to Capital/Top
	Risk    float64 // max loss per trade, % of Capital
	Fill    bool    // try weaker tiers until the plan is full
}

type Row struct {
	swing.Pick
	Name string
	Tier string
}

// Summary is how a tier did across every stock of the latest session.
type Summary struct {
	Tier       swing.Tier
	Candidates int // eligible today
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

// Build tries the tiers from strongest to weakest, stopping at the first unless Fill is set.
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

func Tiers(history map[string][]quote.Quote, latest time.Time, params swing.Params) []Summary {
	summaries := make([]Summary, 0, len(swing.Tiers))
	for _, tier := range swing.Tiers {
		params.Oversold = tier.Oversold
		_, s := analyzeTier(history, latest, params, tier)
		summaries = append(summaries, s)
	}
	return summaries
}

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

type Estimator interface {
	Estimate(ctx context.Context, ticker string) (yahoo.Estimate, error)
}

// Estimates caches analyst targets, asking for each ticker once. Without a client the plan ranks
// by history only. Requests for a ticker already being fetched wait for that answer, so plans
// built at the same time agree.
type Estimates struct {
	client Estimator

	mu       sync.Mutex
	byTicker map[string]yahoo.Estimate
	failed   map[string]error
	inFlight map[string]chan struct{}
}

var ErrDisabled = errors.New("analyst estimates are disabled")

func NewEstimates(client Estimator) *Estimates {
	return &Estimates{
		client:   client,
		byTicker: make(map[string]yahoo.Estimate),
		failed:   make(map[string]error),
		inFlight: make(map[string]chan struct{}),
	}
}

func (e *Estimates) Enabled() bool { return e.client != nil }

// Fetch asks for the tickers not known yet. Failures are not retried, except when ctx ends first.
func (e *Estimates) Fetch(ctx context.Context, tickers []string) {
	if e.client == nil {
		return
	}
	for _, ticker := range tickers {
		if !e.fetch(ctx, ticker) {
			return
		}
	}
}

// fetch reports false when ctx ended first.
func (e *Estimates) fetch(ctx context.Context, ticker string) bool {
	for {
		known, wait, mine := e.claim(ticker)
		switch {
		case known:
			return true
		case !mine:
			select {
			case <-wait: // the other request may have given up
				continue
			case <-ctx.Done():
				return false
			}
		}

		estimate, err := e.client.Estimate(ctx, ticker)
		if err != nil && ctx.Err() != nil {
			e.finish(ticker, func() {})
			return false
		}
		if err != nil && !errors.Is(err, yahoo.ErrNoCoverage) {
			log.Printf("%s: estimate skipped: %v", ticker, err)
		}
		e.finish(ticker, func() {
			if err == nil {
				e.byTicker[ticker] = estimate
			} else {
				e.failed[ticker] = err
			}
		})
		return true
	}
}

func (e *Estimates) Get(ctx context.Context, ticker string) (yahoo.Estimate, error) {
	if e.client == nil {
		return yahoo.Estimate{}, ErrDisabled
	}
	if !e.fetch(ctx, ticker) {
		return yahoo.Estimate{}, ctx.Err()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if estimate, ok := e.byTicker[ticker]; ok {
		return estimate, nil
	}
	return yahoo.Estimate{}, e.failed[ticker]
}

func (e *Estimates) All() map[string]yahoo.Estimate {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.byTicker)
}

func (e *Estimates) claim(ticker string) (known bool, wait chan struct{}, mine bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.byTicker[ticker]; ok {
		return true, nil, false
	}
	if _, ok := e.failed[ticker]; ok {
		return true, nil, false
	}
	if ch, ok := e.inFlight[ticker]; ok {
		return false, ch, false
	}
	e.inFlight[ticker] = make(chan struct{})
	return false, nil, true
}

func (e *Estimates) finish(ticker string, record func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	record()
	close(e.inFlight[ticker])
	delete(e.inFlight, ticker)
}
