package command

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/test/fixture"
	"oracle/test/mock"
)

var strategyDay = fixture.Day(2026, 10, 30)

func strategyStore() *mock.Store {
	fund := fixture.Trend("XPML11", 400, strategyDay, nil)
	for i := range fund {
		fund[i].Spec = "CI"
	}
	var quotes []quote.Quote
	for _, bars := range [][]quote.Quote{
		fixture.Trend("WIN3", 400, strategyDay, fixture.Dips(5), fixture.StrongPullback...), // forte
		fixture.Trend("MED3", 400, strategyDay, fixture.Dips(5), fixture.MildPullback...),   // médio
		fixture.Trend("FLAT3", 400, strategyDay, nil),                                       // no signal
		fixture.Trend("NEW3", 400, strategyDay, fixture.Dips(2), fixture.StrongPullback...), // forte, but 2 trades: not eligible
		fixture.Trend("OLD3", 400, fixture.Day(2024, 6, 28), fixture.Dips(5), fixture.StrongPullback...),
		fund,
	} {
		quotes = append(quotes, bars...)
	}
	return &mock.Store{Quotes: quotes}
}

type analysisBody struct {
	Ticker string `json:"ticker"`
	Tier   string `json:"tier"`
	AsOf   string `json:"as_of"`
	Signal bool   `json:"signal"`
	Levels *struct {
		Close    float64 `json:"close"`
		MaxEntry float64 `json:"max_entry"`
		Target   float64 `json:"target"`
		Stop     float64 `json:"stop"`
	} `json:"levels"`
	Trades     int     `json:"trades"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"win_rate"`
	Expectancy float64 `json:"expectancy"`
	Eligible   bool    `json:"eligible"`
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var body T
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body
}

func TestStrategyParamsAndTiers(t *testing.T) {
	rec := serve(t, &mock.Store{}, "/strategy")
	want := `{"entry_atr":0.5,"target_atr":1,"stop_atr":2,"max_hold":5,"oversold":10,"cost_pct":0.03,"min_trades":5,"min_volume":5000000,"warmup":200}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("strategy: got %d %s", rec.Code, rec.Body.String())
	}
	rec = serve(t, &mock.Store{}, "/strategy/tiers")
	want = `[{"label":"forte","oversold":10,"rule":"RSI(2) < 10"},{"label":"médio","oversold":30,"rule":"RSI(2) < 30"},` +
		`{"label":"fraco","oversold":50,"rule":"RSI(2) < 50"},{"label":"tendência","oversold":101,"rule":"sem recuo"}]`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("tiers: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnalysisIsWhatRecommendComputes(t *testing.T) {
	store := strategyStore()
	rec := serve(t, store, "/tickers/win3/analysis")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	got := decode[analysisBody](t, rec)

	bars, err := store.TickerQuotes(t.Context(), "WIN3", now().AddDate(-3, 0, 0), strategyDay)
	if err != nil {
		t.Fatal(err)
	}
	want := swing.Analyze("WIN3", bars, swing.DefaultParams())
	if !got.Signal || got.Levels == nil || got.Levels.MaxEntry != want.MaxEntry || got.Levels.Target != want.Target ||
		got.Levels.Stop != want.Stop || got.Trades != want.Trades || got.Wins != want.Wins || !got.Eligible ||
		got.Tier != "forte" || got.AsOf != "2026-10-30" {
		t.Errorf("got %+v (levels %+v)\nwant %+v", got, got.Levels, want)
	}
	if want.Trades != 5 {
		t.Fatalf("fixture has %d trades, want 5", want.Trades)
	}
}

func TestAnalysisTiers(t *testing.T) {
	cases := []struct {
		path   string
		signal bool
		tier   string
	}{
		{"/tickers/MED3/analysis", false, "forte"},
		{"/tickers/MED3/analysis?tier=medio", true, "médio"},
		{"/tickers/MED3/analysis?tier=M%C3%A9dio", true, "médio"},
		{"/tickers/FLAT3/analysis?tier=tendencia", true, "tendência"},
	}
	for _, c := range cases {
		rec := serve(t, strategyStore(), c.path)
		got := decode[analysisBody](t, rec)
		if rec.Code != http.StatusOK || got.Signal != c.signal || got.Tier != c.tier || (got.Levels != nil) != c.signal {
			t.Errorf("%s: got %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestStrategyErrors(t *testing.T) {
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/tickers/ZZZZ3/analysis", 404, `{"error":"ticker ZZZZ3 not found"}`},
		{"/tickers/XPML11/analysis", 422, `{"error":"XPML11 is not a stock (spec CI): the strategy runs only on ON, PN and UNT"}`},
		{"/tickers/XPML11/trades", 422, `{"error":"XPML11 is not a stock (spec CI): the strategy runs only on ON, PN and UNT"}`},
		{"/tickers/OLD3/analysis?years=1", 404, `{"error":"no quotes for OLD3 since 2025-11-04"}`},
		{"/tickers/WIN3/analysis?tier=forte2", 400, `{"error":"unknown tier \"forte2\": use forte, médio, fraco or tendência"}`},
		{"/tickers/WIN3/trades?years=0", 400, `{"error":"years \"0\" must be a whole number from 1 to 30"}`},
		{"/signals?years=31", 400, `{"error":"years \"31\" must be a whole number from 1 to 30"}`},
		{"/signals?years=two", 400, `{"error":"years \"two\" must be a whole number from 1 to 30"}`},
		{"/signals?tier=x", 400, `{"error":"unknown tier \"x\": use forte, médio, fraco or tendência"}`},
	}
	for _, c := range cases {
		rec := serve(t, strategyStore(), c.path)
		if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s: got %d %s\nwant %d %s", c.path, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
	if rec := serve(t, &mock.Store{}, "/signals"); rec.Code != http.StatusNotFound {
		t.Errorf("signals of an empty store: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestTradesAreTheTrackRecord(t *testing.T) {
	rec := serve(t, strategyStore(), "/tickers/WIN3/trades")
	got := decode[[]struct {
		SignalDate string  `json:"signal_date"`
		EntryDate  string  `json:"entry_date"`
		ExitDate   string  `json:"exit_date"`
		Return     float64 `json:"return"`
	}](t, rec)
	if rec.Code != http.StatusOK || len(got) != 5 {
		t.Fatalf("got %d %s, want the 5 trades of the fixture", rec.Code, rec.Body.String())
	}
	for i, tr := range got {
		if tr.SignalDate >= tr.EntryDate || tr.EntryDate > tr.ExitDate || tr.Return <= 0 || (i > 0 && got[i-1].ExitDate > tr.SignalDate) {
			t.Errorf("trade %d out of order or losing: %+v", i, tr)
		}
	}
	if rec := serve(t, strategyStore(), "/tickers/FLAT3/trades"); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("no trades: got %s, want []", rec.Body.String())
	}
}

func TestSignals(t *testing.T) {
	rec := serve(t, strategyStore(), "/signals?tier=m%C3%A9dio")
	got := decode[struct {
		Session string         `json:"session"`
		Tier    string         `json:"tier"`
		Rule    string         `json:"rule"`
		Signals []analysisBody `json:"signals"`
	}](t, rec)
	tickers := make([]string, 0, len(got.Signals))
	for _, s := range got.Signals {
		tickers = append(tickers, s.Ticker)
	}
	// OLD3 is off the latest session and XPML11 is not a stock; NEW3 is not eligible, so it's last.
	if rec.Code != http.StatusOK || got.Session != "2026-10-30" || got.Tier != "médio" || got.Rule != "RSI(2) < 30" ||
		strings.Join(tickers, ",") != "MED3,WIN3,NEW3" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	for i, s := range got.Signals {
		if s.Eligible != (s.Ticker != "NEW3") || s.Levels == nil || (i > 0 && s.Eligible && s.Expectancy > got.Signals[i-1].Expectancy) {
			t.Errorf("signal %d: %+v, want eligible signals by expectancy, then the rest", i, s)
		}
	}
	forte := decode[struct {
		Signals []analysisBody `json:"signals"`
	}](t, serve(t, strategyStore(), "/signals"))
	if len(forte.Signals) != 2 || forte.Signals[0].Ticker != "WIN3" || !forte.Signals[0].Eligible ||
		forte.Signals[1].Ticker != "NEW3" || forte.Signals[1].Eligible {
		t.Errorf("forte: got %+v, want WIN3 (eligible) then NEW3 (not eligible)", forte.Signals)
	}
}

func TestStrategyHidesStoreErrors(t *testing.T) {
	store := strategyStore()
	store.ReadErr = errors.New("connection refused")
	store.HistoryErr = errors.New("connection refused")
	for _, path := range []string{"/tickers/WIN3/analysis", "/tickers/WIN3/trades", "/signals"} {
		if rec := serve(t, store, path); rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d %s, want 500", path, rec.Code, rec.Body.String())
		}
	}
	mux := newMux(failQuotes{strategyStore()}, now, nil, nil)
	for _, path := range []string{"/tickers/WIN3/analysis", "/tickers/WIN3/trades"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s with failing quotes: got %d", path, rec.Code)
		}
	}
}
