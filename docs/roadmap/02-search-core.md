# Stage 2 — Search core (Day 2)

## Goal

Ship end-to-end search: a query typed in the UI returns the top 15 ranked
businesses, computed by the 7-signal × archetype-weighted scorer with
spec-literal formulas. Bench pass rate ≥ 60%.

## Where this fits

- **Upstream**: Stage 1 — `domain.BusinessRepo` interface, `Candidate` struct,
  the ranking config struct, the populated `businesses` table, deployed
  skeletons.
- **Downstream**: Stage 3 extends `SearchOpts` with `IntentFilters`, plugs in
  alternative formulas, and tunes weights; the contract under §"Interface to
  next stage" below is what Stage 3 may not break.

## Architectural commitments locked here

- **C4 HTTP response shape** is locked. JSON keys, nested `timings` object,
  field types — additions OK, renames not.
- **`ranking.search_candidates()` SQL function** signature is locked:
  ```sql
  search_candidates(
      q             text,
      lat           float8,
      lng           float8,
      now_ts        timestamptz,
      lim           int,
      -- nullable overlay params (Stage 3 fills them; Stage 2 passes NULL)
      tag_filter            text[]    default null,
      category_filter       text      default null,
      subcategory_filter    text[]    default null,
      price_filter          text[]    default null,
      require_open          boolean   default false
  ) RETURNS TABLE ( … raw signal columns … )
  ```
  Stage 3 only *fills* the nullable params; it does not change the signature.
- **Ranker pipeline order** locked: hard-filter → signals → linear sum →
  new-biz demote → exact-name pin (prepend) → tie-break → de-pin pass. Stage 3
  may insert steps but the order is fixed.
- **Bench schema** stays the same (C7).

## Acceptance criteria

- [ ] Postgres function `search_candidates` deployed; returns up to 150 rows
      with raw signals per call.
- [ ] Separate exact-name SQL path returns at most one row.
- [ ] Go re-ranker implements 7 signals with **spec-literal** formulas
      (defaults from `config/ranking.yaml`).
- [ ] Hard-filter pre-pass drops closed businesses where archetype demands it.
- [ ] Exact-name match prepends at position #1.
- [ ] New-business demote + de-pin pass implemented.
- [ ] `GET /search?q=...&lat=...&lng=...&now=...` returns JSON matching C4
      with per-stage timings.
- [ ] FE SearchBar debounced (~50ms), uses `AbortController`, renders a
      15-row ResultsList. No console errors.
- [ ] `expected_top_3` slots in `bench/queries.json` replaced with real names.
- [ ] Bench script reports pass rate ≥ 60% locally; CI green.
- [ ] Unit tests cover hard filter, exact-name pin, new-biz demote, de-pin
      pass, tie-break (each as a separate, named subtest).

## Deliverables

| Artifact | Path | Notes |
|---|---|---|
| Retrieval SQL | `supabase/migrations/0002_search_candidates.sql` | Function returning 150 rows |
| Domain types | `api/internal/domain/types.go` (extended) | `Score`, `IntentFilters` (empty here, filled Stage 3) |
| Postgres adapter | `api/internal/retrieve/postgres/repo.go` | Implements `BusinessRepo`; pgx pool; prepared statements |
| Ranker | `api/internal/rank/scorer.go`, `signals.go` | Pure functions, unit-tested against fixtures |
| Config loader | `api/internal/config/loader.go` (extended) | Reads `signal_formulas`, archetype weights |
| Intent stub | `api/internal/intent/extract.go` | Returns empty `Overlay`; lexicon ships Stage 3 |
| HTTP handler | `api/internal/api/search.go` | Validate, call repo, call ranker, encode |
| FE SearchBar | `web/components/SearchBar.tsx` | Debounced, AbortController |
| FE ResultsList | `web/components/ResultsList.tsx` | 15 rows, archetype-relevant chips |
| FE page | `web/app/page.tsx` | Wires SearchBar + ResultsList |
| Bench runner | `scripts/bench-runner/main.go` | Finalize report format |
| Day-2 note | `docs/progress/day-2.md` | |

## Sub-tasks (ordered)

1. **Retrieval SQL** — write `search_candidates` with the locked signature.
2. **Exact-name SQL** — separate query, 0–1 row.
3. **Domain + repo** — implement `Search` and `ExactName`; prepared statements
   at pool init.
4. **Ranker** — signals.go (pure functions); scorer.go (linear sum + pipeline);
   60+ unit tests against fixture candidate slices.
5. **HTTP handler** — validate, timings via `observ.Stopwatch`, JSON via
   tagliatelle snake-case.
6. **FE** — SearchBar with 50ms debounce + AbortController; ResultsList with
   the chips defined in C4; no animations beyond fade-in.
7. **Bench fill** — manually curate `expected_top_3` for the 30 queries
   (consult the live DB; pick 3 acceptable hits per query).
8. **Bench run** — confirm pass rate ≥ 60% locally; commit `bench/results-<date>.md`.

## Testing design

### Unit tests
| Subject | File | Cases |
|---|---|---|
| Ranker pipeline | `internal/rank/scorer_test.go` | hard-filter drops closed for low_stakes; hard-filter ignored for high_stakes; exact-name pin overrides everything (incl. ties); rating-demote multiplies signal for is_new; de-pin pass swaps new biz out of top-2 when within `swap_window`; tie-break order (score → claimed → closer → more reviews) |
| Signal computations | `internal/rank/signals_test.go` | each signal formula on edge inputs (n=0, n=10000, missing rating, missing hours, photo_count=0, photo_count=99, distance=0, distance>30mi); `literal` rating and distance only at this stage |
| Config loader | `internal/config/loader_test.go` | sample YAML → struct; missing key → typed err; bad archetype name → typed err; unknown signal → typed err |
| Intent stub | `internal/intent/extract_test.go` | returns empty `Overlay` for any input (placeholder until Stage 3) |
| HTTP shape | `internal/api/search_test.go` | request validation; encoding empties → `results: []` not `null` |

Coverage targets: `rank` ≥ 90%, `config` ≥ 90%, `intent` ≥ 80%, `api` ≥ 80%.

### Integration tests (build tag `integration`)
- `internal/retrieve/postgres/repo_test.go`:
  - Apply migrations to test DB; seed 50 fixture businesses (distinct
    archetypes).
  - `Search("sushi")` returns rows whose category/subcategory or tags
    contain sushi-related terms.
  - `Search("xyzzy-no-such-thing")` returns ≤ a small number of weak matches.
  - `ExactName("Joe's Barber Shop")` returns the seeded row.
  - Geo: businesses farther than 30 mi excluded; closer businesses surface first.

### Contract tests
- `internal/api/contract_test.go`: marshal an empty result + a populated
  result; validate JSON against a schema generated from `web/lib/api.ts`
  (`SearchResponse` type). Generate via `typescript-json-schema` or hand-roll
  a small JSON Schema (`docs/contracts/search-response.schema.json`).
- The FE has a typeguard test (`web/lib/api.test.ts`) that asserts a sample
  payload deserializes to `SearchResponse` without `any` widening.

### Bench
- Run `scripts/bench-runner` against local API + seeded DB.
- Pass: ≥ 60% of non-placeholder tests have at least one expected name in
  top 3.
- Record per-stage timings; surface p50/p95.

### Manual smoke (≤ 5 min)
- Type 5 queries in the live UI. Verify no console errors, no flicker, no
  layout shift on results render.

## Interface to next stage (Stage 3 reads this)

- `search_candidates` function accepts overlay params (currently passed
  NULL by Stage 2). Stage 3 only fills them; signature does not change.
- `ranker.Run(ctx, candidates, cfg, opts) []Result` is the public entry —
  Stage 3 hooks alternative formulas behind `cfg.SignalFormulas.Rating` and
  `cfg.SignalFormulas.Distance` without changing the signature.
- HTTP response shape (C4) is locked.
- Bench file format unchanged; Stage 3 only adds tests.

## Risks + mitigations

- **`pg_trgm` recall on short tokens.** Two-char prefixes are weak.
  **Mitigation**: blend trigram similarity with `ts_rank_cd` on the weighted
  `tsvector`; raise `pg_trgm.similarity_threshold` only if measurable.
- **GIST + bounding-box index choice.** `earthdistance` is simplest; if
  performance disappoints, switch to PostGIS `geography`. Defer unless
  measured slow.
- **Hours JSONB evaluation cost.** 150-row open-now eval is cheap when `now`
  is parameterized once per call. Don't compute per row in a subquery.
- **JSON shape ↔ TS type drift.** Mitigated by the contract test at the seam
  (Go marshals → JSON Schema validation against TS-derived schema).

## Out of scope

- Intent extraction lexicon (Stage 3).
- Alternative formulas (Bayesian rating, decay distance) — leave the config
  switches in place but only the `literal` paths are exercised.
- Latency tuning beyond "doesn't feel sluggish." Stage 3 owns sub-100ms p95.
- Pretty UI.
