# 1. The hacker

> Goal: make the application do what its author did not intend. Steal, bypass, crash, exhaust.

There is no authentication in this API (documented), so the question is not "can I log in", it
is "what can an anonymous stranger on the network do, and what can a bad input or a bad
dependency do to the process". Think about each surface, then check it.

## Surfaces and leads

Leads come from the code at the time of writing; confirm each one before reporting it.

**HTTP API (`internal/command/api.go`, `internal/handler`)**
- Every input is a path or query value: `{ticker}`, `{date}`, `tier`, `years`, `capital`, `top`,
  `risk`, `fill`, `estimates`, `from`, `to`. Where does each one end up? SQL (`internal/quote/postgres.go`:
  parameterized with `?`, but read every `Where`/`Raw`/`Exec` for string concatenation), a cache key,
  a log line, a metric label, a trace attribute, a file path, a number used in a loop or an
  allocation (`years`, `top`, `capital / top`).
- **The response cache key** (`internal/handler/cache.go`) is the path plus the whole sorted query.
  Does an unknown parameter (`?x=1`, `?x=2`, ...) create a new entry and a new computation of a
  heavy route? That would turn the cache into an amplifier: one cheap request, one heavy
  computation, one more Redis key. Check what bounds it.
- **Resource-heavy parameters**: how many distinct `years` values can force a new history load
  (`internal/handler/history.go`), and how much memory each one holds.
- **No rate limit, no body or header limits, no authentication**: what does a single client
  with a fast loop cost? What do `http.TimeoutHandler` and the 9 s limit do to the goroutines
  that keep running after the client got its 503?
- **Responses**: error messages that leak internals (`writeInternal` is meant to hide them:
  does every path use it?), missing security headers, `X-Cache` and other headers, the Swagger
  UI exposed at `/swagger/`, content type sniffing.
- **Logs and traces**: request lines carry the path and trace ids (`internal/telemetry`); can a
  value with newlines or control characters forge a log line? Can a secret reach a log, a
  metric label or a span (SQL statements keep placeholders; Redis statements are off)?

**Other doors**
- `/metrics` on `METRICS_PORT` listens on every interface (`internal/command/api.go`): what does
  it reveal, and who can reach it?
- **Files from B3** (`internal/b3/cotahist.go`): a ZIP downloaded from the internet, written to a
  temporary file, parsed by fixed columns. Think zip bombs, huge entries, path names, malformed
  lines, a 200 that is HTML, redirects. **Yahoo** (`internal/yahoo`): an unofficial API whose
  JSON the app trusts, with a cookie jar and a crumb.
- **CLI flags and env** (`cmd/oracle`, `internal/command`, `config`): `-csv` is a path typed by
  whoever runs it (overwrite of an arbitrary file?), `.env` loading, `REDIS_PREFIX` and
  `REDIS_DB` pointing at someone else's keys, `Flush` scope.
- **Infrastructure** (`docker-compose.yml`, `Dockerfile`, `monitoring/`): default credentials
  (Postgres, Grafana `admin/admin`), Redis without a password, ports published on all
  interfaces, images pinned or `latest`, user of the container, secrets in the image or in the
  repo history (`git log -p` for `.env`, keys, tokens), `.dockerignore`.
- **Supply chain**: `go.mod` dependencies, `govulncheck`, GitHub Actions versions and
  permissions in `.github/workflows/ci.yml`, `dependabot.yml`.
- **Migrations**: can two processes migrate at once, can a hostile migration file ship in a PR
  unnoticed (`internal/quote/migrations`, embedded in the binary)?

## How to check

```
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...        # known vulnerabilities, reachable ones
golangci-lint run ./...                                       # gosec is on: do not repeat its findings
git log -p --all -S'password' -- . ':!*.md' | head            # secrets in history (never print them)
```

To try an attack, start the API against the test database and use the seed:

```
make test-db
go run ./.claude/skills/postman-collection/scripts/seed       # database oracle_postman on :5433
DB_PORT=5433 DB_USER=oracle_test DB_PASSWORD=oracle_test DB_NAME=oracle_postman \
  REDIS_ADDR=localhost:6380 go run ./cmd/oracle api
```

Use the Go client in a throwaway test, `python3 -I` or `curl` (on this machine `curl -X` may get
`400`: use the Python script of the Postman skill if so). Try, for example: SQL metacharacters
and null bytes in `{ticker}`, 100 distinct query strings on `/signals` and watch the heavy
operations in `/metrics` (`oracle_operation_duration_seconds_count`) and the Redis keys
(`redis-cli --scan --pattern 'oracle:cache:*' | wc -l` on the test Redis), `years=30` with many
distinct values, a header of 1 MB, slow headers, a request with a body on a GET.

## Not a finding

- "There is no authentication": it is documented and a product decision. A finding is what the
  absence makes worse, with a concrete path.
- Anything that needs the attacker to already control the database, the host or `.env`.
