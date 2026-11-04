package command

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oracle/internal/plan"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
	"oracle/test/mock"
)

type pickBody struct {
	Ticker   string  `json:"ticker"`
	Name     string  `json:"name"`
	Tier     string  `json:"tier"`
	Qty      int     `json:"qty"`
	MaxEntry float64 `json:"max_entry"`
	Target   float64 `json:"target"`
	Stop     float64 `json:"stop"`
	Invested float64 `json:"invested"`
	Estimate *struct {
		Target   float64  `json:"target"`
		Rating   *float64 `json:"rating"`
		Analysts int      `json:"analysts"`
	} `json:"estimate"`
	Upside *float64 `json:"upside"`
}

type planBody struct {
	Session          string     `json:"session"`
	Stale            bool       `json:"stale"`
	Capital          float64    `json:"capital"`
	Top              int        `json:"top"`
	PerStock         float64    `json:"per_stock"`
	MaxLoss          float64    `json:"max_loss"`
	Invested         float64    `json:"invested"`
	Cash             float64    `json:"cash"`
	MaxHold          int        `json:"max_hold"`
	AnalystEstimates bool       `json:"analyst_estimates"`
	Picks            []pickBody `json:"picks"`
	Tiers            []struct {
		Label      string `json:"label"`
		Candidates int    `json:"candidates"`
		Trades     int    `json:"trades"`
	} `json:"tiers"`
}

type fakeYahoo struct {
	est   plan.Estimator
	err   error
	calls int
}

func (a *fakeYahoo) connect(context.Context) (plan.Estimator, error) {
	a.calls++
	return a.est, a.err
}

func get(t *testing.T, mux http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

func lastClose(t *testing.T, store *mock.Store, ticker string) float64 {
	t.Helper()
	bars, err := store.TickerQuotes(t.Context(), ticker, time.Time{}, time.Time{})
	if err != nil || len(bars) == 0 {
		t.Fatalf("%s: %v", ticker, err)
	}
	return bars[len(bars)-1].Close
}

func TestRecommendationsIsWhatRecommendBuilds(t *testing.T) {
	store := strategyStore()
	rec := get(t, newMux(store, now, nil, nil), "/recommendations?capital=1000&top=3&fill=true")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	got := decode[planBody](t, rec)

	history, err := store.History(t.Context(), now().AddDate(-3, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	rows, summaries := plan.Build(t.Context(), history, strategyDay, swing.DefaultParams(), plan.NewEstimates(nil),
		plan.Options{Capital: 1000, Top: 3, Risk: 2, Fill: true})
	if len(got.Picks) != len(rows) || len(got.Tiers) != len(summaries) || len(rows) != 2 {
		t.Fatalf("got %d picks and %d tiers, plan.Build has %d and %d", len(got.Picks), len(got.Tiers), len(rows), len(summaries))
	}
	invested := 0.0
	for i, row := range rows {
		p := got.Picks[i]
		if p.Ticker != row.Ticker || p.Tier != row.Tier || p.Qty != row.Qty || p.MaxEntry != row.MaxEntry ||
			p.Target != row.Target || p.Stop != row.Stop || p.Name != row.Name || p.Estimate != nil || p.Upside != nil {
			t.Errorf("pick %d: got %+v, want %+v", i, p, row)
		}
		invested += p.Invested
	}
	if got.Session != "2026-10-30" || !got.Stale || got.Capital != 1000 || got.Top != 3 || got.PerStock != 333.33 ||
		got.MaxLoss != 20 || got.MaxHold != 5 || got.AnalystEstimates || got.Invested != invested ||
		got.Cash != fraction2(1000-invested) {
		t.Errorf("got %+v", got)
	}
}

func fraction2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func TestRecommendationsStopsAtTheFirstTier(t *testing.T) {
	got := decode[planBody](t, get(t, newMux(strategyStore(), now, nil, nil), "/recommendations"))
	if len(got.Picks) != 1 || got.Picks[0].Ticker != "WIN3" || len(got.Tiers) != 1 || got.Tiers[0].Label != "forte" ||
		got.Capital != 500 || got.Top != 5 || got.PerStock != 100 || got.MaxLoss != 10 {
		t.Errorf("got %+v, want only WIN3 with the defaults of oracle recommend", got)
	}
}

func TestRecommendationsUseAnalystTargets(t *testing.T) {
	store := strategyStore()
	est := &mock.Estimator{Estimates: map[string]yahoo.Estimate{
		"WIN3": {Target: lastClose(t, store, "WIN3") * 1.2, Rating: 1.5, Analysts: 8},
		"MED3": {Target: lastClose(t, store, "MED3") * 0.9}, // analysts see it falling
	}}
	yahooSession := &fakeYahoo{est: est}
	mux := newMux(store, now, yahooSession.connect, nil)

	got := decode[planBody](t, get(t, mux, "/recommendations?fill=true"))
	if !got.AnalystEstimates || len(got.Picks) != 1 || got.Picks[0].Ticker != "WIN3" {
		t.Fatalf("got %+v, want MED3 dropped for a target below the close", got)
	}
	p := got.Picks[0]
	if p.Estimate == nil || p.Estimate.Analysts != 8 || p.Estimate.Rating == nil || *p.Estimate.Rating != 1.5 ||
		p.Upside == nil || *p.Upside < 0.19 || *p.Upside > 0.21 {
		t.Errorf("got estimate %+v, upside %v", p.Estimate, p.Upside)
	}

	get(t, mux, "/recommendations?fill=true")
	if yahooSession.calls != 1 || len(est.Requested) != 2 {
		t.Errorf("connected %d times and requested %v, want one session asking each ticker once", yahooSession.calls, est.Requested)
	}
	if got := decode[planBody](t, get(t, mux, "/recommendations?estimates=false")); got.AnalystEstimates || got.Picks[0].Estimate != nil {
		t.Errorf("estimates=false: got %+v", got)
	}
}

func TestYahooSessionFollowsTheLatestSession(t *testing.T) {
	store := strategyStore()
	clock := now()
	yahooSession := &fakeYahoo{err: errors.New("yahoo is down")}
	mux := newMux(store, func() time.Time { return clock }, yahooSession.connect, nil)

	if got := decode[planBody](t, get(t, mux, "/recommendations")); got.AnalystEstimates || len(got.Picks) != 1 {
		t.Fatalf("got %+v, want the plan by history only", got)
	}
	get(t, mux, "/recommendations")
	if yahooSession.calls != 1 {
		t.Errorf("connected %d times, want no retry right after a failure", yahooSession.calls)
	}

	clock = clock.Add(11 * time.Minute)
	yahooSession.est, yahooSession.err = &mock.Estimator{}, nil
	if got := decode[planBody](t, get(t, mux, "/recommendations")); !got.AnalystEstimates || yahooSession.calls != 2 {
		t.Errorf("after the wait: estimates %v, %d connections", got.AnalystEstimates, yahooSession.calls)
	}

	store.Quotes = append(store.Quotes, quote.Quote{Ticker: "FLAT3", Spec: "ON", Date: strategyDay.AddDate(0, 0, 3), Close: 20})
	get(t, mux, "/recommendations")
	if yahooSession.calls != 3 {
		t.Errorf("connected %d times, want a new Yahoo session for the new trading session", yahooSession.calls)
	}
}

func TestRecommendationTiers(t *testing.T) {
	rec := get(t, newMux(strategyStore(), now, nil, nil), "/recommendations/tiers")
	got := decode[struct {
		Session string `json:"session"`
		Tiers   []struct {
			Label      string  `json:"label"`
			Rule       string  `json:"rule"`
			Candidates int     `json:"candidates"`
			Trades     int     `json:"trades"`
			WinRate    float64 `json:"win_rate"`
			AvgReturn  float64 `json:"avg_return"`
		} `json:"tiers"`
	}](t, rec)
	if rec.Code != http.StatusOK || got.Session != "2026-10-30" || len(got.Tiers) != len(swing.Tiers) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	// tendência needs no pullback, so it trades more and loses some.
	want := []struct {
		candidates, trades int
		winRate            float64
	}{{1, 12, 1}, {2, 12, 1}, {2, 12, 1}, {2, 24, 0.5}}
	for i, w := range want {
		tier := got.Tiers[i]
		if tier.Label != swing.Tiers[i].Label || tier.Candidates != w.candidates || tier.Trades != w.trades || tier.WinRate != w.winRate {
			t.Errorf("tier %d: got %+v, want %+v", i, tier, w)
		}
	}
}

func TestEstimate(t *testing.T) {
	store := strategyStore()
	est := &mock.Estimator{
		Estimates: map[string]yahoo.Estimate{"WIN3": {Target: lastClose(t, store, "WIN3") * 1.5, Analysts: 3}},
		Err:       map[string]error{"MED3": errors.New("yahoo is down")},
	}
	yahooSession := &fakeYahoo{est: est}
	mux := newMux(store, now, yahooSession.connect, nil)

	rec := get(t, mux, "/tickers/win3/estimate")
	got := decode[struct {
		Ticker   string   `json:"ticker"`
		Close    float64  `json:"close"`
		Target   float64  `json:"target"`
		Rating   *float64 `json:"rating"`
		Analysts int      `json:"analysts"`
		Upside   float64  `json:"upside"`
	}](t, rec)
	if rec.Code != http.StatusOK || got.Ticker != "WIN3" || got.Close != lastClose(t, store, "WIN3") ||
		got.Analysts != 3 || got.Rating != nil || got.Upside != 0.5 {
		t.Errorf("got %d %s", rec.Code, rec.Body.String())
	}

	cases := []struct {
		mux    http.Handler
		path   string
		status int
		body   string
	}{
		{mux, "/tickers/FLAT3/estimate", 404, `{"error":"no analyst coverage for FLAT3"}`},
		{mux, "/tickers/MED3/estimate", 503, `{"error":"analyst estimates are unavailable, try again later"}`},
		{mux, "/tickers/ZZZZ3/estimate", 404, `{"error":"ticker ZZZZ3 not found"}`},
		{newMux(store, now, nil, nil), "/tickers/WIN3/estimate", 503, `{"error":"analyst estimates are unavailable, try again later"}`},
		{newMux(failQuotes{store}, now, yahooSession.connect, nil), "/tickers/WIN3/estimate", 500, `{"error":"internal error"}`},
	}
	for _, c := range cases {
		rec := get(t, c.mux, c.path)
		if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s: got %d %s\nwant %d %s", c.path, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
}

type failLatest struct{ *mock.Store }

func (failLatest) LatestSession(context.Context) (time.Time, error) {
	return time.Time{}, errors.New("query canceled")
}

func TestPlanErrors(t *testing.T) {
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/recommendations?capital=0", 400, `{"error":"capital \"0\" must be more than 0"}`},
		{"/recommendations?capital=lots", 400, `{"error":"capital \"lots\" is not a number"}`},
		{"/recommendations?risk=101", 400, `{"error":"risk \"101\" must be at most 100"}`},
		{"/recommendations?risk=-1", 400, `{"error":"risk \"-1\" must be more than 0"}`},
		{"/recommendations?top=0", 400, `{"error":"top \"0\" must be a whole number from 1 to 50"}`},
		{"/recommendations?top=51", 400, `{"error":"top \"51\" must be a whole number from 1 to 50"}`},
		{"/recommendations?fill=yes", 400, `{"error":"fill \"yes\" must be true or false"}`},
		{"/recommendations?fill=maybe", 400, `{"error":"fill \"maybe\" must be true or false"}`},
		{"/recommendations?estimates=nah", 400, `{"error":"estimates \"nah\" must be true or false"}`},
		{"/recommendations?years=0", 400, `{"error":"years \"0\" must be a whole number from 1 to 30"}`},
		{"/recommendations/tiers?years=x", 400, `{"error":"years \"x\" must be a whole number from 1 to 30"}`},
	}
	for _, c := range cases {
		rec := serve(t, strategyStore(), c.path)
		if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s: got %d %s\nwant %d %s", c.path, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
	for _, path := range []string{"/recommendations", "/recommendations/tiers"} {
		if rec := serve(t, &mock.Store{}, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s of an empty store: got %d", path, rec.Code)
		}
		store := strategyStore()
		store.HistoryErr = errors.New("connection refused")
		if rec := serve(t, store, path); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s with a failing store: got %d", path, rec.Code)
		}
	}
	yahooSession := &fakeYahoo{est: &mock.Estimator{}}
	if rec := get(t, newMux(failLatest{strategyStore()}, now, yahooSession.connect, nil), "/tickers/WIN3/estimate"); rec.Code != http.StatusInternalServerError {
		t.Errorf("estimate with a failing latest session: got %d", rec.Code)
	}
}

// The estimate route can see a later session than the plan; going back must not reconnect.
func TestYahooSessionOnlyMovesForward(t *testing.T) {
	store := strategyStore()
	yahooSession := &fakeYahoo{est: &mock.Estimator{}}
	mux := newMux(store, now, yahooSession.connect, nil)

	get(t, mux, "/recommendations")
	store.Quotes = append(store.Quotes, quote.Quote{Ticker: "XPML11", Spec: "CI", Date: strategyDay.AddDate(0, 0, 1), Close: 100})
	get(t, mux, "/tickers/WIN3/estimate") // latest session of any quote: a day later
	get(t, mux, "/recommendations")       // latest session of the stocks: the day before
	get(t, mux, "/tickers/WIN3/estimate")
	if yahooSession.calls != 2 {
		t.Errorf("connected %d times, want 2: once per newer session, none going back", yahooSession.calls)
	}
}

type emptyQuotes struct{ *mock.Store }

func (emptyQuotes) TickerQuotes(context.Context, string, time.Time, time.Time) ([]quote.Quote, error) {
	return []quote.Quote{}, nil
}

func TestEstimateWithoutTheLastQuote(t *testing.T) {
	yahooSession := &fakeYahoo{est: &mock.Estimator{}}
	rec := get(t, newMux(emptyQuotes{strategyStore()}, now, yahooSession.connect, nil), "/tickers/WIN3/estimate")
	if rec.Code != http.StatusInternalServerError || yahooSession.calls != 0 {
		t.Errorf("got %d %s after %d connections, want 500 before asking Yahoo", rec.Code, rec.Body.String(), yahooSession.calls)
	}
}
