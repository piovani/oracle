# oracle

[![CI](https://github.com/piovani/oracle/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/piovani/oracle/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)](go.mod)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)](docker-compose.yml)
[![Coverage](https://img.shields.io/badge/coverage-%E2%89%A590%25-brightgreen)](#tests)
[![Swagger](https://img.shields.io/badge/docs-Swagger-85EA2D?logo=swagger&logoColor=black)](docs/swagger.yaml)
[![Postman](https://img.shields.io/badge/docs-Postman-FF6C37?logo=postman&logoColor=white)](docs/postman-collection.json)

B3 (Brazilian stock exchange) quote history stored in Postgres, plus a command that builds the day's trading plan from it.

For a visual overview, open [`docs/flow.html`](docs/flow.html) in a browser. It shows the path of each command (`sync`, `recommend`, `backtest`), one trade session by session, and the rules the code must not break.

## Setup

Requires Go 1.27.3 and Docker.

```
docker compose up -d
cp .env.example .env
make sync
```

`make sync` downloads B3's COTAHIST files and stores the cash market quotes (standard lot). Use `make sync SINCE=2020` to limit the first year.

The sync is incremental: every downloaded file is recorded in the database and never downloaded again. Run it every day after the close to bring in the latest session.

### Configuration

Everything comes from `.env` (or environment variables). The defaults match `docker-compose.yml`, so nothing needs to change to run locally.

| Variable | Default |
|---|---|
| `DB_HOST`, `DB_PORT` | `localhost`, `5432` |
| `DB_USER`, `DB_PASSWORD`, `DB_NAME` | `oracle` |
| `DB_SSLMODE` | `disable` |
| `PORT` | `8080` (used only by the API) |
| `ENV` | `development` |

## Trading plan

```
make sync
make recommend
make recommend CAPITAL=2000 TOP=5 RISK=2
```

| Variable | Default | Meaning |
|---|---|---|
| `CAPITAL` | 500 | available capital, in BRL |
| `TOP` | 5 | maximum number of stocks in the plan |
| `RISK` | 2 | maximum loss per trade, as % of capital |
| `FILL` | — | `FILL=1` fills the list with weaker signals |

The binary also takes `-years` (years of history, default 3) and `-no-estimates` (ignores analyst estimates).

For each stock, the output shows: company name, signal strength, quantity, buy order price, sell price (target), stop, amount invested, gain at target, loss at stop, how the rule performed on that stock's history, and the analysts' price target. The output itself is in Portuguese.

### What it does

- **History:** uses the last 3 years in the database, only stocks (ON, PN and units) with a median volume above BRL 5 million per day.
- **Buy signal:** a stock above its 200-day average that fell hard over the last 2 sessions (mean reversion, 2-period RSI below 10).
- **Strong signals only, by default.** On many days that means fewer than 5 stocks, or none. With `FILL=1` the list is filled with weaker levels, in this order: `médio` (RSI < 30), `fraco` (RSI < 50) and `tendência` (above the 200-day average, no pullback required). In simulation over the last 3 years these levels did not pay off.
- **Buy:** limit order 0.5 ATR below the last close, valid only for the next session. The stock has to dip a bit further during the day for the order to fill; about 60% of the time it doesn't.
- **Sell and stop:** target 1 ATR above the last close, stop 2 ATR below, forced exit after 5 sessions. Measured from the buy price, target and stop are 1.5 ATR away each. ATR is the average daily range over the last 14 sessions.
- **Estimates:** analyst price target and consensus rating come from Yahoo Finance. Stocks with a target below the current price are dropped. The ranking combines the rule's past return on the stock with the estimated upside.
- **Sizing:** capital is split by the number of positions, and the loss per trade is capped at `RISK`% of capital. Stocks that don't fit in one position's share are left out. With BRL 500 that is BRL 100 per position, and everything goes to the odd-lot market, hence the `F` in the ticker.

### How to run the plan

1. Before the open, send a limit buy order at the given price, valid for the day only.
2. If the opening auction points to a price at or below the stop, cancel it. If the order doesn't fill during the day, drop it.
3. Once bought: leave the sell order at the target and the stop at the given price.
4. If neither is hit within 5 sessions, sell at the close.

### Backtest

```
make backtest
make backtest FROM=2019-01-01 COST=0.1 RANK=rsi CSV=trades.csv
```

Replays the strategy day by day over the whole portfolio, with a single cash balance and at most `TOP` simultaneous positions. Each day it only uses what was known at the close: signals, order prices, liquidity and the stock's own track record (closed trades only, within a `-lookback` window in years). Delisted stocks stay in the universe, and splits break a series into independent segments. A series that ends with an open position is closed at its last close.

| Variable | Default | Meaning |
|---|---|---|
| `FROM` | 3 years ago | first day of the test (`YYYY-MM-DD`) |
| `CAPITAL`, `TOP`, `RISK` | 500, 5, 2 | same as `recommend` |
| `COST` | 0.03 | cost per side, as % of the trade |
| `RANK` | `expectancy` | `expectancy` filters and ranks by the stock's track record (like `recommend`); `rsi` does no per-stock selection |
| `CSV` | — | writes every executed trade to this file |

The binary also takes `-warmup`, `-lookback`, `-oversold` and `-min-volume`. The output shows return, max drawdown, Sharpe, exposure, order fill rate, win rate and a per-year table, next to an equal-weight universe (all liquid stocks, rebalanced daily) as a reference. There is no index in the database, and neither the strategy nor the reference include dividends.

### Limitations

- **The statistical edge is small.** Six entry rules were tested on 2015–2021 and validated on 2022–2026; all of them came out near zero in validation. The rule in use was the only one that wasn't negative in both periods. Fifteen variations of exit, entry and filters were tested afterwards; only the limit order improved the result in both periods (+0.41% and +0.19% per trade, against +0.07% and +0.04%).
- **In a day-by-day simulation over the last 3 years, strong signals only, the average return was +0.18% per trade, with a 51% win rate.** The simulation assumes the limit order fills whenever the day's low touches the price, and that target and stop fill exactly at their price. In the odd-lot market real execution tends to be worse.
- **The per-stock return is optimistic.** Picking the best stocks by their own history inflates the number.
- **In BRL it is little.** With BRL 100 per position, +0.18% is BRL 0.18 per trade.
- **It is not day trading.** The database only has daily data, so the plan is a swing trade, lasting up to 5 sessions.
- **Yahoo Finance is an unofficial API.** If it fails, the command warns and ranks by history only.
- **B3 prices are not adjusted for dividends.** Splits and reverse splits are detected, and the history before them is discarded.
- **The `F` suffix is always added.** For quantities of 100 or more, the part in lots of 100 should go to the ticker without the `F`.

## Project layout

See [`docs/flow.html`](docs/flow.html) for a diagram of how these pieces connect.

```
docs/          flow diagram (open flow.html in a browser)
cmd/oracle/    the single binary; the first argument picks the command
internal/
  command/     one function per command: Sync, Recommend, Backtest, API
  bootstrap/   startup shared by every command
  b3/          COTAHIST file reader
  quote/       quote model, sync and history loading
  swing/       the strategy: signal, order prices, simulation and ranking
  backtest/    day-by-day portfolio simulation and results summary
  yahoo/       analyst estimates
config/        reads .env
pkg/db/        Postgres connection
test/
  mock/        in-memory implementations of the contracts, for unit tests
  contract/    behavior shared by every implementation of a contract
  fixture/     synthetic quote series shaped to trigger the strategy
  integration/ tests against a real Postgres and the compiled binary
docs/          flow diagram (flow.html), OpenAPI spec generated by swag and Postman collection of the HTTP API
```

The project builds one binary, `oracle`, and the first argument picks the command:

```
go build -o oracle ./cmd/oracle
./oracle sync -since 2020
./oracle recommend -capital 2000
./oracle backtest -from 2019-01-01
./oracle api
./oracle recommend -h    # flags of a command
```

The `make` targets call the same binary through `go run ./cmd/oracle <command>`. To add a command, write a `func Name(args []string)` in `internal/command` and register it in the map in `cmd/oracle/main.go`.

Every command starts the same way:

```go
ctx, app := bootstrap.Start()
defer app.Close()
```

`Start` loads the configuration, connects to the database and returns a context that is canceled on Ctrl+C. From there the command uses `app.DB` and `app.Config`.

To change the strategy, go to `internal/swing`. `recommend` and `backtest` share the same signal and simulation functions, so a change to one applies to the other.

## HTTP API

`oracle api` serves the HTTP API on `PORT` (default `8080`). Every route is a read-only `GET` that answers JSON; errors come as `{"error": "..."}`.

| Route | Returns |
|---|---|
| `GET /health` | `{"status":"ok","time":"<UTC, RFC 3339>"}` |
| `GET /tickers` | every ticker stored, of any spec, with its first and last session |
| `GET /tickers/{ticker}` | one ticker and its latest quote (`404` if it has no quotes) |
| `GET /tickers/{ticker}/quotes?from=&to=` | its daily quotes, in date order; `from`/`to` are optional `YYYY-MM-DD`, both included |
| `GET /sessions/latest` | the last day with quotes and whether it is stale (older than 4 days) |
| `GET /sessions/{date}/quotes` | every quote of one day (`404` on weekends, holidays or days not synced) |

Prices are as traded, not adjusted for splits or dividends, like everywhere else in the project.

### Swagger

The running server documents itself with Swagger UI at [`/swagger/index.html`](http://localhost:8080/swagger/index.html); the OpenAPI (Swagger 2.0) spec is at `/swagger/doc.json`, and a copy lives in `docs/swagger.json` and `docs/swagger.yaml`.

The spec is generated by [swag](https://github.com/swaggo/swag) from annotations in the code: the general information on `API` in `internal/command/api.go` and one block per route on its handler in `internal/handler`. After adding or changing a route or its annotations, regenerate and commit `docs/`:

```
make swagger
```

CI fails when the committed spec is stale.

### Postman

The API is documented in `docs/postman-collection.json` (Postman Collection v2.1): each request has its description, saved examples (including the `405` for a wrong method) and tests. Import the file in Postman, or run it from the terminal with the [Postman CLI](https://learning.postman.com/docs/postman-cli/postman-cli-overview/):

```
postman collection run docs/postman-collection.json --env-var baseUrl=http://localhost:8080
```

`baseUrl` defaults to `http://localhost:8080`. When a route is added or changed, update its request and examples in the collection in the same change.

## Docker

```
make docker-build
docker run --rm --env-file .env oracle sync
docker run --rm --env-file .env oracle recommend -capital 2000
docker run --rm --env-file .env -p 8080:8080 oracle    # api, the default
```

The image is built for `linux/arm64` and holds only the `oracle` binary. Inside a container `localhost` is the container itself, so point `DB_HOST` at the database host (for example `postgres` when running next to the compose service).

## Tests

```
go test ./...
```

The unit tests sit next to the code they test and don't need a database. What a package depends on is a contract: `quote.Store` for the database, `quote.Source` for the B3 files and an estimator for Yahoo in `recommend`. Unit tests use the in-memory implementations in `test/mock`. The B3 and Yahoo HTTP clients themselves are tested against local HTTP servers.

The integration tests are in `test/integration`, behind the `integration` build tag, so `go test ./...` skips them:

```
make test-integration    # starts the test database, then runs the tests
make coverage            # unit + integration tests with merged coverage; fails below COVERAGE_MIN (90)
make test-db-down        # stops it
```

`make coverage` counts the unit tests, the integration tests and the `oracle` binary the CLI tests run (built with `-cover`), and writes the merged profile to `coverage.out` (`go tool cover -html=coverage.out` to browse it).

They use their own Postgres, the `postgres-test` service of `docker-compose.yml` on port 5433, kept in memory and never started by `docker compose up`. They read only the `TEST_DB_*` variables, so they can't reach the database with your data. Before the tests the schema is seeded into a template database; each test clones it (or starts empty, to test the migration), inserts the data it needs and drops its database at the end, so tests run in parallel.

They cover the Postgres store, the sync over HTTP into Postgres (filtering, incremental loads, rollback of a corrupt file), the whole sync → history → strategy pipeline, and the compiled `oracle` binary for every command and its error exits. B3 and Yahoo are still faked, so the tests don't depend on the network.

The suite in `test/contract` runs against both `mock.Store` (unit tests) and the Postgres store (integration tests), so the mock can't drift from the real database.

CI (`.github/workflows/ci.yml`) runs the lint, the unit tests and `make coverage` (which includes the integration tests and requires 90% coverage) on every pull request.

This is not investment advice.
