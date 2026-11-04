# 5. The dev of the future

> Goal: open this repository six months from now, with a bug in production and no memory of why
> anything is the way it is. Will it be a good afternoon or a bad week?

This persona does not look for bugs; it looks for **cost of change** and **things that rot**.
Think of three readers: someone new who must add a feature, the author who forgot everything,
and whoever is on call at 3 a.m.

## What ages, and how

**Dependencies and platform**
- `go list -m -u all`: what is already behind; the Go version in `go.mod`, the `Dockerfile`, CI
  and the `README` badge (do they agree?); images in `docker-compose.yml` pinned to a version
  that will be unsupported (Postgres 16, Redis 7, Prometheus, Grafana, Loki, Tempo, exporters);
  GitHub Actions versions; `dependabot.yml` covering what it should.
- Hardcoded platform: the `Dockerfile` builds only `linux/arm64`; `make docker-build` too. Who
  runs on amd64?
- The unofficial and external: **Yahoo Finance** (unofficial API, cookie and crumb; it will
  change) and **B3 COTAHIST** (fixed-width layout, URL scheme, file naming). What happens the day
  one changes: does the failure show up as a clear message, or as quietly wrong data? Are there
  fixtures of the real format, so a future change can be diffed?

**Data growth**
- 3.2 million quotes today, a few hundred thousand more a year. In two years: the `History` load, the
  in-memory copy, the `/tickers` scan, the indexes, the size of `quotes`, the time of `make sync` on
  an empty database, the Postgres volume. What is the plan when the table passes 10 million rows
  (partitions, summary table, archive)? Is there a backup or restore story for the volume?
- Migrations (`internal/quote/migrations`): one transaction for all of them (no
  `CREATE INDEX CONCURRENTLY`), applied by `sync`, never edited: what is the day a migration must
  run on a big table, or be rolled back? There is no down migration.

**Time bombs**
- Anything tied to the calendar: `b3.FirstYear`, `fixture.Today()` and weekdays in tests, holidays,
  daylight saving and time zones in dates stored as `date`, the 4-day `StaleAfter`, year 2999
  used in tests, a `now()` captured at start.
- Tests that pass today because of today's data.

**Change cost** (try one real change and count the places you must touch)
- Add a route: `newMux`, `cached(...)`, the handler and its swag annotations, `make swag`, the
  Postman collection, `README.md`, `docs/flow.html` and its PNGs, `tests/load/common.js`, the
  dashboard. Which of these does something check for you (`flow.py check`, `api.py check`, the
  swag CI step, the dashboard test), and which only a human remembers?
- Add a command, add a config variable, add a strategy parameter, change a tier, add a column to
  `quotes`, add a metric, change the cache TTL. Is each one a small diff in one or two places?
- Are the package boundaries honest (`swing` pure, `plan` shared, `handler` thin, `quote` the only
  one that knows SQL)? Where would a new person put a thing and be wrong?

**Understanding**
- `CLAUDE.md` and the README: do they describe the code that exists? Is there a place that says
  *why* (the invariants, the rejected options, the numbers from the strategy research), not only
  what? Names that need a comment to be understood; comments that explain the history instead of
  the reason.
- The strategy itself (`internal/swing`) is the heart and the riskiest thing to touch: how well
  is it protected by tests of behavior (look-ahead, splits, costs), by the backtest, by a way to
  see that a change made results worse?
- Operating it: when the dashboard shows red, is there a path from the panel to the cause (trace
  id in the log, route in the metric, `X-Cache` in the response)? What alerts exist (none today)?
  What is the runbook for: Redis down, Postgres at the connection limit, B3 not publishing, Yahoo
  blocked, `sync` failing halfway, a bad migration?
- Config sprawl: how many env vars, with how many defaults in how many places; is a wrong value
  caught at startup or three requests later?

**Safety nets that decay**
- Skills and scripts (`.claude/skills`, `scripts/`): do they still match the repository? Run
  their `check` commands.
- CI: what does it not run (the load tests by design, the docs checks, the monitoring files, the
  Docker build)? What would break silently?
- The test suite as a safety net: speed (does `make coverage` take minutes?), flakiness
  (`-count=5 -shuffle=on`), coverage that is high but empty, parts with no tests at all.

## How to check

```
go list -m -u all | grep '\['                          # outdated modules
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
python3 -I .claude/skills/flow-diagram/scripts/flow.py check
python3 -I .claude/skills/postman-collection/scripts/api.py check
make swag && git status --porcelain -- docs/           # must show nothing new
git log --since='6 months ago' --format= --name-only | sort | uniq -c | sort -rn | head -15
go test -count=5 -shuffle=on ./...                      # time bombs and order dependence
```

Then do the exercise: pick two plausible changes for the next six months (a new route; a new
data source) and list, file by file, what you would have to touch and what would warn you if you
forgot one.

## How to report

Each finding names the thing that will age, **when** it will start to hurt (a date, a data size,
a dependency release) and the cheapest step that prevents it. Rank by (cost when it hurts) times
(how soon). "Add a runbook for Redis down" is a fine finding; "use hexagonal architecture" is not.
