package plan

import (
	"context"
	"errors"
	"slices"
	"sync"
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

func noEstimates() *Estimates { return NewEstimates(nil) }

func withEstimator(e Estimator) *Estimates { return NewEstimates(e) }

func picked(rows []Row) (tickers, tiers []string) {
	for _, r := range rows {
		tickers = append(tickers, r.Ticker)
		tiers = append(tiers, r.Tier)
	}
	return tickers, tiers
}

func TestBuildPlanStopsAtTheFirstTier(t *testing.T) {
	opts := Options{Capital: 1000, Top: 5, Risk: 2}
	rows, summaries := Build(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	tickers, strengths := picked(rows)
	if !slices.Equal(tickers, []string{"WIN3"}) || !slices.Equal(strengths, []string{"forte"}) {
		t.Errorf("got %v %v, want only WIN3 as forte", tickers, strengths)
	}
	if rows[0].Name != "WIN3 SA" || rows[0].Qty == 0 || rows[0].Estimate != nil {
		t.Errorf("got row %+v", rows[0])
	}
	// OLD3 did not trade on the last session, so its trades are not in the summary.
	if len(summaries) != 1 || summaries[0].Tier.Label != "forte" || summaries[0].Candidates != 1 || summaries[0].Trades != 10 || summaries[0].Wins != 10 {
		t.Errorf("got summaries %+v, want forte with 1 candidate and the 10 trades of WIN3 and MED3", summaries)
	}
}

func TestBuildPlanFillsWithWeakerSignals(t *testing.T) {
	opts := Options{Capital: 1000, Top: 5, Risk: 2, Fill: true}
	rows, summaries := Build(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	tickers, strengths := picked(rows)
	if !slices.Equal(tickers, []string{"WIN3", "MED3"}) || !slices.Equal(strengths, []string{"forte", "médio"}) {
		t.Errorf("got %v %v, want WIN3 forte then MED3 médio, each once", tickers, strengths)
	}
	if len(summaries) != len(swing.Tiers) {
		t.Errorf("got %d summaries, want every tier while the list is short", len(summaries))
	}
}

func TestBuildPlanStopsWhenFull(t *testing.T) {
	opts := Options{Capital: 1000, Top: 1, Risk: 2, Fill: true}
	rows, summaries := Build(t.Context(), market(), lastDay, swing.DefaultParams(), noEstimates(), opts)

	if tickers, _ := picked(rows); !slices.Equal(tickers, []string{"WIN3"}) || len(summaries) != 1 {
		t.Errorf("got %v and %d summaries, want only WIN3 and the first tier", tickers, len(summaries))
	}
}

func TestBuildPlanWithoutSignals(t *testing.T) {
	history := map[string][]quote.Quote{"FLAT3": fixture.Trend("FLAT3", 400, lastDay, nil)}
	opts := Options{Capital: 1000, Top: 5, Risk: 2, Fill: true}
	rows, summaries := Build(t.Context(), history, lastDay, swing.DefaultParams(), noEstimates(), opts)

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
	opts := Options{Capital: 1000, Top: 5, Risk: 2, Fill: true}

	rows, _ := Build(t.Context(), history, lastDay, swing.DefaultParams(), withEstimator(est), opts)

	if tickers, _ := picked(rows); !slices.Equal(tickers, []string{"WIN3"}) {
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

	e.Fetch(t.Context(), []string{"AAA3", "NONE3", "ERR3"})
	e.Fetch(t.Context(), []string{"AAA3", "ERR3", "BBB3"})

	if !slices.Equal(est.Requested, []string{"AAA3", "NONE3", "ERR3", "BBB3"}) {
		t.Errorf("requested %v, want each ticker once", est.Requested)
	}
	if all := e.All(); len(all) != 1 || all["AAA3"].Target != 10 {
		t.Errorf("got %v, want only the covered ticker", all)
	}
}

func TestEstimatesDisabled(t *testing.T) {
	e := noEstimates()
	e.Fetch(t.Context(), []string{"AAA3"})
	if e.Enabled() || len(e.All()) != 0 || len(e.tried) != 0 {
		t.Errorf("got %+v, want nothing fetched", e)
	}
	if _, err := e.Get(t.Context(), "AAA3"); !errors.Is(err, ErrDisabled) {
		t.Errorf("get: %v, want ErrDisabled", err)
	}
}

func TestEstimatesGet(t *testing.T) {
	fail := errors.New("yahoo is down")
	est := &mock.Estimator{Estimates: map[string]yahoo.Estimate{"AAA3": {Target: 10}}, Err: map[string]error{"ERR3": fail}}
	e := withEstimator(est)

	if got, err := e.Get(t.Context(), "AAA3"); err != nil || got.Target != 10 {
		t.Errorf("covered: got %+v, %v", got, err)
	}
	if _, err := e.Get(t.Context(), "NONE3"); !errors.Is(err, yahoo.ErrNoCoverage) {
		t.Errorf("no coverage: got %v", err)
	}
	if _, err := e.Get(t.Context(), "ERR3"); !errors.Is(err, fail) {
		t.Errorf("failed: got %v, want the request error", err)
	}
	if _, err := e.Get(t.Context(), "AAA3"); err != nil || len(est.Requested) != 3 {
		t.Errorf("cached: got %v after %v requests, want each ticker asked once", err, est.Requested)
	}
	e.claim("BUSY3") // another request is fetching it
	if _, err := e.Get(t.Context(), "BUSY3"); !errors.Is(err, ErrPending) {
		t.Errorf("pending: got %v", err)
	}
}

// honorsContext fails once ctx ended, like the HTTP client of *yahoo.Client.
type honorsContext struct{ est *mock.Estimator }

func (h honorsContext) Estimate(ctx context.Context, ticker string) (yahoo.Estimate, error) {
	if err := ctx.Err(); err != nil {
		h.est.Requested = append(h.est.Requested, ticker)
		return yahoo.Estimate{}, err
	}
	return h.est.Estimate(ctx, ticker)
}

// A request that ran out of time leaves the ticker to be tried again, and stops asking.
func TestEstimatesRetryAfterTimeout(t *testing.T) {
	est := &mock.Estimator{Estimates: map[string]yahoo.Estimate{"AAA3": {Target: 10}, "BBB3": {Target: 20}}}
	e := withEstimator(honorsContext{est})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	e.Fetch(ctx, []string{"AAA3", "BBB3"})
	if len(est.Requested) != 1 || len(e.All()) != 0 {
		t.Fatalf("requested %v, got %v: want one try and nothing kept", est.Requested, e.All())
	}
	if _, err := e.Get(ctx, "AAA3"); !errors.Is(err, context.Canceled) {
		t.Errorf("get with an ended context: %v", err)
	}
	e.Fetch(t.Context(), []string{"AAA3", "BBB3"})
	if all := e.All(); len(all) != 2 {
		t.Errorf("got %v, want both fetched on the next try", all)
	}
}

// constant is safe for concurrent use, unlike mock.Estimator, to check Estimates under -race.
type constant struct{}

func (constant) Estimate(context.Context, string) (yahoo.Estimate, error) {
	return yahoo.Estimate{Target: 1}, nil
}

func TestEstimatesConcurrentUse(t *testing.T) {
	e := withEstimator(constant{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			e.Fetch(t.Context(), []string{"AAA3", "BBB3", "CCC3"})
			_ = e.All()
		})
	}
	wg.Wait()
	if len(e.All()) != 3 {
		t.Errorf("got %v", e.All())
	}
}

func TestAnalyzeTierSkipsStocksOffTheLastSession(t *testing.T) {
	analyses, s := analyzeTier(market(), lastDay, swing.DefaultParams(), swing.Tiers[0])
	tickers := make([]string, 0, len(analyses))
	for _, a := range analyses {
		tickers = append(tickers, a.Ticker)
	}
	slices.Sort(tickers)
	if !slices.Equal(tickers, []string{"FLAT3", "MED3", "WIN3"}) || s.Tier.Label != "forte" || s.Candidates != 1 {
		t.Errorf("got %v for tier %q with %d candidates", tickers, s.Tier.Label, s.Candidates)
	}
}

func TestTiersSummarizesEveryTier(t *testing.T) {
	got := Tiers(market(), lastDay, swing.DefaultParams())
	if len(got) != len(swing.Tiers) {
		t.Fatalf("got %d summaries", len(got))
	}
	// WIN3 is a candidate from forte down, MED3 from médio down; both have 5 winning trades in every tier.
	for i, want := range []int{1, 2, 2} {
		if got[i].Candidates != want || got[i].Trades != 10 || got[i].WinRate() != 1 || got[i].AvgReturn() <= 0 {
			t.Errorf("%s: got %+v", got[i].Tier.Label, got[i])
		}
	}
	if (Summary{}).WinRate() != 0 || (Summary{}).AvgReturn() != 0 {
		t.Error("an empty summary must not divide by zero")
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
