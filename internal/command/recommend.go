package command

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"oracle/internal/bootstrap"
	"oracle/internal/plan"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
)

type options struct {
	plan.Options
	years       int
	noEstimates bool
}

// Recommend prints the trading plan for the next session.
func Recommend(args []string) {
	fs := flag.NewFlagSet("oracle recommend", flag.ExitOnError)
	var opts options
	fs.Float64Var(&opts.Capital, "capital", 500, "capital available in BRL")
	fs.IntVar(&opts.Top, "top", 5, "number of stocks to recommend")
	fs.Float64Var(&opts.Risk, "risk", 2, "max loss per trade as a percentage of capital")
	fs.IntVar(&opts.years, "years", 3, "years of history used in the backtest")
	fs.BoolVar(&opts.noEstimates, "no-estimates", false, "skip analyst estimates")
	fs.BoolVar(&opts.Fill, "fill", false, "complete the list with weaker signals")
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
	estimates := plan.NewEstimates(analysts(ctx, !opts.noEstimates))
	rows, summaries := plan.Build(ctx, history, latest, params, estimates, opts.Options)
	printPlan(rows, summaries, params, latest, opts)
}

// analysts connects to Yahoo for analyst targets, or returns nil to rank by history only.
func analysts(ctx context.Context, enabled bool) plan.Estimator {
	if !enabled {
		return nil
	}
	client, err := yahoo.NewClient(ctx)
	if err != nil {
		log.Printf("analyst estimates unavailable, ranking by history only: %v", err)
		return nil
	}
	return client
}

func printPlan(rows []plan.Row, summaries []plan.Summary, params swing.Params, latest time.Time, opts options) {
	capital, risk, top, years := opts.Capital, opts.Risk, opts.Top, opts.years
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
			r.Ticker, r.Name, r.Tier, r.Qty, r.MaxEntry, r.Target, r.Stop, r.Invested(), r.Gain(), r.Loss(),
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
		if s.Trades == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s\t%s\t%d candidatas hoje\t%d operações\t%.1f%% de acerto\t%+.2f%% por operação\n",
			s.Tier.Label, s.Tier.Rule, s.Candidates, s.Trades, 100*s.WinRate(), 100*s.AvgReturn())
	}
	w.Flush()
}
