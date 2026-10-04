package swing

import (
	"math"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bars := flat(i+1, 10)
			bars = append(bars, tt.next...)
			bars = append(bars, flat(p.MaxHold, 10)...)

			ret, exit, ok := p.simulate(bars, i)
			if ok != tt.wantOK || exit != tt.wantExit || math.Abs(ret-tt.wantRet) > 1e-9 {
				t.Errorf("got (%v, %d, %v), want (%v, %d, %v)", ret, exit, ok, tt.wantRet, tt.wantExit, tt.wantOK)
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
