# 2. The careless user

> Goal: use the application without reading anything, and break it by accident. Types the wrong
> thing, in the wrong place, in the wrong order, with the network down.

This persona is not malicious: the question is whether every wrong input gets a clear answer
and no wrong input gets a crash, a hang, a `500`, wrong numbers or a stuck state.

## Inputs to try, by place

**Path values**: `{ticker}` lowercase, with spaces, `petr4.sa`, 40 characters, empty
(`/tickers//quotes`), unicode, `%00`, `%2F`, a ticker that exists in no table, a fund or ETF on a
strategy route (`422` expected). `{date}` as `2026-02-30`, `2026-13-01`, `0000-00-00`, `26-10-30`,
`yesterday`, a weekend or holiday, the future, a year 1900, `99999-01-01`.

**Query values** (`tier`, `years`, `capital`, `top`, `risk`, `fill`, `estimates`, `from`, `to`):
empty (`?years=`), negative, zero, one above the documented maximum, decimals where an integer
is expected, `1e309`, `NaN`, `Inf`, `-0`, thousands separators (`1,000`), `yes` or `1` or `TRUE`
for booleans, the same parameter twice (`?top=1&top=9`), `from` after `to`, `tier=Forte` with
the accent and without, `top=0` with a capital (division of `capital / top`), `risk=100`,
`capital=0.01` (zero shares), a capital so large that quantities overflow.

**Shape of the request**: wrong method on every route (`405` with `Allow`), a trailing slash, a
double slash, a very long URL, a body on a GET, HEAD, OPTIONS, a route that does not exist
(`404` in plain text, as documented).

**State**: empty database (no quotes at all, only funds, a single session, a ticker with fewer
bars than the warm-up), data older than 4 days (`stale`), a ticker that stopped trading, a
stock after a split, a day with no signals, Redis down, Redis slow, Postgres down mid-request,
Yahoo down or returning garbage.

**Commands (`cmd/oracle`)**: every flag with a bad value. `recommend -capital -5 -top 0 -risk 500
-years 0`, `backtest -from tomorrow -from 1800-01-01 -csv /no/such/dir/x.csv`, `sync -since 1800
-since 2999 -since abc`, no subcommand, an unknown subcommand, `-h`. Ctrl+C in the middle of a
sync and run it again. A missing `.env`. `PORT=abc`, `PORT` equal to `METRICS_PORT`, `LOG_FORMAT=xml`,
`REDIS_DB=abc`. Two syncs at once.

## What good looks like

- A `4xx` with a message that says what was wrong and what is allowed, in the JSON error shape.
- Never a `500` for something the user typed; never a panic (read the logs: a recovered panic
  in `net/http` still prints a stack).
- No answer that looks valid and is wrong (a plan with `NaN`, a negative quantity, a quantity
  of 0 sold as a pick, a `from` that silently became "everything").
- A CLI that exits non-zero with a sentence, not a stack trace, and leaves no half-written file.

## How to check, cheaply

Most of this needs no server and no database: the handlers are reachable through `newMux` with
the in-memory store. Write a throwaway test in the scratchpad (or a temporary `_test.go` that you
delete) that loops over a table of bad inputs and records status, content type and body, and
that checks no handler panics. Native fuzzing works on the parameter parsing:

```
go test ./internal/handler/ -run xxx -fuzz FuzzParams -fuzztime 30s   # after writing the fuzz target
go test ./internal/command/ -run 'TestAPI|TestQuote' -count=1 -v      # the existing table tests show the style
```

For the CLI, run the compiled binary against `postgres-test` (see `test/integration/cli_test.go`
for the `oracle(...)` helper) and read exit code and stderr.

Report the inputs that gave a `500`, a hang, a panic or a wrong-but-valid answer, with the exact
request. The inputs that already get a clean `4xx` are not findings: list them as "tried, held".
