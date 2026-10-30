package swing

import (
	"math"
	"slices"

	"oracle/internal/quote"
	"oracle/internal/yahoo"
)

const (
	trendLen   = 200
	rsiLen     = 2
	atrLen     = 14
	volumeLen  = 60
	shrinkage  = 10
	splitRatio = 0.6
)

type Params struct {
	TargetATR float64
	StopATR   float64
	EntryATR  float64
	MaxHold   int
	Oversold  float64
	CostPct   float64
	MinTrades int
	MinVolume float64
}

type Tier struct {
	Oversold float64
	Label    string
	Rule     string
}

var Tiers = []Tier{
	{10, "forte", "RSI(2) < 10"},
	{30, "médio", "RSI(2) < 30"},
	{50, "fraco", "RSI(2) < 50"},
	{101, "tendência", "sem recuo"},
}

func DefaultParams() Params {
	return Params{
		TargetATR: 1,
		StopATR:   2,
		EntryATR:  0.5,
		MaxHold:   5,
		Oversold:  10,
		CostPct:   0.03,
		MinTrades: 5,
		MinVolume: 5_000_000,
	}
}

type Analysis struct {
	Ticker    string
	Signal    bool
	Close     float64
	MaxEntry  float64
	Target    float64
	Stop      float64
	Trades    int
	Wins      int
	SumReturn float64
}

func (a Analysis) WinRate() float64 {
	if a.Trades == 0 {
		return 0
	}
	return float64(a.Wins) / float64(a.Trades)
}

func (a Analysis) AvgReturn() float64 {
	if a.Trades == 0 {
		return 0
	}
	return a.SumReturn / float64(a.Trades)
}

func (a Analysis) Expectancy() float64 {
	return a.SumReturn / float64(a.Trades+shrinkage)
}

func (a Analysis) Eligible(p Params) bool {
	return a.Signal && a.Trades >= p.MinTrades && a.Expectancy() > 0
}

type Pick struct {
	Analysis
	Estimate *yahoo.Estimate
	Upside   float64
	Qty      int
	score    int
}

func (p Pick) Invested() float64 { return float64(p.Qty) * p.MaxEntry }

func (p Pick) Gain() float64 { return float64(p.Qty) * (p.Target - p.MaxEntry) }

func (p Pick) Loss() float64 { return float64(p.Qty) * (p.MaxEntry - p.Stop) }

func Analyze(ticker string, bars []quote.Quote, p Params) Analysis {
	a := Analysis{Ticker: ticker}
	bars = continuous(bars)
	n := len(bars)
	if n < trendLen+volumeLen || medianVolume(bars[n-volumeLen:]) < p.MinVolume {
		return a
	}

	for i := trendLen; i+p.MaxHold < n; {
		if !p.Signal(bars, i) {
			i++
			continue
		}
		trade, ok := p.Simulate(bars, i)
		if !ok {
			i++
			continue
		}
		a.Trades++
		a.SumReturn += trade.Return
		if trade.Return > 0 {
			a.Wins++
		}
		i = trade.ExitIdx
	}

	last := n - 1
	if p.Signal(bars, last) {
		l := p.Levels(bars, last)
		a.Signal = true
		a.Close, a.MaxEntry, a.Target, a.Stop = l.Close, l.MaxEntry, l.Target, l.Stop
	}
	return a
}

// Levels are the order prices planned after the close of bar i.
type Levels struct {
	Close    float64
	MaxEntry float64
	Target   float64
	Stop     float64
}

func (p Params) Levels(bars []quote.Quote, i int) Levels {
	volatility := atr(bars, i)
	last := bars[i].Close
	return Levels{
		Close:    last,
		MaxEntry: round(last - p.EntryATR*volatility),
		Target:   round(last + p.TargetATR*volatility),
		Stop:     round(last - p.StopATR*volatility),
	}
}

// Liquid reports whether the median volume of the volumeLen bars ending at i reaches MinVolume.
func (p Params) Liquid(bars []quote.Quote, i int) bool {
	return i >= volumeLen-1 && medianVolume(bars[i-volumeLen+1:i+1]) >= p.MinVolume
}

// Warmup is the number of bars a series needs before its first signal.
const Warmup = trendLen

func Rank(all []Analysis, estimates map[string]yahoo.Estimate, p Params, capital, riskPct float64, top int) []Pick {
	var picks []Pick
	for _, a := range all {
		if !a.Eligible(p) {
			continue
		}
		pick := Pick{Analysis: a}
		if e, ok := estimates[a.Ticker]; ok {
			pick.Estimate = &e
			pick.Upside = e.Target/a.Close - 1
			if pick.Upside <= 0 {
				continue
			}
		}
		picks = append(picks, pick)
	}

	slices.SortFunc(picks, func(a, b Pick) int { return compare(b.Expectancy(), a.Expectancy()) })
	for i := range picks {
		picks[i].score = i
	}
	slices.SortStableFunc(picks, func(a, b Pick) int { return compare(b.Upside, a.Upside) })
	for i := range picks {
		picks[i].score += i
	}
	slices.SortStableFunc(picks, func(a, b Pick) int { return a.score - b.score })

	allocation := capital / float64(top)
	maxRisk := capital * riskPct / 100

	sized := make([]Pick, 0, top)
	for _, pick := range picks {
		if len(sized) == top {
			break
		}
		qty := math.Floor(min(allocation/pick.MaxEntry, maxRisk/(pick.MaxEntry-pick.Stop)))
		if qty < 1 {
			continue
		}
		pick.Qty = int(qty)
		sized = append(sized, pick)
	}
	return sized
}

// Trade is a simulated operation planned after the close of bar i.
type Trade struct {
	Entry   float64
	Exit    float64
	ExitIdx int
	Return  float64
}

// Simulate plays the plan made after the close of bar i. It reports false when the entry order does not fill.
// A series that ends before the holding period is closed at its last close.
func (p Params) Simulate(bars []quote.Quote, i int) (Trade, bool) {
	if i+1 >= len(bars) {
		return Trade{}, false
	}
	volatility := atr(bars, i)
	ref := bars[i].Close
	stop := ref - p.StopATR*volatility
	target := ref + p.TargetATR*volatility

	limit := ref - p.EntryATR*volatility
	first := bars[i+1]
	if first.Low > limit || first.Open <= stop {
		return Trade{}, false
	}
	entry := min(first.Open, limit)

	last := min(i+p.MaxHold, len(bars)-1)
	exit := last
	price := bars[exit].Close
	for j := i + 1; j <= last; j++ {
		bar := bars[j]
		if bar.Low <= stop {
			exit, price = j, stop
			if j > i+1 {
				price = min(bar.Open, stop)
			}
			break
		}
		if j == i+1 && bar.Close < target {
			continue
		}
		if bar.High >= target {
			exit, price = j, target
			if j > i+1 {
				price = max(bar.Open, target)
			}
			break
		}
	}
	return Trade{Entry: entry, Exit: price, ExitIdx: exit, Return: price/entry - 1 - 2*p.CostPct/100}, true
}

// Signal reports whether bar i closes above the trend and oversold.
func (p Params) Signal(bars []quote.Quote, i int) bool {
	return i >= trendLen && bars[i].Close > sma(bars, i, trendLen) && RSI(bars, i) < p.Oversold
}

func RSI(bars []quote.Quote, i int) float64 {
	gains, losses := 0.0, 0.0
	for j := i - rsiLen + 1; j <= i; j++ {
		change := bars[j].Close - bars[j-1].Close
		if change > 0 {
			gains += change
		} else {
			losses -= change
		}
	}
	if gains+losses == 0 {
		return 50
	}
	return 100 * gains / (gains + losses)
}

func sma(bars []quote.Quote, i, n int) float64 {
	sum := 0.0
	for _, bar := range bars[i-n+1 : i+1] {
		sum += bar.Close
	}
	return sum / float64(n)
}

func atr(bars []quote.Quote, i int) float64 {
	sum := 0.0
	for j := i - atrLen + 1; j <= i; j++ {
		prev := bars[j-1].Close
		sum += max(bars[j].High-bars[j].Low, math.Abs(bars[j].High-prev), math.Abs(bars[j].Low-prev))
	}
	return sum / atrLen
}

// Segments splits a series at detected splits and reverse splits, so no segment spans a price discontinuity.
func Segments(bars []quote.Quote) [][]quote.Quote {
	var segments [][]quote.Quote
	start := 0
	for i := 1; i < len(bars); i++ {
		if isSplit(bars[i].Close / bars[i-1].Close) {
			segments = append(segments, bars[start:i])
			start = i
		}
	}
	return append(segments, bars[start:])
}

func isSplit(ratio float64) bool { return ratio < splitRatio || ratio > 1/splitRatio }

func continuous(bars []quote.Quote) []quote.Quote {
	for i := len(bars) - 1; i > 0; i-- {
		if isSplit(bars[i].Close / bars[i-1].Close) {
			return bars[i:]
		}
	}
	return bars
}

func medianVolume(bars []quote.Quote) float64 {
	volumes := make([]float64, len(bars))
	for i, bar := range bars {
		volumes[i] = bar.Volume
	}
	slices.Sort(volumes)
	return volumes[len(volumes)/2]
}

func compare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func round(v float64) float64 {
	return math.Round(v*100) / 100
}
