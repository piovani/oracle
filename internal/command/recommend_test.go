package command

import (
	"errors"
	"slices"
	"testing"

	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
	"oracle/test/fixture"
	"oracle/test/mock"
)

var lastDay = fixture.Day(2026, 10, 30)

// market has one stock per situation the plan must handle.
func market() map[string][]quote.Quote {
	return map[string][]quote.Quote{
		"WIN3":  fixture.Trend("WIN3", 400, lastDay, fixture.Dips(5), fixture.StrongPullback...), // forte
		"MED3":  fixture.Trend("MED3", 400, lastDay, fixture.Dips(5), fixture.MildPullback...),   // médio
		"FLAT3": fixture.Trend("FLAT3", 400, lastDay, nil),                                       // no signal
		"OLD3":  fixture.Trend("OLD3", 400, lastDay.AddDate(0, 0, -10), fixture.Dips(5), fixture.StrongPullback...),
	}
}

func noEstimates() *estimates {
	return &estimates{byTicker: map[string]yahoo.Estimate{}, tried: map[string]bool{}}
}

func withEstimator(e estimator) *estimates {
	est := noEstimates()
	est.client = e
	return est
}

func plan(rows []row) (tickers, strengths []string) {
	for _, r := range rows {
		tickers = append(tickers, r.Ticker)
		strengths = append(strengths, r.strength)
	}
	return tickers, strengths
}

func TestBuildPlanStopsAtTheFirstTier(t *testing.T) {
	opts := options{capital: 1000, top: 5, risk: 2}
	rows, summaries := buildPlan(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	tickers, strengths := plan(rows)
	if !slices.Equal(tickers, []string{"WIN3"}) || !slices.Equal(strengths, []string{"forte"}) {
		t.Errorf("got %v %v, want only WIN3 as forte", tickers, strengths)
	}
	if rows[0].name != "WIN3 SA" || rows[0].Qty == 0 || rows[0].Estimate != nil {
		t.Errorf("got row %+v", rows[0])
	}
	// OLD3 did not trade on the last session, so its trades are not in the summary.
	if len(summaries) != 1 || summaries[0].tier.Label != "forte" || summaries[0].candidates != 1 || summaries[0].trades != 10 || summaries[0].wins != 10 {
		t.Errorf("got summaries %+v, want forte with 1 candidate and the 10 trades of WIN3 and MED3", summaries)
	}
}

func TestBuildPlanFillsWithWeakerSignals(t *testing.T) {
	opts := options{capital: 1000, top: 5, risk: 2, fill: true}
	rows, summaries := buildPlan(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	tickers, strengths := plan(rows)
	if !slices.Equal(tickers, []string{"WIN3", "MED3"}) || !slices.Equal(strengths, []string{"forte", "médio"}) {
		t.Errorf("got %v %v, want WIN3 forte then MED3 médio, each once", tickers, strengths)
	}
	if len(summaries) != len(swing.Tiers) {
		t.Errorf("got %d summaries, want every tier while the list is short", len(summaries))
	}
}

func TestBuildPlanStopsWhenFull(t *testing.T) {
	opts := options{capital: 1000, top: 1, risk: 2, fill: true}
	rows, summaries := buildPlan(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	if tickers, _ := plan(rows); !slices.Equal(tickers, []string{"WIN3"}) || len(summaries) != 1 {
		t.Errorf("got %v and %d summaries, want only WIN3 and the first tier", tickers, len(summaries))
	}
}

func TestBuildPlanWithoutSignals(t *testing.T) {
	history := map[string][]quote.Quote{"FLAT3": fixture.Trend("FLAT3", 400, lastDay, nil)}
	opts := options{capital: 1000, top: 5, risk: 2, fill: true}
	rows, summaries := buildPlan(t.Context(), history, lastDay, swing.DefaultParams(), noEstimates(), opts)

	if len(rows) != 0 || len(summaries) != len(swing.Tiers) {
		t.Errorf("got %d rows and %d summaries, want an empty plan after every tier", len(rows), len(summaries))
	}
}

func TestBuildPlanUsesAnalystTargets(t *testing.T) {
	history := market()
	lastClose := func(ticker string) float64 { bars := history[ticker]; return bars[len(bars)-1].Close }
	est := &mock.Estimator{Estimates: map[string]yahoo.Estimate{
		"WIN3": {Target: lastClose("WIN3") * 1.2, Rating: 1.5, Analysts: 8},
		"MED3": {Target: lastClose("MED3") * 0.9}, // analysts see it falling
	}}
	opts := options{capital: 1000, top: 5, risk: 2, fill: true}

	rows, _ := buildPlan(t.Context(), history, lastDay, swing.DefaultParams(), withEstimator(est), opts)

	if tickers, _ := plan(rows); !slices.Equal(tickers, []string{"WIN3"}) {
		t.Fatalf("got %v, want MED3 dropped for a target below the close", tickers)
	}
	if e := rows[0].Estimate; e == nil || e.Analysts != 8 || rows[0].Upside < 0.19 || rows[0].Upside > 0.21 {
		t.Errorf("got estimate %+v, upside %v", e, rows[0].Upside)
	}
	// Only eligible stocks are looked up, each once, even though they qualify again in weaker tiers.
	if !slices.Equal(est.Requested, []string{"WIN3", "MED3"}) {
		t.Errorf("requested %v", est.Requested)
	}
}

func TestEstimatesFetch(t *testing.T) {
	est := &mock.Estimator{
		Estimates: map[string]yahoo.Estimate{"AAA3": {Target: 10}},
		Err:       map[string]error{"ERR3": errors.New("timeout")},
	}
	e := withEstimator(est)

	e.fetch(t.Context(), []string{"AAA3", "NONE3", "ERR3"})
	e.fetch(t.Context(), []string{"AAA3", "ERR3", "BBB3"})

	if !slices.Equal(est.Requested, []string{"AAA3", "NONE3", "ERR3", "BBB3"}) {
		t.Errorf("requested %v, want each ticker once", est.Requested)
	}
	if len(e.byTicker) != 1 || e.byTicker["AAA3"].Target != 10 {
		t.Errorf("got %v, want only the covered ticker", e.byTicker)
	}
}

func TestEstimatesDisabled(t *testing.T) {
	e := newEstimates(t.Context(), false)
	e.fetch(t.Context(), []string{"AAA3"})
	if e.client != nil || len(e.byTicker) != 0 || len(e.tried) != 0 {
		t.Errorf("got %+v, want nothing fetched", e)
	}
}

func TestAnalyzeTierSkipsStocksOffTheLastSession(t *testing.T) {
	analyses, s := analyzeTier(market(), lastDay, swing.DefaultParams(), swing.Tiers[0])
	tickers := make([]string, 0, len(analyses))
	for _, a := range analyses {
		tickers = append(tickers, a.Ticker)
	}
	slices.Sort(tickers)
	if !slices.Equal(tickers, []string{"FLAT3", "MED3", "WIN3"}) || s.tier.Label != "forte" {
		t.Errorf("got %v for tier %q", tickers, s.tier.Label)
	}
}

func TestEligibleTickers(t *testing.T) {
	p := swing.Params{MinTrades: 1}
	analyses := []swing.Analysis{
		{Ticker: "ZZZ3", Signal: true, Trades: 1, SumReturn: 0.1},
		{Ticker: "AAA3", Signal: true, Trades: 1, SumReturn: 0.1},
		{Ticker: "NOPE3", Trades: 1, SumReturn: 0.1},
	}
	if got := eligibleTickers(analyses, p); !slices.Equal(got, []string{"AAA3", "ZZZ3"}) {
		t.Errorf("got %v, want the eligible ones sorted", got)
	}
}
