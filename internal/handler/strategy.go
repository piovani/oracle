package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"oracle/internal/quote"
	"oracle/internal/swing"
)

const (
	defaultYears = 3 // as oracle recommend
	maxYears     = 30
)

// StrategyReader is the part of quote.Store the quote and strategy routes read from.
type StrategyReader interface {
	QuoteReader
	History(ctx context.Context, since time.Time) (map[string][]quote.Quote, error)
}

// Strategy runs the swing rule of internal/swing, the same functions oracle recommend calls.
type Strategy struct {
	store StrategyReader
	now   func() time.Time
}

// NewStrategy runs the rule on the history in store; now sets where the years of history start.
func NewStrategy(store StrategyReader, now func() time.Time) *Strategy {
	return &Strategy{store: store, now: now}
}

type strategyResponse struct {
	EntryATR  float64 `json:"entry_atr" example:"0.5"`      // limit buy this many ATRs below the signal close
	TargetATR float64 `json:"target_atr" example:"1"`       // sell this many ATRs above the signal close
	StopATR   float64 `json:"stop_atr" example:"2"`         // stop this many ATRs below the signal close
	MaxHold   int     `json:"max_hold" example:"5"`         // sessions before selling at the close
	Oversold  float64 `json:"oversold" example:"10"`        // RSI(2) below this is a signal, in the strongest tier
	CostPct   float64 `json:"cost_pct" example:"0.03"`      // cost per side, in percent
	MinTrades int     `json:"min_trades" example:"5"`       // trades in the track record to be eligible
	MinVolume float64 `json:"min_volume" example:"5000000"` // median daily volume in BRL over 60 sessions
	Warmup    int     `json:"warmup" example:"200"`         // sessions before the first signal (the SMA200)
} //	@name	Strategy

type tierResponse struct {
	Label    string  `json:"label" example:"forte"`
	Oversold float64 `json:"oversold" example:"10"` // RSI(2) must be below this; 101 means no pullback is required
	Rule     string  `json:"rule" example:"RSI(2) < 10"`
} //	@name	Tier

type levelsResponse struct {
	Close    float64 `json:"close" example:"38.2"`      // signal close, BRL
	MaxEntry float64 `json:"max_entry" example:"37.71"` // limit buy price for the next session, BRL
	Target   float64 `json:"target" example:"39.18"`    // BRL
	Stop     float64 `json:"stop" example:"36.24"`      // BRL
} //	@name	Levels

type analysisResponse struct {
	Ticker     string          `json:"ticker" example:"PETR4"`
	Name       string          `json:"name" example:"PETROBRAS"`
	Tier       string          `json:"tier" example:"forte"`
	AsOf       string          `json:"as_of" example:"2026-10-30"` // last session analyzed, YYYY-MM-DD
	Signal     bool            `json:"signal" example:"true"`      // the rule fires on the as_of close
	Levels     *levelsResponse `json:"levels"`                     // order prices for the next session, null without a signal
	Trades     int             `json:"trades" example:"12"`        // trades in the track record
	Wins       int             `json:"wins" example:"9"`
	WinRate    float64         `json:"win_rate" example:"0.75"`     // wins / trades, 0 to 1
	AvgReturn  float64         `json:"avg_return" example:"0.0123"` // mean return per trade, net of costs (0.01 = 1%)
	Expectancy float64         `json:"expectancy" example:"0.0067"` // return per trade shrunk toward 0 for short records; ranks recommend
	Eligible   bool            `json:"eligible" example:"true"`     // signal, at least min_trades trades and positive expectancy
} //	@name	Analysis

type tradeResponse struct {
	SignalDate string  `json:"signal_date" example:"2026-03-12"` // close the plan was made after
	EntryDate  string  `json:"entry_date" example:"2026-03-13"`  // session the limit order filled
	ExitDate   string  `json:"exit_date" example:"2026-03-17"`
	Entry      float64 `json:"entry" example:"35.1"`    // BRL
	Exit       float64 `json:"exit" example:"35.92"`    // BRL
	Return     float64 `json:"return" example:"0.0228"` // net of costs (0.01 = 1%)
} //	@name	Trade

type signalsResponse struct {
	Session string             `json:"session" example:"2026-10-30"` // latest session, YYYY-MM-DD
	Tier    string             `json:"tier" example:"forte"`
	Rule    string             `json:"rule" example:"RSI(2) < 10"`
	Signals []analysisResponse `json:"signals"` // eligible first, then by expectancy
} //	@name	Signals

// Params lists the parameters of the rule.
//
//	@Summary		Get the strategy parameters
//	@Description	The parameters of the swing rule that oracle recommend and oracle backtest use. Prices are set from the ATR (average true range of 14 sessions) of the signal day.
//	@Description	A signal is a close above the 200-day average with RSI(2) below oversold. The tiers lower the bar for oversold; see /strategy/tiers.
//	@Tags			strategy
//	@Produce		json
//	@Success		200	{object}	strategyResponse
//	@Router			/strategy [get]
func (*Strategy) Params(w http.ResponseWriter, _ *http.Request) {
	p := swing.DefaultParams()
	writeJSON(w, http.StatusOK, strategyResponse{
		EntryATR:  p.EntryATR,
		TargetATR: p.TargetATR,
		StopATR:   p.StopATR,
		MaxHold:   p.MaxHold,
		Oversold:  p.Oversold,
		CostPct:   p.CostPct,
		MinTrades: p.MinTrades,
		MinVolume: p.MinVolume,
		Warmup:    swing.Warmup,
	})
}

// Tiers lists the signal levels.
//
//	@Summary		List the signal tiers
//	@Description	The signal levels, from strongest to weakest. oracle recommend stops at the first that fills the list, or goes down the tiers with -fill.
//	@Description	Labels are in Portuguese, as the CLI prints them; the tier query parameter also accepts them without accents (medio, tendencia).
//	@Tags			strategy
//	@Produce		json
//	@Success		200	{array}	tierResponse
//	@Router			/strategy/tiers [get]
func (*Strategy) Tiers(w http.ResponseWriter, _ *http.Request) {
	body := make([]tierResponse, 0, len(swing.Tiers))
	for _, t := range swing.Tiers {
		body = append(body, tierResponse{Label: t.Label, Oversold: t.Oversold, Rule: t.Rule})
	}
	writeJSON(w, http.StatusOK, body)
}

// Analysis runs the rule on one stock.
//
//	@Summary		Analyze a stock
//	@Description	Runs the rule on the last years of one stock, as oracle recommend does for every stock: whether there is a signal on its latest session, the order prices, and its track record.
//	@Description	Only the part of the series after the last split counts. Without at least 260 sessions there, or with a median volume below min_volume, the track record is empty and there is no signal.
//	@Tags			strategy
//	@Produce		json
//	@Param			ticker	path		string	true	"B3 ticker of a stock (ON, PN or UNT)"		example(PETR4)
//	@Param			tier	query		string	false	"Signal tier (default forte)"				Enums(forte, médio, fraco, tendência)
//	@Param			years	query		int		false	"Years of history (default 3, at most 30)"	minimum(1)	maximum(30)
//	@Success		200		{object}	analysisResponse
//	@Failure		400		{object}	errorResponse	"unknown tier or years out of range"
//	@Failure		404		{object}	errorResponse	"no quotes for the ticker in the period"
//	@Failure		422		{object}	errorResponse	"the ticker is not a stock"
//	@Failure		500		{object}	errorResponse
//	@Router			/tickers/{ticker}/analysis [get]
func (h *Strategy) Analysis(w http.ResponseWriter, r *http.Request) {
	tier, bars, ok := h.stock(w, r)
	if !ok {
		return
	}
	last := bars[len(bars)-1]
	a := swing.Analyze(last.Ticker, bars, tierParams(tier))
	writeJSON(w, http.StatusOK, newAnalysis(a, last, tier))
}

// Trades lists the track record of one stock.
//
//	@Summary		List the simulated trades of a stock
//	@Description	The trades behind the track record of /tickers/{ticker}/analysis, in date order: each signal is bought with a limit order the next session and sold at the target, the stop or the close of the last holding session.
//	@Description	Empty when the series after the last split is too short or not liquid enough.
//	@Tags			strategy
//	@Produce		json
//	@Param			ticker	path		string	true	"B3 ticker of a stock (ON, PN or UNT)"		example(PETR4)
//	@Param			tier	query		string	false	"Signal tier (default forte)"				Enums(forte, médio, fraco, tendência)
//	@Param			years	query		int		false	"Years of history (default 3, at most 30)"	minimum(1)	maximum(30)
//	@Success		200		{array}		tradeResponse
//	@Failure		400		{object}	errorResponse	"unknown tier or years out of range"
//	@Failure		404		{object}	errorResponse	"no quotes for the ticker in the period"
//	@Failure		422		{object}	errorResponse	"the ticker is not a stock"
//	@Failure		500		{object}	errorResponse
//	@Router			/tickers/{ticker}/trades [get]
func (h *Strategy) Trades(w http.ResponseWriter, r *http.Request) {
	tier, bars, ok := h.stock(w, r)
	if !ok {
		return
	}
	records := tierParams(tier).TrackRecord(bars)
	body := make([]tradeResponse, 0, len(records))
	for _, t := range records {
		body = append(body, tradeResponse{
			SignalDate: t.SignalDay.Format(time.DateOnly),
			EntryDate:  t.EntryDay.Format(time.DateOnly),
			ExitDate:   t.ExitDay.Format(time.DateOnly),
			Entry:      cents(t.Entry),
			Exit:       cents(t.Exit),
			Return:     fraction(t.Return),
		})
	}
	writeJSON(w, http.StatusOK, body)
}

// Signals lists the stocks with a signal on the latest session.
//
//	@Summary		List today's signals
//	@Description	Every stock traded on the latest session whose close fires the rule of the tier, with its order prices and track record: the candidates oracle recommend picks from, before analyst targets and position sizing.
//	@Description	Eligible stocks come first, then by expectancy. An empty list means no signal in that tier.
//	@Tags			strategy
//	@Produce		json
//	@Param			tier	query		string	false	"Signal tier (default forte)"				Enums(forte, médio, fraco, tendência)
//	@Param			years	query		int		false	"Years of history (default 3, at most 30)"	minimum(1)	maximum(30)
//	@Success		200		{object}	signalsResponse
//	@Failure		400		{object}	errorResponse	"unknown tier or years out of range"
//	@Failure		404		{object}	errorResponse	"no stock quotes stored in the period"
//	@Failure		500		{object}	errorResponse
//	@Router			/signals [get]
func (h *Strategy) Signals(w http.ResponseWriter, r *http.Request) {
	tier, since, ok := h.options(w, r)
	if !ok {
		return
	}
	history, err := h.store.History(r.Context(), since)
	if err != nil {
		writeInternal(w, "load history", err)
		return
	}
	latest := quote.LastSession(history)
	if latest.IsZero() {
		writeError(w, http.StatusNotFound, "no stock quotes stored in the period, run oracle sync")
		return
	}

	signals := []analysisResponse{}
	for _, a := range swing.Scan(history, latest, tierParams(tier)) {
		if a.Signal {
			bars := history[a.Ticker]
			signals = append(signals, newAnalysis(a, bars[len(bars)-1], tier))
		}
	}
	slices.SortStableFunc(signals, func(a, b analysisResponse) int {
		if a.Eligible != b.Eligible {
			if a.Eligible {
				return -1
			}
			return 1
		}
		return compareDesc(a.Expectancy, b.Expectancy)
	})
	writeJSON(w, http.StatusOK, signalsResponse{
		Session: latest.Format(time.DateOnly),
		Tier:    tier.Label,
		Rule:    tier.Rule,
		Signals: signals,
	})
}

// stock reads the options and the quotes of the stock in the path, answering the error itself when it can't.
func (h *Strategy) stock(w http.ResponseWriter, r *http.Request) (swing.Tier, []quote.Quote, bool) {
	tier, since, ok := h.options(w, r)
	if !ok {
		return swing.Tier{}, nil, false
	}
	name := pathTicker(r)
	ticker, ok := loadTicker(w, r, h.store, name)
	if !ok {
		return swing.Tier{}, nil, false
	}
	if !quote.IsStock(ticker.Spec) {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("%s is not a stock (spec %s): the strategy runs only on ON, PN and UNT", name, spec(ticker.Spec)))
		return swing.Tier{}, nil, false
	}
	bars, err := h.store.TickerQuotes(r.Context(), ticker.Ticker, since, time.Time{})
	if err != nil {
		writeInternal(w, "load ticker quotes", err)
		return swing.Tier{}, nil, false
	}
	if len(bars) == 0 {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no quotes for %s since %s", name, since.Format(time.DateOnly)))
		return swing.Tier{}, nil, false
	}
	return tier, bars, true
}

// options reads the tier and years query parameters, answering 400 itself when they are invalid.
func (h *Strategy) options(w http.ResponseWriter, r *http.Request) (swing.Tier, time.Time, bool) {
	tier := swing.Tiers[0]
	if label := r.URL.Query().Get("tier"); label != "" {
		found, ok := findTier(label)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown tier %q: use forte, médio, fraco or tendência", label))
			return swing.Tier{}, time.Time{}, false
		}
		tier = found
	}
	years, ok := queryYears(w, r)
	if !ok {
		return swing.Tier{}, time.Time{}, false
	}
	return tier, h.now().AddDate(-years, 0, 0), true
}

var unaccent = strings.NewReplacer("é", "e", "ê", "e")

// findTier matches a tier label, ignoring case and accents.
func findTier(label string) (swing.Tier, bool) {
	want := unaccent.Replace(strings.ToLower(label))
	for _, t := range swing.Tiers {
		if unaccent.Replace(t.Label) == want {
			return t, true
		}
	}
	return swing.Tier{}, false
}

// tierParams are the default parameters with the oversold level of the tier, as recommend sets them.
func tierParams(tier swing.Tier) swing.Params {
	p := swing.DefaultParams()
	p.Oversold = tier.Oversold
	return p
}

func newAnalysis(a swing.Analysis, last quote.Quote, tier swing.Tier) analysisResponse {
	body := analysisResponse{
		Ticker:     a.Ticker,
		Name:       last.Name,
		Tier:       tier.Label,
		AsOf:       last.Date.Format(time.DateOnly),
		Signal:     a.Signal,
		Trades:     a.Trades,
		Wins:       a.Wins,
		WinRate:    fraction(a.WinRate()),
		AvgReturn:  fraction(a.AvgReturn()),
		Expectancy: fraction(a.Expectancy()),
		Eligible:   a.Eligible(tierParams(tier)),
	}
	if a.Signal {
		body.Levels = &levelsResponse{Close: a.Close, MaxEntry: a.MaxEntry, Target: a.Target, Stop: a.Stop}
	}
	return body
}

// cents rounds a price to cents, as B3 quotes it.
func cents(v float64) float64 { return math.Round(v*100) / 100 }

// fraction rounds a rate or a return to 4 decimals, a hundredth of a percent.
func fraction(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func compareDesc(a, b float64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}
