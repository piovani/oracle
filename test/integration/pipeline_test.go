//go:build integration

package integration

import (
	"reflect"
	"testing"

	"oracle/internal/backtest"
	"oracle/internal/quote"
	"oracle/internal/swing"
)

// TestPipelineMatchesInMemoryData syncs quotes through B3 files and Postgres, then checks that the
// strategy sees exactly what it would see with the same quotes in memory: storing and loading must
// not change a price, a date or the order of the bars.
func TestPipelineMatchesInMemoryData(t *testing.T) {
	db, _ := newDatabase(t)
	last := day(2021, 12, 30)
	want := map[string][]quote.Quote{
		"WIN3":  trend("WIN3", 500, last, dips(8), true),
		"FLAT3": trend("FLAT3", 500, last, nil, false),
	}
	_, client := newFakeB3(t, map[string]file{a2020: {records: append(records(want["WIN3"]), records(want["FLAT3"])...)}})

	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}
	history, err := quote.History(t.Context(), db, day(2000, 1, 1))
	if err != nil {
		t.Fatal(err)
	}

	p := swing.DefaultParams()
	for ticker, bars := range want {
		if got, exp := swing.Analyze(ticker, history[ticker], p), swing.Analyze(ticker, bars, p); got != exp {
			t.Errorf("%s: analysis from the database %+v, from memory %+v", ticker, got, exp)
		}
	}
	if a := swing.Analyze("WIN3", history["WIN3"], p); !a.Signal || a.Trades != 8 || a.Wins != 8 {
		t.Errorf("WIN3: got %+v, want 8 winning trades and a signal today", a)
	}

	cfg := backtest.Config{Params: p, Capital: 1000, Positions: 2, RiskPct: 2, From: want["WIN3"][300].Date, Rank: backtest.RankExpectancy}
	got, err := backtest.Run(history, cfg)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := backtest.Run(want, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Trades) < 3 {
		t.Errorf("got %d trades, want the dips after the track record is built", len(got.Trades))
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("backtest from the database differs from memory:\n%+v\n%+v", got.Summary(), exp.Summary())
	}
}
