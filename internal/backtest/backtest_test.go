package backtest

import (
	"math"
	"testing"
	"time"

	"oracle/internal/quote"
	"oracle/internal/swing"
)

var start = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// uptrend returns 250 rising bars, then the given bars, then flat bars so every trade can finish.
func uptrend(ticker string, last ...quote.Quote) []quote.Quote {
	var bars []quote.Quote
	add := func(b quote.Quote) {
		b.Ticker = ticker
		b.Date = start.AddDate(0, 0, len(bars))
		b.Volume = 1e8
		bars = append(bars, b)
	}
	for i := 0; i < 250; i++ {
		c := 10 + 0.04*float64(i)
		add(quote.Quote{Open: c - 0.02, High: c + 0.1, Low: c - 0.1, Close: c})
	}
	for _, b := range last {
		add(b)
	}
	c := bars[len(bars)-1].Close
	for i := 0; i < 6; i++ {
		add(quote.Quote{Open: c, High: c + 0.1, Low: c - 0.1, Close: c})
	}
	return bars
}

func config(from time.Time) Config {
	p := swing.DefaultParams()
	p.MinVolume = 1e6
	return Config{Params: p, Capital: 1000, Positions: 2, RiskPct: 2, From: from, Rank: RankRSI}
}

// Two down days after the uptrend trigger the signal; the next day dips to the limit and closes above the target.
var winner = []quote.Quote{
	{Open: 19.9, High: 19.95, Low: 19.5, Close: 19.56},
	{Open: 19.6, High: 20.5, Low: 19.3, Close: 20.4},
}

func TestRunFillsAndSettlesATrade(t *testing.T) {
	bars := uptrend("WIN3", winner...)
	res, err := Run(map[string][]quote.Quote{"WIN3": bars}, config(start.AddDate(0, 0, 250)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 1 || res.Fills != 1 || len(res.Trades) != 1 {
		t.Fatalf("got %d orders, %d fills, %d trades, want 1 each", res.Orders, res.Fills, len(res.Trades))
	}
	tr := res.Trades[0]
	if !tr.Entry.Equal(start.AddDate(0, 0, 251)) || !tr.Exit.Equal(tr.Entry) || tr.Return <= 0 {
		t.Errorf("unexpected trade %+v", tr)
	}
	end := res.Days[len(res.Days)-1].Equity
	if math.Abs(end-(1000+tr.PnL)) > 1e-6 {
		t.Errorf("final equity %.4f, want %.4f", end, 1000+tr.PnL)
	}
}

func TestRunKeepsDelistedTickers(t *testing.T) {
	// The series ends right after the entry, so the position is closed at the last close instead of being dropped.
	bars := uptrend("GONE3", winner[0], quote.Quote{Open: 19.6, High: 19.7, Low: 19.3, Close: 19.4})
	bars = bars[:len(bars)-6]
	res, err := Run(map[string][]quote.Quote{"GONE3": bars}, config(start.AddDate(0, 0, 250)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || res.Trades[0].ExitPrice != 19.4 {
		t.Fatalf("got %+v, want one trade closed at 19.4", res.Trades)
	}
}

func TestRunDoesNotFillUnreachedLimit(t *testing.T) {
	missed := []quote.Quote{winner[0], {Open: 19.8, High: 20, Low: 19.7, Close: 19.9}}
	res, err := Run(map[string][]quote.Quote{"MISS3": uptrend("MISS3", missed...)}, config(start.AddDate(0, 0, 250)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 1 || res.Fills != 0 || len(res.Trades) != 0 {
		t.Errorf("got %d orders, %d fills, %d trades, want 1, 0, 0", res.Orders, res.Fills, len(res.Trades))
	}
	if end := res.Days[len(res.Days)-1].Equity; end != 1000 {
		t.Errorf("equity %.2f, want 1000", end)
	}
}

func TestRunIgnoresSignalsBeforeStart(t *testing.T) {
	bars := uptrend("OLD3", winner...)
	res, err := Run(map[string][]quote.Quote{"OLD3": bars}, config(start.AddDate(0, 0, 252)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 0 {
		t.Errorf("got %d orders, want none", res.Orders)
	}
}

func TestRunRespectsPositionLimit(t *testing.T) {
	cfg := config(start.AddDate(0, 0, 250))
	cfg.Positions = 1
	history := map[string][]quote.Quote{
		"AAA3": uptrend("AAA3", winner...),
		"BBB3": uptrend("BBB3", winner...),
	}
	res, err := Run(history, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 1 {
		t.Errorf("got %d orders with one slot, want 1", res.Orders)
	}
}

func TestSummary(t *testing.T) {
	res := Result{
		Initial: 100,
		Days: []Day{
			{Date: start, Equity: 100, Benchmark: 1},
			{Date: start.AddDate(0, 0, 100), Equity: 120, Benchmark: 1.1},
			{Date: start.AddDate(0, 0, 200), Equity: 90, Benchmark: 0.9},
		},
		Trades: []Executed{{Return: 0.1, PnL: 10, Exit: start}, {Return: -0.05, PnL: -5, Exit: start}},
		Orders: 4, Fills: 2,
	}
	s := res.Summary()
	if math.Abs(s.MaxDrawdown+0.25) > 1e-9 || math.Abs(s.Return+0.1) > 1e-9 {
		t.Errorf("got drawdown %.3f, return %.3f", s.MaxDrawdown, s.Return)
	}
	if s.FillRate != 0.5 || s.ProfitFactor != 2 || s.WinRate != 0.5 {
		t.Errorf("got fill %.2f, profit factor %.2f, win rate %.2f", s.FillRate, s.ProfitFactor, s.WinRate)
	}
	if years := res.Years(); len(years) != 1 || years[0].Trades != 2 {
		t.Errorf("got %+v", years)
	}
}
