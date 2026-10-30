# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```
docker compose up -d          # Postgres 16 on localhost:5432 (user/pass/db: oracle)
make sync [SINCE=2020]        # download B3 COTAHIST files into Postgres (incremental)
make recommend [CAPITAL= TOP= RISK= FILL=1]
make backtest  [FROM=YYYY-MM-DD CAPITAL= TOP= RISK= COST= RANK=expectancy|rsi CSV=file]

go build ./...
go vet ./...
make lint                     # golangci-lint v2, config in .golangci.yml; must report 0 issues
make lint-fix
go test ./...
go test ./internal/swing -run TestName -v   # single test
```

Tests don't need a database; they build synthetic quote series in memory. Extra flags not exposed by the Makefile (`-years`, `-no-estimates`, `-warmup`, `-lookback`, `-oversold`, `-min-volume`) are passed with `go run ./cmd/oracle <command> -flag`.

## Architecture

Go module `oracle`. One binary, `cmd/oracle`, whose first argument picks the command (`sync`, `recommend`, `backtest`, `api`) from a map. Each command is a `func Name(args []string)` in `internal/command` that parses its own `flag.FlagSet` and starts with:

```go
ctx, app := bootstrap.Start()   // config from .env, DB connection, Ctrl+C context; exits on failure
defer app.Close()
```

After `Start`, exit with `app.Fatalf` instead of `log.Fatalf`, so the deferred `Close` isn't skipped (`gocritic` flags it).

Data flow:

1. **`internal/b3`** parses B3's fixed-width COTAHIST files (annual/monthly/daily ZIPs), keeping only round-lot cash-market records. `ErrNotFound` means B3 hasn't published that file (holiday, weekend, session not closed) and is skipped, not an error.
2. **`internal/quote`** stores them via GORM (`Quote`, PK ticker+date). `SyncedFile` records every file already loaded, so sync is incremental and idempotent (one transaction per file, `ON CONFLICT DO NOTHING`). `History` loads stocks only (spec `ON`/`PN`/`UNT`) as `map[ticker][]Quote` ordered by date.
3. **`internal/swing`** is the strategy and the single source of truth for trading logic: `Signal` (close above SMA200 and RSI(2) below `Oversold`), `Levels` (limit entry 0.5 ATR below close, target +1 ATR, stop −2 ATR), `Simulate` (next-day fill, gaps, max hold of 5 sessions, costs), `Analyze` (per-ticker track record) and `Rank` (sizing by capital share and max risk).
4. **`command.Recommend`** runs `swing.Analyze` on every ticker that traded on the last session, tries `swing.Tiers` from strongest to weakest (stops after the first unless `-fill`), fetches analyst targets from **`internal/yahoo`** (unofficial API with cookie+crumb; failure degrades to history-only ranking) and prints the plan.
5. **`internal/backtest`** replays the same `swing` functions day by day over the whole portfolio with one cash balance. `prepare` builds the calendar, signals and per-ticker track records; `simulate` walks the days; `rank` picks candidates. `summary.go` computes return, drawdown, Sharpe and per-year stats against an equal-weight benchmark.

Invariants to preserve when changing strategy or backtest code:

- **No look-ahead.** Every decision at day `d` uses only data known at that close. A ticker's track record only counts trades whose exit is on or before `d` (`series.rating`).
- **Splits.** Prices are not adjusted. A day-over-day close ratio outside 0.6–1/0.6 is treated as a split: `swing.Segments` breaks the series (backtest) and `continuous` keeps only the part after the last split (recommend).
- **Shared logic.** `recommend` and `backtest` must keep calling the same `swing` functions, so backtest results stay representative of the live plan.

User-facing output (reports, column names, tier labels `forte`/`médio`/`fraco`/`tendência`) is in Portuguese; code, comments and README are in English.
