package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"oracle/internal/bootstrap"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
)

type row struct {
	swing.Pick
	name     string
	strength string
}

// summary is how one signal level performed across the whole universe.
type summary struct {
	tier       swing.Tier
	candidates int
	trades     int
	wins       int
	sum        float64
}

type options struct {
	capital     float64
	top         int
	risk        float64
	years       int
	noEstimates bool
	fill        bool
}

// Recommend prints the trading plan for the next session.
func Recommend(args []string) {
	fs := flag.NewFlagSet("oracle recommend", flag.ExitOnError)
	var opts options
	fs.Float64Var(&opts.capital, "capital", 500, "capital available in BRL")
	fs.IntVar(&opts.top, "top", 5, "number of stocks to recommend")
	fs.Float64Var(&opts.risk, "risk", 2, "max loss per trade as a percentage of capital")
	fs.IntVar(&opts.years, "years", 3, "years of history used in the backtest")
	fs.BoolVar(&opts.noEstimates, "no-estimates", false, "skip analyst estimates")
	fs.BoolVar(&opts.fill, "fill", false, "complete the list with weaker signals")
	_ = fs.Parse(args) // ExitOnError: a bad flag prints the usage and exits

	ctx, app := bootstrap.Start()
	defer app.Close()

	history, err := quote.NewPostgres(app.DB).History(ctx, time.Now().AddDate(-opts.years, 0, 0))
	if err != nil {
		app.Fatalf("failed to load history: %v", err)
	}

	latest := quote.LastSession(history)
	if latest.IsZero() {
		app.Fatalf("no quotes in the database, run `make sync` first")
	}
	if time.Since(latest) > quote.StaleAfter {
		log.Printf("last session in the database is %s, run `make sync` to update", latest.Format("02/01/2006"))
	}

	params := swing.DefaultParams()
	estimates := newEstimates(ctx, !opts.noEstimates)
	rows, summaries := buildPlan(ctx, history, latest, params, estimates, opts)
	printPlan(rows, summaries, params, latest, opts)
}

// buildPlan tries the signal levels from strongest to weakest. It stops at the first level,
// unless -fill is set and the list is still short.
func buildPlan(ctx context.Context, history map[string][]quote.Quote, latest time.Time, params swing.Params, estimates *estimates, opts options) ([]row, []summary) {
	var rows []row
	var summaries []summary
	taken := make(map[string]bool)

	for _, tier := range swing.Tiers {
		params.Oversold = tier.Oversold
		analyses, s := analyzeTier(history, latest, params, tier)

		eligible := eligibleTickers(analyses, params)
		s.candidates = len(eligible)
		summaries = append(summaries, s)

		estimates.fetch(ctx, eligible)
		for _, pick := range swing.Rank(analyses, estimates.byTicker, params, opts.capital, opts.risk, opts.top) {
			if len(rows) == opts.top {
				break
			}
			if taken[pick.Ticker] {
				continue
			}
			taken[pick.Ticker] = true
			bars := history[pick.Ticker]
			rows = append(rows, row{pick, bars[len(bars)-1].Name, tier.Label})
		}
		if len(rows) == opts.top || !opts.fill {
			break
		}
	}
	return rows, summaries
}

// analyzeTier runs the rule on every stock traded on the last session.
func analyzeTier(history map[string][]quote.Quote, latest time.Time, params swing.Params, tier swing.Tier) ([]swing.Analysis, summary) {
	s := summary{tier: tier}
	analyses := swing.Scan(history, latest, params)
	for _, a := range analyses {
		s.trades += a.Trades
		s.wins += a.Wins
		s.sum += a.SumReturn
	}
	return analyses, s
}

func eligibleTickers(analyses []swing.Analysis, params swing.Params) []string {
	var tickers []string
	for _, a := range analyses {
		if a.Eligible(params) {
			tickers = append(tickers, a.Ticker)
		}
	}
	slices.Sort(tickers)
	return tickers
}

// estimator gives analyst targets; *yahoo.Client in production.
type estimator interface {
	Estimate(ctx context.Context, ticker string) (yahoo.Estimate, error)
}

type estimates struct {
	client   estimator
	byTicker map[string]yahoo.Estimate
	tried    map[string]bool
}

func newEstimates(ctx context.Context, enabled bool) *estimates {
	e := &estimates{byTicker: make(map[string]yahoo.Estimate), tried: make(map[string]bool)}
	if !enabled {
		return e
	}
	client, err := yahoo.NewClient(ctx)
	if err != nil {
		log.Printf("analyst estimates unavailable, ranking by history only: %v", err)
		return e
	}
	e.client = client
	return e
}

func (e *estimates) fetch(ctx context.Context, tickers []string) {
	if e.client == nil {
		return
	}
	for _, ticker := range tickers {
		if e.tried[ticker] {
			continue
		}
		e.tried[ticker] = true

		estimate, err := e.client.Estimate(ctx, ticker)
		if errors.Is(err, yahoo.ErrNoCoverage) {
			continue
		}
		if err != nil {
			log.Printf("%s: estimate skipped: %v", ticker, err)
			continue
		}
		e.byTicker[ticker] = estimate
	}
}

func printPlan(rows []row, summaries []summary, params swing.Params, latest time.Time, opts options) {
	capital, risk, top, years := opts.capital, opts.risk, opts.top, opts.years
	fmt.Printf("Plano de operações — base: pregão de %s\n", latest.Format("02/01/2006"))
	fmt.Printf("Capital R$ %.2f | até R$ %.2f por ação | perda máxima R$ %.2f por operação\n\n",
		capital, capital/float64(top), capital*risk/100)

	if len(rows) == 0 {
		fmt.Println("Nenhuma ação passou nos filtros hoje. Ficar fora também é uma operação.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ATIVO\tEMPRESA\tSINAL\tQTD\tCOMPRA ATÉ\tVENDA\tSTOP\tINVESTE\tGANHO\tPERDA\tOPERAÇÕES\tACERTO\tMÉDIA\tALVO ANALISTAS\tUPSIDE\tNOTA")
	invested := 0.0
	for _, r := range rows {
		target, upside, rating := "-", "-", "-"
		if r.Estimate != nil {
			target = fmt.Sprintf("%.2f (%d)", r.Estimate.Target, r.Estimate.Analysts)
			upside = fmt.Sprintf("%+.1f%%", 100*r.Upside)
			if r.Estimate.Rating > 0 {
				rating = fmt.Sprintf("%.1f", r.Estimate.Rating)
			}
		}
		fmt.Fprintf(w, "%sF\t%s\t%s\t%d\t%.2f\t%.2f\t%.2f\t%.2f\t+%.2f\t-%.2f\t%d\t%.0f%%\t%+.2f%%\t%s\t%s\t%s\n",
			r.Ticker, r.name, r.strength, r.Qty, r.MaxEntry, r.Target, r.Stop, r.Invested(), r.Gain(), r.Loss(),
			r.Trades, 100*r.WinRate(), 100*r.AvgReturn(), target, upside, rating)
		invested += r.Invested()
	}
	w.Flush()

	fmt.Printf("\nTotal investido: R$ %.2f | em caixa: R$ %.2f\n", invested, capital-invested)

	fmt.Println("\nComo executar:")
	fmt.Println("  1. Antes da abertura, envie no fracionário uma ordem de compra limitada em COMPRA ATÉ, válida só para o dia.")
	fmt.Println("     Se o leilão de abertura indicar preço no STOP ou abaixo, cancele. Se a ordem não executar no dia, descarte.")
	fmt.Println("  2. Comprou: deixe a ordem de venda em VENDA e o stop em STOP.")
	fmt.Printf("  3. Se nenhum dos dois for atingido em %d pregões, venda no fechamento.\n", params.MaxHold)
	fmt.Println("  NOTA: consenso dos analistas, de 1 (compra forte) a 5 (venda).")

	fmt.Printf("\nSinais usados e desempenho de cada regra em todo o universo (%d anos, sem seleção):\n", years)
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, s := range summaries {
		if s.trades == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s\t%s\t%d candidatas hoje\t%d operações\t%.1f%% de acerto\t%+.2f%% por operação\n",
			s.tier.Label, s.tier.Rule, s.candidates, s.trades, 100*float64(s.wins)/float64(s.trades), 100*s.sum/float64(s.trades))
	}
	w.Flush()
}
