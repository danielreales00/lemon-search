# Lemon Search — 4-day build writeup

> Status: **DRAFT — landed on Day 4**. This stub is committed at
> Stage 1 so the structure is visible from the start. Each section is filled
> in during Stage 4 against real numbers from `bench/results-final.md` and
> `bench/loadtest-final.md`.

## TL;DR

_One paragraph: what shipped, p95 latency, bench pass rate, biggest call._

## Stack

- Backend: Go API on Fly.io (`iad`)
- DB: Supabase Postgres (`us-east-1`)
- Frontend: Next.js 15 on Vercel
- Why this combination: see [adr/0001-stack-choice.md](adr/0001-stack-choice.md).

## Search engine

- Choice: Postgres `pg_trgm` + weighted `tsvector` + GIN on tag arrays +
  `earthdistance`.
- Alternatives considered: Algolia, Meilisearch, Typesense. Why Postgres
  won at 23k rows: see [adr/0002-search-engine.md](adr/0002-search-engine.md).

## Schema

- See [data/schema.md](data/schema.md) for the column-by-column reference.
- Generated columns do the index-time precomputation:
  `loc` (earth), `photo_count`, `is_new`, `search_vector`.
- The malformed-JSON ingestion gotcha and how we handle it:
  [data/ingestion.md](data/ingestion.md).

## Ranking + archetypes

- Default = spec-literal everywhere
  (`rating = lemon_score / 10`, `distance = max(1 - d/30mi, 0)`, archetype
  strictly per-business).
- Two alternative formulas (Bayesian rating, decay distance) live behind
  config switches for the comparison below.
- Math reference: [ranking/semantics.md](ranking/semantics.md).
- Architecture: [adr/0003-ranking-strategy.md](adr/0003-ranking-strategy.md).
- Why we held the spec contract instead of silently substituting smarter
  variants: [adr/0004-spec-contract-discipline.md](adr/0004-spec-contract-discipline.md).

## Smart semantic intent

- Lexicon-driven, no LLM, no embeddings. Sub-millisecond extractor.
- Reference: [ranking/intent.md](ranking/intent.md).

## Bench results

_Filled on Day 4 from `bench/results-final.md`._

| Mode | Pass rate (top-3) | p50 total | p95 total | p99 total |
|---|---|---|---|---|
| `rating: literal`, `distance: literal` (default) | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: bayesian`, `distance: literal` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: literal`, `distance: decay` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: bayesian`, `distance: decay` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |

Commentary: _which mode performed best, why, and what we'd recommend at V2._

## p95 latency

_Filled on Day 4 from `bench/loadtest-final.md`._

| Stage | p50 | p95 | p99 |
|---|---|---|---|
| `intent_ms` | _TBD_ | _TBD_ | _TBD_ |
| `sql_ms` | _TBD_ | _TBD_ | _TBD_ |
| `rerank_ms` | _TBD_ | _TBD_ | _TBD_ |
| `total_ms` (server) | _TBD_ | _TBD_ | _TBD_ |
| End-to-end (with network) | _TBD_ | _TBD_ | _TBD_ |

Methodology: `scripts/loadtest.sh` (`hey -z 60s -c 50 -q 10`) against the
deployed `/search` endpoint with a query pool simulating one-keystroke-per-RPS.

## Spec ambiguities + calls

- **"reaction count"** — no such column in the data. We use
  `google_review_count` (98% coverage). Documented in [data/quality.md](data/quality.md).
- **"inverse distance, capped at 30 miles"** — interpreted as
  `max(1 - d/30mi, 0)`. Per-archetype emphasis lives entirely in the weight,
  not in the curve. Decay-curve alternative behind config switch.
- **`lemon_score` skew (mean ≈ 9)** — kept the literal formula
  (`lemon_score / 10`) as the contract; surfaced Bayesian-google as an
  opt-in switch and quoted the comparison.
- **Archetype assignment** — strictly per-business via category mapping.
  Intent extraction does *not* override archetype; it only narrows
  retrieval. (See ranking decision D6.)
- **Exact-name "boost" vs. category-aware matching** — the spec lists these as
  *separate* behaviors with deliberate verbs: a name "returns that business
  first, **regardless** of other ranking signals" (a hard override) vs. a prefix
  "**surfaces**" a match (rank, don't override). Our first pass pinned on
  `name ILIKE q || '%'`, which conflated them — `coffee` pinned "Coffee To Go",
  `sushi` pinned "Sushi Joe" over far better results, though both are *category*
  leaves in the taxonomy (Café→Coffee Shop, Casual/Fast→Sushi). **The deeper
  tension:** trigram similarity can't separate a typo'd *full name* from a
  *category prefix* — measured, the spec's own `joes barbr shop → Joe's Barber
  Shop` scores **0.57**, the same band as the false positives (`coffee` 0.54,
  `sushi` 0.60), so *no single threshold separates them*. Since the pin is a
  max-cost override (a false positive is catastrophic; a false negative merely
  demotes a still-ranked result), we took the **high-precision call**: pin only
  on `similarity ≥ 0.85` (dropped the bare prefix clause). The real
  discriminator is name *coverage* + *taxonomy membership*, deferred to the
  Stage-3 intent layer (where the pin also folds into main retrieval for real
  distance + one fewer round-trip).
- **Rubric says 4 archetypes, body lists 6** — we implemented 6 per the body.

## What's broken / known gaps

- **Hours coverage 81%** — for the 19% with missing hours, the open-status
  signal defaults to 0.7 ("soft-open") and `hard_filter` archetypes never
  drop them. Honest mitigation; alternative is to drop those businesses
  (more honest data) at the cost of ~4k results.
- **Categories drift** — ~5% of raw values don't map to the spec taxonomy
  ("Tobacco shop", "Trucking company", etc.). They go to an `Other`
  archetype with reduced weights. ~280 rows have empty category and are
  dropped.
- **~3% non-Miami records dropped** at ingestion (including Versailles, FR).
- **Friend signal denormalized** as `friend_count` on `businesses`. Real
  multi-user Lemon needs a per-user join.
- **Exact-name pin is conservative (Stage 2)** — pins only near-identical names
  (`similarity ≥ 0.85`); typo'd full names like "joes barbr shop" currently fall
  through to normal ranking (still surfaced, just not hard-pinned) rather than
  risk pinning category words. Proper coverage + taxonomy-suppressed pin is
  Stage-3 intent work.
- **No diversity (MMR)** — coffee chains can clump near the top of `coffee`.
- **No personalization** — no learning loop, no per-user history.

## What I'd do with another week

- **`pgvector` + sentence-transformer embedding** for semantic recall on
  hard queries (`a place to study quietly with good wifi`).
- **MMR / Maximum Marginal Relevance** to diversify the top-15 across
  chains/owners.
- **Click-through learning loop** — log `(query, result, clicked)` and use
  it to nudge ranker weights nightly.
- **Per-user friends / relations** — proper join table, swap the
  denormalized `friend_count`.
- **Second adapter (Meilisearch)** wired behind the same `BusinessRepo`
  port — the architecture supports the swap without touching the ranker.
- **Better hours data** — re-scrape from Google Places to push 81% → 99%
  and turn open-status into a fully-reliable signal.

## Architecture appendix

- [architecture.md](architecture.md): patterns adopted, patterns rejected,
  topology diagram.
- [roadmap/05-architectural-contracts.md](roadmap/05-architectural-contracts.md):
  cross-stage interface contracts (Repo, Candidate, HTTP shape, Overlay,
  migrations, bench).

## Quality stack appendix

See [development.md](development.md) for the full quality stack —
correctness, complexity, dead code, drift, duplication, cycles, secrets,
conventions. CI mirrors pre-push and adds migration idempotency, web build,
PR commitlint, markdownlint, and gitleaks history scan.
