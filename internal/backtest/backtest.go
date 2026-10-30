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

// market is everything the simulation needs, prepared once before the first day.
type market struct {
	dates     []time.Time
	first     int                 // index of the first day to simulate
	benchmark []float64           // average daily return of the liquid universe, per day
	byDay     map[int][]candidate // signals seen at the close of each day
}

func Run(history map[string][]quote.Quote, cfg Config) (Result, error) {
	if cfg.Positions < 1 || cfg.Capital <= 0 {
		return Result{}, errors.New("backtest: capital and positions must be positive")
	}
	m, err := prepare(history, cfg)
	if err != nil {
		return Result{}, err
	}
	return simulate(m, cfg), nil
}

// prepare finds signals and the track record of every ticker, using only that ticker's own past.
func prepare(history map[string][]quote.Quote, cfg Config) (*market, error) {
	p := cfg.Params

	tickers := make([]string, 0, len(history))
	for ticker := range history {
		tickers = append(tickers, ticker)
	}
	slices.Sort(tickers)

	// A split starts a new segment, so no series jumps in price.
	var segments [][]quote.Quote
	var all []*series
	seen := make(map[int64]time.Time)
	for _, ticker := range tickers {
		for _, seg := range swing.Segments(history[ticker]) {
			segments = append(segments, seg)
			for _, bar := range seg {
				seen[bar.Date.Unix()] = bar.Date
			}
			if len(seg) > swing.Warmup+1 {
				all = append(all, &series{ticker: ticker, bars: seg})
			}
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("backtest: no quotes")
	}

	dates := make([]time.Time, 0, len(seen))
	for _, d := range seen {
		dates = append(dates, d)
	}
	slices.SortFunc(dates, time.Time.Compare)
	dayOf := make(map[int64]int, len(dates))
	for i, d := range dates {
		dayOf[d.Unix()] = i
	}

	first := sort.Search(len(dates), func(i int) bool { return !dates[i].Before(cfg.From) })
	if first >= len(dates) {
		return nil, errors.New("backtest: no quotes after the start date")
	}

	m := &market{dates: dates, first: first, byDay: make(map[int][]candidate)}
	m.benchmark = benchmark(segments, dayOf, len(dates), p.MinVolume)

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
		s.rateOwnTrades(p)
		for _, i := range s.signals {
			if d := s.days[i]; d >= first {
				m.byDay[d] = append(m.byDay[d], candidate{s: s, i: i, rsi: swing.RSI(s.bars, i)})
			}
		}
	}
	return m, nil
}

// benchmark is the average daily return of the stocks that were liquid the day before.
func benchmark(segments [][]quote.Quote, dayOf map[int64]int, days int, minVolume float64) []float64 {
	sum := make([]float64, days)
	count := make([]int, days)
	for _, seg := range segments {
		for i := 1; i < len(seg); i++ {
			if seg[i-1].Volume < minVolume {
				continue
			}
			d := dayOf[seg[i].Date.Unix()]
			sum[d] += seg[i].Close/seg[i-1].Close - 1
			count[d]++
		}
	}
	for d := range sum {
		if count[d] > 0 {
			sum[d] /= float64(count[d])
		}
	}
	return sum
}

// rateOwnTrades plays the rule on the ticker's history, one trade at a time, to rate it later.
func (s *series) rateOwnTrades(p swing.Params) {
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
}

// simulate walks the calendar day by day: settle exits, record equity, then open new positions.
func simulate(m *market, cfg Config) Result {
	res := Result{Initial: cfg.Capital}
	cash := cfg.Capital
	bench := 1.0
	var open []*position

	for d := m.first; d < len(m.dates); d++ {
		if d > m.first {
			bench *= 1 + m.benchmark[d]
		}

		var stillOpen []*position
		for _, pos := range open {
			if pos.exitDay > d {
				stillOpen = append(stillOpen, pos)
				continue
			}
			cash += float64(pos.qty) * pos.trade.Entry * (1 + pos.trade.Return)
			res.Trades = append(res.Trades, pos.executed(m.dates))
		}
		open = stillOpen

		invested, pending := 0.0, 0.0
		for _, pos := range open {
			if pos.entryDay > d {
				pending += float64(pos.qty) * pos.trade.Entry // order sent, not filled yet
			} else {
				invested += pos.value(d)
			}
		}
		equity := cash + invested + pending
		res.Days = append(res.Days, Day{Date: m.dates[d], Equity: equity, Invested: invested, Benchmark: bench})

		slots := cfg.Positions - len(open)
		if slots <= 0 {
			continue
		}
		picks := rank(m.byDay[d], open, d, m.dates[d], cfg)
		slotSize := equity / float64(cfg.Positions)
		maxLoss := equity * cfg.RiskPct / 100
		for _, c := range picks[:min(slots, len(picks))] {
			l := cfg.Params.Levels(c.s.bars, c.i)
			if l.MaxEntry <= 0 || l.MaxEntry <= l.Stop {
				continue
			}
			qty := math.Floor(min(slotSize/l.MaxEntry, maxLoss/(l.MaxEntry-l.Stop), cash/l.MaxEntry))
			if qty < 1 {
				continue
			}
			res.Orders++
			trade, ok := cfg.Params.Simulate(c.s.bars, c.i)
			if !ok {
				continue // the limit order never filled
			}
			res.Fills++
			cash -= qty * trade.Entry
			open = append(open, &position{
				s: c.s, qty: int(qty), trade: trade, signal: c.i,
				entryDay: c.s.days[c.i+1], exitDay: c.s.days[trade.ExitIdx],
			})
		}
	}
	return res
}

// rank keeps the signals of day d worth trading and orders them best first.
func rank(signals []candidate, open []*position, d int, today time.Time, cfg Config) []candidate {
	held := make(map[string]bool, len(open))
	for _, pos := range open {
		held[pos.s.ticker] = true
	}

	cutoff := today.AddDate(-cfg.Lookback, 0, 0)
	var picks []candidate
	for _, c := range signals {
		if held[c.s.ticker] || !cfg.Params.Liquid(c.s.bars, c.i) {
			continue
		}
		c.rate = c.s.rating(d, cutoff, cfg.Lookback > 0)
		if cfg.Rank == RankExpectancy && !c.rate.Eligible(cfg.Params) {
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
	return picks
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
