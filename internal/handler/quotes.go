package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oracle/internal/quote"
)

// QuoteReader is the part of quote.Store the quote routes read from.
type QuoteReader interface {
	Tickers(ctx context.Context) ([]quote.Ticker, error)
	Ticker(ctx context.Context, ticker string) (quote.Ticker, error)
	TickerQuotes(ctx context.Context, ticker string, from, to time.Time) ([]quote.Quote, error)
	Session(ctx context.Context, day time.Time) ([]quote.Quote, error)
	LatestSession(ctx context.Context) (time.Time, error)
}

// Quotes serves the stored B3 quotes.
type Quotes struct {
	store QuoteReader
	now   func() time.Time
}

// NewQuotes serves the quotes in store; now tells whether the latest session is stale.
func NewQuotes(store QuoteReader, now func() time.Time) *Quotes {
	return &Quotes{store: store, now: now}
}

type tickerResponse struct {
	Ticker       string `json:"ticker" example:"PETR4"`
	Name         string `json:"name" example:"PETROBRAS"`           // company name as B3 abbreviates it
	Spec         string `json:"spec" example:"PN N2"`               // share type and listing segment: ON, PN, UNT for stocks; CI, DRN... for funds, ETFs and BDRs
	ISIN         string `json:"isin" example:"BRPETRACNPR6"`        // ISIN of the latest quote
	FirstSession string `json:"first_session" example:"2020-01-02"` // first day with a quote, YYYY-MM-DD
	LastSession  string `json:"last_session" example:"2026-10-30"`  // last day with a quote, YYYY-MM-DD
	Sessions     int    `json:"sessions" example:"1700"`            // days with a quote
} //	@name	Ticker

type tickerDetailResponse struct {
	tickerResponse
	LastQuote quoteResponse `json:"last_quote"`
} //	@name	TickerDetail

type quoteResponse struct {
	Ticker   string  `json:"ticker" example:"PETR4"`
	Name     string  `json:"name" example:"PETROBRAS"`
	Date     string  `json:"date" example:"2026-10-30"` // YYYY-MM-DD
	Open     float64 `json:"open" example:"37.9"`       // BRL
	High     float64 `json:"high" example:"38.45"`      // BRL
	Low      float64 `json:"low" example:"37.62"`       // BRL
	Average  float64 `json:"average" example:"38.1"`    // BRL, volume-weighted
	Close    float64 `json:"close" example:"38.2"`      // BRL
	Trades   int64   `json:"trades" example:"52310"`    // number of trades
	Quantity int64   `json:"quantity" example:"41250300"`
	Volume   float64 `json:"volume" example:"1571632430.5"` // BRL
} //	@name	Quote

type sessionResponse struct {
	Date  string `json:"date" example:"2026-10-30"` // YYYY-MM-DD
	Stale bool   `json:"stale" example:"false"`     // more than 4 days old: a sync is missing
} //	@name	Session

// Tickers lists every ticker stored.
//
//	@Summary		List tickers
//	@Description	Every ticker with quotes in the database, of any spec (stocks, real estate funds, ETFs, BDRs), in ticker order.
//	@Description	Name, spec and ISIN come from the latest quote of each ticker. An empty database answers an empty list.
//	@Tags			quotes
//	@Produce		json
//	@Success		200	{array}		tickerResponse
//	@Failure		500	{object}	errorResponse
//	@Router			/tickers [get]
func (h *Quotes) Tickers(w http.ResponseWriter, r *http.Request) {
	tickers, err := h.store.Tickers(r.Context())
	if err != nil {
		writeInternal(w, "list tickers", err)
		return
	}
	body := make([]tickerResponse, 0, len(tickers))
	for _, t := range tickers {
		body = append(body, newTicker(t))
	}
	writeJSON(w, http.StatusOK, body)
}

// Ticker describes one ticker and its latest quote.
//
//	@Summary		Get a ticker
//	@Description	Summary of one ticker and its latest quote. The ticker is case-insensitive.
//	@Tags			quotes
//	@Produce		json
//	@Param			ticker	path		string	true	"B3 ticker"	example(PETR4)
//	@Success		200		{object}	tickerDetailResponse
//	@Failure		404		{object}	errorResponse	"no quotes for the ticker"
//	@Failure		500		{object}	errorResponse
//	@Router			/tickers/{ticker} [get]
func (h *Quotes) Ticker(w http.ResponseWriter, r *http.Request) {
	ticker, ok := loadTicker(w, r, h.store, pathTicker(r))
	if !ok {
		return
	}
	last, err := h.store.TickerQuotes(r.Context(), ticker.Ticker, ticker.Last, ticker.Last)
	if err != nil {
		writeInternal(w, "get ticker", err)
		return
	}
	body := tickerDetailResponse{tickerResponse: newTicker(ticker)}
	if len(last) > 0 {
		body.LastQuote = newQuote(last[0])
	}
	writeJSON(w, http.StatusOK, body)
}

// TickerQuotes lists the daily quotes of one ticker.
//
//	@Summary		List the quotes of a ticker
//	@Description	Daily quotes of one ticker, in date order. Prices are as traded, not adjusted for splits or dividends.
//	@Description	Without from and to, the whole history is returned. A range without sessions answers an empty list.
//	@Tags			quotes
//	@Produce		json
//	@Param			ticker	path		string	true	"B3 ticker"							example(PETR4)
//	@Param			from	query		string	false	"First day, included (YYYY-MM-DD)"	example(2026-10-01)
//	@Param			to		query		string	false	"Last day, included (YYYY-MM-DD)"	example(2026-10-30)
//	@Success		200		{array}		quoteResponse
//	@Failure		400		{object}	errorResponse	"from or to is not a YYYY-MM-DD date, or from is after to"
//	@Failure		404		{object}	errorResponse	"no quotes for the ticker"
//	@Failure		500		{object}	errorResponse
//	@Router			/tickers/{ticker}/quotes [get]
func (h *Quotes) TickerQuotes(w http.ResponseWriter, r *http.Request) {
	from, err := optionalDay(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := optionalDay(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		writeError(w, http.StatusBadRequest, "from must not be after to")
		return
	}
	ticker, ok := loadTicker(w, r, h.store, pathTicker(r))
	if !ok {
		return
	}
	quotes, err := h.store.TickerQuotes(r.Context(), ticker.Ticker, from, to)
	if err != nil {
		writeInternal(w, "list ticker quotes", err)
		return
	}
	writeJSON(w, http.StatusOK, newQuotes(quotes))
}

// LatestSession tells the last day with quotes.
//
//	@Summary		Get the latest session
//	@Description	The last day with quotes in the database, and whether it is stale: older than 4 days, which means a sync is missing.
//	@Tags			quotes
//	@Produce		json
//	@Success		200	{object}	sessionResponse
//	@Failure		404	{object}	errorResponse	"no quotes stored yet"
//	@Failure		500	{object}	errorResponse
//	@Router			/sessions/latest [get]
func (h *Quotes) LatestSession(w http.ResponseWriter, r *http.Request) {
	latest, err := h.store.LatestSession(r.Context())
	if err != nil {
		writeInternal(w, "latest session", err)
		return
	}
	if latest.IsZero() {
		writeError(w, http.StatusNotFound, "no quotes stored yet, run oracle sync")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Date:  latest.Format(time.DateOnly),
		Stale: h.now().Sub(latest) > quote.StaleAfter,
	})
}

// SessionQuotes lists every quote of one day.
//
//	@Summary		List the quotes of a session
//	@Description	Every quote of one trading day, of any spec, in ticker order.
//	@Tags			quotes
//	@Produce		json
//	@Param			date	path		string	true	"Trading day (YYYY-MM-DD)"	example(2026-10-30)
//	@Success		200		{array}		quoteResponse
//	@Failure		400		{object}	errorResponse	"date is not a YYYY-MM-DD date"
//	@Failure		404		{object}	errorResponse	"no session on that day (weekend, holiday or not synced)"
//	@Failure		500		{object}	errorResponse
//	@Router			/sessions/{date}/quotes [get]
func (h *Quotes) SessionQuotes(w http.ResponseWriter, r *http.Request) {
	day, err := time.Parse(time.DateOnly, r.PathValue("date"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("date %q is not a YYYY-MM-DD date", r.PathValue("date")))
		return
	}
	quotes, err := h.store.Session(r.Context(), day)
	if err != nil {
		writeInternal(w, "list session quotes", err)
		return
	}
	if len(quotes) == 0 {
		writeError(w, http.StatusNotFound, "no session on "+day.Format(time.DateOnly))
		return
	}
	writeJSON(w, http.StatusOK, newQuotes(quotes))
}

// loadTicker loads a ticker, answering 404 or 500 itself when it can't.
func loadTicker(w http.ResponseWriter, r *http.Request, store QuoteReader, name string) (quote.Ticker, bool) {
	ticker, err := store.Ticker(r.Context(), name)
	switch {
	case errors.Is(err, quote.ErrUnknownTicker):
		writeError(w, http.StatusNotFound, "ticker "+name+" not found")
		return quote.Ticker{}, false
	case err != nil:
		writeInternal(w, "load ticker", err)
		return quote.Ticker{}, false
	}
	return ticker, true
}

func pathTicker(r *http.Request) string { return strings.ToUpper(r.PathValue("ticker")) }

func optionalDay(r *http.Request, param string) (time.Time, error) {
	value := r.URL.Query().Get(param)
	if value == "" {
		return time.Time{}, nil
	}
	day, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not a YYYY-MM-DD date", param, value)
	}
	return day, nil
}

func newTicker(t quote.Ticker) tickerResponse {
	return tickerResponse{
		Ticker:       t.Ticker,
		Name:         t.Name,
		Spec:         spec(t.Spec),
		ISIN:         t.ISIN,
		FirstSession: t.First.Format(time.DateOnly),
		LastSession:  t.Last.Format(time.DateOnly),
		Sessions:     t.Sessions,
	}
}

func newQuote(q quote.Quote) quoteResponse {
	return quoteResponse{
		Ticker:   q.Ticker,
		Name:     q.Name,
		Date:     q.Date.Format(time.DateOnly),
		Open:     q.Open,
		High:     q.High,
		Low:      q.Low,
		Average:  q.Average,
		Close:    q.Close,
		Trades:   q.Trades,
		Quantity: q.Quantity,
		Volume:   q.Volume,
	}
}

func newQuotes(quotes []quote.Quote) []quoteResponse {
	body := make([]quoteResponse, 0, len(quotes))
	for _, q := range quotes {
		body = append(body, newQuote(q))
	}
	return body
}

// spec collapses the fixed-width padding of the B3 spec: "PN      N2" becomes "PN N2".
func spec(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
