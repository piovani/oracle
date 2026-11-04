---
name: persona-review
description: Review the oracle project through five hostile or demanding points of view, one after the other - a hacker looking for vulnerabilities, a careless user feeding it invalid input, a traffic peak of 10k simultaneous users, a picky senior dev hunting duplicated code, dead code and hacks, and a future dev reading the code six months from now. Use when asked for a persona review, a "five eyes" review, to attack or stress the project, to think like a hacker, a dumb user or a senior dev, to check how the code will age, or before a release.
---

# Persona review

Five characters walk through the same project, each with a different goal. A review by one
point of view misses what another sees at once: the hacker reads the code the user never
types into, the peak of traffic breaks what the careful dev tested alone, and the future dev
sees the cost of what every other persona accepted as "good enough".

This is a read-only review. Do not edit files, do not apply fixes unless the user asks for
them afterwards, never commit. Generic Go quality (idioms, linters, tests) is the job of the
`go-reviewer` skill: do not repeat it here, look at what only these points of view see.

## The five

| # | Persona | Goal | Playbook |
|---|---|---|---|
| 1 | **Hacker** | break the application on purpose: steal, bypass, crash, exhaust | [references/hacker.md](references/hacker.md) |
| 2 | **Careless user** | break it by accident with wrong, odd and absurd input | [references/careless-user.md](references/careless-user.md) |
| 3 | **Traffic peak** | 10,000 simultaneous users: what gives way first | [references/peak.md](references/peak.md) |
| 4 | **Picky senior dev** | duplicated code, dead code, hacks and shortcuts | [references/picky-senior.md](references/picky-senior.md) |
| 5 | **Dev of the future** | will this age well: can someone maintain it in six months | [references/future-dev.md](references/future-dev.md) |

## 1. Pick the scope

- Default: the whole project (`cmd/`, `internal/`, `pkg/`, `config/`, `test/`, `tests/`,
  `monitoring/`, `docker-compose.yml`, `Makefile`, `Dockerfile`, `.github/`, the docs).
- A path, a package, a route or a diff (`git diff main...HEAD` plus the working tree): look at
  that and at what it touches.
- "only the hacker", "just personas 2 and 3": run those and say which you skipped.

Say the scope and the personas at the top of the report.

## 2. Run each persona

Take the personas in order. For each one, read its playbook first: it lists the project's
real hunting grounds (files, routes, limits, commands), which came from the code and are leads
to check, not findings. Then do the work in three moves:

1. **Think in character.** What would this person try first? Write the attack, the input, the
   load or the question before looking for the code that answers it.
2. **Check against the code, and run it when you can.** Read the exact lines. Prove a
   suspicion with a test, a request or a measurement whenever that is cheap and safe (see the
   rules below); otherwise reason from the code and say it was not run.
3. **Keep only what holds.** A finding needs the exact `file:line`, the concrete input, state or
   load that triggers it, and what goes wrong. Drop guesses, taste and anything the linters in
   `.golangci.yml` already enforce.

Do the personas one at a time, in this conversation. Spawn subagents only if the user asks for
them.

## 3. Rules of engagement

- **Local only.** Attacks, fuzzing and load go against a server you started on this machine.
  Never against a host you did not start, never against Yahoo Finance or B3 (they are real
  services: use the fakes in `test/`).
- **Never touch the real data.** Use `postgres-test` and `redis-test` (`make test-db`, ports
  5433 and 6380) and the `TEST_DB_*` variables, never `DB_*` or the database with synced data.
  The Postman skill's seed (`.claude/skills/postman-collection/scripts/seed`) gives a small
  database for the API.
- **Throwaway code stays out of the repo.** Scripts and temporary `_test.go` files go in the
  scratchpad directory or are deleted afterwards; `git status` must show nothing the review
  created.
- **Stop what you start.** Servers, the test containers and the monitoring stack, unless it was
  already running.
- **Heavy runs ask first.** The 10k-user profile can exhaust the memory of a laptop: say what
  it will do and let the user decide.
- **Secrets.** Never print a password or token you find; say where it is.

## 4. Report

Write in the language the user writes in. Start each persona with one line in its voice, then
the findings. Facts only after that line.

```
## Persona review: <scope>
Personas: all five | Not run: <what and why>

### 1. Hacker
> "<one line in character>"
1. **[critical] internal/handler/cache.go:48 (cache key)**: <what is wrong>.
   Attack/input: <exactly what the persona sends>. Result: <what happens>. Proof: <test or
   request that showed it | not run: reason>. Fix: <the smallest change that closes it>.
2. **[medium] ...**

### 2. Careless user
...

### Summary
| Persona | critical | high | medium | low |
|---|---|---|---|---|
| Hacker | 1 | 0 | 2 | 1 |
...

### Fix first
1. <the five findings that matter most, across personas, in order, with why>
```

Severity is about impact, not about the persona:

- **critical**: data loss or corruption, remote code execution, bypass of a protection, a crash
  or hang that one request can cause, wrong money numbers in the plan.
- **high**: a clear path to degrade or take down the service, leak data, or silently return
  wrong results.
- **medium**: a weakness that needs a particular situation, or a maintenance cost that will
  bite within months.
- **low**: hardening, polish, small debts.

If a persona finds nothing, say so plainly and what it tried: an empty section with the list of
attacks that failed is information too. Two personas finding the same root cause is one finding:
report it under the first and mention the other.
