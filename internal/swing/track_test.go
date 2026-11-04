package swing

import (
	"slices"
	"testing"

	"oracle/internal/quote"
	"oracle/test/fixture"
)

var trackDay = fixture.Day(2026, 10, 30)

func TestTrackRecordIsWhatAnalyzeCounts(t *testing.T) {
	p := DefaultParams()
	bars := fixture.Trend("WIN3", 400, trackDay, fixture.Dips(5), fixture.StrongPullback...)
	records := p.TrackRecord(bars)
	a := Analyze("WIN3", bars, p)

	sum, wins := 0.0, 0
	for _, r := range records {
		sum += r.Return
		if r.Return > 0 {
			wins++
		}
	}
	if len(records) != a.Trades || wins != a.Wins || sum != a.SumReturn {
		t.Fatalf("track record has %d trades, %d wins, sum %v; Analyze counts %d, %d, %v",
			len(records), wins, sum, a.Trades, a.Wins, a.SumReturn)
	}
	for i, r := range records {
		dip := fixture.Dips(5)[i]
		if !r.SignalDay.Equal(bars[dip].Date) || !r.EntryDay.Equal(bars[dip+1].Date) || !r.ExitDay.Equal(bars[r.ExitIdx].Date) {
			t.Errorf("trade %d: days %v %v %v, want signal on dip %d and entry the next day", i, r.SignalDay, r.EntryDay, r.ExitDay, dip)
		}
	}
}

func TestTrackRecordUsesOnlyTheSeriesAfterTheLastSplit(t *testing.T) {
	p := DefaultParams()
	bars := fixture.Trend("SPL3", 400, trackDay, fixture.Dips(5))
	for i := 300; i < len(bars); i++ { // a 1:2 split after the third dip
		b := &bars[i]
		b.Open, b.High, b.Low, b.Close, b.Average = b.Open/2, b.High/2, b.Low/2, b.Close/2, b.Average/2
	}
	if got := p.TrackRecord(bars); got != nil {
		t.Errorf("got %d trades, want none: the 100 sessions after the split are too few", len(got))
	}
}

func TestTrackRecordNeedsLiquidity(t *testing.T) {
	p := DefaultParams()
	bars := fixture.Trend("ILQ3", 400, trackDay, fixture.Dips(5))
	for i := range bars {
		bars[i].Volume = p.MinVolume / 2
	}
	if got := p.TrackRecord(bars); got != nil {
		t.Errorf("got %d trades for an illiquid stock", len(got))
	}
}

func TestScanAnalyzesStocksOnTheLatestSession(t *testing.T) {
	history := map[string][]quote.Quote{
		"WIN3": fixture.Trend("WIN3", 400, trackDay, fixture.Dips(5), fixture.StrongPullback...),
		"AAA3": fixture.Trend("AAA3", 400, trackDay, nil),
		"OLD3": fixture.Trend("OLD3", 400, trackDay.AddDate(0, 0, -3), fixture.Dips(5), fixture.StrongPullback...),
		"NIL3": nil,
	}
	got := Scan(history, trackDay, DefaultParams())
	tickers := make([]string, 0, len(got))
	for _, a := range got {
		tickers = append(tickers, a.Ticker)
	}
	if !slices.Equal(tickers, []string{"AAA3", "WIN3"}) {
		t.Fatalf("tickers %v, want those on the latest session, in ticker order", tickers)
	}
	if got[0].Signal || !got[1].Signal || got[1].Trades != 5 {
		t.Errorf("got %+v", got)
	}
}

func TestTrackRecordSkipsSignalsWhileATradeIsOpen(t *testing.T) {
	p := DefaultParams()
	bars := fixture.Trend("TWO3", 400, trackDay, []int{220, 221, 222, 300})
	if !p.Signal(bars, 220) || !p.Signal(bars, 221) {
		t.Fatal("fixture: want signals on two days in a row")
	}
	records := p.TrackRecord(bars)
	if len(records) == 0 {
		t.Fatal("no trades")
	}
	for i := 1; i < len(records); i++ {
		if records[i].SignalDay.Before(records[i-1].ExitDay) {
			t.Errorf("trade %d signals on %v, before trade %d exits on %v", i, records[i].SignalDay, i-1, records[i-1].ExitDay)
		}
	}
	if a := Analyze("TWO3", bars, p); a.Trades != len(records) {
		t.Errorf("Analyze counts %d trades, the track record has %d", a.Trades, len(records))
	}
}
