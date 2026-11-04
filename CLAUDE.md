# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```
docker compose up -d          # Postgres 16 on localhost:5432 (user/pass/db: oracle) and Redis 7 on localhost:6379
make sync [SINCE=2020]        # download B3 COTAHIST files into Postgres (incremental)
make recommend [CAPITAL= TOP= RISK= FILL=1]
make backtest  [FROM=YYYY-MM-DD CAPITAL= TOP= RISK= COST= RANK=expectancy|rsi CSV=file]

go build ./...
go vet ./...
make swag                     # regenerate the OpenAPI spec in docs/ from the swag annotations
make lint                     # golangci-lint v2, config in .golangci.yml; must report 0 issues
make lint-fix
go test ./...
go test ./internal/swing -run TestName -v   # single test
make test-integration         # starts postgres-test (port 5433) and redis-test (port 6380) and runs test/integration
make coverage [COVERAGE_MIN=90]  # unit + integration + binary coverage merged into coverage.out; fails below the minimum; same as CI
make test-db-down
make load-test [PROFILE=smoke|load|stress BASE_URL= TICKER=]   # k6 against a running API; apart from the tests above and never in the CI
```

The k6 load tests in `tests/load` (not `test/`) are not Go tests: `go test ./...`, `make coverage` and the CI never run them, and `.github/workflows/ci.yml` must keep it that way. They hit a running API read-only, without Yahoo (`estimates=false`); when a route is added or renamed, update `tests/load/common.js`.

Unit tests sit next to the code and don't need a database. Dependencies are contracts (`quote.Store`, `quote.Source`, `plan.Estimator`); unit tests use the in-memory mocks in `test/mock` (`mock.Store` is safe for concurrent use, like Postgres, because the API calls it from many goroutines) (tests that import it must be in the external `_test` package of `quote`, or it is an import cycle). `test/fixture` builds price series that trigger the strategy. Integration tests live in `test/integration` behind the `integration` build tag: they use only the `TEST_DB_*` variables (never `DB_*`), clone a seeded template database per test, and run in parallel. `test/contract` runs the same `quote.Store` suite against the mock and against Postgres; when the contract changes, update the suite, the mock and `quote.Postgres` together. Extra flags not exposed by the Makefile (`-years`, `-no-estimates`, `-warmup`, `-lookback`, `-oversold`, `-min-volume`) are passed with `go run ./cmd/oracle <command> -flag`.

## Architecture

Go module `oracle`. One binary, `cmd/oracle`, whose first argument picks the command (`sync`, `recommend`, `backtest`, `api`) from a map. Each command is a `func Name(args []string)` in `internal/command` that parses its own `flag.FlagSet` and starts with:

```go
ctx, app := bootstrap.Start()   // config from .env, DB connection, Ctrl+C context; exits on failure
defer app.Close()
```

After `Start`, exit with `app.Fatalf` instead of `log.Fatalf`, so the deferred `Close` isn't skipped (`gocritic` flags it).

Data flow:

1. **`internal/b3`** parses B3's fixed-width COTAHIST files (annual/monthly/daily ZIPs), keeping only round-lot cash-market records. `ErrNotFound` means B3 hasn't published that file (holiday, weekend, session not closed) and is skipped, not an error.
2. **`internal/quote`** stores them behind the `Store` contract; `Postgres` is the GORM implementation (`Quote`, PK ticker+date). The schema is built by versioned SQL migrations (`internal/quote/migrations/NNNN_name.sql`, embedded, recorded in `schema_migrations`, run by `Store.Migrate` at the start of every sync), not by GORM's AutoMigrate: to change the schema or add an index, add the next numbered file (idempotent: `IF NOT EXISTS`) and never edit one that already ran. `SyncedFile` records every file already loaded, so `SyncHistory` is incremental and idempotent (`SaveFile` is one transaction per file, `ON CONFLICT DO NOTHING`). `History` loads stocks only (spec `ON`/`PN`/`UNT`) as `map[ticker][]Quote` ordered by date. The read methods behind the API (`Tickers`, `Ticker`, `TickerQuotes`, `Session`, `LatestSession`) cover every spec; Postgres passes days as `YYYY-MM-DD` text so they compare as dates. `quote.StaleAfter` (4 days) is the shared rule for an out-of-date database.
3. **`internal/swing`** is the strategy and the single source of truth for trading logic: `Signal` (close above SMA200 and RSI(2) below `Oversold`), `Levels` (limit entry 0.5 ATR below close, target +1 ATR, stop −2 ATR), `Simulate` (next-day fill, gaps, max hold of 5 sessions, costs), `Analyze` (per-ticker track record) and `Rank` (sizing by capital share and max risk). `Scan` (every series on the latest session) and `TrackRecord` (the dated trades `Analyze` counts) are what the API's strategy routes and `recommend` share.
4. **`internal/plan`** builds the trading plan: `plan.Build` runs `swing.Scan` on every stock that traded on the last session, tries `swing.Tiers` from strongest to weakest (stops after the first unless `Fill`), fetches analyst targets from **`internal/yahoo`** through `plan.Estimates` (unofficial API with cookie+crumb; failure degrades to history-only ranking; safe for concurrent use) and ranks with `swing.Rank`. `command.Recommend` prints it; the API's `/recommendations` serves it, caching one Yahoo client and its targets per trading session.
5. **`internal/backtest`** replays the same `swing` functions day by day over the whole portfolio with one cash balance. `prepare` builds the calendar, signals and per-ticker track records; `simulate` walks the days; `rank` picks candidates. `summary.go` computes return, drawdown, Sharpe and per-year stats against an equal-weight benchmark.

Invariants to preserve when changing strategy or backtest code:

- **No look-ahead.** Every decision at day `d` uses only data known at that close. A ticker's track record only counts trades whose exit is on or before `d` (`series.rating`).
- **Splits.** Prices are not adjusted. A day-over-day close ratio outside 0.6–1/0.6 is treated as a split: `swing.Segments` breaks the series (backtest) and `continuous` keeps only the part after the last split (recommend).
- **Shared logic.** `recommend`, `backtest` and the API must keep calling the same `swing` functions, so backtest results stay representative of the live plan.

The **cache** (`internal/cache`: the `Cache` contract, `Redis` its implementation, `mock.Cache` for unit tests, `test/contract.Cache` the shared suite) keeps the 200 answers of the data routes for 1 minute (`cacheTTL` in `internal/command/api.go`): `handler.ResponseCache` wraps each route in `newMux` (a nil cache turns it off), keys them by path and sorted query, shares identical misses through singleflight and serves without the cache when Redis fails. `oracle sync` calls `flushCache` when it ends, dropping every key under `REDIS_PREFIX`; integration tests give each test its own prefix, so they never empty each other\'s cache. A route added to `newMux` must decide whether it goes through `cached(...)`: yes for anything that reads the database or computes the strategy.

The HTTP API (`oracle api`, routes in `internal/command/api.go`, registered in `newMux`; handlers in `internal/handler` read through `handler.QuoteReader`/`StrategyReader`, subsets of `quote.Store`; `handler.Histories` keeps the stock history in memory per years value until `LatestSession` changes and is shared read-only, so `swing` and `plan` must never write into the series they get; `withTimeout` answers 503 after 9 s; Ctrl+C shuts the server down) is documented twice. Swagger: swag annotations (general info on `API`, one block per handler in `internal/handler`) generate `docs/docs.go`, `docs/swagger.json` and `docs/swagger.yaml` with `make swag` (never edit them by hand; CI fails if they are stale), served at `/swagger/index.html`. Postman: the collection `docs/postman-collection.json` (v2.1); when a route changes, update its request, examples and tests there and check with `postman collection run` against a running server.

Project skills in `.claude/skills`: `flow-diagram` keeps `docs/flow.html` (the flow diagram) and the docs that describe it up to date (`scripts/flow.py check` compares the page with the code), `postman-collection` keeps the Postman collection, `go-reviewer` reviews Go code. Run `flow-diagram` when a command, package, route or invariant changes.

User-facing output (reports, column names, tier labels `forte`/`médio`/`fraco`/`tendência`) is in Portuguese; code, comments and README are in English.
