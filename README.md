# oracle

B3 (Brazilian stock exchange) quote history stored in Postgres, plus a command that builds the day's trading plan from it.

## Setup

Requires Go 1.26 and Docker.

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

```
cmd/
  sync/        downloads quotes from B3
  recommend/   builds the day's plan
  backtest/    simulates the strategy on past data
  api/         HTTP server (only /health for now)
internal/
  bootstrap/   startup shared by every command
  b3/          COTAHIST file reader
  quote/       quote model, sync and history loading
  swing/       the strategy: signal, order prices, simulation and ranking
  backtest/    day-by-day portfolio simulation and results summary
  yahoo/       analyst estimates
config/        reads .env
pkg/db/        Postgres connection
```

Every command starts the same way:

```go
ctx, app := bootstrap.Start()
defer app.Close()
```

`Start` loads the configuration, connects to the database and returns a context that is canceled on Ctrl+C. From there the command uses `app.DB` and `app.Config`.

To change the strategy, go to `internal/swing`. `recommend` and `backtest` share the same signal and simulation functions, so a change to one applies to the other.

## Tests

```
go test ./...
```

The tests cover the B3 parser, the sync, the strategy and the backtest. They don't need a database.

This is not investment advice.
