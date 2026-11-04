# 3. The traffic peak

> Goal: 10,000 users on the site at the same moment. Not an attacker, a success: the day the
> project is mentioned somewhere and everyone opens the dashboard at once.

Think in numbers first. 10,000 simultaneous users who ask for something every 0.5 to 1.5 s are
about **10,000 requests per second**. For one instance that is a lot; the answer may be "it
cannot, and it must degrade well", so the review is about **what gives way first, how, and
whether it recovers**, not about making the number green.

## Walk the path of a request and count

For each stage, ask: what is the limit, who enforces it, what happens when it is hit, and does
the next stage make it worse?

1. **Accept**: `http.Server` in `internal/command/api.go`: header, read, write and idle timeouts;
   no `MaxHeaderBytes`, no connection limit; the open-files limit of the process; the TCP backlog.
2. **Telemetry on every request**: `otelhttp`, `Instrument` (metrics and one log line per
   request), the OTLP batchers for logs and traces. At 10k requests per second that is 10k log
   lines per second: what is the cost, what happens when Loki or Tempo is slow (queues, drops,
   memory)? Trace sampling is "all" by default.
3. **The 9 s timeout** (`withTimeout`): `http.TimeoutHandler` answers `503` but the handler goroutine
   keeps running. Under overload that is the multiplier: abandoned work piles up.
4. **Cache** (`internal/handler/cache.go`, `internal/cache`): Redis client pool size and timeouts
   (300 to 500 ms), what a Redis that is down or slow costs *per request* at this rate, the
   singleflight is per process (N instances, N recomputations at each expiry), the stampede when
   every entry expires at the same minute, the key space (see the hacker playbook), the
   `maxmemory` and eviction in `docker-compose.yml`.
5. **In-process history** (`internal/handler/history.go`): 3 years of quotes held in memory, up
   to 3 entries, shared read-only; the first load takes seconds and every waiter blocks on it;
   memory in the heap, GC pressure, and what a deploy or restart under traffic does (cold start
   with everyone asking at once).
6. **Postgres** (`internal/quote/postgres.go`, `pkg/db`): the connection pool has no limit set and
   the server has `max_connections` of 100 (the load test already showed this: connection
   failures turned into `503`s). Which queries scan the whole table (`/tickers`), and what are
   their cost at the real 3.2 million rows? Only ever trust the numbers of a database of the real
   size: the sample database hides everything.
7. **CPU-bound work** (`internal/swing`, `internal/plan`): strategy scans over every stock; `Scan`
   and `Build` per request when the cache misses, per distinct query string.
8. **Outbound calls**: Yahoo (unofficial, rate limited, per-session client and cache in
   `internal/handler/plan.go`) and what happens when 10k users trigger the estimate route.
9. **Observability itself**: Prometheus scraping a busy process, label cardinality, the
   `oracle_http_requests_in_flight` gauge, the `go_sql_*` pool metrics.
10. **The client side of the test**: 10k virtual users on one laptop is also a test of the
    laptop. k6 needs memory per user and file descriptors; a saturated generator looks like a
    slow server. Say which one you measured.

## How to check

Reading first: write down for each stage the limit and the failure mode before running anything.

Then measure, with the tools of the repo, on a server you started (see the rules in `SKILL.md`):

```
make load-test PROFILE=smoke                  # sanity: every route answers
make load-test PROFILE=load                   # 1000 users
make load-test PROFILE=stress                 # 1000 to 2500
make load-test PROFILE=peak                   # 10,000 users: ask the user first
make monitoring                               # Grafana dashboard Oracle API, to watch the run
```

`PROFILE=peak` is `tests/load/peak.js`. Before it: `ulimit -n 65535` in that shell, enough free
memory (several GB), the monitoring stack on, and ideally a Postgres and a Redis that look like
production (`docker-compose.yml`: `redis` has the memory cap and LRU of production). Watch, in
the dashboard or in `/metrics`: p95 and p99, the share of `503`, `oracle_http_requests_in_flight`,
`go_sql_in_use_connections` against `pg_settings_max_connections`, `go_goroutines`, heap bytes,
cache hits against errors, `oracle_operation_duration_seconds`.

Also try the combinations that real peaks bring, one at a time: Redis stopped during the peak
(`docker compose stop redis`), a restart of the API during the peak, a `sync` finishing during
the peak (the cache is flushed: everything recomputes at once).

## What to report

For each stage that gives way: the load at which it did (or, if not run, the arithmetic), what
the user sees (latency, `503`, connection reset), whether it recovers by itself when the load
drops, and the smallest change that moves the limit or makes the failure gentler (a pool size, a
concurrency limit with a fast `503`, a jittered TTL, a bound on distinct keys, sampling). A
review that says "it handles 10k" without saying what was measured, where, against what size of
data, is not a finding.
