# Stage 1 — Foundation (Day 1)

## Goal

Stand up the entire skeleton — DB schema, ingestion, deployed (empty) API and
FE — so that Stage 2 can start writing real search logic on Day 2 morning
without setup detours.

## Where this fits

- **Upstream**: spec + JSON data file. Nothing else.
- **Downstream**: Stage 2 consumes the schema (C2 Candidate struct fields),
  the `BusinessRepo` port (C1), the synth seeds in the database, the bench
  scaffolding (C7), and the deployed API/FE skeletons.

## Architectural commitments locked here

- **C1 `BusinessRepo` interface** is declared (with no method bodies yet — a
  stub is fine). Stage 2 will implement it.
- **C2 `Candidate` struct** lands in `domain/types.go`. Fields list is locked;
  Stage 3 may add nullable fields but not rename/remove.
- **C3 ranking config schema** lands in `internal/config`. `signal_formulas`
  switches default to `literal` but are wired through Stage 1; Stage 3 fills
  in the alternative formulas.
- **C6 migrations format** is set by `0001_initial_schema.sql`.
- **C7 bench file format** is set by the committed `bench/queries.json`.

## Acceptance criteria

- [ ] Supabase project provisioned (`us-east-1`), DB password stored, direct
      connection string in `.env.local` and as a GitHub Actions secret.
- [ ] Migration `0001_initial_schema.sql` applied on Supabase; extensions
      (`pg_trgm`, `cube`, `earthdistance`) present.
- [ ] Ingestion CLI loads ≥ 22,000 rows from `businesses-2026-05-27.json`.
- [ ] ≥ 95% of loaded rows have: non-null `category`, `archetype`, and
      `latitude`/`longitude`.
- [ ] `friend_count` synthesized deterministically; re-running ingestion
      produces identical values. `is_claimed` is carried from the source JSON
      (real passthrough, default false), not synthesized.
- [ ] Go API on Fly.io serves `GET /healthz` → 200; Next.js on Vercel serves
      `/` with the skeleton page.
- [ ] CI green on `main`.
- [ ] `bench/queries.json` committed with the 30 curated queries.
- [ ] Read-only `lemon_grader` Postgres role created (password set
      out-of-band).
- [ ] Day-1 progress note committed (`docs/progress/day-1.md`).

## Deliverables

| Artifact | Path | Notes |
|---|---|---|
| Schema migration | `supabase/migrations/0001_initial_schema.sql` | Tables, indexes, generated columns, `lemon_seed()`, `lemon_grader` role |
| Domain types | `api/internal/domain/types.go`, `repo.go` | `Candidate`, `Archetype`, `BusinessRepo` interface stub |
| Config loader | `api/internal/config/loader.go` | YAML → typed struct, validation, fail-fast errors |
| Ingestion CLI | `api/cmd/ingest/main.go` + `api/internal/ingest/*` | Stream parser, taxonomy, synth, bulk loader |
| Taxonomy map | `api/internal/ingest/taxonomy.go` | Raw → spec category + archetype assignment |
| Synth seeds | `api/internal/ingest/synth.go` | `FriendCount(id)` (deterministic); `is_claimed` is source passthrough, not synthesized |
| API skeleton | `api/cmd/api/main.go` | `/healthz`, `/version`; reads `LEMON_DATABASE_URL` |
| FE skeleton | `web/app/page.tsx` | Plain heading + tagline |
| Fly config | `fly.toml` | API region `iad`, Postgres URL from secret |
| Bench scaffold | `scripts/bench-runner/main.go` | HTTP client over `bench/queries.json` |
| Day-1 note | `docs/progress/day-1.md` | What's done, what's blocked, what's next |

## Sub-tasks (ordered)

1. **Project init**
   - `cd api && go mod tidy`
   - `npm install` (root) and `cd web && npm install` (workspace)
   - `lefthook install`
2. **Supabase + secrets**
   - Create project (`us-east-1`).
   - Apply `0001_initial_schema.sql` via Supabase SQL editor.
   - Add `LEMON_DATABASE_URL` to `.env.local` and GitHub secrets.
3. **Domain + config**
   - Land `Candidate`, `Archetype`, `BusinessRepo`, `SearchOpts` in `domain/`.
   - Write `internal/config/loader.go` with validation; round-trip test.
4. **Ingestion pipeline**
   - Stream-parser (depth-counted state machine; never `json.Unmarshal` whole file).
   - Taxonomy normalizer (dirty → spec; unmapped → `Other`).
   - Archetype assigner (1-of-6, centralized table).
   - Non-Miami filter (bounding box + manual blocklist).
   - Synthesize `friend_count` only (3% have 1–5); `is_claimed` is a real
     passthrough from the source JSON (default false), not synthesized.
   - Bulk `pgx.CopyFrom`; upsert on `id` (idempotent).
5. **API + FE skeletons + deploy**
   - Go API: `/healthz`, `/version`; structured logging.
   - `fly.toml`; deploy: `fly deploy`.
   - Next.js skeleton; Vercel via GitHub integration.
6. **Bench scaffolding**
   - `scripts/bench-runner/main.go` reads `bench/queries.json`, writes
     `bench/results-<date>.json`. Skips `__FILL__` expectations.
7. **CI green** — confirm all jobs pass on `main`.
8. **Day-1 progress note** — committed.

## Testing design

### Unit tests
| Subject | File | Cases |
|---|---|---|
| JSON streaming parser | `internal/ingest/parser_test.go` | well-formed array, `}\n{` separator, escaped quotes, nested objects (depth > 5), empty array, single object, truncated file |
| Taxonomy normalizer | `internal/ingest/taxonomy_test.go` | golden file of 100 raw→spec mappings; unknown → `Other`; every spec category maps to exactly one archetype |
| Synth seeds | `internal/ingest/synth_test.go` | `FriendCount(id)` deterministic across repeated calls; over 10000 sampled IDs ~3% have non-zero `friend_count`, all nonzero values in 1..5, mean of nonzero ≈ 3.0 |
| Non-Miami filter | `internal/ingest/filter_test.go` | inside bbox → keep; outside → drop; null lat/lng → drop; bounded-test of Versailles, FR sample |
| Config loader | `internal/config/loader_test.go` | valid YAML round-trip; missing required field → typed error; unknown archetype → typed error |

### Integration tests (build tag `integration`)
- `internal/ingest/integration_test.go`: spin up Postgres (CI service
  container), apply migration, run ingest on a 100-row fixture, query back,
  assert row count + per-archetype distribution + claimed-rate. Re-run; assert
  identical results (idempotency).

### Contract tests
- None at this stage — the `BusinessRepo` interface has no body yet.

### Bench
- `scripts/bench-runner` executes against the deployed (empty-search-result)
  API; all tests skip pass/fail but record connectivity + timing for `/healthz`.

### CI smoke
- Migrations apply **twice**.
- `/healthz` and `/version` return 200.
- DB connectivity from API process (one `SELECT 1` at startup).

## Interface to next stage (Stage 2 reads this)

- `domain.BusinessRepo` declared; the `Search` and `ExactName` methods exist
  on the interface but the Postgres adapter has only stubs (returns
  `ErrNotImplemented`).
- `domain.Candidate` fields are final (additions in Stage 3 are nullable).
- `config.Ranking` struct is loadable from `config/ranking.yaml`. The struct
  exposes archetype weights and behavior flags; `signal_formulas` switches
  default to `literal`.
- `businesses` table has all rows loaded, with `archetype` and `friend_count`
  populated and all generated columns computed. `is_claimed` holds the real
  source value (passthrough, default false), not a synthesized one.
- `bench/queries.json` is committed (still with `__FILL__` placeholders);
  the runner exists.

## Risks + mitigations

- **JSON parser correctness.** 626 MB file with `}\n{` delimiters → naive
  `json.Unmarshal` and naive splitting both fail. **Mitigation**:
  depth-counted state machine; unit-test against handcrafted fixtures with
  embedded quotes and nested braces; fuzz with random byte insertion.
- **Archetype assignment coverage.** ~5% of raw categories don't map to spec
  taxonomy. **Mitigation**: route to `Other` with reduced weights; log a
  histogram of unmapped values; include the histogram in the Day-1 progress
  note so we can revisit during Stage 3 if needed.
- **Supabase pooler vs. direct connection.** Pooler can choke on `COPY`.
  **Mitigation**: use the direct connection for ingest; pooler for the API.

## Out of scope

- `/search` endpoint logic (Stage 2 returns a placeholder).
- Ranking, intent extraction, alternative formulas.
- UI input box, debounce, results list.
- Filling in `expected_top_3` slots in `bench/queries.json` (Stage 2's
  responsibility once stable names exist).
