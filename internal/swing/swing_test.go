package swing

import (
	"math"
	"slices"
	"testing"

	"oracle/internal/quote"
	"oracle/internal/yahoo"
)

func flat(n int, price float64) []quote.Quote {
	bars := make([]quote.Quote, n)
	for i := range bars {
		bars[i] = quote.Quote{Open: price, High: price + 0.5, Low: price - 0.5, Close: price}
	}
	return bars
}

func TestSimulate(t *testing.T) {
	p := Params{TargetATR: 2, StopATR: 1.5, EntryATR: 0.5, MaxHold: 3}
	const i = 20

	tests := []struct {
		name     string
		next     []quote.Quote
		wantRet  float64
		wantExit int
		wantOK   bool
	}{
		{"target", []quote.Quote{{Open: 10, High: 12.5, Low: 9.4, Close: 12.2}}, 12/9.5 - 1, i + 1, true},
		{"limit not reached", []quote.Quote{{Open: 10, High: 10.5, Low: 9.6, Close: 10}}, 0, 0, false},
		{"stop on entry day", []quote.Quote{{Open: 10, High: 12.5, Low: 8, Close: 9}}, 8.5/9.5 - 1, i + 1, true},
		{"target touched on entry day is ignored", []quote.Quote{{Open: 10, High: 12.5, Low: 9.4, Close: 10}, {Open: 10, High: 10.5, Low: 9.5, Close: 10}, {Open: 10, High: 10.5, Low: 9.5, Close: 10.4}}, 10.4/9.5 - 1, i + 3, true},
		{"gap below limit enters at open", []quote.Quote{{Open: 9, High: 9.2, Low: 8.9, Close: 9.1}, {Open: 8, High: 8.2, Low: 7.9, Close: 8}}, 8.0/9 - 1, i + 2, true},
		{"open at stop", []quote.Quote{{Open: 8.5, High: 9, Low: 8, Close: 9}}, 0, 0, false},
		{"open below stop", []quote.Quote{{Open: 8, High: 9, Low: 7.5, Close: 9}}, 0, 0, false},
		{"low exactly at the limit fills", []quote.Quote{{Open: 10, High: 10.5, Low: 9.5, Close: 10}}, 10.0/9.5 - 1, i + 3, true},
		{"stop wins over target on entry day", []quote.Quote{{Open: 10, High: 12.5, Low: 8, Close: 12.2}}, 8.5/9.5 - 1, i + 1, true},
		{"stop after entry day", []quote.Quote{{Open: 10, High: 10.2, Low: 9.4, Close: 9.8}, {Open: 9, High: 9.2, Low: 8.4, Close: 8.6}}, 8.5/9.5 - 1, i + 2, true},
		{"gap below stop exits at open", []quote.Quote{{Open: 10, High: 10.2, Low: 9.4, Close: 9.8}, {Open: 8, High: 8.2, Low: 7.9, Close: 8}}, 8.0/9.5 - 1, i + 2, true},
		{"target after entry day", []quote.Quote{{Open: 10, High: 10.2, Low: 9.4, Close: 9.8}, {Open: 10, High: 12.3, Low: 9.9, Close: 12}}, 12/9.5 - 1, i + 2, true},
		{"gap above target exits at open", []quote.Quote{{Open: 10, High: 10.2, Low: 9.4, Close: 9.8}, {Open: 13, High: 13.5, Low: 12.8, Close: 13.2}}, 13/9.5 - 1, i + 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bars := flat(i+1, 10)
			bars = append(bars, tt.next...)
			bars = append(bars, flat(p.MaxHold, 10)...)

			trade, ok := p.Simulate(bars, i)
			if ok != tt.wantOK || trade.ExitIdx != tt.wantExit || math.Abs(trade.Return-tt.wantRet) > 1e-9 {
				t.Errorf("got (%v, %d, %v), want (%v, %d, %v)", trade.Return, trade.ExitIdx, ok, tt.wantRet, tt.wantExit, tt.wantOK)
			}
		})
	}
}

func TestContinuous(t *testing.T) {
	bars := append(flat(5, 40), flat(5, 20)...)
	if got := len(continuous(bars)); got != 5 {
		t.Errorf("got %d bars after split, want 5", got)
	}
}

func TestSimulateTruncatedSeries(t *testing.T) {
	p := Params{TargetATR: 2, StopATR: 1.5, EntryATR: 0.5, MaxHold: 5}
	const i = 20
	bars := flat(i+1, 10)
	bars = append(bars, quote.Quote{Open: 10, High: 10.5, Low: 9.4, Close: 10}, quote.Quote{Open: 10, High: 10.5, Low: 9.5, Close: 10.3})

	trade, ok := p.Simulate(bars, i)
	if !ok || trade.ExitIdx != i+2 || trade.Exit != 10.3 {
		t.Errorf("got %+v, %v, want exit at the last close", trade, ok)
	}
}

func TestSegments(t *testing.T) {
	bars := append(flat(5, 40), flat(5, 20)...)
	got := Segments(bars)
	if len(got) != 2 || len(got[0]) != 5 || len(got[1]) != 5 {
		t.Errorf("got %d segments, want 2 of 5 bars", len(got))
	}
}

func TestRank(t *testing.T) {
	p := Params{MinTrades: 10}
	candidate := func(ticker string, sum, price float64) Analysis {
		return Analysis{Ticker: ticker, Signal: true, Trades: 10, SumReturn: sum, Close: price, MaxEntry: price, Target: price * 1.1, Stop: price * 0.95}
	}
	all := []Analysis{
		candidate("BEST3", 0.4, 10),
		candidate("GOOD3", 0.3, 20),
		candidate("DOWN3", 0.5, 10),
		candidate("RICH3", 0.6, 300),
		candidate("LOSS3", -0.1, 10),
		{Ticker: "IDLE3", Trades: 10, SumReturn: 0.9},
	}
	estimates := map[string]yahoo.Estimate{
		"BEST3": {Target: 15},
		"GOOD3": {Target: 22},
		"DOWN3": {Target: 9},
		"RICH3": {Target: 600},
	}

	picks := Rank(all, estimates, p, 500, 2, 5)

	if len(picks) != 2 || picks[0].Ticker != "BEST3" || picks[1].Ticker != "GOOD3" {
		t.Fatalf("got %+v", picks)
	}
	if picks[0].Qty != 10 || picks[1].Qty != 5 {
		t.Errorf("got quantities %d and %d, want 10 and 5", picks[0].Qty, picks[1].Qty)
	}
}

// closes builds bars from closing prices, with a 1-point range around each close and the given volume.
func closes(volume float64, prices ...float64) []quote.Quote {
	bars := make([]quote.Quote, len(prices))
	for i, c := range prices {
		bars[i] = quote.Quote{Open: c, High: c + 0.5, Low: c - 0.5, Close: c, Volume: volume}
	}
	return bars
}

// rising returns n closes going up 0.04 a day from 10.
func rising(n int) []float64 {
	prices := make([]float64, n)
	for i := range prices {
		prices[i] = 10 + 0.04*float64(i)
	}
	return prices
}

func TestDefaultParams(t *testing.T) {
	want := Params{TargetATR: 1, StopATR: 2, EntryATR: 0.5, MaxHold: 5, Oversold: 10, CostPct: 0.03, MinTrades: 5, MinVolume: 5_000_000}
	if got := DefaultParams(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestTiersGoFromStrongestToWeakest(t *testing.T) {
	for i := 1; i < len(Tiers); i++ {
		if Tiers[i].Oversold <= Tiers[i-1].Oversold {
			t.Errorf("tier %q (%v) is not weaker than %q (%v)", Tiers[i].Label, Tiers[i].Oversold, Tiers[i-1].Label, Tiers[i-1].Oversold)
		}
	}
	if last := Tiers[len(Tiers)-1]; last.Oversold <= 100 {
		t.Errorf("last tier %q must accept any RSI, got %v", last.Label, last.Oversold)
	}
}

func TestAnalysisStats(t *testing.T) {
	tests := []struct {
		name                     string
		a                        Analysis
		winRate, avg, expectancy float64
	}{
		{"no trades", Analysis{}, 0, 0, 0},
		{"winning record", Analysis{Trades: 10, Wins: 7, SumReturn: 0.4}, 0.7, 0.04, 0.02},
		{"losing record", Analysis{Trades: 5, Wins: 1, SumReturn: -0.3}, 0.2, -0.06, -0.02},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.WinRate(); math.Abs(got-tt.winRate) > 1e-9 {
				t.Errorf("win rate %v, want %v", got, tt.winRate)
			}
			if got := tt.a.AvgReturn(); math.Abs(got-tt.avg) > 1e-9 {
				t.Errorf("average return %v, want %v", got, tt.avg)
			}
			if got := tt.a.Expectancy(); math.Abs(got-tt.expectancy) > 1e-9 {
				t.Errorf("expectancy %v, want %v", got, tt.expectancy)
			}
		})
	}
}

func TestEligible(t *testing.T) {
	p := Params{MinTrades: 5}
	tests := []struct {
		name string
		a    Analysis
		want bool
	}{
		{"eligible", Analysis{Signal: true, Trades: 5, SumReturn: 0.1}, true},
		{"no signal today", Analysis{Trades: 5, SumReturn: 0.1}, false},
		{"too few trades", Analysis{Signal: true, Trades: 4, SumReturn: 0.1}, false},
		{"zero expectancy", Analysis{Signal: true, Trades: 5}, false},
		{"negative expectancy", Analysis{Signal: true, Trades: 5, SumReturn: -0.1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Eligible(p); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPickAmounts(t *testing.T) {
	p := Pick{MaxEntry: 10, Target: 11.5, Stop: 9, Qty: 4}
	if got := p.Invested(); got != 40 {
		t.Errorf("invested %v, want 40", got)
	}
	if got := p.Gain(); math.Abs(got-6) > 1e-9 {
		t.Errorf("gain %v, want 6", got)
	}
	if got := p.Loss(); got != 4 {
		t.Errorf("loss %v, want 4", got)
	}
}

func TestRSI(t *testing.T) {
	tests := []struct {
		name   string
		prices []float64
		want   float64
	}{
		{"only gains", []float64{10, 11, 12}, 100},
		{"only losses", []float64{12, 11, 10}, 0},
		{"no change", []float64{10, 10, 10}, 50},
		{"mixed", []float64{10, 13, 12}, 75},
		{"only the last two changes count", []float64{1, 10, 10, 9}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bars := closes(0, tt.prices...)
			if got := RSI(bars, len(bars)-1); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSMA(t *testing.T) {
	bars := closes(0, 1, 2, 3, 4, 5)
	if got := sma(bars, 4, 2); got != 4.5 {
		t.Errorf("got %v, want 4.5", got)
	}
	if got := sma(bars, 4, 5); got != 3 {
		t.Errorf("got %v, want 3", got)
	}
}

func TestATR(t *testing.T) {
	t.Run("high minus low", func(t *testing.T) {
		if got := atr(flat(atrLen+1, 10), atrLen); got != 1 {
			t.Errorf("got %v, want 1", got)
		}
	})
	t.Run("gap up uses the previous close", func(t *testing.T) {
		bars := flat(atrLen+1, 10)
		bars[atrLen] = quote.Quote{High: 13.5, Low: 12.5, Close: 13}
		// 13 bars of range 1, plus one bar whose true range is 13.5 - 10.
		if got, want := atr(bars, atrLen), (13+3.5)/atrLen; math.Abs(got-want) > 1e-9 {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("gap down uses the previous close", func(t *testing.T) {
		bars := flat(atrLen+1, 10)
		bars[atrLen] = quote.Quote{High: 7.5, Low: 6.5, Close: 7}
		if got, want := atr(bars, atrLen), (13+3.5)/atrLen; math.Abs(got-want) > 1e-9 {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestLevels(t *testing.T) {
	bars := flat(atrLen+1, 10) // ATR of 1
	got := DefaultParams().Levels(bars, atrLen)
	want := Levels{Close: 10, MaxEntry: 9.5, Target: 11, Stop: 8}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	bars = flat(atrLen+1, 10.333)
	if got := DefaultParams().Levels(bars, atrLen); got.MaxEntry != 9.83 || got.Target != 11.33 || got.Stop != 8.33 {
		t.Errorf("levels are not rounded to cents: %+v", got)
	}
}

func TestLiquid(t *testing.T) {
	p := Params{MinVolume: 100}
	tests := []struct {
		name   string
		volume float64
		n, i   int
		want   bool
	}{
		{"not enough bars", 1000, volumeLen, volumeLen - 2, false},
		{"exactly enough bars", 1000, volumeLen, volumeLen - 1, true},
		{"volume at the minimum", 100, volumeLen, volumeLen - 1, true},
		{"volume below the minimum", 99, volumeLen, volumeLen - 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bars := closes(tt.volume, make([]float64, tt.n)...)
			if got := p.Liquid(bars, tt.i); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("uses the median, not the mean", func(t *testing.T) {
		bars := closes(0, make([]float64, volumeLen)...)
		bars[0].Volume = 1e12 // one huge day does not make the stock liquid
		if p.Liquid(bars, volumeLen-1) {
			t.Error("got liquid, want illiquid")
		}
	})
}

func TestMedianVolume(t *testing.T) {
	odd := []quote.Quote{{Volume: 5}, {Volume: 1}, {Volume: 3}}
	if got := medianVolume(odd); got != 3 {
		t.Errorf("odd: got %v, want 3", got)
	}
	even := []quote.Quote{{Volume: 4}, {Volume: 1}, {Volume: 3}, {Volume: 2}}
	if got := medianVolume(even); got != 3 {
		t.Errorf("even: got %v, want the upper middle 3", got)
	}
}

func TestSignal(t *testing.T) {
	p := Params{Oversold: 10}
	up := rising(trendLen + 1)

	tests := []struct {
		name   string
		prices []float64
		i      int
		want   bool
	}{
		{"before the trend is known", append(rising(trendLen-2), 18, 17), trendLen - 1, false},
		{"rising, not oversold", up, trendLen, false},
		{"pullback in an uptrend", append(rising(trendLen), 17.5, 17), trendLen + 1, true},
		{"pullback below the trend", append(rising(trendLen), 13, 12), trendLen + 1, false},
		{"RSI at the threshold is not oversold", append(rising(trendLen-1), 17, 17.25, 15), trendLen + 1, false}, // RSI = 0.25 / 2.5 = 10
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bars := closes(0, tt.prices...)
			if got := p.Signal(bars, tt.i); got != tt.want {
				t.Errorf("got %v, want %v (RSI %.2f)", got, tt.want, RSI(bars, tt.i))
			}
		})
	}
}

func TestSimulateWithoutNextBar(t *testing.T) {
	p := Params{TargetATR: 2, StopATR: 1.5, EntryATR: 0.5, MaxHold: 3}
	bars := flat(21, 10)
	if trade, ok := p.Simulate(bars, len(bars)-1); ok {
		t.Errorf("got %+v, want no trade on the last bar", trade)
	}
}

func TestSimulateChargesCosts(t *testing.T) {
	p := Params{TargetATR: 2, StopATR: 1.5, EntryATR: 0.5, MaxHold: 3, CostPct: 0.5}
	const i = 20
	bars := append(flat(i+1, 10), quote.Quote{Open: 10, High: 12.5, Low: 9.4, Close: 12.2})

	trade, ok := p.Simulate(bars, i)
	want := Trade{Entry: 9.5, Exit: 12, ExitIdx: i + 1, Return: 12/9.5 - 1 - 0.01}
	if !ok || trade.Entry != want.Entry || trade.Exit != want.Exit || trade.ExitIdx != want.ExitIdx || math.Abs(trade.Return-want.Return) > 1e-9 {
		t.Errorf("got %+v, want %+v", trade, want)
	}
}

func TestSegmentsCases(t *testing.T) {
	tests := []struct {
		name string
		bars []quote.Quote
		want []int
	}{
		{"no split", flat(5, 10), []int{5}},
		{"reverse split", append(flat(3, 10), flat(4, 20)...), []int{3, 4}},
		{"two splits", append(append(flat(2, 40), flat(3, 20)...), flat(4, 40)...), []int{2, 3, 4}},
		{"move inside the limits is not a split", append(flat(3, 10), flat(3, 6.1)...), []int{6}},
		{"single bar", flat(1, 10), []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Segments(tt.bars)
			lens := make([]int, len(got))
			for i, seg := range got {
				lens[i] = len(seg)
			}
			if !slices.Equal(lens, tt.want) {
				t.Errorf("got segment lengths %v, want %v", lens, tt.want)
			}
		})
	}
}

func TestContinuousCases(t *testing.T) {
	tests := []struct {
		name string
		bars []quote.Quote
		want int
	}{
		{"no split keeps everything", flat(5, 10), 5},
		{"keeps only after the last split", append(append(flat(2, 40), flat(3, 20)...), flat(4, 40)...), 4},
		{"split on the last bar", append(flat(5, 10), flat(1, 30)...), 1},
		{"empty", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(continuous(tt.bars)); got != tt.want {
				t.Errorf("got %d bars, want %d", got, tt.want)
			}
		})
	}
}

func TestIsSplit(t *testing.T) {
	tests := []struct {
		ratio float64
		want  bool
	}{
		{1, false},
		{splitRatio, false},
		{1 / splitRatio, false},
		{splitRatio - 0.01, true},
		{1/splitRatio + 0.01, true},
	}
	for _, tt := range tests {
		if got := isSplit(tt.ratio); got != tt.want {
			t.Errorf("isSplit(%v) = %v, want %v", tt.ratio, got, tt.want)
		}
	}
}

func TestCompare(t *testing.T) {
	if compare(1, 2) != -1 || compare(2, 1) != 1 || compare(2, 2) != 0 {
		t.Error("compare does not order floats")
	}
}

func TestRound(t *testing.T) {
	tests := map[float64]float64{1.234: 1.23, 1.235: 1.24, -1.236: -1.24, 2: 2}
	for in, want := range tests {
		if got := round(in); got != want {
			t.Errorf("round(%v) = %v, want %v", in, got, want)
		}
	}
}

// pullback is an uptrend with one winning trade in the past and a pullback on the last two bars.
func pullback(volume float64) []quote.Quote {
	tight := func(prices ...float64) []quote.Quote {
		bars := closes(volume, prices...)
		for i := range bars {
			bars[i].High, bars[i].Low = bars[i].Close+0.1, bars[i].Close-0.1
		}
		return bars
	}
	bars := tight(rising(250)...)
	// Signal on the first bar (RSI 9), filled and closed at the target the next day.
	bars = append(bars,
		quote.Quote{Open: 19.9, High: 19.95, Low: 19.5, Close: 19.56, Volume: volume},
		quote.Quote{Open: 19.6, High: 20.5, Low: 19.3, Close: 20.4, Volume: volume},
	)
	bars = append(bars, tight(20.4, 20.4, 20.4, 20.4, 20.4, 20.4, 20.4, 20.4, 20, 19.6)...)
	return bars
}

func TestAnalyze(t *testing.T) {
	p := DefaultParams()

	t.Run("track record and signal today", func(t *testing.T) {
		bars := pullback(1e8)
		got := Analyze("WIN3", bars, p)
		l := p.Levels(bars, len(bars)-1)
		if got.Ticker != "WIN3" || got.Trades != 1 || got.Wins != 1 || got.SumReturn <= 0 {
			t.Errorf("unexpected track record %+v", got)
		}
		if !got.Signal || got.Close != 19.6 || got.MaxEntry != l.MaxEntry || got.Target != l.Target || got.Stop != l.Stop {
			t.Errorf("got levels %+v, want %+v", got, l)
		}
	})

	t.Run("no signal today", func(t *testing.T) {
		bars := pullback(1e8)
		bars = bars[:len(bars)-2]
		bars = append(bars, closes(1e8, 20.4, 20.4)...)
		got := Analyze("WIN3", bars, p)
		if got.Signal || got.MaxEntry != 0 || got.Trades != 1 {
			t.Errorf("got %+v, want the track record without a signal", got)
		}
	})

	t.Run("a losing trade is not a win", func(t *testing.T) {
		bars := pullback(1e8)
		bars[251] = quote.Quote{Open: 19.6, High: 19.7, Low: 15, Close: 15.5, Volume: 1e8}
		bars[252] = quote.Quote{Open: 15.5, High: 16, Low: 15, Close: 15.5, Volume: 1e8}
		got := Analyze("LOSS3", bars, p)
		if got.Trades != 1 || got.Wins != 0 || got.SumReturn >= 0 {
			t.Errorf("got %+v, want one losing trade", got)
		}
	})

	t.Run("unfilled signals are not trades", func(t *testing.T) {
		bars := pullback(1e8)
		bars[251] = quote.Quote{Open: 19.8, High: 20, Low: 19.7, Close: 19.9, Volume: 1e8}
		if got := Analyze("MISS3", bars, p); got.Trades != 0 {
			t.Errorf("got %d trades, want 0", got.Trades)
		}
	})

	t.Run("short history", func(t *testing.T) {
		bars := pullback(1e8)
		bars = bars[len(bars)-(trendLen+volumeLen-1):]
		if got := Analyze("NEW3", bars, p); got != (Analysis{Ticker: "NEW3"}) {
			t.Errorf("got %+v, want an empty analysis", got)
		}
	})

	t.Run("illiquid", func(t *testing.T) {
		if got := Analyze("THIN3", pullback(p.MinVolume-1), p); got != (Analysis{Ticker: "THIN3"}) {
			t.Errorf("got %+v, want an empty analysis", got)
		}
	})

	t.Run("only the part after the last split counts", func(t *testing.T) {
		bars := pullback(1e8)
		for i := range bars[:len(bars)-100] {
			bars[i].Open, bars[i].High, bars[i].Low, bars[i].Close = bars[i].Open*3, bars[i].High*3, bars[i].Low*3, bars[i].Close*3
		}
		if got := Analyze("SPLT3", bars, p); got != (Analysis{Ticker: "SPLT3"}) {
			t.Errorf("got %+v, want an empty analysis", got)
		}
	})
}

func TestRankCases(t *testing.T) {
	p := Params{MinTrades: 1}
	candidate := func(ticker string, sum float64) Analysis {
		return Analysis{Ticker: ticker, Signal: true, Trades: 1, SumReturn: sum, Close: 10, MaxEntry: 10, Target: 11, Stop: 9}
	}
	tickers := func(picks []Pick) []string {
		out := make([]string, 0, len(picks))
		for _, p := range picks {
			out = append(out, p.Ticker)
		}
		return out
	}

	t.Run("without estimates ranks by expectancy", func(t *testing.T) {
		picks := Rank([]Analysis{candidate("LOW3", 0.1), candidate("TOP3", 0.3), candidate("MID3", 0.2)}, nil, p, 1000, 100, 3)
		if got := tickers(picks); !slices.Equal(got, []string{"TOP3", "MID3", "LOW3"}) {
			t.Errorf("got %v", got)
		}
		if picks[0].Estimate != nil || picks[0].Upside != 0 {
			t.Errorf("got estimate %+v, upside %v, want none", picks[0].Estimate, picks[0].Upside)
		}
	})

	t.Run("combines expectancy and upside ranks, ties go to the higher upside", func(t *testing.T) {
		all := []Analysis{candidate("AAA3", 0.3), candidate("BBB3", 0.2), candidate("CCC3", 0.1)}
		estimates := map[string]yahoo.Estimate{"AAA3": {Target: 12}, "BBB3": {Target: 13}, "CCC3": {Target: 11}}
		picks := Rank(all, estimates, p, 1000, 100, 3)
		if got := tickers(picks); !slices.Equal(got, []string{"BBB3", "AAA3", "CCC3"}) {
			t.Errorf("got %v", got)
		}
		if picks[0].Estimate == nil || picks[0].Estimate.Target != 13 || math.Abs(picks[0].Upside-0.3) > 1e-9 {
			t.Errorf("got estimate %+v, upside %v", picks[0].Estimate, picks[0].Upside)
		}
	})

	t.Run("drops targets at or below the close", func(t *testing.T) {
		estimates := map[string]yahoo.Estimate{"AAA3": {Target: 10}, "BBB3": {Target: 9}}
		if picks := Rank([]Analysis{candidate("AAA3", 0.3), candidate("BBB3", 0.3)}, estimates, p, 1000, 100, 3); len(picks) != 0 {
			t.Errorf("got %v, want none", tickers(picks))
		}
	})

	t.Run("drops ineligible analyses", func(t *testing.T) {
		idle := candidate("IDLE3", 0.3)
		idle.Signal = false
		if picks := Rank([]Analysis{idle, candidate("LOSS3", -0.1)}, nil, p, 1000, 100, 3); len(picks) != 0 {
			t.Errorf("got %v, want none", tickers(picks))
		}
	})

	t.Run("stops at top", func(t *testing.T) {
		picks := Rank([]Analysis{candidate("AAA3", 0.3), candidate("BBB3", 0.2), candidate("CCC3", 0.1)}, nil, p, 1000, 100, 2)
		if got := tickers(picks); !slices.Equal(got, []string{"AAA3", "BBB3"}) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("skips what cannot buy one share and fills with the next", func(t *testing.T) {
		pricey := candidate("RICH3", 0.3)
		pricey.MaxEntry, pricey.Stop = 200, 190
		picks := Rank([]Analysis{pricey, candidate("AAA3", 0.2)}, nil, p, 100, 100, 1)
		if got := tickers(picks); !slices.Equal(got, []string{"AAA3"}) {
			t.Errorf("got %v", got)
		}
	})

	t.Run("sizing by capital share", func(t *testing.T) {
		// 1000 / 2 slots = 500 per stock; risk allows 1000 shares.
		picks := Rank([]Analysis{candidate("AAA3", 0.3)}, nil, p, 1000, 100, 2)
		if len(picks) != 1 || picks[0].Qty != 50 {
			t.Errorf("got %+v, want 50 shares", picks)
		}
	})

	t.Run("sizing by max risk", func(t *testing.T) {
		// 1% of 1000 = 10 of risk, 1 per share.
		picks := Rank([]Analysis{candidate("AAA3", 0.3)}, nil, p, 1000, 1, 1)
		if len(picks) != 1 || picks[0].Qty != 10 {
			t.Errorf("got %+v, want 10 shares", picks)
		}
	})

	t.Run("nothing to rank", func(t *testing.T) {
		if picks := Rank(nil, nil, p, 1000, 2, 5); len(picks) != 0 {
			t.Errorf("got %v", picks)
		}
	})
}
