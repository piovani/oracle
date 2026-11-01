// Package contract holds behavior every implementation of a contract must share.
//
// The same suite runs against the real implementation in the integration tests and against the
// mock in the unit tests, so a mock cannot drift from what it stands in for.
package contract

import (
	"errors"
	"slices"
	"testing"
	"time"

	"oracle/internal/quote"
	"oracle/test/fixture"
)

// NewStore returns an empty, migrated store for one test.
type NewStore func(t *testing.T) quote.Store

// Store checks the behavior of a quote.Store implementation.
func Store(t *testing.T, newStore NewStore) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, NewStore)
	}{
		{"migrate twice", migrateTwice},
		{"save a file", saveFile},
		{"save an empty file", saveEmptyFile},
		{"a failed file leaves nothing behind", failedFile},
		{"a quote already stored is kept", keepStored},
		{"history loads stocks since a day, in date order", history},
		{"history of an empty store", emptyHistory},
		{"tickers summarize every spec from the latest quote", tickers},
		{"one ticker, or ErrUnknownTicker", oneTicker},
		{"ticker quotes between two days, in date order", tickerQuotes},
		{"a session has every quote of the day, in ticker order", session},
		{"latest session", latestSession},
		{"reads of an empty store", emptyReads},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore) })
	}
}

var day = fixture.Day(2024, 1, 2)

func bar(ticker, spec string, d time.Time, price float64) quote.Quote {
	return quote.Quote{Ticker: ticker, Spec: spec, Date: d, Open: price, High: price, Low: price, Close: price, Volume: 1000}
}

// load passes the quotes to save, the way a B3 download does.
func load(quotes ...quote.Quote) func(func(quote.Quote) error) error {
	return func(save func(quote.Quote) error) error {
		for _, q := range quotes {
			if err := save(q); err != nil {
				return err
			}
		}
		return nil
	}
}

func migrateTwice(t *testing.T, newStore NewStore) {
	if err := newStore(t).Migrate(t.Context()); err != nil {
		t.Errorf("second migration: %v", err)
	}
}

func saveFile(t *testing.T, newStore NewStore) {
	s := newStore(t)
	n, err := s.SaveFile(t.Context(), "A.ZIP", load(bar("AAA3", "ON", day, 10), bar("BBB4", "PN", day, 20)))
	if err != nil || n != 2 {
		t.Fatalf("got %d, %v, want 2 quotes saved", n, err)
	}
	if got := synced(t, s); !slices.Equal(got, []string{"A.ZIP"}) {
		t.Errorf("synced %v", got)
	}
	if got := all(t, s); len(got) != 2 {
		t.Errorf("stored %+v", got)
	}
}

func saveEmptyFile(t *testing.T, newStore NewStore) {
	s := newStore(t)
	if n, err := s.SaveFile(t.Context(), "EMPTY.ZIP", load()); err != nil || n != 0 {
		t.Fatalf("got %d, %v", n, err)
	}
	if got := synced(t, s); !slices.Equal(got, []string{"EMPTY.ZIP"}) {
		t.Errorf("synced %v, want the empty file recorded", got)
	}
}

func failedFile(t *testing.T, newStore NewStore) {
	s := newStore(t)
	fail := errors.New("download cut short")
	// More quotes than one write batch, so the failure comes after some were already written.
	bars := fixture.Trend("AAA3", 1500, day, nil)
	_, err := s.SaveFile(t.Context(), "A.ZIP", func(save func(quote.Quote) error) error {
		if err := load(bars...)(save); err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("got %v, want the load error", err)
	}
	if got := all(t, s); len(got) != 0 {
		t.Errorf("stored %d quotes of a failed file", len(got))
	}
	if got := synced(t, s); len(got) != 0 {
		t.Errorf("synced %v, want the failed file to be tried again", got)
	}
}

func keepStored(t *testing.T, newStore NewStore) {
	s := newStore(t)
	if _, err := s.SaveFile(t.Context(), "A.ZIP", load(bar("AAA3", "ON", day, 10))); err != nil {
		t.Fatal(err)
	}
	n, err := s.SaveFile(t.Context(), "B.ZIP", load(bar("AAA3", "ON", day, 99), bar("AAA3", "ON", day.AddDate(0, 0, 1), 11)))
	if err != nil || n != 2 {
		t.Fatalf("got %d, %v", n, err)
	}
	if got := all(t, s); len(got) != 2 || got[0].Close != 10 || got[1].Close != 11 {
		t.Errorf("stored %+v, want the first copy of the repeated day and the new one", got)
	}
}

func history(t *testing.T, newStore NewStore) {
	s := newStore(t)
	quotes := []quote.Quote{
		bar("AAA3", "ON      NM", day.AddDate(0, 0, 2), 12), // saved out of order on purpose
		bar("AAA3", "ON      NM", day, 10),
		bar("AAA3", "ON      NM", day.AddDate(0, 0, -1), 9), // before the cutoff
		bar("BBB4", "PN      N1", day, 20),
		bar("CCC5", "PNA     N1", day, 30),
		bar("DDD11", "UNT     N2", day, 40),
		bar("XPML11", "CI", day, 100),    // real estate fund
		bar("AAPL34", "DRN", day, 50),    // BDR
		bar("BOVA11", "CI  ER", day, 60), // ETF
	}
	if _, err := s.SaveFile(t.Context(), "A.ZIP", load(quotes...)); err != nil {
		t.Fatal(err)
	}

	got, err := s.History(t.Context(), day)
	if err != nil {
		t.Fatal(err)
	}
	if tickers := tickersOf(got); !slices.Equal(tickers, []string{"AAA3", "BBB4", "CCC5", "DDD11"}) {
		t.Errorf("tickers %v, want only the stocks", tickers)
	}
	if aaa := got["AAA3"]; len(aaa) != 2 || !aaa[0].Date.Equal(day) || aaa[0].Close != 10 || !aaa[1].Date.Equal(day.AddDate(0, 0, 2)) {
		t.Errorf("AAA3 %+v, want the 2 bars on or after the cutoff, in date order", aaa)
	}
}

func emptyHistory(t *testing.T, newStore NewStore) {
	got, err := newStore(t).History(t.Context(), day)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %v, %v, want an empty map", got, err)
	}
}

const (
	aaa     = "AAA3"
	aaaName = "AAA SA"
)

// market is two tickers over three sessions, saved out of order, with a renamed company.
func market(t *testing.T, s quote.Store) {
	t.Helper()
	old := bar(aaa, "ON", day.AddDate(0, 0, -1), 9)
	old.Name, old.ISIN = "OLD NAME", "BRAAAAACNOR0"
	latest := bar(aaa, "ON      NM", day.AddDate(0, 0, 1), 11)
	latest.Name, latest.ISIN = aaaName, "BRAAAAACNOR1"
	quotes := []quote.Quote{
		latest,
		bar("XPML11", "CI", day, 100), // not a stock: still listed
		old,
		bar(aaa, "ON      NM", day, 10),
	}
	if _, err := s.SaveFile(t.Context(), "A.ZIP", load(quotes...)); err != nil {
		t.Fatal(err)
	}
}

func tickers(t *testing.T, newStore NewStore) {
	s := newStore(t)
	market(t, s)
	got, err := s.Tickers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []quote.Ticker{
		{Ticker: aaa, Name: aaaName, Spec: "ON      NM", ISIN: "BRAAAAACNOR1", First: day.AddDate(0, 0, -1), Last: day.AddDate(0, 0, 1), Sessions: 3},
		{Ticker: "XPML11", Spec: "CI", First: day, Last: day, Sessions: 1},
	}
	if !slices.EqualFunc(got, want, sameTicker) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func oneTicker(t *testing.T, newStore NewStore) {
	s := newStore(t)
	market(t, s)
	got, err := s.Ticker(t.Context(), aaa)
	if err != nil || got.Name != aaaName || got.Sessions != 3 || !got.Last.Equal(day.AddDate(0, 0, 1)) {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := s.Ticker(t.Context(), "ZZZZ3"); !errors.Is(err, quote.ErrUnknownTicker) {
		t.Errorf("unknown ticker: got %v, want ErrUnknownTicker", err)
	}
}

func tickerQuotes(t *testing.T, newStore NewStore) {
	s := newStore(t)
	market(t, s)
	cases := []struct {
		name     string
		from, to time.Time
		want     []float64 // closes
	}{
		{"open range", time.Time{}, time.Time{}, []float64{9, 10, 11}},
		{"from a day", day, time.Time{}, []float64{10, 11}},
		{"until a day", time.Time{}, day, []float64{9, 10}},
		{"one day", day, day, []float64{10}},
		{"no session in range", day.AddDate(0, 0, 5), time.Time{}, nil},
	}
	for _, c := range cases {
		got, err := s.TickerQuotes(t.Context(), aaa, c.from, c.to)
		if err != nil {
			t.Fatal(err)
		}
		if closes := closesOf(got); !slices.Equal(closes, c.want) {
			t.Errorf("%s: closes %v, want %v", c.name, closes, c.want)
		}
	}
	if got, err := s.TickerQuotes(t.Context(), "ZZZZ3", time.Time{}, time.Time{}); err != nil || len(got) != 0 {
		t.Errorf("unknown ticker: got %v, %v, want no quotes", got, err)
	}
}

func session(t *testing.T, newStore NewStore) {
	s := newStore(t)
	market(t, s)
	got, err := s.Session(t.Context(), day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ticker != aaa || got[0].Close != 10 || got[1].Ticker != "XPML11" {
		t.Errorf("got %+v, want AAA3 then XPML11 on the day", got)
	}
	if got, err := s.Session(t.Context(), day.AddDate(0, 0, 3)); err != nil || len(got) != 0 {
		t.Errorf("day without quotes: got %v, %v", got, err)
	}
}

func latestSession(t *testing.T, newStore NewStore) {
	s := newStore(t)
	market(t, s)
	if got, err := s.LatestSession(t.Context()); err != nil || !got.Equal(day.AddDate(0, 0, 1)) {
		t.Errorf("got %v, %v, want %v", got, err, day.AddDate(0, 0, 1))
	}
}

func emptyReads(t *testing.T, newStore NewStore) {
	s := newStore(t)
	if got, err := s.Tickers(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("tickers: got %v, %v", got, err)
	}
	if got, err := s.LatestSession(t.Context()); err != nil || !got.IsZero() {
		t.Errorf("latest session: got %v, %v, want the zero time", got, err)
	}
}

func sameTicker(a, b quote.Ticker) bool {
	return a.Ticker == b.Ticker && a.Name == b.Name && a.Spec == b.Spec && a.ISIN == b.ISIN &&
		a.First.Equal(b.First) && a.Last.Equal(b.Last) && a.Sessions == b.Sessions
}

func closesOf(quotes []quote.Quote) []float64 {
	closes := make([]float64, 0, len(quotes))
	for _, q := range quotes {
		closes = append(closes, q.Close)
	}
	return closes
}

func synced(t *testing.T, s quote.Store) []string {
	t.Helper()
	names, err := s.SyncedFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	return names
}

// all is every stored stock, by ticker and date.
func all(t *testing.T, s quote.Store) []quote.Quote {
	t.Helper()
	got, err := s.History(t.Context(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var quotes []quote.Quote
	for _, ticker := range tickersOf(got) {
		quotes = append(quotes, got[ticker]...)
	}
	return quotes
}

func tickersOf(history map[string][]quote.Quote) []string {
	tickers := make([]string, 0, len(history))
	for ticker := range history {
		tickers = append(tickers, ticker)
	}
	slices.Sort(tickers)
	return tickers
}
