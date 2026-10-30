package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"oracle/internal/backtest"
	"oracle/internal/bootstrap"
	"oracle/internal/quote"
	"oracle/internal/swing"
)

func main() {
	defaults := swing.DefaultParams()

	capital := flag.Float64("capital", 500, "initial capital in BRL")
	positions := flag.Int("top", 5, "maximum simultaneous positions")
	risk := flag.Float64("risk", 2, "max loss per trade as a percentage of equity")
	fromFlag := flag.String("from", "", "first day of the test, YYYY-MM-DD (default: 3 years ago)")
	warmup := flag.Int("warmup", 4, "years of history before the start, used for signals and per-ticker track records")
	lookback := flag.Int("lookback", 3, "years of closed trades used to rate a ticker, 0 for all")
	rankFlag := flag.String("rank", "expectancy", "ranking: expectancy (per-ticker track record, as recommend) or rsi (no per-ticker selection)")
	oversold := flag.Float64("oversold", defaults.Oversold, "RSI(2) threshold of the signal")
	cost := flag.Float64("cost", defaults.CostPct, "cost per side, in % of the trade")
	minVolume := flag.Float64("min-volume", defaults.MinVolume, "minimum median daily volume in BRL")
	tradesFile := flag.String("csv", "", "write every executed trade to this CSV file")
	flag.Parse()

	from := time.Now().AddDate(-3, 0, 0)
	if *fromFlag != "" {
		var err error
		if from, err = time.Parse("2006-01-02", *fromFlag); err != nil {
			log.Fatalf("invalid -from: %v", err)
		}
	}
	rank := backtest.RankExpectancy
	switch *rankFlag {
	case "expectancy":
	case "rsi":
		rank = backtest.RankRSI
	default:
		log.Fatalf("invalid -rank %q, use expectancy or rsi", *rankFlag)
	}

	params := defaults
	params.Oversold = *oversold
	params.CostPct = *cost
	params.MinVolume = *minVolume

	ctx, app := bootstrap.Start()
	defer app.Close()

	history, err := quote.History(ctx, app.DB, from.AddDate(-*warmup, 0, 0))
	if err != nil {
		log.Fatalf("failed to load history: %v", err)
	}

	result, err := backtest.Run(history, backtest.Config{
		Params:    params,
		Capital:   *capital,
		Positions: *positions,
		RiskPct:   *risk,
		From:      from,
		Lookback:  *lookback,
		Rank:      rank,
	})
	if err != nil {
		log.Fatalf("backtest failed: %v", err)
	}

	report(result, params, *positions, *risk, *rankFlag)

	if *tradesFile != "" {
		if err := writeTrades(*tradesFile, result.Trades); err != nil {
			log.Fatalf("failed to write trades: %v", err)
		}
		fmt.Printf("\n%d operações gravadas em %s\n", len(result.Trades), *tradesFile)
	}
}

func report(r backtest.Result, p swing.Params, positions int, risk float64, rank string) {
	if len(r.Days) == 0 {
		fmt.Println("Sem pregões no período.")
		return
	}
	s := r.Summary()
	first, last := r.Days[0].Date, r.Days[len(r.Days)-1].Date

	fmt.Printf("Backtest — %s a %s (%.1f anos)\n", first.Format("02/01/2006"), last.Format("02/01/2006"), s.Years)
	fmt.Printf("Capital R$ %.2f | até %d posições | risco %.1f%% | custo %.2f%% por lado | ranking %s | RSI(2) < %.0f\n\n",
		r.Initial, positions, risk, p.CostPct, rank, p.Oversold)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\tESTRATÉGIA\tUNIVERSO IGUAL-PESO\n")
	fmt.Fprintf(w, "Retorno total\t%+.1f%%\t%+.1f%%\n", 100*s.Return, 100*s.Benchmark)
	fmt.Fprintf(w, "Retorno anual\t%+.1f%%\t%+.1f%%\n", 100*s.CAGR, 100*s.BenchmarkCAGR)
	fmt.Fprintf(w, "Queda máxima\t%.1f%%\t%.1f%%\n", 100*s.MaxDrawdown, 100*s.BenchmarkDrawdown)
	fmt.Fprintf(w, "Capital final\tR$ %.2f\t\n", s.Final)
	fmt.Fprintf(w, "Volatilidade anual\t%.1f%%\t\n", 100*s.Volatility)
	fmt.Fprintf(w, "Sharpe (sem taxa livre de risco)\t%.2f\t\n", s.Sharpe)
	fmt.Fprintf(w, "Exposição média\t%.0f%%\t\n", 100*s.Exposure)
	w.Flush()

	fmt.Printf("\nOperações: %d | ordens enviadas: %d, executadas: %.0f%% | acerto: %.0f%% | média: %+.2f%% | fator de lucro: %.2f\n",
		s.Trades, r.Orders, 100*s.FillRate, 100*s.WinRate, 100*s.AvgReturn, s.ProfitFactor)

	fmt.Println("\nPor ano:")
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  ANO\tESTRATÉGIA\tUNIVERSO\tOPERAÇÕES\tACERTO\tMÉDIA")
	for _, y := range r.Years() {
		fmt.Fprintf(w, "  %d\t%+.1f%%\t%+.1f%%\t%d\t%.0f%%\t%+.2f%%\n",
			y.Year, 100*y.Return, 100*y.Benchmark, y.Trades, 100*y.WinRate, 100*y.AvgReturn)
	}
	w.Flush()

	fmt.Println("\nO que está dentro: ações que saíram da bolsa, custos nos dois lados, caixa único com limite de posições,")
	fmt.Println("e só informação disponível no fechamento de cada dia (inclusive o histórico de cada ação).")
	fmt.Println("O que não está: dividendos (preços não ajustados), taxa livre de risco, spread e slippage do fracionário.")
}

func writeTrades(path string, trades []backtest.Executed) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	w.Write([]string{"ticker", "signal", "entry", "exit", "qty", "entry_price", "exit_price", "return", "pnl"})
	for _, t := range trades {
		w.Write([]string{
			t.Ticker, t.Signal.Format("2006-01-02"), t.Entry.Format("2006-01-02"), t.Exit.Format("2006-01-02"),
			strconv.Itoa(t.Qty), fmt.Sprintf("%.2f", t.EntryPrice), fmt.Sprintf("%.2f", t.ExitPrice),
			fmt.Sprintf("%.6f", t.Return), fmt.Sprintf("%.2f", t.PnL),
		})
	}
	w.Flush()
	return w.Error()
}
