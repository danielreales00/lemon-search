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

We ran the shadow A/B before committing. Benchmark: 726 cases generated from 300
real businesses (sampled by `md5(id)`; per-word typos, accent-stripped, and
3-token partials, all with automatic ground truth), identical cases across
engines, **same ranker + same pin coverage logic** — only the retrieval engine
differs (`cmd/bench-runner -generate`).

**The first pass used Meili's DEFAULT settings and under-sold it.** Meili's
defaults give zero typo tolerance to words under 5 chars
(`minWordSizeForTypos {oneTypo:5, twoTypos:9}`) and drop the *last* query word on
multi-word misses (`matchingStrategy: last`) — far stricter than our levenshtein
budget. After tuning Meili the way one actually would for typo-tolerant name
search (`minWordSizeForTypos {3,7}`, `matchingStrategy: frequency`, pin probe 50),
the picture changed materially:

| dimension | Postgres trgm 0.85 | Postgres cov+leven | Meili (default) | Meili (tuned) |
|---|---|---|---|---|
| exact     | 100% | 100%    | 100% | 100% |
| typo      | 69%  | **97%** | 77%  | 92%  |
| accent    | 67%  | 100%    | 83%  | 100% |
| over_fire | 88%  | 76%     | 100% | **92%** |
| partial   | 37%  | 37%     | 39%  | **43%** |
| **overall** | 76% | **86%** | 80% | **86%** |
| search p95 (local) | ~105ms | ~40ms | ~11ms | **~8ms** |

**Tuned Meili ties the Postgres matcher overall (86% each)**, with a different
profile: Meili wins **partials** (43% vs 37%) and **over-fire** (92% vs 76% —
ranking beats hard-pinning on bare category words) and raw search latency;
Postgres wins **typo** (97% vs 92% — its per-word edit budget exceeds Meili's
2-typo cap); accent and exact tie.

**Decision: Postgres for this build — simplicity, not superiority.** On quality
they are comparable. Postgres stays a single system (no sync pipeline, no second
source of truth, the Supabase deliverable is self-contained), the right call
under a 4-day budget. Meili is now a **validated** escape hatch behind the
`BusinessRepo` port: it measurably helps the two dimensions our pin is weakest on
(partials, over-fire), and we'd adopt it if those — or multilingual/semantic —
become priorities. (Meili's raw search is faster locally, but a real deployment
adds an API→engine hop that partially offsets it.)

Follow-ups this surfaced: (1) the coverage pin's over-fire on bare category words
(76%) — to be fixed by Stage-3 taxonomy suppression in the intent layer, which
Meili sidesteps by ranking; (2) partials are weak for the pin and want
ranking/recall help.

Caveats: the bench Meili adapter simplifies open-status (soft 0.7) and uses
rune-levenshtein for the pin; neither materially affects name-match pass@3.
Meili could likely be tuned further (custom ranking rules, synonyms) — the
comparison is "competently tuned", not "maximally tuned".

## Cross-references

- Index spec: [../data/schema.md](../data/schema.md#indexes)
- Ranking division of labor: [0003-ranking-strategy.md](0003-ranking-strategy.md)
