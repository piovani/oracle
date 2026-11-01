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
