package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"text/tabwriter"
	"time"

	"oracle/config"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/internal/yahoo"
	"oracle/pkg/db"
)

type row struct {
	swing.Pick
	name     string
	strength string
}

type summary struct {
	tier       swing.Tier
	candidates int
	trades     int
	wins       int
	sum        float64
}

func main() {
	capital := flag.Float64("capital", 500, "capital available in BRL")
	top := flag.Int("top", 5, "number of stocks to recommend")
	risk := flag.Float64("risk", 2, "max loss per trade as a percentage of capital")
	years := flag.Int("years", 3, "years of history used in the backtest")
	noEstimates := flag.Bool("no-estimates", false, "skip analyst estimates")
	fill := flag.Bool("fill", false, "complete the list with weaker signals")
	flag.Parse()

	cfg := config.Load()

	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	history, err := quote.History(ctx, database, time.Now().AddDate(-*years, 0, 0))
	if err != nil {
		log.Fatalf("failed to load history: %v", err)
	}

	var latest time.Time
	for _, bars := range history {
		if day := bars[len(bars)-1].Date; day.After(latest) {
			latest = day
		}
	}
	if latest.IsZero() {
		log.Fatal("no quotes in the database, run `make sync` first")
	}
	if time.Since(latest) > 4*24*time.Hour {
		log.Printf("last session in the database is %s, run `make sync` to update", latest.Format("02/01/2006"))
	}

	params := swing.DefaultParams()
	estimates := newEstimates(ctx, !*noEstimates)

	var (
		rows      []row
		summaries []summary
		seen      = make(map[string]bool)
	)
	for _, t := range swing.Tiers {
		params.Oversold = t.Oversold
		s := summary{tier: t}

		var (
			analyses []swing.Analysis
			eligible []string
		)
		for ticker, bars := range history {
			if !bars[len(bars)-1].Date.Equal(latest) {
				continue
			}
			a := swing.Analyze(ticker, bars, params)
			s.trades += a.Trades
			s.wins += a.Wins
			s.sum += a.SumReturn
			analyses = append(analyses, a)
			if a.Eligible(params) {
				eligible = append(eligible, ticker)
			}
		}
		slices.Sort(eligible)
		s.candidates = len(eligible)
		summaries = append(summaries, s)

		estimates.fetch(ctx, eligible)
		for _, pick := range swing.Rank(analyses, estimates.byTicker, params, *capital, *risk, *top) {
			if len(rows) == *top {
				break
			}
			if seen[pick.Ticker] {
				continue
			}
			seen[pick.Ticker] = true
			bars := history[pick.Ticker]
			rows = append(rows, row{pick, bars[len(bars)-1].Name, t.Label})
		}
		if len(rows) == *top || !*fill {
			break
		}
	}

	report(rows, summaries, params, latest, *capital, *risk, *top, *years)
}

type estimates struct {
	client   *yahoo.Client
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

func report(rows []row, summaries []summary, params swing.Params, latest time.Time, capital, risk float64, top, years int) {
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
