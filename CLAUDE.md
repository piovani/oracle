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
make swagger                  # regenerate the OpenAPI spec in docs/ from the swag annotations
make lint                     # golangci-lint v2, config in .golangci.yml; must report 0 issues
make lint-fix
go test ./...
go test ./internal/swing -run TestName -v   # single test
make test-integration         # starts postgres-test (port 5433) and runs test/integration
make coverage [COVERAGE_MIN=90]  # unit + integration + binary coverage merged into coverage.out; fails below the minimum; same as CI
make test-db-down
```

Unit tests sit next to the code and don't need a database. Dependencies are contracts (`quote.Store`, `quote.Source`, the `estimator` in `command`); unit tests use the in-memory mocks in `test/mock` (tests that import it must be in the external `_test` package of `quote`, or it is an import cycle). `test/fixture` builds price series that trigger the strategy. Integration tests live in `test/integration` behind the `integration` build tag: they use only the `TEST_DB_*` variables (never `DB_*`), clone a seeded template database per test, and run in parallel. `test/contract` runs the same `quote.Store` suite against the mock and against Postgres; when the contract changes, update the suite, the mock and `quote.Postgres` together. Extra flags not exposed by the Makefile (`-years`, `-no-estimates`, `-warmup`, `-lookback`, `-oversold`, `-min-volume`) are passed with `go run ./cmd/oracle <command> -flag`.

## Architecture

Go module `oracle`. One binary, `cmd/oracle`, whose first argument picks the command (`sync`, `recommend`, `backtest`, `api`) from a map. Each command is a `func Name(args []string)` in `internal/command` that parses its own `flag.FlagSet` and starts with:

```go
ctx, app := bootstrap.Start()   // config from .env, DB connection, Ctrl+C context; exits on failure
defer app.Close()
```

After `Start`, exit with `app.Fatalf` instead of `log.Fatalf`, so the deferred `Close` isn't skipped (`gocritic` flags it).

Data flow:

1. **`internal/b3`** parses B3's fixed-width COTAHIST files (annual/monthly/daily ZIPs), keeping only round-lot cash-market records. `ErrNotFound` means B3 hasn't published that file (holiday, weekend, session not closed) and is skipped, not an error.
2. **`internal/quote`** stores them behind the `Store` contract; `Postgres` is the GORM implementation (`Quote`, PK ticker+date). `SyncedFile` records every file already loaded, so `SyncHistory` is incremental and idempotent (`SaveFile` is one transaction per file, `ON CONFLICT DO NOTHING`). `History` loads stocks only (spec `ON`/`PN`/`UNT`) as `map[ticker][]Quote` ordered by date.
3. **`internal/swing`** is the strategy and the single source of truth for trading logic: `Signal` (close above SMA200 and RSI(2) below `Oversold`), `Levels` (limit entry 0.5 ATR below close, target +1 ATR, stop −2 ATR), `Simulate` (next-day fill, gaps, max hold of 5 sessions, costs), `Analyze` (per-ticker track record) and `Rank` (sizing by capital share and max risk).
4. **`command.Recommend`** runs `swing.Analyze` on every ticker that traded on the last session, tries `swing.Tiers` from strongest to weakest (stops after the first unless `-fill`), fetches analyst targets from **`internal/yahoo`** (unofficial API with cookie+crumb; failure degrades to history-only ranking) and prints the plan.
5. **`internal/backtest`** replays the same `swing` functions day by day over the whole portfolio with one cash balance. `prepare` builds the calendar, signals and per-ticker track records; `simulate` walks the days; `rank` picks candidates. `summary.go` computes return, drawdown, Sharpe and per-year stats against an equal-weight benchmark.

Invariants to preserve when changing strategy or backtest code:

- **No look-ahead.** Every decision at day `d` uses only data known at that close. A ticker's track record only counts trades whose exit is on or before `d` (`series.rating`).
- **Splits.** Prices are not adjusted. A day-over-day close ratio outside 0.6–1/0.6 is treated as a split: `swing.Segments` breaks the series (backtest) and `continuous` keeps only the part after the last split (recommend).
- **Shared logic.** `recommend` and `backtest` must keep calling the same `swing` functions, so backtest results stay representative of the live plan.

The HTTP API (`oracle api`, routes in `internal/command/api.go`) is documented twice. Swagger: swag annotations (general info on `API`, one block per handler in `internal/handler`) generate `docs/docs.go`, `docs/swagger.json` and `docs/swagger.yaml` with `make swagger` (never edit them by hand; CI fails if they are stale), served at `/swagger/index.html`. Postman: the collection `docs/postman-collection.json` (v2.1); when a route changes, update its request, examples and tests there and check with `postman collection run` against a running server.

User-facing output (reports, column names, tier labels `forte`/`médio`/`fraco`/`tendência`) is in Portuguese; code, comments and README are in English.
