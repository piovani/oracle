---
name: go-reviewer
description: Review Go code in this repository against Go best practices and the project's own rules (CLAUDE.md invariants, contracts, tests, docs). Use when asked to review Go code, a diff, a branch or the whole project, to check best practices or code quality, or before opening a pull request.
---

# Go reviewer

Review Go code for correctness, idiomatic design and the rules this project depends on, and
report verified findings. This is a read-only review: do not edit files, do not apply fixes
unless the user asks for them afterwards, and never commit.

## 1. Pick the scope

- Default: the pending changes, `git diff main...HEAD` plus the working tree (`git diff`,
  `git status --short` for new files). Review the changed code and what it touches.
- "whole project", "all", "everything": every package under `cmd/`, `internal/`, `pkg/`,
  `config/` and `test/`.
- A path, package or PR: just that.

Say which scope you reviewed at the top of the report.

## 2. Run the tools

Run from the repository root and keep each result (pass/fail plus the relevant output):

```
gofmt -l .                                   # must print nothing
go build ./...
go vet ./... && go vet -tags integration ./...
make lint                                    # golangci-lint v2, must report 0 issues
go mod tidy -diff                            # must print nothing
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
```

When Docker is running (`docker info` succeeds), also run `make coverage` (unit + integration,
must stay ≥ 90%). If routes or swag annotations changed, run `make swag` and check that
`git status --porcelain -- docs/` shows nothing new; if `internal/command/api.go` or
`internal/handler` changed, run
`python3 -I .claude/skills/postman-collection/scripts/api.py check`. If a command, package or
route changed, also run `python3 -I .claude/skills/flow-diagram/scripts/flow.py check`, which
compares `docs/flow.html` with the code.

A tool that cannot run (no network, no Docker) is reported as **not run**, with the reason;
never as passed. `make lint` enables ~60 linters (see `.golangci.yml`): do not re-report each
lint finding by hand, summarize them, and spend the manual review on what linters miss.

## 3. Review by hand

Read the code in scope, and the callers and tests of what changed. Go through
[the checklist](references/checklist.md): project invariants first, then correctness,
concurrency, resources, errors, API and package design, tests, performance and security.

Before reporting a finding, confirm it in the code: the exact line, the input or state that
makes it go wrong, and why. Drop anything you cannot tie to a concrete failure or to a rule
in the checklist or CLAUDE.md. Do not report pure taste, and do not ask for changes the
linters already enforce.

## 4. Report

```
## Go review: <scope>

### Tools
| Check | Result |
|---|---|
| gofmt | ok |
| make lint | 2 issues (errcheck, gocritic) |
| make coverage | not run: Docker is not running |
...

### Findings
1. **[blocker] internal/backtest/simulate.go:84 (correctness)**: <what is wrong>.
   <When it breaks: inputs/state → wrong result>. Fix: <the smallest change that fixes it>.
2. **[major] ...**

### Good
<one or two lines on what is solid, only if it is worth keeping>
```

Severities:

- **blocker**: wrong results, data loss, look-ahead in the backtest, panics, races, leaks,
  security issues, broken build/tests/CI.
- **major**: a project rule broken (CLAUDE.md), missing error handling or tests for a real
  path, contract drift between `quote.Store`, the mock and Postgres, stale docs that CI or
  callers rely on.
- **minor**: maintainability, unclear API, missing docs on exported identifiers.
- **nit**: small style points not covered by linters. Keep them few.

Order findings by severity, then by file. Use `file:line` paths relative to the repo root.
If there are no findings, say so plainly and still show the tools table.
