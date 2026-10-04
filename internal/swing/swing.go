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

func DefaultParams() Params {
	return Params{
		TargetATR: 1,
		StopATR:   2,
		EntryATR:  0.5,
		MaxHold:   5,
		Oversold:  10,
		CostPct:   0.03,
		MinTrades: 10,
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
		if !p.signal(bars, i) {
			i++
			continue
		}
		ret, exit, ok := p.simulate(bars, i)
		if !ok {
			i++
			continue
		}
		a.Trades++
		a.SumReturn += ret
		if ret > 0 {
			a.Wins++
		}
		i = exit
	}

	last := n - 1
	if p.signal(bars, last) {
		volatility := atr(bars, last)
		a.Signal = true
		a.Close = bars[last].Close
		a.MaxEntry = round(a.Close + p.EntryATR*volatility)
		a.Target = round(a.Close + p.TargetATR*volatility)
		a.Stop = round(a.Close - p.StopATR*volatility)
	}
	return a
}

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

func (p Params) simulate(bars []quote.Quote, i int) (ret float64, exit int, ok bool) {
	volatility := atr(bars, i)
	ref := bars[i].Close
	stop := ref - p.StopATR*volatility
	target := ref + p.TargetATR*volatility

	entry := bars[i+1].Open
	if entry <= stop || entry > ref+p.EntryATR*volatility {
		return 0, 0, false
	}

	exit = i + p.MaxHold
	price := bars[exit].Close
	for j := i + 1; j <= i+p.MaxHold; j++ {
		bar := bars[j]
		if bar.Low <= stop {
			exit, price = j, min(bar.Open, stop)
			break
		}
		if bar.High >= target {
			exit, price = j, max(bar.Open, target)
			break
		}
	}
	return price/entry - 1 - 2*p.CostPct/100, exit, true
}

func (p Params) signal(bars []quote.Quote, i int) bool {
	return bars[i].Close > sma(bars, i, trendLen) && rsi(bars, i) < p.Oversold
}

func rsi(bars []quote.Quote, i int) float64 {
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

func continuous(bars []quote.Quote) []quote.Quote {
	for i := len(bars) - 1; i > 0; i-- {
		ratio := bars[i].Close / bars[i-1].Close
		if ratio < splitRatio || ratio > 1/splitRatio {
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
