# Roadmap — Overview

Four stages, one per day. Each stage is shippable on its own; later stages
compose onto earlier work without rewriting it.

| # | Day | Theme | Spec |
|---|---|---|---|
| 0 | (cross-cutting) | Architectural contracts — interfaces locked across stages | [05-architectural-contracts.md](05-architectural-contracts.md) |
| 1 | Day 1 | Foundation: schema, ingestion, deployed skeletons | [01-foundation.md](01-foundation.md) |
| 2 | Day 2 | Search core: retrieval + ranker online | [02-search-core.md](02-search-core.md) |
| 3 | Day 3 | Intent + polish + perf | [03-intent-polish.md](03-intent-polish.md) |
| 4 | Day 4 | Writeup + ship | [04-writeup-ship.md](04-writeup-ship.md) |

Architecture rationale (and the decisions rejected) is in
[`docs/architecture.md`](../architecture.md). The full prior-art / decision log
of the planning phase lives at
`~/.claude/plans/we-have-a-spec-scalable-pony.md`.

## How to read a stage spec

Every stage doc has the same shape, so they compose:

1. **Goal** — one or two sentences.
2. **Where this fits** — upstream stages it consumes; downstream stages that
   consume it.
3. **Architectural commitments** — the abstractions/interfaces *locked* by
   this stage. Later stages may extend them but not break them.
4. **Acceptance criteria** — what "done" means.
5. **Deliverables** — concrete artifacts (paths) produced.
6. **Sub-tasks** — ordered.
7. **Testing design** — unit / integration / contract / bench / loadtest.
8. **Interface to next stage** — what we hand off (with file paths).
9. **Risks + mitigations**.
10. **Out of scope**.

## Global testing strategy (the pyramid)

```
                ┌─────────────────────────────┐
                │       Manual smoke           │   live UI, 5 min per day
                │       Lighthouse / mobile    │
                └─────────────────────────────┘
              ┌─────────────────────────────────┐
              │ Bench (queries.json)             │   nightly + on PR
              │ Loadtest (hey)                   │
              └─────────────────────────────────┘
            ┌───────────────────────────────────┐
            │ Integration (real Postgres)        │   per-PR CI
            │ Contract (TS shape ↔ Go response) │
            └───────────────────────────────────┘
        ┌─────────────────────────────────────────┐
        │ Unit tests (ranker fixtures, intent,     │   per-commit, fast
        │ taxonomy, signals, parser, config loader)│
        └─────────────────────────────────────────┘
```

| Layer | Where | When |
|---|---|---|
| Unit | `api/internal/**/*_test.go`, `web/**/*.test.ts` | Pre-commit (changed pkg) + CI per push |
| Integration | `api/internal/**/integration_test.go` (build tag `integration`) | CI per push, against a Postgres-15 service container |
| Contract | Go test compares the `/search` JSON shape against a schema generated from `web/lib/api.ts` | CI per push |
| Bench | `bench/queries.json` via `scripts/bench-runner` | Nightly + manual; results in `bench/results-<date>.json` |
| Loadtest | `scripts/loadtest.sh` (uses `hey`) against deployed API | Manually each stage; recorded in writeup |
| Manual smoke | Live UI on Vercel | Daily; cap at 5 min |

### What earns a test

- **Always**: ranker decisions, intent extractor entries, taxonomy mappings,
  synth determinism, the JSON streaming parser.
- **At seams**: anything that crosses a port (BusinessRepo, config loader,
  intent → retrieval overlay).
- **At hard guarantees**: idempotent migrations, exact-name pin, hard filters,
  new-biz de-pin.
- **Skip**: HTTP handler shape past serialization (covered by contract test),
  trivial getters, glue code.

## Quality gates each commit must pass

Defined in detail in [`docs/development.md`](../development.md). One-line
summary: correctness (lint), complexity (cyclomatic + cognitive + length),
dead code (unused, knip), architectural drift (`go-arch-lint`,
`eslint-plugin-boundaries`, `depguard`), duplication (`dupl`, `goconst`,
`sonarjs/no-duplicate-string`), secrets (`gitleaks`), and conventional commits.

## North-star checks (run them every day)

- `bench/queries.json` pass rate
- p95 latency from API timing logs
- `cd api && make lint test build`
- `cd web && npm run quality`
- Migrations apply cleanly twice in a row (idempotent)
- All hooks green: `lefthook run pre-push --all-files`

## Working agreements

- **Daily commit, daily progress note.** Spec calls this out as a positive
  grading signal. Don't batch.
- **Spec contract first.** The 7 signals × archetype weights × linear sum is
  the contract. Alternative formulas are config switches; default is
  spec-literal. See [[feedback-spec-contract]] in the memory store.
- **Index-time work beats query-time work.** Anything precomputable at ingest
  belongs in a generated column or in the taxonomy map.
- **No silent deviations.** Anything that strays from the spec gets flagged
  in the writeup as a deliberate call, with measured comparison numbers.
- **No work without measurement.** A change that doesn't show up in the bench
  or the loadtest report doesn't ship in Stage 4.

## What "done" looks like at the end of each day

- **Stage 1**: `curl https://lemon-search-api.fly.dev/healthz` → 200. Supabase
  project has 22k+ businesses. Schema, ingestion, skeletons green. CI green.
- **Stage 2**: Type in the UI, get ranked results. Bench pass rate ≥ 60%.
  p95 ≤ 200ms (rough budget).
- **Stage 3**: Intent queries work. Bench pass rate ≥ 80%. p95 ≤ 100ms under
  load. Both signal-formula modes produce a comparison.
- **Stage 4**: Writeup committed. Graders have read access to Supabase. Live
  URL stable. Loadtest + bench numbers in the writeup.
