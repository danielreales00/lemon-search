# Day 1 — 2026-05-27 progress note

> Backfilled from the commit history. The build compressed: Day 1 was scaffold +
> architecture + the data/schema foundation; the bulk of the feature PRs merged
> early on 2026-05-28 (see Day 2). Notes are organized by stage, not calendar.

## What I shipped

- **Repo + quality scaffold**: Go (`api/`) + Next.js (`web/`) monorepo; lefthook
  commit/push hooks mirroring CI; `commitlint`; `gitleaks`; and the
  hexagonal-boundary enforcement that everything else leans on —
  `go-arch-lint` + `depguard` (Go) and `eslint-plugin-boundaries` (TS).
- **Architecture locked** (`docs/adr/`, `docs/roadmap/`): hexagonal core +
  two-phase retrieval; ADR-0001 stack, ADR-0002 Postgres-first search,
  ADR-0003 ranking strategy, ADR-0004 spec-contract discipline, ADR-0005
  hexagonal core; cross-stage contracts C1–C8; `CLAUDE.md` + the parallel-agent
  / worktree workflow + the feature-flag registry.
- **Schema + migrations** (`supabase/migrations/0001_initial_schema.sql`):
  `businesses` with GIN `pg_trgm` (name) + GIN weighted `tsvector` +
  GIST `earthdistance` (`loc`) + GIN tag arrays; generated columns
  `photo_count` / `is_new` / `loc` / `search_vector`. Local dev DB harness via
  the Supabase CLI.
- **Pure domain core** (`api/internal/domain`): `Candidate`, `Archetype`,
  `SearchOpts`, and the `BusinessRepo` port (#58, contract C1/C2).
- **Ingestion pipeline** (`api/internal/ingest`, `cmd/ingest`): streaming
  depth-counted JSON parser (handles the malformed `}\n{` data), Miami geo
  bbox + FL-address filter (#59), taxonomy normalizer + archetype assigner
  (#63), deterministic `friend_count` synth (#65), COPY-stream loader. ~22.5k
  Miami businesses ingested.
- **Typed config loader** with fail-fast validation (`api/internal/config`, #60).
- **Skeletons**: web page + API base-URL wiring (#56); `healthz`/`readyz`/
  `version` API with `slog` + a pgx pool (#64).
- **CI tiers**: unit / integration / e2e by build tag + a core coverage gate (#69).

## What's in flight

- The search core (retrieval SQL + ranker + `/search`) — Stage 2.

## What I'm blocked on

- Nothing.

## Numbers

- ~22,568 Miami businesses ingested (after geo/address filtering).
- CI status on `main`: green.
- Migrations applied through: `0001_initial_schema.sql`.

## Decisions made today

- **Stack A3a**: Go API (Fly `iad`) + Supabase Postgres (`us-east-1`) + Next.js
  on Vercel — Go for the backend, Supabase to satisfy the shared-DB deliverable,
  paired regions for a ≤10ms API↔DB hop.
- **Postgres-first search** (`pg_trgm` + `tsvector` + tag GIN + `earthdistance`);
  Meilisearch kept as a measured escape hatch behind the `BusinessRepo` port.
- **Data fidelity**: keep `is_claimed` as the 10 real source rows (do NOT
  synthesize it); `friend_count` is the one sanctioned synthesis.
- Hexagonal boundaries are **machine-enforced**, not a convention — drift fails
  the hook and CI.

## Tomorrow's first move

- Build the SQL retrieval function + the pure 7-signal ranker, wire `/search`.

## Stage-1 acceptance criteria touched

- [x] Schema + indexes (trgm / tsvector / earthdistance / tag GIN) applied.
- [x] Ingestion: stream-parse, normalize taxonomy, assign archetype, geo-filter, load.
- [x] Pure domain core + `BusinessRepo` port; config loader.
- [x] API/web skeletons live; CI test tiers + coverage gate enforced.
