# Load tests

[k6](https://grafana.com/docs/k6/latest/set-up/install-k6/) scripts for the HTTP API. They are
**not** part of `go test`, `make coverage` or the CI: run them by hand, against a server you
started.

```
go run ./cmd/oracle api          # in one terminal, with the database synced
make load-test                   # in another: the load profile
make load-test PROFILE=smoke
make load-test PROFILE=stress BASE_URL=http://localhost:8080 TICKER=VALE3
```

| Profile | What it does |
|---|---|
| `smoke` | one user calls every route once; every check must pass |
| `load` | ramps to 1000 users in 2 minutes, holds 2 minutes, ramps down; 70% light and 30% heavy routes |
| `stress` | 1000, 1500, 2000 and 2500 users, 1 minute each, to find where latency and errors start; loose thresholds |

| Variable | Default | Meaning |
|---|---|---|
| `PROFILE` | `load` | script to run: `smoke`, `load` or `stress` |
| `BASE_URL` | `http://localhost:8080` | server under test |
| `TICKER` | `PETR4` | stock used by the per-ticker routes; it must exist in the database |

Thresholds of `smoke` and `load` (the run fails when one is broken): under 1% failed requests,
more than 99% of checks passing, p95 under 300 ms for light routes and under 1.5 s for heavy ones
(the ones that run the strategy over the history).

Notes:

- The API answers the data routes from Redis for 1 minute, so most of the load measures the cache, not Postgres: the first request of each path in a minute is a `MISS`, the rest a `HIT` (`X-Cache`). To measure the database, stop Redis (`docker compose stop redis`): the API then answers without a cache.
- Before measuring, the script warms the server: the first strategy call after a start loads three
  years of quotes and can hit the 9 s timeout.
- Every route is a read-only `GET`, so the tests don't change the database.
- `/recommendations` runs with `estimates=false` and `/tickers/{ticker}/estimate` is not called, so
  the tests never hit Yahoo Finance (an unofficial API that would rate-limit the load).
- With this many users, raise the limit of open files first (`ulimit -n 65535` in the shell that runs `make load-test`): the macOS default is low and shows up as `too many open files` errors.
- To watch a run live, start the monitoring stack first (`make monitoring`) and open the *Oracle API* dashboard in Grafana: request rate, latency percentiles, cache hit ratio, connection pool. With `OTEL_TRACES_SAMPLER=parentbased_traceidratio` and `OTEL_TRACES_SAMPLER_ARG=0.1` the API traces a tenth of the requests, which is enough.
- Run it against a local server. The numbers depend on the machine and on the size of the database.
