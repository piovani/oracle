package backtest

import (
	"math"
	"slices"
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
	for i := range 250 {
		c := 10 + 0.04*float64(i)
		add(quote.Quote{Open: c - 0.02, High: c + 0.1, Low: c - 0.1, Close: c})
	}
	for _, b := range last {
		add(b)
	}
	c := bars[len(bars)-1].Close
	for range 6 {
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

func TestRunRejectsInvalidConfig(t *testing.T) {
	history := map[string][]quote.Quote{"WIN3": uptrend("WIN3", winner...)}
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"no positions", func(c *Config) { c.Positions = 0 }},
		{"zero capital", func(c *Config) { c.Capital = 0 }},
		{"negative capital", func(c *Config) { c.Capital = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config(start)
			tt.edit(&cfg)
			if _, err := Run(history, cfg); err == nil {
				t.Error("got no error")
			}
		})
	}
}

func TestRunWithoutData(t *testing.T) {
	if _, err := Run(nil, config(start)); err == nil {
		t.Error("empty history: got no error")
	}
	history := map[string][]quote.Quote{"WIN3": uptrend("WIN3", winner...)}
	if _, err := Run(history, config(start.AddDate(1, 0, 0))); err == nil {
		t.Error("start after the last quote: got no error")
	}
}

func TestRunHoldsUntilMaxHold(t *testing.T) {
	// Fills on the entry day, then never reaches the target nor the stop.
	// With a single slot, the portfolio stays full while the position is held.
	bars := uptrend("HOLD3", winner[0], quote.Quote{Open: 19.6, High: 19.7, Low: 19.3, Close: 19.5})
	cfg := config(start.AddDate(0, 0, 250))
	cfg.Positions = 1
	res, err := Run(map[string][]quote.Quote{"HOLD3": bars}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(res.Trades))
	}
	tr := res.Trades[0]
	if !tr.Exit.Equal(tr.Signal.AddDate(0, 0, 5)) || tr.ExitPrice != 19.5 {
		t.Errorf("got exit %s at %.2f, want the close 5 sessions after the signal", tr.Exit.Format(time.DateOnly), tr.ExitPrice)
	}
	// While held, the position is marked at the close.
	held := res.Days[2]
	if want := float64(tr.Qty) * 19.5; math.Abs(held.Invested-want) > 1e-9 {
		t.Errorf("invested %.2f on %s, want %.2f", held.Invested, held.Date.Format(time.DateOnly), want)
	}
}

func TestRunCountsUnfilledEntryAsPending(t *testing.T) {
	// AAA3 does not trade the day after its signal, so the order waits one more calendar day.
	aaa := uptrend("AAA3", winner...)
	for i := 251; i < len(aaa); i++ {
		aaa[i].Date = aaa[i].Date.AddDate(0, 0, 1)
	}
	history := map[string][]quote.Quote{"AAA3": aaa, "BBB3": uptrend("BBB3")}

	res, err := Run(history, config(start.AddDate(0, 0, 250)))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trades) != 1 || !res.Trades[0].Entry.Equal(start.AddDate(0, 0, 252)) {
		t.Fatalf("got %+v, want one trade entered after the gap", res.Trades)
	}
	gap := res.Days[1]
	if gap.Invested != 0 || math.Abs(gap.Equity-1000) > 1e-9 {
		t.Errorf("on the gap day got equity %.2f invested %.2f, want 1000 and 0", gap.Equity, gap.Invested)
	}
}

func TestRunExpectancyNeedsATrackRecord(t *testing.T) {
	cfg := config(start.AddDate(0, 0, 250))
	cfg.Rank = RankExpectancy
	res, err := Run(map[string][]quote.Quote{"WIN3": uptrend("WIN3", winner...)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 0 {
		t.Errorf("got %d orders for a ticker without closed trades, want 0", res.Orders)
	}
}

func TestRunSkipsShortSegments(t *testing.T) {
	// A split near the end leaves too few bars after it to produce signals.
	bars := uptrend("SPLT3", winner...)
	for i := len(bars) - 3; i < len(bars); i++ {
		bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = bars[i].Open/3, bars[i].High/3, bars[i].Low/3, bars[i].Close/3
	}
	res, err := Run(map[string][]quote.Quote{"SPLT3": bars}, config(start.AddDate(0, 0, 250)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Orders != 1 {
		t.Errorf("got %d orders, want only the one before the split", res.Orders)
	}
}

// liquidSeries is a series of 60 flat, liquid bars with the given closed trades.
func liquidSeries(ticker string, rated ...rated) *series {
	bars := make([]quote.Quote, 60)
	days := make([]int, 60)
	for i := range bars {
		bars[i] = quote.Quote{Ticker: ticker, Date: start.AddDate(0, 0, i), Open: 10, High: 10.5, Low: 9.5, Close: 10, Volume: 1e8}
		days[i] = i
	}
	return &series{ticker: ticker, bars: bars, days: days, rated: rated}
}

func TestRank(t *testing.T) {
	const d = 59
	today := start.AddDate(0, 0, d)
	win := func(exitDay int) rated {
		return rated{signal: start.AddDate(0, 0, exitDay-1), exitDay: exitDay, ret: 0.05}
	}
	tickers := func(picks []candidate) []string {
		out := make([]string, 0, len(picks))
		for _, c := range picks {
			out = append(out, c.s.ticker)
		}
		return out
	}
	expectancy := func() Config {
		cfg := config(start)
		cfg.Rank = RankExpectancy
		cfg.Params.MinTrades = 2
		return cfg
	}

	t.Run("RSI ranks lowest first, ties by ticker", func(t *testing.T) {
		signals := []candidate{
			{s: liquidSeries("CCC3"), i: d, rsi: 5},
			{s: liquidSeries("BBB3"), i: d, rsi: 1},
			{s: liquidSeries("AAA3"), i: d, rsi: 5},
		}
		got := tickers(rank(signals, nil, d, today, config(start)))
		if !slices.Equal(got, []string{"BBB3", "AAA3", "CCC3"}) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("skips held tickers", func(t *testing.T) {
		held := liquidSeries("AAA3")
		signals := []candidate{{s: held, i: d}, {s: liquidSeries("BBB3"), i: d}}
		got := tickers(rank(signals, []*position{{s: held}}, d, today, config(start)))
		if !slices.Equal(got, []string{"BBB3"}) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("skips illiquid tickers", func(t *testing.T) {
		thin := liquidSeries("THIN3")
		for i := range thin.bars {
			thin.bars[i].Volume = 1
		}
		if got := rank([]candidate{{s: thin, i: d}}, nil, d, today, config(start)); len(got) != 0 {
			t.Errorf("got %v", tickers(got))
		}
	})

	t.Run("expectancy ranks the best record first and drops ineligible ones", func(t *testing.T) {
		loss := rated{exitDay: 10, ret: -0.05}
		signals := []candidate{
			{s: liquidSeries("ONE3", win(10)), i: d},                    // too few trades
			{s: liquidSeries("BAD3", loss, loss), i: d},                 // negative expectancy
			{s: liquidSeries("GOOD3", win(10), win(20)), i: d},          // 2 wins
			{s: liquidSeries("BEST3", win(10), win(20), win(30)), i: d}, // 3 wins
		}
		got := tickers(rank(signals, nil, d, today, expectancy()))
		if !slices.Equal(got, []string{"BEST3", "GOOD3"}) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("expectancy ties go by ticker", func(t *testing.T) {
		signals := []candidate{
			{s: liquidSeries("BBB3", win(10), win(20)), i: d},
			{s: liquidSeries("AAA3", win(10), win(20)), i: d},
		}
		got := tickers(rank(signals, nil, d, today, expectancy()))
		if !slices.Equal(got, []string{"AAA3", "BBB3"}) {
			t.Errorf("got %v", got)
		}
	})
}

func TestRating(t *testing.T) {
	s := &series{ticker: "AAA3", rated: []rated{
		{signal: start, exitDay: 5, ret: 0.1},
		{signal: start.AddDate(1, 0, 0), exitDay: 400, ret: -0.05},
		{signal: start.AddDate(1, 0, 10), exitDay: 410, ret: 0.02},
	}}
	tests := []struct {
		name     string
		d        int
		cutoff   time.Time
		windowed bool
		trades   int
		sum      float64
	}{
		{"before any exit", 4, start, false, 0, 0},
		{"exit on the day counts", 5, start, false, 1, 0.1},
		{"open trades are not counted", 405, start, false, 2, 0.05},
		{"everything closed", 410, start, false, 3, 0.07},
		{"lookback drops older signals", 410, start.AddDate(0, 6, 0), true, 2, -0.03},
		{"cutoff is ignored without a lookback", 410, start.AddDate(0, 6, 0), false, 3, 0.07},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := s.rating(tt.d, tt.cutoff, tt.windowed)
			if a.Ticker != "AAA3" || !a.Signal || a.Trades != tt.trades || math.Abs(a.SumReturn-tt.sum) > 1e-9 {
				t.Errorf("got %+v, want %d trades summing %v", a, tt.trades, tt.sum)
			}
		})
	}
}

func TestRunLookbackDropsOldTrades(t *testing.T) {
	cfg := config(start)
	cfg.Lookback = 1
	cfg.Rank = RankExpectancy
	cfg.Params.MinTrades = 1
	s := liquidSeries("AAA3", rated{signal: start, exitDay: 1, ret: 0.1})
	today := start.AddDate(2, 0, 0)
	if got := rank([]candidate{{s: s, i: 59}}, nil, 59, today, cfg); len(got) != 0 {
		t.Errorf("got a pick rated by a trade older than the lookback")
	}
	cfg.Lookback = 0
	if got := rank([]candidate{{s: s, i: 59}}, nil, 59, today, cfg); len(got) != 1 {
		t.Errorf("without a lookback the old trade should count")
	}
}

func TestBuy(t *testing.T) {
	cfg := config(start)
	// Bar 20 is the signal, bar 21 dips to the limit and closes.
	fill := func(price, atrRange float64) *series {
		s := liquidSeries("AAA3")
		for i := range s.bars {
			s.bars[i].Open, s.bars[i].High, s.bars[i].Low, s.bars[i].Close = price, price+atrRange/2, price-atrRange/2, price
		}
		s.bars[21].Low = price - atrRange
		return s
	}

	t.Run("fills an order", func(t *testing.T) {
		pf := &portfolio{cash: 1000}
		var res Result
		pf.buy([]candidate{{s: fill(10, 1), i: 20}}, 1000, cfg, &res)
		if res.Orders != 1 || res.Fills != 1 || len(pf.open) != 1 {
			t.Fatalf("got %d orders, %d fills, %d open", res.Orders, res.Fills, len(pf.open))
		}
		// Slot 500 / 9.5 = 52; risk 20 / (9.5 - 8) = 13.
		pos := pf.open[0]
		if pos.qty != 13 || math.Abs(pf.cash-(1000-13*9.5)) > 1e-9 || pos.entryDay != 21 {
			t.Errorf("got qty %d, cash %.2f, entry day %d", pos.qty, pf.cash, pos.entryDay)
		}
	})

	t.Run("unfilled order is counted but not opened", func(t *testing.T) {
		s := fill(10, 1)
		s.bars[21].Low = 9.9
		pf := &portfolio{cash: 1000}
		var res Result
		pf.buy([]candidate{{s: s, i: 20}}, 1000, cfg, &res)
		if res.Orders != 1 || res.Fills != 0 || len(pf.open) != 0 || pf.cash != 1000 {
			t.Errorf("got %d orders, %d fills, %d open, cash %.2f", res.Orders, res.Fills, len(pf.open), pf.cash)
		}
	})

	tests := []struct {
		name string
		s    *series
		cash float64
	}{
		{"no volatility leaves no room for a stop", fill(10, 0), 1000},
		{"entry price at or below zero", fill(0.01, 0.1), 1000},
		{"not enough cash for one share", fill(10, 1), 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf := &portfolio{cash: tt.cash}
			var res Result
			pf.buy([]candidate{{s: tt.s, i: 20}}, 1000, cfg, &res)
			if res.Orders != 0 || len(pf.open) != 0 {
				t.Errorf("got %d orders, %d open, want none", res.Orders, len(pf.open))
			}
		})
	}
}

func TestSettle(t *testing.T) {
	s := liquidSeries("AAA3")
	dates := make([]time.Time, 60)
	for i := range dates {
		dates[i] = start.AddDate(0, 0, i)
	}
	early := &position{s: s, qty: 10, trade: swing.Trade{Entry: 10, Exit: 11, Return: 0.1}, signal: 1, entryDay: 2, exitDay: 3}
	late := &position{s: s, qty: 5, trade: swing.Trade{Entry: 10, Return: -0.05}, signal: 1, entryDay: 2, exitDay: 6}
	pf := &portfolio{cash: 0, open: []*position{early, late}}

	if closed := pf.settle(2, dates); len(closed) != 0 || len(pf.open) != 2 {
		t.Fatalf("day 2: got %d closed, %d open", len(closed), len(pf.open))
	}
	closed := pf.settle(3, dates)
	if len(closed) != 1 || len(pf.open) != 1 || pf.open[0] != late {
		t.Fatalf("day 3: got %d closed, %d open", len(closed), len(pf.open))
	}
	want := Executed{Ticker: "AAA3", Signal: dates[1], Entry: dates[2], Exit: dates[3], Qty: 10, EntryPrice: 10, ExitPrice: 11, Return: 0.1, PnL: 10}
	if got := closed[0]; got.Ticker != want.Ticker || !got.Signal.Equal(want.Signal) || !got.Entry.Equal(want.Entry) || !got.Exit.Equal(want.Exit) ||
		got.Qty != want.Qty || got.EntryPrice != want.EntryPrice || got.ExitPrice != want.ExitPrice || got.Return != want.Return || math.Abs(got.PnL-want.PnL) > 1e-9 {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if math.Abs(pf.cash-110) > 1e-9 {
		t.Errorf("cash %.2f, want 110", pf.cash)
	}
}

func TestBenchmark(t *testing.T) {
	segments := [][]quote.Quote{
		{{Date: start, Close: 10, Volume: 100}, {Date: start.AddDate(0, 0, 1), Close: 11, Volume: 100}, {Date: start.AddDate(0, 0, 2), Close: 11, Volume: 1}},
		{{Date: start, Close: 20, Volume: 100}, {Date: start.AddDate(0, 0, 1), Close: 19, Volume: 1}, {Date: start.AddDate(0, 0, 2), Close: 38, Volume: 100}},
	}
	dayOf := map[int64]int{start.Unix(): 0, start.AddDate(0, 0, 1).Unix(): 1, start.AddDate(0, 0, 2).Unix(): 2}

	got := benchmark(segments, dayOf, 3, 50)
	// Day 1: average of +10% and -5%. Day 2: both stocks were illiquid the day before.
	want := []float64{0, 0.025, 0}
	for d := range want {
		if math.Abs(got[d]-want[d]) > 1e-9 {
			t.Errorf("day %d: got %v, want %v", d, got[d], want[d])
		}
	}
}

func TestRateOwnTrades(t *testing.T) {
	p := swing.Params{TargetATR: 2, StopATR: 1.5, EntryATR: 0.5, MaxHold: 3}
	s := liquidSeries("AAA3")
	s.bars[21].Low = 9.4                                                                         // signal 20 fills, held to the max: exit at 23
	s.bars[26] = quote.Quote{Date: s.bars[26].Date, Open: 10, High: 12.5, Low: 9.4, Close: 12.2} // signal 25 hits the target
	for _, i := range []int{30, 31} {
		s.bars[i].Low = 9.8 // signal 30 never fills
	}
	s.signals = []int{20, 22, 25, 30}

	s.rateOwnTrades(p)
	if len(s.rated) != 2 {
		t.Fatalf("got %d rated trades, want 2: %+v", len(s.rated), s.rated)
	}
	if r := s.rated[0]; !r.signal.Equal(s.bars[20].Date) || r.exitDay != 23 {
		t.Errorf("first trade %+v, want signal on bar 20 and exit on day 23", r)
	}
	if r := s.rated[1]; !r.signal.Equal(s.bars[25].Date) || r.exitDay != 26 || r.ret <= 0 {
		t.Errorf("second trade %+v, want a win exiting on day 26", r)
	}
}

func TestSummaryEdgeCases(t *testing.T) {
	if s := (Result{Initial: 100}).Summary(); s != (Summary{}) {
		t.Errorf("no days: got %+v", s)
	}
	if y := (Result{Initial: 100}).Years(); y != nil {
		t.Errorf("no days: got %+v", y)
	}

	flatRes := Result{
		Initial: 100,
		Days:    []Day{{Date: start, Equity: 100, Benchmark: 1}, {Date: start.AddDate(0, 0, 1), Equity: 100, Benchmark: 1}},
		Trades:  []Executed{{Return: 0.1, PnL: 10, Exit: start}},
	}
	s := flatRes.Summary()
	if s.Sharpe != 0 || s.Volatility != 0 {
		t.Errorf("constant equity: got Sharpe %v, volatility %v", s.Sharpe, s.Volatility)
	}
	if s.ProfitFactor != 0 || s.FillRate != 0 || s.WinRate != 1 {
		t.Errorf("no losses and no orders: got profit factor %v, fill rate %v, win rate %v", s.ProfitFactor, s.FillRate, s.WinRate)
	}
}

func TestSummaryExposureAndBenchmark(t *testing.T) {
	res := Result{
		Initial: 100,
		Days: []Day{
			{Date: start, Equity: 100, Invested: 0, Benchmark: 1},
			{Date: start.AddDate(1, 0, 0), Equity: 100, Invested: 50, Benchmark: 1.2},
			{Date: start.AddDate(2, 0, 1), Equity: 121, Invested: 121, Benchmark: 0.9},
		},
	}
	s := res.Summary()
	if math.Abs(s.Exposure-0.5) > 1e-9 {
		t.Errorf("exposure %v, want 0.5", s.Exposure)
	}
	if math.Abs(s.Benchmark+0.1) > 1e-9 || math.Abs(s.BenchmarkDrawdown+0.25) > 1e-9 {
		t.Errorf("benchmark %v, drawdown %v, want -0.1 and -0.25", s.Benchmark, s.BenchmarkDrawdown)
	}
	if s.Trades != 0 || s.WinRate != 0 || s.AvgReturn != 0 {
		t.Errorf("no trades: got %+v", s)
	}
}

func TestYears(t *testing.T) {
	jan := func(year, day int) time.Time { return time.Date(year, 1, day, 0, 0, 0, 0, time.UTC) }
	res := Result{
		Initial: 100,
		Days: []Day{
			{Date: jan(2020, 2), Equity: 110, Benchmark: 1.1},
			{Date: jan(2020, 3), Equity: 120, Benchmark: 1.2},
			{Date: jan(2021, 4), Equity: 90, Benchmark: 0.9},
		},
		Trades: []Executed{
			{Exit: jan(2020, 3), Return: 0.1},
			{Exit: jan(2020, 3), Return: -0.02},
			{Exit: jan(2019, 1), Return: 0.5}, // outside the simulated days
		},
	}
	got := res.Years()
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 years", got)
	}
	y0, y1 := got[0], got[1]
	if y0.Year != 2020 || math.Abs(y0.Return-0.2) > 1e-9 || math.Abs(y0.Benchmark-0.2) > 1e-9 || y0.Trades != 2 || y0.WinRate != 0.5 || math.Abs(y0.AvgReturn-0.04) > 1e-9 {
		t.Errorf("2020: got %+v", y0)
	}
	// 2021 starts from the last equity of 2020.
	if y1.Year != 2021 || math.Abs(y1.Return+0.25) > 1e-9 || math.Abs(y1.Benchmark+0.25) > 1e-9 || y1.Trades != 0 || y1.WinRate != 0 || y1.AvgReturn != 0 {
		t.Errorf("2021: got %+v", y1)
	}
}

func TestAnnualize(t *testing.T) {
	tests := []struct {
		name         string
		total, years float64
		want         float64
	}{
		{"two years", 0.21, 2, 0.1},
		{"one year", 0.3, 1, 0.3},
		{"no time", 0.1, 0, 0},
		{"negative time", 0.1, -1, 0},
		{"total loss", -1, 2, 0},
		{"partial loss", -0.19, 2, -0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := annualize(tt.total, tt.years); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMeanStd(t *testing.T) {
	if m, s := meanStd(nil); m != 0 || s != 0 {
		t.Errorf("empty: got %v, %v", m, s)
	}
	if m, s := meanStd([]float64{2, 4, 4, 4, 5, 5, 7, 9}); m != 5 || s != 2 {
		t.Errorf("got %v, %v, want 5 and 2", m, s)
	}
}
