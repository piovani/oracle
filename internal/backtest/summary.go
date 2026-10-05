package backtest

import (
	"math"
)

type Summary struct {
	Years        float64
	Final        float64
	Return       float64
	CAGR         float64
	MaxDrawdown  float64
	Volatility   float64 // annualized, from daily equity
	Sharpe       float64 // annualized, without a risk-free rate
	Exposure     float64 // average share of equity invested
	Trades       int
	WinRate      float64
	AvgReturn    float64 // per trade, net of costs
	ProfitFactor float64
	FillRate     float64

	Benchmark         float64
	BenchmarkCAGR     float64
	BenchmarkDrawdown float64
}

type Year struct {
	Year      int
	Return    float64
	Benchmark float64
	Trades    int
	WinRate   float64
	AvgReturn float64
}

func (r Result) Summary() Summary {
	var s Summary
	if len(r.Days) == 0 {
		return s
	}
	first, last := r.Days[0], r.Days[len(r.Days)-1]
	s.Years = last.Date.Sub(first.Date).Hours() / 24 / 365.25
	s.Final = last.Equity
	s.Return = last.Equity/r.Initial - 1
	s.CAGR = annualize(s.Return, s.Years)
	s.Benchmark = last.Benchmark - 1
	s.BenchmarkCAGR = annualize(s.Benchmark, s.Years)

	peak, benchPeak := 0.0, 0.0
	var returns []float64
	exposure := 0.0
	for i, d := range r.Days {
		peak = math.Max(peak, d.Equity)
		benchPeak = math.Max(benchPeak, d.Benchmark)
		s.MaxDrawdown = math.Min(s.MaxDrawdown, d.Equity/peak-1)
		s.BenchmarkDrawdown = math.Min(s.BenchmarkDrawdown, d.Benchmark/benchPeak-1)
		exposure += d.Invested / d.Equity
		if i > 0 {
			returns = append(returns, d.Equity/r.Days[i-1].Equity-1)
		}
	}
	s.Exposure = exposure / float64(len(r.Days))

	mean, std := meanStd(returns)
	s.Volatility = std * math.Sqrt(252)
	if std > 0 {
		s.Sharpe = mean / std * math.Sqrt(252)
	}

	s.Trades = len(r.Trades)
	var wins int
	var sum, gain, loss float64
	for _, t := range r.Trades {
		sum += t.Return
		if t.Return > 0 {
			wins++
		}
		if t.PnL > 0 {
			gain += t.PnL
		} else {
			loss -= t.PnL
		}
	}
	if s.Trades > 0 {
		s.WinRate = float64(wins) / float64(s.Trades)
		s.AvgReturn = sum / float64(s.Trades)
	}
	if loss > 0 {
		s.ProfitFactor = gain / loss
	}
	if r.Orders > 0 {
		s.FillRate = float64(r.Fills) / float64(r.Orders)
	}
	return s
}

// Years breaks the result down by calendar year of the closing day (trades by the year they were closed).
func (r Result) Years() []Year {
	if len(r.Days) == 0 {
		return nil
	}
	type acc struct {
		start, end, benchStart, benchEnd float64
		trades, wins                     int
		sum                              float64
	}
	byYear := map[int]*acc{}
	var order []int
	equity, bench := r.Initial, 1.0
	for _, d := range r.Days {
		y := d.Date.Year()
		a := byYear[y]
		if a == nil {
			a = &acc{start: equity, benchStart: bench}
			byYear[y] = a
			order = append(order, y)
		}
		a.end, a.benchEnd = d.Equity, d.Benchmark
		equity, bench = d.Equity, d.Benchmark
	}
	for _, t := range r.Trades {
		a := byYear[t.Exit.Year()]
		if a == nil {
			continue
		}
		a.trades++
		a.sum += t.Return
		if t.Return > 0 {
			a.wins++
		}
	}

	years := make([]Year, 0, len(order))
	for _, y := range order {
		a := byYear[y]
		year := Year{Year: y, Return: a.end/a.start - 1, Benchmark: a.benchEnd/a.benchStart - 1, Trades: a.trades}
		if a.trades > 0 {
			year.WinRate = float64(a.wins) / float64(a.trades)
			year.AvgReturn = a.sum / float64(a.trades)
		}
		years = append(years, year)
	}
	return years
}

func annualize(total, years float64) float64 {
	if years <= 0 || total <= -1 {
		return 0
	}
	return math.Pow(1+total, 1/years) - 1
}

func meanStd(v []float64) (mean, std float64) {
	if len(v) == 0 {
		return 0, 0
	}
	for _, x := range v {
		mean += x
	}
	mean /= float64(len(v))
	for _, x := range v {
		std += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(std / float64(len(v)))
}
