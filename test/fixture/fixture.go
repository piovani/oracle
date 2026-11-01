// Package fixture builds synthetic quote series for tests, shaped to trigger the strategy's rules.
package fixture

import (
	"math"
	"time"

	"oracle/internal/quote"
)

// StrongPullback drops the last two closes by 0.4 each: RSI(2) of 0, the "forte" tier.
var StrongPullback = []float64{0.4, 0.4}

// MildPullback drops the last close by 0.16 after a 0.04 gain: RSI(2) of 20, the "médio" tier.
var MildPullback = []float64{0.16}

// Trend is a liquid stock in a steady uptrend whose last bar is on last. It has a winning pullback
// trade at every index in dips, and the last len(drops) closes fall by the given amounts.
//
// A dip is one down day (RSI(2) about 9, above the SMA200) followed by a day that touches the
// limit entry and closes above the target, so the rule records a win.
func Trend(ticker string, n int, last time.Time, dips []int, drops ...float64) []quote.Quote {
	isDip := make(map[int]bool, len(dips))
	for _, d := range dips {
		isDip[d] = true
	}
	bars := make([]quote.Quote, 0, n)
	c := 10.0
	for i := range n {
		var q quote.Quote
		switch {
		case i >= n-len(drops):
			c -= drops[i-n+len(drops)]
			q = quote.Quote{Open: c + 0.34, High: c + 0.39, Low: c - 0.06, Close: c}
		case isDip[i]:
			c -= 0.4
			q = quote.Quote{Open: c + 0.34, High: c + 0.39, Low: c - 0.06, Close: c}
		case isDip[i-1]:
			q = quote.Quote{Open: c + 0.04, High: c + 0.94, Low: c - 0.26, Close: c + 0.84}
			c += 0.84
		default:
			c += 0.04
			q = quote.Quote{Open: c - 0.02, High: c + 0.1, Low: c - 0.1, Close: c}
		}
		q.Ticker, q.Name, q.Spec, q.ISIN = ticker, ticker+" SA", "ON      NM", "BR"+ticker+"ACNOR"
		q.Date = last.AddDate(0, 0, i-n+1)
		q.Average = Round(q.Close)
		q.Open, q.High, q.Low, q.Close = Round(q.Open), Round(q.High), Round(q.Low), Round(q.Close)
		q.Trades, q.Quantity, q.Volume = 1000, 1_000_000, 1e8
		bars = append(bars, q)
	}
	return bars
}

// Dips spaces n winning trades 30 sessions apart, after the 200 sessions the trend filter needs.
func Dips(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = 220 + 30*i
	}
	return idx
}

// Round rounds to cents, the precision B3 publishes and the database stores.
func Round(v float64) float64 { return math.Round(v*100) / 100 }

// Day is a date at midnight UTC, how quote dates are stored.
func Day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// Today is the current date at midnight UTC.
func Today() time.Time {
	y, m, d := time.Now().Date()
	return Day(y, m, d)
}
