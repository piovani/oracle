package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"oracle/internal/plan"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
)

const (
	// estimatesBudget bounds the analyst requests of one API call, inside the server's write timeout.
	// Tickers left out are fetched on a later call.
	estimatesBudget = 5 * time.Second
	// reconnectAfter is how long to wait for a new Yahoo session after one failed.
	reconnectAfter = 10 * time.Minute
)

// Connect opens a client for analyst targets; yahoo.NewClient in production.
type Connect func(ctx context.Context) (plan.Estimator, error)

// Plan serves the trading plan of oracle recommend, built by the same functions.
//
// Analyst targets are cached for the latest session: a new session opens a new Yahoo client
// and starts an empty cache, so a long-running server neither reconnects on every call nor
// keeps an expired session or old targets.
type Plan struct {
	store   StrategyReader
	now     func() time.Time
	connect Connect // nil: rank by history only

	mu        sync.Mutex
	session   time.Time
	estimates *plan.Estimates
	retryAt   time.Time
}

// NewPlan serves the plan from store; connect opens the analyst client, nil to go without it.
func NewPlan(store StrategyReader, now func() time.Time, connect Connect) *Plan {
	return &Plan{store: store, now: now, connect: connect}
}

type estimateResponse struct {
	Target   float64  `json:"target" example:"44.5"` // consensus price target, BRL
	Rating   *float64 `json:"rating" example:"2.1"`  // consensus from 1 (strong buy) to 5 (sell), null when Yahoo has none
	Analysts int      `json:"analysts" example:"12"` // analysts behind the target
} //	@name	AnalystEstimate

type pickResponse struct {
	Ticker     string            `json:"ticker" example:"PETR4"`
	Name       string            `json:"name" example:"PETROBRAS"`
	Tier       string            `json:"tier" example:"forte"`        // tier that picked it
	Qty        int               `json:"qty" example:"2"`             // shares to buy, in the odd-lot market
	MaxEntry   float64           `json:"max_entry" example:"37.71"`   // limit buy price, BRL
	Target     float64           `json:"target" example:"39.18"`      // sell order, BRL
	Stop       float64           `json:"stop" example:"36.24"`        // stop, BRL
	Invested   float64           `json:"invested" example:"75.42"`    // qty × max_entry, BRL
	Gain       float64           `json:"gain" example:"2.94"`         // at the target, BRL
	Loss       float64           `json:"loss" example:"2.94"`         // at the stop, BRL
	Trades     int               `json:"trades" example:"12"`         // trades in the track record
	WinRate    float64           `json:"win_rate" example:"0.75"`     // 0 to 1
	AvgReturn  float64           `json:"avg_return" example:"0.0123"` // per trade, net of costs (0.01 = 1%)
	Expectancy float64           `json:"expectancy" example:"0.0067"` // what the ranking uses
	Estimate   *estimateResponse `json:"estimate"`                    // analyst target, null without coverage or estimates
	Upside     *float64          `json:"upside" example:"0.165"`      // estimate.target / close - 1, null without an estimate
} //	@name	Pick

type tierSummaryResponse struct {
	Label      string  `json:"label" example:"forte"`
	Rule       string  `json:"rule" example:"RSI(2) < 10"`
	Candidates int     `json:"candidates" example:"3"`      // eligible stocks on the latest session
	Trades     int     `json:"trades" example:"1840"`       // trades of every stock on the latest session, before selection
	WinRate    float64 `json:"win_rate" example:"0.68"`     // 0 to 1
	AvgReturn  float64 `json:"avg_return" example:"0.0041"` // per trade, net of costs (0.01 = 1%)
} //	@name	TierSummary

type planResponse struct {
	Session          string                `json:"session" example:"2026-10-30"` // latest session, the plan is for the next one
	Stale            bool                  `json:"stale" example:"false"`        // the session is more than 4 days old: a sync is missing
	Capital          float64               `json:"capital" example:"500"`
	Top              int                   `json:"top" example:"5"`
	Risk             float64               `json:"risk" example:"2"`                 // max loss per trade, percent of capital
	PerStock         float64               `json:"per_stock" example:"100"`          // capital / top, BRL
	MaxLoss          float64               `json:"max_loss" example:"10"`            // capital × risk / 100, BRL
	Invested         float64               `json:"invested" example:"275.4"`         // sum of the picks, BRL
	Cash             float64               `json:"cash" example:"224.6"`             // capital - invested, BRL
	MaxHold          int                   `json:"max_hold" example:"5"`             // sell at the close after this many sessions
	AnalystEstimates bool                  `json:"analyst_estimates" example:"true"` // false when disabled or Yahoo is unavailable
	Picks            []pickResponse        `json:"picks"`                            // empty when no stock passed the filters
	Tiers            []tierSummaryResponse `json:"tiers"`                            // the tiers tried, strongest first
} //	@name	Plan

type tiersSummaryResponse struct {
	Session string                `json:"session" example:"2026-10-30"`
	Tiers   []tierSummaryResponse `json:"tiers"`
} //	@name	TierSummaries

type tickerEstimateResponse struct {
	Ticker string  `json:"ticker" example:"PETR4"`
	Close  float64 `json:"close" example:"38.2"` // latest close, BRL
	estimateResponse
	Upside float64 `json:"upside" example:"0.165"` // target / close - 1
} //	@name	TickerEstimate

// Recommendations builds the plan for the next session.
//
//	@Summary		Get the trading plan
//	@Description	The plan of oracle recommend for the session after the latest one: which stocks to buy, how many and at what prices, ranked by track record and analyst upside.
//	@Description	Tiers are tried from strongest to weakest: the plan stops at the first unless fill is true. Analyst targets come from Yahoo Finance, an unofficial API: when it fails the plan ranks by history only (analyst_estimates is false), and targets that take too long are left for a later call.
//	@Tags			plan
//	@Produce		json
//	@Param			capital		query		number	false	"Capital in BRL (default 500)"										minimum(0)
//	@Param			top			query		int		false	"Stocks in the plan (default 5, at most 50)"						minimum(1)	maximum(50)
//	@Param			risk		query		number	false	"Max loss per trade, percent of capital (default 2, at most 100)"	minimum(0)	maximum(100)
//	@Param			fill		query		bool	false	"Go down the tiers until the plan is full (default false)"
//	@Param			years		query		int		false	"Years of history (default 3, at most 30)"	minimum(1)	maximum(30)
//	@Param			estimates	query		bool	false	"Use analyst targets (default true)"
//	@Success		200			{object}	planResponse
//	@Failure		400			{object}	errorResponse	"an option is out of range or not a number/boolean"
//	@Failure		404			{object}	errorResponse	"no stock quotes stored in the period"
//	@Failure		500			{object}	errorResponse
//	@Router			/recommendations [get]
func (h *Plan) Recommendations(w http.ResponseWriter, r *http.Request) {
	opts, years, useEstimates, ok := planOptions(w, r)
	if !ok {
		return
	}
	history, latest, ok := h.history(w, r, years)
	if !ok {
		return
	}

	params := swing.DefaultParams()
	estimates := h.estimatesFor(r.Context(), latest, useEstimates)
	ctx, cancel := context.WithTimeout(r.Context(), estimatesBudget)
	defer cancel()
	rows, summaries := plan.Build(ctx, history, latest, params, estimates, opts)

	body := planResponse{
		Session:          latest.Format(time.DateOnly),
		Stale:            h.now().Sub(latest) > quote.StaleAfter,
		Capital:          opts.Capital,
		Top:              opts.Top,
		Risk:             opts.Risk,
		PerStock:         cents(opts.Capital / float64(opts.Top)),
		MaxLoss:          cents(opts.Capital * opts.Risk / 100),
		MaxHold:          params.MaxHold,
		AnalystEstimates: estimates.Enabled(),
		Picks:            make([]pickResponse, 0, len(rows)),
		Tiers:            newTierSummaries(summaries),
	}
	invested := 0.0
	for _, row := range rows {
		body.Picks = append(body.Picks, newPick(row))
		invested += row.Invested()
	}
	body.Invested, body.Cash = cents(invested), cents(opts.Capital-invested)
	writeJSON(w, http.StatusOK, body)
}

// RecommendationTiers summarizes every tier on the latest session.
//
//	@Summary		Summarize the tiers
//	@Description	How each tier did over the history of every stock traded on the latest session, before any selection, and how many stocks are candidates today: the table under the plan of oracle recommend, for every tier.
//	@Tags			plan
//	@Produce		json
//	@Param			years	query		int	false	"Years of history (default 3, at most 30)"	minimum(1)	maximum(30)
//	@Success		200		{object}	tiersSummaryResponse
//	@Failure		400		{object}	errorResponse	"years out of range"
//	@Failure		404		{object}	errorResponse	"no stock quotes stored in the period"
//	@Failure		500		{object}	errorResponse
//	@Router			/recommendations/tiers [get]
func (h *Plan) RecommendationTiers(w http.ResponseWriter, r *http.Request) {
	years, ok := queryYears(w, r)
	if !ok {
		return
	}
	history, latest, ok := h.history(w, r, years)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, tiersSummaryResponse{
		Session: latest.Format(time.DateOnly),
		Tiers:   newTierSummaries(plan.Tiers(history, latest, swing.DefaultParams())),
	})
}

// Estimate gives the analyst target of one ticker.
//
//	@Summary		Get the analyst target of a ticker
//	@Description	The consensus price target and rating from Yahoo Finance, and the upside from the latest close. Targets are cached for the latest session.
//	@Tags			plan
//	@Produce		json
//	@Param			ticker	path		string	true	"B3 ticker"	example(PETR4)
//	@Success		200		{object}	tickerEstimateResponse
//	@Failure		404		{object}	errorResponse	"no quotes for the ticker, or no analyst coverage"
//	@Failure		500		{object}	errorResponse
//	@Failure		503		{object}	errorResponse	"Yahoo Finance is unavailable or too slow; try again later"
//	@Router			/tickers/{ticker}/estimate [get]
func (h *Plan) Estimate(w http.ResponseWriter, r *http.Request) {
	name := pathTicker(r)
	ticker, ok := loadTicker(w, r, h.store, name)
	if !ok {
		return
	}
	last, err := h.store.TickerQuotes(r.Context(), ticker.Ticker, ticker.Last, ticker.Last)
	if err != nil || len(last) == 0 {
		writeInternal(w, "load latest quote", fmt.Errorf("%s on %s: %d quotes, %w", name, ticker.Last.Format(time.DateOnly), len(last), err))
		return
	}
	latest, err := h.store.LatestSession(r.Context())
	if err != nil {
		writeInternal(w, "latest session", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), estimatesBudget)
	defer cancel()
	estimate, err := h.estimatesFor(r.Context(), latest, true).Get(ctx, ticker.Ticker)
	switch {
	case errors.Is(err, yahoo.ErrNoCoverage):
		writeError(w, http.StatusNotFound, "no analyst coverage for "+name)
		return
	case err != nil:
		log.Printf("estimate %s: %v", ticker.Ticker, err)
		writeError(w, http.StatusServiceUnavailable, "analyst estimates are unavailable, try again later")
		return
	}
	lastClose := last[0].Close
	writeJSON(w, http.StatusOK, tickerEstimateResponse{
		Ticker:           ticker.Ticker,
		Close:            lastClose,
		estimateResponse: newEstimate(estimate),
		Upside:           fraction(estimate.Target/lastClose - 1),
	})
}

// history loads the stocks of the last years, answering 404 or 500 itself when it can't.
func (h *Plan) history(w http.ResponseWriter, r *http.Request, years int) (map[string][]quote.Quote, time.Time, bool) {
	history, err := h.store.History(r.Context(), h.now().AddDate(-years, 0, 0))
	if err != nil {
		writeInternal(w, "load history", err)
		return nil, time.Time{}, false
	}
	latest := quote.LastSession(history)
	if latest.IsZero() {
		writeError(w, http.StatusNotFound, "no stock quotes stored in the period, run oracle sync")
		return nil, time.Time{}, false
	}
	return history, latest, true
}

// estimatesFor is the analyst cache of the session, connecting to Yahoo on the first call of a new
// session. Without estimates, or while Yahoo is unavailable, it has no client.
func (h *Plan) estimatesFor(ctx context.Context, session time.Time, enabled bool) *plan.Estimates {
	if !enabled || h.connect == nil {
		return plan.NewEstimates(nil)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.session.Equal(session) {
		h.session, h.estimates, h.retryAt = session, nil, time.Time{}
	}
	if h.estimates == nil {
		if h.now().Before(h.retryAt) {
			return plan.NewEstimates(nil)
		}
		connectCtx, cancel := context.WithTimeout(ctx, estimatesBudget)
		defer cancel()
		client, err := h.connect(connectCtx)
		if err != nil {
			log.Printf("analyst estimates unavailable, ranking by history only: %v", err)
			h.retryAt = h.now().Add(reconnectAfter)
			return plan.NewEstimates(nil)
		}
		h.estimates = plan.NewEstimates(client)
	}
	return h.estimates
}

// planOptions reads the query parameters of /recommendations, answering 400 itself when one is invalid.
func planOptions(w http.ResponseWriter, r *http.Request) (opts plan.Options, years int, estimates, ok bool) {
	q := r.URL.Query()
	opts = plan.Options{Capital: 500, Top: 5, Risk: 2} // as oracle recommend
	estimates = true
	var err error
	if opts.Capital, err = queryFloat(q.Get("capital"), opts.Capital, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "capital "+err.Error())
		return opts, 0, false, false
	}
	if opts.Risk, err = queryFloat(q.Get("risk"), opts.Risk, 100, true); err != nil {
		writeError(w, http.StatusBadRequest, "risk "+err.Error())
		return opts, 0, false, false
	}
	if v := q.Get("top"); v != "" {
		if opts.Top, err = strconv.Atoi(v); err != nil || opts.Top < 1 || opts.Top > 50 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("top %q must be a whole number from 1 to 50", v))
			return opts, 0, false, false
		}
	}
	for _, b := range []struct {
		name string
		dst  *bool
	}{{"fill", &opts.Fill}, {"estimates", &estimates}} {
		if v := q.Get(b.name); v != "" {
			if *b.dst, err = strconv.ParseBool(v); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("%s %q must be true or false", b.name, v))
				return opts, 0, false, false
			}
		}
	}
	if years, ok = queryYears(w, r); !ok {
		return opts, 0, false, false
	}
	return opts, years, estimates, true
}

// queryFloat parses a positive number, up to limit when bounded.
func queryFloat(value string, fallback, limit float64, bounded bool) (float64, error) {
	if value == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(value, 64)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%q is not a number", value)
	case v <= 0:
		return 0, fmt.Errorf("%q must be more than 0", value)
	case bounded && v > limit:
		return 0, fmt.Errorf("%q must be at most %g", value, limit)
	}
	return v, nil
}

// queryYears reads the years query parameter, answering 400 itself when it is invalid.
func queryYears(w http.ResponseWriter, r *http.Request) (int, bool) {
	value := r.URL.Query().Get("years")
	if value == "" {
		return defaultYears, true
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > maxYears {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("years %q must be a whole number from 1 to %d", value, maxYears))
		return 0, false
	}
	return n, true
}

func newPick(row plan.Row) pickResponse {
	body := pickResponse{
		Ticker:     row.Ticker,
		Name:       row.Name,
		Tier:       row.Tier,
		Qty:        row.Qty,
		MaxEntry:   row.MaxEntry,
		Target:     row.Target,
		Stop:       row.Stop,
		Invested:   cents(row.Invested()),
		Gain:       cents(row.Gain()),
		Loss:       cents(row.Loss()),
		Trades:     row.Trades,
		WinRate:    fraction(row.WinRate()),
		AvgReturn:  fraction(row.AvgReturn()),
		Expectancy: fraction(row.Expectancy()),
	}
	if row.Estimate != nil {
		e := newEstimate(*row.Estimate)
		upside := fraction(row.Upside)
		body.Estimate, body.Upside = &e, &upside
	}
	return body
}

func newEstimate(e yahoo.Estimate) estimateResponse {
	body := estimateResponse{Target: e.Target, Analysts: e.Analysts}
	if e.Rating > 0 {
		rating := e.Rating
		body.Rating = &rating
	}
	return body
}

func newTierSummaries(summaries []plan.Summary) []tierSummaryResponse {
	body := make([]tierSummaryResponse, 0, len(summaries))
	for _, s := range summaries {
		body = append(body, tierSummaryResponse{
			Label:      s.Tier.Label,
			Rule:       s.Tier.Rule,
			Candidates: s.Candidates,
			Trades:     s.Trades,
			WinRate:    fraction(s.WinRate()),
			AvgReturn:  fraction(s.AvgReturn()),
		})
	}
	return body
}
