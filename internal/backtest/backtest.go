// Package backtest replays the swing strategy day by day on the whole portfolio.
//
// Every decision uses only what was known at the close of that day: signals, trade levels,
// liquidity and the per-ticker track record (trades already closed). Delisted tickers stay in
// the universe, positions share one cash balance, and costs are charged on every trade.
package backtest

import (
	"cmp"
	"errors"
	"math"
	"slices"
	"sort"
	"time"

	"oracle/internal/quote"
	"oracle/internal/swing"
)

type Rank int

const (
	// RankExpectancy keeps tickers whose rule has a positive track record and ranks by it, like the recommend command.
	RankExpectancy Rank = iota
	// RankRSI skips the per-ticker track record and ranks by the lowest RSI.
	RankRSI
)

type Config struct {
	Params    swing.Params
	Capital   float64
	Positions int
	RiskPct   float64
	From      time.Time
	Lookback  int // years of closed trades used to rate a ticker; 0 means all
	Rank      Rank
}

type Executed struct {
	Ticker     string
	Signal     time.Time
	Entry      time.Time
	Exit       time.Time
	Qty        int
	EntryPrice float64
	ExitPrice  float64
	Return     float64 // net of costs
	PnL        float64 // in BRL, net of costs
}

type Day struct {
	Date      time.Time
	Equity    float64
	Invested  float64
	Benchmark float64 // equal-weight liquid universe, rebalanced daily, starting at 1
}

type Result struct {
	Initial float64
	Days    []Day
	Trades  []Executed
	Orders  int // orders sized and sent
	Fills   int // orders that executed
}

type series struct {
	ticker  string
	bars    []quote.Quote
	days    []int // calendar index of each bar
	signals []int // bar indexes with a signal
	rated   []rated
}

// rated is a closed trade of the ticker's own rule, used to rate it later.
type rated struct {
	signal  time.Time
	exitDay int
	ret     float64
}

type candidate struct {
	s    *series
	i    int
	rsi  float64
	rate swing.Analysis
}

type position struct {
	s        *series
	qty      int
	trade    swing.Trade
	signal   int
	entryDay int
	exitDay  int
}

func Run(history map[string][]quote.Quote, cfg Config) (Result, error) {
	if cfg.Positions < 1 || cfg.Capital <= 0 {
		return Result{}, errors.New("backtest: capital and positions must be positive")
	}
	p := cfg.Params

	var tickers []string
	for ticker := range history {
		tickers = append(tickers, ticker)
	}
	slices.Sort(tickers)

	var segments [][]quote.Quote
	var all []*series
	dateSet := make(map[int64]time.Time)
	for _, ticker := range tickers {
		for _, seg := range swing.Segments(history[ticker]) {
			segments = append(segments, seg)
			for _, bar := range seg {
				dateSet[bar.Date.Unix()] = bar.Date
			}
			if len(seg) > swing.Warmup+1 {
				all = append(all, &series{ticker: ticker, bars: seg})
			}
		}
	}
	if len(dateSet) == 0 {
		return Result{}, errors.New("backtest: no quotes")
	}

	dates := make([]time.Time, 0, len(dateSet))
	for _, d := range dateSet {
		dates = append(dates, d)
	}
	slices.SortFunc(dates, time.Time.Compare)
	dayOf := make(map[int64]int, len(dates))
	for i, d := range dates {
		dayOf[d.Unix()] = i
	}

	from := sort.Search(len(dates), func(i int) bool { return !dates[i].Before(cfg.From) })
	if from >= len(dates) {
		return Result{}, errors.New("backtest: no quotes after the start date")
	}

	benchSum := make([]float64, len(dates))
	benchCount := make([]int, len(dates))
	for _, seg := range segments {
		for i := 1; i < len(seg); i++ {
			if seg[i-1].Volume < p.MinVolume {
				continue
			}
			d := dayOf[seg[i].Date.Unix()]
			benchSum[d] += seg[i].Close/seg[i-1].Close - 1
			benchCount[d]++
		}
	}

	byDay := make(map[int][]candidate)
	for _, s := range all {
		s.days = make([]int, len(s.bars))
		for i, bar := range s.bars {
			s.days[i] = dayOf[bar.Date.Unix()]
		}
		for i := swing.Warmup; i < len(s.bars)-1; i++ {
			if p.Signal(s.bars, i) {
				s.signals = append(s.signals, i)
			}
		}
		free := 0
		for _, i := range s.signals {
			if i < free {
				continue
			}
			trade, ok := p.Simulate(s.bars, i)
			if !ok {
				continue
			}
			s.rated = append(s.rated, rated{s.bars[i].Date, s.days[trade.ExitIdx], trade.Return})
			free = trade.ExitIdx
		}
		for _, i := range s.signals {
			if d := s.days[i]; d >= from {
				byDay[d] = append(byDay[d], candidate{s: s, i: i, rsi: swing.RSI(s.bars, i)})
			}
		}
	}

	res := Result{Initial: cfg.Capital}
	cash := cfg.Capital
	bench := 1.0
	var open []*position

	for d := from; d < len(dates); d++ {
		if d > from && benchCount[d] > 0 {
			bench *= 1 + benchSum[d]/float64(benchCount[d])
		}

		kept := open[:0]
		for _, pos := range open {
			if pos.exitDay > d {
				kept = append(kept, pos)
				continue
			}
			cash += float64(pos.qty) * pos.trade.Entry * (1 + pos.trade.Return)
			res.Trades = append(res.Trades, pos.executed(dates))
		}
		open = kept

		invested, pending := 0.0, 0.0
		for _, pos := range open {
			if pos.entryDay > d {
				pending += float64(pos.qty) * pos.trade.Entry
			} else {
				invested += pos.value(d)
			}
		}
		equity := cash + invested + pending
		res.Days = append(res.Days, Day{Date: dates[d], Equity: equity, Invested: invested, Benchmark: bench})

		slots := cfg.Positions - len(open)
		if slots <= 0 {
			continue
		}
		held := make(map[string]bool, len(open))
		for _, pos := range open {
			held[pos.s.ticker] = true
		}

		cutoff := dates[d].AddDate(-cfg.Lookback, 0, 0)
		var picks []candidate
		for _, c := range byDay[d] {
			if held[c.s.ticker] || !p.Liquid(c.s.bars, c.i) {
				continue
			}
			c.rate = c.s.rating(d, cutoff, cfg.Lookback > 0)
			if cfg.Rank == RankExpectancy && !c.rate.Eligible(p) {
				continue
			}
			picks = append(picks, c)
		}
		slices.SortFunc(picks, func(a, b candidate) int {
			if cfg.Rank == RankRSI {
				return cmp.Or(cmp.Compare(a.rsi, b.rsi), cmp.Compare(a.s.ticker, b.s.ticker))
			}
			return cmp.Or(cmp.Compare(b.rate.Expectancy(), a.rate.Expectancy()), cmp.Compare(a.s.ticker, b.s.ticker))
		})

		slot := equity / float64(cfg.Positions)
		maxRisk := equity * cfg.RiskPct / 100
		for _, c := range picks[:min(slots, len(picks))] {
			l := p.Levels(c.s.bars, c.i)
			if l.MaxEntry <= 0 || l.MaxEntry <= l.Stop {
				continue
			}
			qty := math.Floor(min(slot/l.MaxEntry, maxRisk/(l.MaxEntry-l.Stop), cash/l.MaxEntry))
			if qty < 1 {
				continue
			}
			res.Orders++
			trade, ok := p.Simulate(c.s.bars, c.i)
			if !ok {
				continue
			}
			res.Fills++
			cash -= qty * trade.Entry
			open = append(open, &position{
				s: c.s, qty: int(qty), trade: trade, signal: c.i,
				entryDay: c.s.days[c.i+1], exitDay: c.s.days[trade.ExitIdx],
			})
		}
	}
	return res, nil
}

// rating is the ticker's track record at day d: its own closed trades, optionally within the lookback window.
func (s *series) rating(d int, cutoff time.Time, windowed bool) swing.Analysis {
	a := swing.Analysis{Ticker: s.ticker, Signal: true}
	for _, r := range s.rated {
		if r.exitDay > d || (windowed && r.signal.Before(cutoff)) {
			continue
		}
		a.Trades++
		a.SumReturn += r.ret
	}
	return a
}

func (pos *position) value(d int) float64 {
	k := sort.SearchInts(pos.s.days, d+1) - 1
	return float64(pos.qty) * pos.s.bars[k].Close
}

func (pos *position) executed(dates []time.Time) Executed {
	return Executed{
		Ticker:     pos.s.ticker,
		Signal:     pos.s.bars[pos.signal].Date,
		Entry:      dates[pos.entryDay],
		Exit:       dates[pos.exitDay],
		Qty:        pos.qty,
		EntryPrice: pos.trade.Entry,
		ExitPrice:  pos.trade.Exit,
		Return:     pos.trade.Return,
		PnL:        float64(pos.qty) * pos.trade.Entry * pos.trade.Return,
	}
}
