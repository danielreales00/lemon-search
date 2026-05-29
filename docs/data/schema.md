# Data dictionary — `businesses`

Source of truth for the `businesses` table. The schema lives in
`supabase/migrations/0001_initial_schema.sql`; this doc explains *what each
column means*, where its value comes from, valid ranges, and how the
ranker consumes it.

## Table overview

```
businesses (≈ 22,000 rows after non-Miami filter)
  └── friend_count, loc, search_vector              (computed at ingest, persisted)
  └── is_claimed                                    (passthrough from source JSON, default false)
  └── photo_count, is_new                           (GENERATED ALWAYS AS … STORED)
```

No related tables in V1. (See [adr/0003-ranking-strategy.md](../adr/0003-ranking-strategy.md)
for why `friend_reactions` was denormalized.)

## Columns

### Identity

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `id` | `uuid` | no (PK) | Lemon JSON `id` | Stable across re-ingests |
| `name` | `text` | no | Lemon JSON `name` | Free text, used in `search_vector` (weight A) and `pg_trgm` index |
| `created_at` | `timestamptz` | no | Defaults to `now()` at first insert | Ingest is idempotent; only set on insert |

### Taxonomy

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `category` | `text` | no | Normalized at ingest | Spec taxonomy. See [taxonomy.md](taxonomy.md) |
| `subcategory` | `text` | yes | Normalized at ingest | Spec sub-taxonomy |
| `specialty` | `text` | yes | Normalized at ingest | Sub-sub level; mostly free text |
| `archetype` | `text` | no | Derived from `category` at ingest | One of 6 enum values; `CHECK` constraint enforces |

Valid `archetype` values:
`low_stakes_fast_nearby`, `medium_stakes_occasion`, `high_stakes_one_time`,
`experiential`, `recurring_service`, `utility_distance_dominant`.

### Location

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `address` | `text` | yes | Lemon JSON `address` | Free text, surfaced in UI but not indexed |
| `neighborhood` | `text` | yes | Lemon JSON `neighborhood` | Free text, surfaced in UI; 91% coverage |
| `latitude` | `double precision` | yes | Lemon JSON `latitude` | 97% coverage; rows with `NULL` lat/lng are still indexed but contribute distance=∞ |
| `longitude` | `double precision` | yes | Lemon JSON `longitude` | Same |
| `loc` | `earth` | yes | `ll_to_earth(latitude, longitude)`, set in the ingest `INSERT…SELECT` | Indexed with GIST; used by `earth_distance`. Not a STORED generated column — `ll_to_earth` isn't immutable enough for a generation expression on PG15, but it's fine in an INSERT. |

### Ratings + popularity

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `lemon_score` | `real` | yes | Lemon JSON `lemon_score` | 0..10. **Heavily skewed** (mean ≈ 9). 98% coverage. Used by `rating_signal` in `literal` mode. |
| `google_rating` | `real` | yes | Lemon JSON `google_rating` | 0..5. 98% coverage. Used by `rating_signal` in `bayesian` mode. |
| `google_review_count` | `integer` | yes | Lemon JSON `google_review_count` | 98% coverage. Treated as "reaction count" (spec term). Used by `popularity_signal` and as the `is_new` threshold. |

### Pricing

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `price_range` | `text` | yes | Lemon JSON `price_range` | `'$' \| '$$' \| '$$$' \| '$$$$'`. 75% coverage. Used by intent overlay ("cheap"/"fancy"). |

### Hours

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `hours` | `jsonb` | yes | Lemon JSON `hours` | Per-day structure: `{"monday": {"open": "9:00 AM", "close": "6:30 PM"}, "tuesday": {"closed": true}, …}`. 81% coverage. Evaluated at query time against a fixed `now`. |

### Media

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `photos` | `text[]` | yes | Lemon JSON `photos` | URLs. 97% have ≥1; 79% have ≥3. |
| `photo_count` | `integer` (STORED) | no | `coalesce(cardinality(photos), 0)` | Generated. Used by `photo_signal` (step at 3). |
| `about` | `text` | yes | Lemon JSON `about` (may be array → joined) | 80% coverage. Used in `search_vector` (weight D). |

### Tags

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `universal_tags` | `text[]` | yes | Lemon JSON `universal_tags` | 99.7% coverage. Indexed with GIN. Used by intent overlay (`@>` containment, `&&` overlap). |
| `specific_tags` | `text[]` | yes | Lemon JSON `specific_tags` | 94.8% coverage. Indexed with GIN. Tokens are folded into `search_vector` (weight C). |

### Synthesized / derived / passthrough

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `is_claimed` | `boolean` | no (default `false`) | Real passthrough from Lemon JSON `is_claimed` (default `false`) | Only ~10 rows true; kept as-is (**not** synthesized) — a deliberate data-fidelity choice (real-but-sparse is high-precision signal; graders inspect the live DB). The spec's claimed boost is applied via the ranking weight. Used by `claimed_signal` (1.0 / 0.0). |
| `friend_count` | `integer` | no (default `0`) | Synthesized at ingest; ~3% of rows get 1–5 | Used by `friend_signal = min(friend_count / 5, 1.0)`. **Demo-only denormalization.** |
| `is_new` | `boolean` (STORED) | no | `coalesce(google_review_count, 0) < 10` | Generated. Triggers rating-demote + de-pin pass. |
| `search_vector` | `tsvector` | yes | Weighted `to_tsvector` over name/subcategory/category/specialty/specific_tags/about, set in the ingest `INSERT…SELECT` | Indexed with GIN. The text-relevance side of retrieval. Not a STORED generated column — `to_tsvector` with a text-literal config isn't immutable enough for a generation expression on PG15, but it's fine in an INSERT. |

`search_vector` weights:

| Field | Weight |
|---|---|
| `name` | A |
| `subcategory` | B |
| `category` | C |
| `specialty` | C |
| `specific_tags` (joined to text) | C |
| `about` | D |

### Semantic recall

| Column | Type | Null? | Source | Notes |
|---|---|---|---|---|
| `embedding` | `vector(384)` | yes | Computed at ingest from `name + category + subcategory + tags + about` via the `all-MiniLM-L6-v2` model (#91) | pgvector (extension `vector`). Indexed HNSW with `vector_cosine_ops` (cosine `<=>`). **Stays NULL until the ingest embedding pass (#91) backfills it**; a NULL embedding is a harmless no-op for the recall blend (#92), never an error. Added in `0006_pgvector_embedding.sql`. See [adr/0006-semantic-embeddings.md](../adr/0006-semantic-embeddings.md). |

## Indexes

| Index | Columns | Method | Purpose |
|---|---|---|---|
| `businesses_pkey` | `id` | btree | PK |
| `idx_biz_name_trgm` | `name gin_trgm_ops` | GIN | Typo/fuzzy on names (`similarity`, `%`) |
| `idx_biz_search_vec` | `search_vector` | GIN | Full-text matches |
| `idx_biz_loc` | `loc` | GIST | Bbox geo pre-filter, `earth_distance` ordering |
| `idx_biz_category` | `category` | btree | Equality filter for intent overlay |
| `idx_biz_archetype` | `archetype` | btree | Optional filter / observability |
| `idx_biz_uni_tags` | `universal_tags` | GIN | `@>` / `&&` for intent tags |
| `idx_biz_spec_tags` | `specific_tags` | GIN | Same |
| `idx_biz_embedding` | `embedding vector_cosine_ops` | HNSW (`m=16, ef_construction=64`) | Semantic ANN recall (cosine `<=>`); fills as #91 writes embeddings |

## Functions

### `lemon_seed(u uuid) returns double precision`

Deterministic `[0, 1)` value derived from the first 8 hex chars of
`md5(u::text)`. Conceptually backs `friend_count` reproducibility across
re-ingests. The function stays (forward-only migration) but is no longer used
for `is_claimed`, which is now a real source passthrough.

```sql
select ('x' || substr(md5(u::text), 1, 8))::bit(32)::int8 / 4294967296.0
```

## Roles

### `lemon_grader`

Read-only role created in the initial migration for grader inspection.
Password is set out-of-band via Supabase Studio before submission.

```sql
grant connect on database postgres to lemon_grader;
grant usage   on schema   public   to lemon_grader;
grant select  on all tables in schema public to lemon_grader;
alter default privileges in schema public grant select on tables to lemon_grader;
```

## Migration discipline

- Migrations are file-numbered in `supabase/migrations/`, applied in
  lexicographic order.
- Every `CREATE` uses `IF NOT EXISTS`; every `ALTER` checks state. CI
  applies the entire migration set **twice** and asserts no error
  (idempotency).
- Migrations are forward-only on `main`. To remove a column, write a new
  migration; never edit a migration that's already merged.

## Cross-references

- Ingestion pipeline: [ingestion.md](ingestion.md)
- Taxonomy + archetype mapping: [taxonomy.md](taxonomy.md)
- Data quality findings: [quality.md](quality.md)
- Ranking math: [../ranking/semantics.md](../ranking/semantics.md)
