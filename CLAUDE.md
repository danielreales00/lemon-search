# CLAUDE.md

Always-loaded context for working in this repo. Keep it high-signal; link to
`docs/` for depth. If something here drifts from reality, fix it here first.

## What this is

A 4-day build trial for [Lemon](https://uselemon.com/): a search + ranking
engine over ~23k Miami businesses. Graded on **search quality, ranking
quality, speed (sub-100ms p95), and writeup judgment**. The UI is deliberately
thin — the engine is the point.

- Repo: <https://github.com/danielreales00/lemon-search>
- Project board: <https://github.com/users/danielreales00/projects/2> (`PVT_kwHOA--NDs4BY_nZ`)
- Full plan / decision log: `~/.claude/plans/we-have-a-spec-scalable-pony.md`
- Spec: `Lemon_4_Day Build Trial.md`

## Stack (locked — see ADR-0001)

| Layer | Choice | Region |
|---|---|---|
| Frontend | Next.js 15 (App Router, React 19) on Vercel | edge |
| API | Go 1.23, `pgx/v5`, `slog`, on AWS EC2 (`c7i.xlarge`, ADR-0007) | `us-east-1` |
| Database | Supabase Postgres 15 (`pg_trgm`, `cube`, `earthdistance`) | `us-east-1` |
| Ranking config | YAML (`config/ranking.yaml`) | — |

EC2 + Supabase both in `us-east-1` are paired on purpose (API↔DB hop ≤ 10ms).
The API host moved Fly→EC2 for SSH/profiling of the CGo embedder (ADR-0007);
ADR-0001 still governs the rest of the stack.

## Architecture — boundaries you MUST NOT violate

Hexagonal core (ADR-0005) + two-phase retrieval (ADR-0003). The import rules
are enforced by `api/.go-arch-lint.yml` + `depguard` (Go) and
`eslint-plugin-boundaries` (TS). Violations fail the hook and CI.

```
api/internal/
  domain/            PURE. no I/O, no vendor deps (not even pgx). Types + ports.
  observ/            leaf utility (logging, timing). depends on nothing internal.
  config/            → domain        (+ yaml)
  intent/            → domain        (lexicon → Overlay; no infra)
  rank/              → domain, config (pure scoring; no DB, no HTTP)
  retrieve/postgres/ → domain        (+ pgx; the ONLY adapter that touches the DB)
  api/               → domain, intent, rank, config, observ, retrieve (HTTP only)
api/cmd/
  api/               composition root for the server (wires everything)
  ingest/            composition root for the ingestion CLI
web/
  app/        → app, component, lib
  components/ → component, lib
  lib/        → lib (leaf)
```

The seam: **SQL returns rich raw signals; Go composes them into the score.**
The ranker is pure and unit-tested against fixture candidates — never give it
a DB dependency.

Search request flow:
`intent.Extract → SQL retrieval (1 round-trip, ≤150 candidates) → Go re-rank → top 15`.

### Design principles

- **Stateless API.** All state lives in Postgres. Any API instance can serve
  any request; no in-process caches that must stay coherent.
- **Composition root.** Only `cmd/api` and `cmd/ingest` construct dependencies
  (pgx pool, config, adapters, ranker). Everything else receives what it needs
  as constructor arguments. No globals, no `init()`, no service locators.
- **Accept interfaces, return structs.** Consumers declare the interface they
  need (`domain.BusinessRepo` is owned by `domain`, not by the adapter).
  Constructors return concrete types.
- **Pure core.** `rank` and `intent` do zero I/O. Same input → same output.
  That's what makes them testable against fixtures without a DB.
- **Index-time over query-time.** Precompute anything you can at ingest
  (generated columns `photo_count`, `is_new`; `loc` + `search_vector` set in the
  ingest INSERT; archetype assignment; taxonomy normalization). The hot path
  stays thin.
- **Config over code for anything tunable.** Archetype weights, formula
  choice, thresholds → `config/ranking.yaml`. Incomplete-work gates →
  feature flags. Neither is hardcoded.
- **One round-trip per query.** Retrieval is a single SQL call returning rich
  raw signals; the ranker composes them in-process.

## The spec contract — non-negotiable (ADR-0004)

The ranking spec is **7 signals × archetype weights × linear sum**. Honor it
literally by default. When you find a "smarter" alternative:

- **Do NOT silently substitute it.** Default config is spec-literal
  (`rating: lemon_score/10`, `distance: max(1 - d/30mi, 0)`, archetype strictly
  per-business).
- Implement alternatives behind a **config switch** (`signal_formulas.*`),
  default off, and quote a measured bench comparison in the writeup.

Detail: `docs/ranking/semantics.md`, `docs/adr/0004-spec-contract-discipline.md`.

## How to write code here

General (both languages):

- **Comments explain WHY, not WHAT.** Default to none. Add one only for a
  non-obvious constraint, invariant, or workaround. Don't narrate the code or
  reference the task/PR. No commented-out code; delete it (git remembers).
- **No premature abstraction.** Concrete first. Three similar lines beat a
  wrong abstraction. Don't add layers, options, or generics for hypothetical
  futures.
- **No dead code, no backward-compat shims.** If it's unused, delete it.
  We're pre-launch; there are no external callers to keep happy.
- **Validate at boundaries only** (HTTP params, the JSON ingest). Trust
  internal code and the contracts (C1–C8).
- **Tests live next to code** (`*_test.go`, `*.test.ts`). Cover the decisions,
  not the glue. The ranker and intent extractor get fixture-based tables.

Go:

- **Errors**: wrap with `fmt.Errorf("doing x: %w", err)`; compare with
  `errors.Is/As`. No `panic` outside `cmd/*/main` startup. Never ignore an
  error (errcheck enforces); `_ =` only with a reason comment.
- **Context**: first parameter of anything that blocks; propagate it; never
  store it on a struct. No `context.Background()` below `main`.
- **Constructors**: `New*(deps...) (*T, error)`. Explicit dependencies. No
  package-level mutable state.
- **Interfaces** are small and defined by the consumer. Don't pre-declare
  broad interfaces "just in case."
- **Functions** stay under the complexity/length limits (gocyclo 12, funlen
  80). Guard clauses + early returns over nesting (max-depth-ish; `nestif`).
- **Naming**: short locals (`c`, `q`, `i`), descriptive exports, no stutter
  (`rank.Result`, not `rank.RankResult`). snake_case for SQL/JSON/YAML tags.
- **pgx**: prepared statements at pool init; always `defer rows.Close()`;
  always check `rows.Err()`; parameterize every query (never string-concat
  SQL). 1s statement timeout.
- **Logging**: `slog` via `internal/observ`, structured key=value. Never
  `fmt.Print*` (forbidigo blocks it).
- **Concurrency**: only where it earns its keep (the ingest pipeline). Bounded
  via channels; no goroutine leaks; respect ctx cancellation.

TypeScript / React:

- **Strict everything**: no `any` (use `unknown` + narrowing). Honor the
  strict `tsconfig` (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`).
- **`import type`** for type-only imports. Named exports except where Next.js
  requires a default (pages/layouts/route handlers). Files are kebab-case;
  React components PascalCase.
- **Server Components by default**; add `'use client'` only when you need
  state/effects/handlers. Keep the client bundle small.
- **Model async UI as a discriminated union** (`{status:'idle'|'loading'|'error'|'ok', ...}`),
  not a pile of booleans. `AbortController` on every fetch; no floating
  promises.
- **No business logic in components.** Fetch + present. Types live in
  `web/lib/`.

SQL / migrations:

- Idempotent (`create … if not exists`), numbered, **forward-only**. Never
  edit a merged migration; write a new one.
- Parameterized always. snake_case. `EXPLAIN ANALYZE` the hot queries and
  confirm GIN/GIST indexes are used (no seq scans on `businesses`).

When unsure, match the nearest existing code and the contracts in
`docs/roadmap/05-architectural-contracts.md`.

## Working model — chunked work, worktrees, parallel agents

Detailed procedure: `docs/operations/workflow.md`. Summary:

**One chunk = one board issue = one branch = one worktree = one PR.**

1. **Chunk** a roadmap stage into issues small enough for one PR (Size ≤ M).
   Each issue names the files it touches and the contract (C1–C8) it honors,
   so parallel agents don't collide.
2. **Claim**: set the board item `Status: In progress`, assign yourself.
3. **Branch + worktree**: `git worktree add ../lemon-search-<slug> -b <type>/<scope>-<slug>`.
   (Or spawn an Agent with `isolation: "worktree"`.)
4. **Build behind a feature flag** if the work can't ship complete in one PR
   (see below). `main` must always stay deployable.
5. **PR**: open against `main`; set board item `Status: In review`; fill the
   PR template (links the stage + the contract).
6. **Merge** when CI is green; board item → `Done`; remove the worktree.

Coordination is via the **contracts in `docs/roadmap/05-architectural-contracts.md`**
(C1 `BusinessRepo`, C2 `Candidate`, C4 HTTP shape, C5 `Overlay`, …). Agents code
against the interface, not against each other's in-flight work.

### Feature flags

Any feature that can't land complete-and-correct in a single PR goes behind a
flag, default **off** in prod, so `main` stays shippable while parallel work
proceeds.

- **Backend**: env var `LEMON_FF_<NAME>` → read once into `internal/flags`
  at startup. Example: `LEMON_FF_INTENT` gates the intent extractor while the
  lexicon is incomplete.
- **Frontend**: `NEXT_PUBLIC_FF_<NAME>`.
- **Not the same as** `config/ranking.yaml` `signal_formulas` switches — those
  are tuning knobs for *complete* features; feature flags gate *incomplete*
  work.
- Register every flag in `docs/operations/feature-flags.md` with its purpose,
  default, and removal condition. Delete the flag once the feature is on by
  default everywhere (no permanent flags).

## Quality gates — must pass before a PR merges

Full matrix in `docs/development.md`. Enforced by `lefthook` (commit/push) and
CI. Highlights:

- **Correctness**: `golangci-lint` (errcheck, staticcheck, govet, errorlint,
  bodyclose, sqlclosecheck, …); TS `strict-type-checked`.
- **Complexity**: gocyclo ≤12, gocognit ≤15, funlen ≤80; TS complexity ≤12,
  cognitive ≤15.
- **Dead code**: Go `unused`; TS `knip`.
- **Drift**: `go-arch-lint` + `depguard`; `eslint-plugin-boundaries`.
- **Duplication**: `dupl`, `goconst`; `sonarjs/no-duplicate-string`.
- **Secrets**: `gitleaks` (staged + history + CI).
- **Tests**: three tiers by build tag — **unit** (untagged, pure, `go test -race
  ./...`), **integration** (`-tags=integration`), **e2e** (`-tags=e2e`); both
  DB-backed tiers run vs a real Postgres in CI. Core (`rank`/`intent`/`config`)
  ≥90% via `scripts/ci/coverage-gate.sh`. See `docs/development.md` (Tests).
- **Migrations**: idempotent — CI applies them twice.
- `--max-warnings=0` everywhere.

Run locally before pushing: `lefthook run pre-push --all-files`.

## Commits + PRs

- **Conventional Commits**, enforced by `commitlint`. Format:
  `type(scope): subject` (lower-case subject, no trailing period, ≤100 chars).
  - types: `feat fix perf refactor docs test build ci chore style revert rank bench data`
  - scopes: `api web ingest schema config rank intent retrieve observ bench ci hooks docs deps repo`
- **No Claude/AI attribution in commits** (no "Generated with Claude Code"). The
  Orca CLI auto-appends its own `Co-authored-by: Orca` trailer — that's
  intentional; leave it in.
- One logical change per PR. Link the board item + stage doc. Use the PR
  template's spec-faithfulness checklist.
- Never force-push `main`. Never merge red CI.

## Commands

```bash
# Go
cd api && make fmt lint test build
cd api && go-arch-lint check --project-path .
go run ./cmd/ingest -input ../businesses-2026-05-27.json

# Web
cd web && npm run dev | build | lint | typecheck | knip | madge

# DB — local dev (Supabase Postgres in Docker; no cloud needed). See docs/operations/local-dev.md
make db-up                   # start local Postgres on :54322 (supabase start)
make db-reset                # apply supabase/migrations/* (extensions, tables, indexes, role)
make db-ingest               # load businesses JSON into the local DB (needs cmd/ingest — #20)
psql "$LEMON_DATABASE_URL" -f supabase/migrations/0001_initial_schema.sql   # direct apply

# Bench
go run ./scripts/bench-runner            # writes bench/results-<date>.json

# Hooks (whole repo, mirrors CI)
lefthook run pre-push --all-files
```

## Where to look

| Need | Doc |
|---|---|
| Roadmap (4 stages) | `docs/roadmap/00-overview.md` |
| Cross-stage interface contracts (C1–C8) | `docs/roadmap/05-architectural-contracts.md` |
| The ranking math | `docs/ranking/semantics.md` |
| Intent lexicon | `docs/ranking/intent.md` |
| Schema / data dictionary | `docs/data/schema.md` |
| Data quality + what we drop/synth | `docs/data/quality.md` |
| Ingestion pipeline | `docs/data/ingestion.md` |
| API contract | `docs/api.md` |
| Deploy runbook | `docs/operations/deployment.md` |
| Parallel-agent / worktree / board workflow | `docs/operations/workflow.md` |
| Feature-flag registry | `docs/operations/feature-flags.md` |
| Decisions (ADRs) | `docs/adr/` |
| Quality stack | `docs/development.md` |

## Gotchas

- **The data JSON is malformed.** `businesses-*.json` is pretty-printed objects
  separated by `}\n{` (not `},\n{`). Stream-parse with a depth counter; never
  `json.Unmarshal` the whole file. See `docs/data/ingestion.md`.
- **`lemon_score` is skewed** (mean ≈ 9). Weak discriminator. Kept literal per
  the contract; Bayesian alternative behind a switch.
- **Hours are 81% populated.** Missing-hours → soft-open (signal 0.7), never
  hard-filtered.
- **No `reaction_count` column.** We use `google_review_count` as the proxy.
- **12 open Dependabot PRs** (some major: TypeScript 6, unicorn 64, boundaries
  6). Triage before merging — major bumps may need config updates. Don't
  blanket-merge.
- **Data dumps are gitignored** (`businesses_lemon.csv`, `businesses-*.json`).
  Don't commit them.
```
