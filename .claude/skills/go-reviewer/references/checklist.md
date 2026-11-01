# Go review checklist

Each item is a question to answer for the code in scope. Items marked *(lint)* are already
enforced by `.golangci.yml`: rely on `make lint` for them and only look further when the
linter cannot see the problem (for example across packages or at runtime).

## Project rules (CLAUDE.md)

- **No look-ahead.** Any decision for day `d` in `internal/backtest` or `internal/swing` uses
  only data known at that close. A ticker's track record counts only trades whose exit is on
  or before `d` (`series.rating`). Check indexes (`i+1`, slices up to `len`), the order of
  `settle`/`worth`/`rank`/`buy`, and anything that sorts or filters on future values.
- **Splits.** Prices are not adjusted. A close ratio outside 0.6–1/0.6 breaks the series
  (`swing.Segments` in the backtest, `continuous`, also in `internal/swing`, in recommend). New code that walks prices
  must respect segments, not raw series.
- **Shared logic.** `recommend` and `backtest` call the same `swing` functions. Flag trading
  logic duplicated or reimplemented outside `internal/swing`.
- **Commands.** Each command is `func Name(args []string)` with its own `flag.FlagSet`, starts
  with `ctx, app := bootstrap.Start()` + `defer app.Close()`, and exits with `app.Fatalf`,
  never `log.Fatalf`/`os.Exit` after `Start` (it would skip `Close`). The command is
  registered in the map in `cmd/oracle/main.go`.
- **Contracts.** Dependencies go through `quote.Store`, `quote.Source` and the `estimator` in
  `command`. A change to `quote.Store` updates the suite in `test/contract`, `test/mock` and
  `quote.Postgres` together. Tests importing `test/mock` live in the external `_test` package.
- **Data.** `SaveFile` is one transaction per file with `ON CONFLICT DO NOTHING`; sync stays
  incremental and idempotent through `SyncedFile`. `b3.ErrNotFound` is skipped, not an error.
- **Integration tests** use only `TEST_DB_*` (never `DB_*`), clone the template database per
  test, run in parallel and sit behind the `integration` build tag.
- **Language.** User-facing output (reports, column names, tiers `forte`/`médio`/`fraco`/
  `tendência`) is Portuguese; code, comments, README and docs are English.
- **API docs.** A route change updates its swag annotations (`make swag`, committed
  `docs/`), the Postman collection `docs/postman-collection.json` and the README.

## Correctness

- Off-by-one in loops over sessions and slices; empty and single-element inputs; first and
  last day of a series.
- Floating point: money and prices in `float64` are compared with tolerance where equality
  matters; rounding of quantities (lots) and of costs is explicit; no division by zero (ATR,
  volume, capital, number of trades).
- Time: dates compared as dates (no time-zone drift), `time.Time` rather than strings,
  UTC or `America/Sao_Paulo` used deliberately, trading calendar from the data, not weekdays.
- Maps: iteration order is random; anything printed, ranked or tested from a map is sorted
  first, with a stable tie-break.
- Zero values: structs are usable or clearly invalid at zero; `nil` slices/maps handled.
- Shadowed variables (`err :=` inside a block hiding the outer one) changing what is returned.
- `switch` on enums/strings has a sensible default *(lint: exhaustive)*.

## Errors

- Errors are returned, not just logged; logged *or* returned, not both at every level.
- Wrapped with context: `fmt.Errorf("load history %s: %w", ticker, err)`; compared with
  `errors.Is`/`errors.As` *(lint: errorlint)*.
- Sentinels (`ErrX`) and error types are exported only when callers need to branch on them
  *(lint: errname)*.
- No `panic` for expected failures; `recover` only at a boundary that needs it.
- Partial work on failure: transactions roll back, files are not half-written, the caller
  can retry.

## Context, goroutines and concurrency

- Functions doing I/O take `ctx context.Context` as the first parameter and pass it down
  (DB via `WithContext`, HTTP via `NewRequestWithContext`) *(lint: contextcheck, noctx)*.
- Ctrl+C cancels long work: loops over files/days/tickers check `ctx.Err()`.
- Every goroutine has an owner and a way to stop; no goroutine blocked forever on a channel
  nobody reads; `sync.WaitGroup`/`errgroup` waits before returning.
- Shared state is guarded (mutex or single owner); `go test -race` covers the concurrent path.
- No `time.Sleep` for synchronization in code or tests (polling with a deadline is fine).

## Resources

- `defer Close()` right after a successful open (files, HTTP bodies, rows, ZIP readers)
  *(lint: bodyclose, sqlclosecheck)*; close errors matter for writes.
- HTTP clients have timeouts; HTTP servers set read/write/idle timeouts and shut down on
  context cancellation (`server.Shutdown`).
- DB connections and transactions are closed or committed on every path.
- Large files are streamed, not read whole into memory, unless the size is bounded.

## API and package design

- Packages have one clear purpose and a name that says it; no `util`/`common`/`helpers`.
  Dependencies point inward (`command` → `swing`/`quote`, never the reverse).
- Interfaces are small, defined by the consumer, and introduced for a real second
  implementation or a test seam; functions accept interfaces and return concrete types.
- Exported identifiers are needed outside the package and have doc comments starting with
  the name; unexported otherwise.
- Constructors validate input; configuration comes from `config`, not scattered
  `os.Getenv` calls.
- Function signatures: few parameters (an options struct beyond ~4), no boolean flags that
  switch behavior, results ordered `(value, error)`.
- Generics only where they remove real duplication; no `any` where a concrete type works.
- No global mutable state; package-level vars are constants in practice.

## Tests

- Changed behavior has a test, including the error path and edge cases (empty series,
  split day, no signal, Yahoo failure).
- Table-driven where cases share a shape, with names that describe the case; `t.Run`
  subtests; `t.Parallel()` where safe *(lint: tparallel)*.
- Unit tests use `test/mock` and `test/fixture`, not a database or the network; integration
  tests use real Postgres and the compiled binary.
- Assertions check values, not just "no error"; failure messages show got and want.
- Helpers call `t.Helper()` *(lint: thelper)*; use `t.Context()`, `t.TempDir()`,
  `t.Setenv()` *(lint: usetesting)*.
- Deterministic: no dependence on the clock, map order or the current date without
  injecting them.
- Coverage stays ≥ 90% (`make coverage`); a drop is explained by the change.

## Performance

- No repeated work inside per-day/per-ticker loops (recomputing indicators, re-sorting,
  re-querying); allocations hoisted or preallocated *(lint: prealloc)*.
- DB access batched; no N+1 queries over tickers.
- Benchmarks or measurements back any change justified as an optimization.

## Security

- No SQL built by string concatenation with input; GORM `Raw`/`Exec` use placeholders.
- No secrets (DB password, tokens) in logs, errors, examples or committed files; `.env`
  stays ignored.
- External input (B3 files, Yahoo responses, HTTP requests) is size-bounded and validated
  before use *(lint: gosec covers part of this)*.
- Dependencies: `govulncheck` clean; new modules are justified and pinned in `go.mod`.

## Style not covered by linters

- Names are short in small scopes, descriptive in large ones; no stutter
  (`quote.QuoteStore`); receivers are one or two letters and consistent.
- Comments explain why, not what; no commented-out code; no TODOs without context.
- Early returns over nested `else`; the happy path is left-aligned.
