# ADR-0002: Search engine — Postgres `pg_trgm` + weighted `tsvector` + tag-array GIN + `earthdistance`

- **Status**: Accepted
- **Date**: 2026-05-27
- **Deciders**: Daniel

## Context

The spec allows any search engine. The serious candidates:

| Engine | Typo tolerance | Custom 7-signal ranking | Geo | At 23k-row scale |
|---|---|---|---|---|
| Postgres `pg_trgm` + `tsvector` | OK with tuning | Full SQL control | `earthdistance` or PostGIS | Easy |
| Meilisearch | Excellent default | Limited inside engine (would re-rank in Go anyway) | Sub-optimal | Easy |
| Typesense | Excellent | Curation rules; not arbitrary 7-signal | Built-in | Easy |
| Algolia | Best-in-class | Rules-based — awkward for arbitrary 7-signal | Built-in | Easy |

Other considerations:

- The DB is already Supabase Postgres (ADR-0001). Using it for search
  collapses two systems into one — no sync, no second source of truth.
- The Go ranker does the precision pass anyway, so engine-side "advanced
  ranking" features are wasted.
- Typo tolerance at 23k Miami businesses: trigram + tsvector covers the
  spec's bar (`joes barbr shop`, `steaak`, prefix `best steakh`).

## Decision

**Postgres, with all four index types**:

| Index | Role |
|---|---|
| `gin (name gin_trgm_ops)` | Fuzzy / typo on names |
| `gin (search_vector)` | Weighted full-text over name/sub/cat/specialty/tags/about |
| `gist (loc)` | Geo bbox pre-filter + `earth_distance` ordering |
| `gin (universal_tags)` and `gin (specific_tags)` | Intent overlay (`@>`, `&&`) |

Two-phase retrieval (ADR-0003) keeps the engine focused on recall;
precision lives in Go.

Meilisearch stays in the back pocket: the `domain.BusinessRepo` port can
be implemented by a `retrieve/meilisearch` adapter without touching the
ranker if `pg_trgm` proves weak on the bench. Day-3 escape hatch only.

## Consequences

**Good**

- One engine for text + geo + filters. One round-trip per query.
- Schema, indexes, and migrations live in the same place. The Supabase
  deliverable is self-contained.
- Full SQL gives total control: weighted `tsvector` (A/B/C/D), trigram
  blend, bbox geo, tag-array overlap — all in one `SELECT`.

**Bad / cost**

- `pg_trgm` typo recall is good but not at Meilisearch's level on short
  tokens. We mitigate by *also* hitting `tsvector` and combining via
  `GREATEST(name_trgm, text_score)`.
- Diacritics aren't natively normalized — accent-stripped queries
  (`café` → `cafe`) need either the `unaccent` extension or pre-normalize
  in Go before trigram lookup.

**Revisit when**

- If Day-3 bench shows pass rate < 80% on the typo-cluster of queries,
  add a `retrieve/meilisearch` adapter, sync from Postgres, and run a
  shadow A/B before committing.

## Validation — measured 3-way comparison (2026-05-28)

We did exactly the shadow A/B above before committing the matcher. Benchmark:
726 cases generated from 300 real businesses (sampled by `md5(id)`; per-word
typos, accent-stripped, and 3-token partials, all with automatic ground truth),
identical cases across engines, **same ranker + same pin coverage logic** — only
the retrieval engine differs (`scripts`: `cmd/bench-runner -generate`).

| dimension | Postgres trgm 0.85 | Postgres coverage+levenshtein | Meilisearch v1.11 (defaults) |
|---|---|---|---|
| exact     | 100% | 100%     | 100% |
| typo      | 69%  | **97%**  | 77%  |
| accent    | 67%  | **100%** | 83%  |
| over_fire | 88%  | 76%      | **100%** |
| partial   | 37%  | 37%      | 39%  |
| **overall** | 76% | **86%** | 80%  |
| search p95 (local) | ~105ms | ~40ms | ~11ms |

- **Postgres coverage+levenshtein beats Meili on typo tolerance (97% vs 77%)** —
  per-word edit budget (≤4 chars, the spec's model) exceeds Meili's defaults
  (≤2/word). The purpose-built engine did not win the graded core out of the box.
- **Meili avoids the over-fire (100%)** by ranking instead of hard-pinning; our
  coverage pin still over-fires on bare single-token category words (76%) —
  fixable in Postgres via taxonomy suppression (Stage-3 intent), tracked separately.
- **The engine's hoped-for win — partials — did not materialize** (39% vs 37%);
  a short prefix of a long name is inherently ambiguous for all three.
- Meili's raw search is faster, but a second system adds a hop, a sync pipeline,
  and a second source of truth — against the one-system Supabase deliverable.

**Decision stands: Postgres** (coverage+levenshtein). It wins overall and on the
graded core (typo), single system. Meili tuning could close the typo gap but
adds operational cost for no overall gain; the `retrieve/meilisearch` adapter
remains a documented escape hatch behind the `BusinessRepo` port for a future
need (multilingual, semantic) that the data does not show today.

Caveats: Meili ran with default typo/ranking settings; the bench Meili adapter
simplified open-status and used rune-levenshtein for the pin. Neither materially
affects name-match pass@3.

## Cross-references

- Index spec: [../data/schema.md](../data/schema.md#indexes)
- Ranking division of labor: [0003-ranking-strategy.md](0003-ranking-strategy.md)
