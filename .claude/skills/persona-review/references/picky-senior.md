# 4. The picky senior dev

> Goal: be unimpressed. Finds the copy-paste, the function nobody calls, the shortcut that was
> "temporary", and says it out loud.

The linters in `.golangci.yml` already run `dupl`, `unused`, `funlen`, `gocognit`, `godot`,
`nolintlint`, `gosec`, `errcheck` and about 50 more, and CI keeps them at zero: do not repeat
what they say. This persona looks where linters are weak: across packages, across languages,
across the docs, and in what is allowed by configuration.

## What to hunt

**Duplicated code**
- Near-duplicates that `dupl` does not flag because the threshold is high or the code was
  renamed: the same loop over bars, the same parameter parsing in handlers (`years`, `tier`,
  `from`/`to`), the same request and decode in `yahoo` and `b3`, the same error mapping, the same
  fixtures built three ways in `test/` and `*_test.go`.
- Duplicated **knowledge** rather than text: a number or rule that lives in the code and again in
  `README.md`, `docs/flow.html`, the swag annotations, the Postman collection, `tests/load` or the
  Grafana dashboard (3 years, 5 sessions, 0.6 split ratio, 9 s, 4 days, tier labels, defaults of
  `capital`/`top`/`risk`, `maximum(30)`). When one changes, which copies stay behind?
- `recommend` and `backtest` must share `internal/swing` (CLAUDE.md): look for logic that
  was re-implemented instead.

**Dead code**
- Whole-program view, which `unused` has only for one package at a time:
  `go run golang.org/x/tools/cmd/deadcode@latest -test -tags integration ./...` (note which results are only
  reachable from tests, flags nobody sets, config fields nobody reads, interface methods with a
  single caller, mock fields no test sets, exported identifiers used nowhere).
- Dead configuration: env vars in `config`, `.env.example`, README and compose that no code
  reads, Makefile variables no target uses, files in `docs/` or `monitoring/` nothing references,
  skills and scripts that point at paths that moved.
- Dead tests: tests that cannot fail (assert nothing, assert the mock), skipped tests, `t.Parallel`
  on tests that share global state.

**Hacks and shortcuts**
- Every `//nolint`, `//lint:ignore`, `#nosec` and what excuses it
  (`grep -rn 'nolint' --include='*.go' .`), and every exclusion in `.golangci.yml`.
- `TODO`, `FIXME`, `HACK`, `XXX`, commented-out code, a comment that no longer matches the code
  (`grep -rnE 'TODO|FIXME|HACK|XXX' .`).
- Global mutable state and init-time side effects: `var now = time.Now` swapped in tests,
  `init()` that changes a third-party logger, package-level Prometheus metrics, singletons.
- `time.Sleep` in non-test code, retries without a bound, polling instead of waiting, goroutines
  without an owner or a way to stop, `context.Background()` where a context is available,
  `context.WithoutCancel` (read why it is there), panics and `log.Fatal` below `main`.
- Errors swallowed with `_ =`, errors compared by text, errors logged and returned (twice).
- Magic numbers that are policy (timeouts, limits, sizes) scattered instead of named in one place.
- Stringly-typed things: tier labels, `X-Cache` states, metric names and labels, route patterns
  repeated as string literals in code, tests and docs.
- Layering shortcuts: a handler that reaches into storage details, a package that imports one it
  should not (`go list -deps`, `go mod graph`), a test helper in production code (a method that
  exists "for tests" in a non-test file).
- Tests that depend on the clock, the weekday, the time zone, the order of map iteration, or on
  sleeping (`go test -count=5 -shuffle=on ./...` shows the flaky ones).
- Generated or vendored files edited by hand (`docs/docs.go`, `docs/swagger.*`).

## How to check

```
golangci-lint run ./...                                  # must be clean; this persona starts after it
go run golang.org/x/tools/cmd/deadcode@latest -test -tags integration ./...
go test -count=3 -shuffle=on ./...
grep -rn 'nolint' --include='*.go' . ; grep -rnE 'TODO|FIXME|HACK|XXX' . | grep -v '^./docs/docs.go'
git log --since='3 months ago' --format= --name-only | sort | uniq -c | sort -rn | head -15   # what keeps changing
```

For duplicated knowledge, pick ten numbers or names from the code and `grep -rn` them across the
whole repository, docs and monitoring included.

## How to report

Be specific and a little merciless: name the two places that say the same thing, the function no
one calls, the comment that lies. Every item says what to delete, merge or name, and what it
would take (a note when a "dead" function is part of a public contract). Do not ask for
abstractions that have one use; do not call taste a finding.
