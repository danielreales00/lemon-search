# Ingestion pipeline

The `api/cmd/ingest` CLI reads `businesses-*.json` and loads it into
Supabase Postgres. This doc is the spec for that pipeline.

## High-level flow

```
businesses-2026-05-27.json   (626 MB, malformed pretty-print)
        │
        ▼
[1] stream-parser ─ yields one record at a time
        │
        ▼
[2] sanitize ─ trim, coerce types, default empties
        │
        ▼
[3] geo filter ─ drop non-Miami records
        │
        ▼
[4] taxonomy normalizer ─ raw → spec category + subcategory
        │
        ▼
[5] archetype assigner ─ category → one of 6 archetypes
        │
        ▼
[6] synth seeder ─ is_claimed, friend_count (deterministic by id)
        │
        ▼
[7] COPY-stream loader ─ pgx.CopyFrom, batched
        │
        ▼
businesses table (≈ 22k rows)
```

Each step is a single-responsibility pure function (except [1] and [7]),
chained via Go channels for backpressure. Lives in `api/internal/ingest/`.

## Stages in detail

### 1. Stream parser

**Input**: file path
**Output**: channel of `map[string]any` (one per record)

The JSON is **malformed**: pretty-printed objects separated by `}\n{`
instead of `},\n{`, with a leading `[` and trailing `]`. `json.Unmarshal`
on the whole file fails at the first object boundary.

Approach: a depth-counted state machine over a buffered reader.

```go
type Parser struct {
    r       *bufio.Reader
    depth   int
    inString bool
    escape  bool
    buf     []byte
}

func (p *Parser) Next() (json.RawMessage, error) {
    // advance until we see '{' at depth 0
    // accumulate until depth returns to 0
    // honoring '"' (toggle inString) and '\\' (escape)
}
```

**Invariants**:
- Emits exactly one balanced JSON object per call.
- `io.EOF` only after the final `}` returns depth to 0.
- Embedded quotes and backslash-escapes do not break the state machine
  (`\\"` is a literal quote, `\\\\` is a literal backslash).

**Tests**: handcrafted fixtures with escaped quotes (`{"name":"Joe\\'s"}`),
nested objects (`{"hours":{"monday":{…}}}`), and truncated input.

### 2. Sanitize

**Input**: raw decoded record
**Output**: `RawBusiness` typed struct

- Trim whitespace from all strings.
- `null` → Go zero value (or `nil` pointer for nullable fields).
- Coerce `lemon_score` and `google_rating` to `*float64` (preserves null).
- `photos`: keep as `[]string`, deduplicate, drop empties.
- `hours`: keep as `json.RawMessage` for passthrough into the JSONB column.

### 3. Geo filter

**Drop** if both:
- `latitude`/`longitude` are null OR outside bbox
  `lat ∈ [25.10, 26.10], lng ∈ [-80.95, -80.05]`
- AND `address` doesn't match the regex `(?i),\s*FL\b|,\s*Florida\b`

Logged drops:
- Versailles, FR (Activities & Experiences)
- 700+ similar non-Miami records (see [quality.md](quality.md))

### 4. Taxonomy normalizer

**Input**: `RawBusiness`
**Output**: `RawBusiness` with `category` and `subcategory` rewritten to
spec values, or a drop signal.

The normalization map is hardcoded in `api/internal/ingest/taxonomy.go` —
see [taxonomy.md](taxonomy.md) for the rules.

Drops:
- Empty category after normalization (~287 rows).

Bucket-to-Other:
- ~150 Google-API leak categories → `category = "Other"`, archetype
  `low_stakes_fast_nearby`. Logged histogram.

### 5. Archetype assigner

**Input**: normalized `category` + `subcategory`
**Output**: one of six archetype enum values.

Lookup table indexed first by `(category, subcategory)` then falling back
to `category` alone. See [taxonomy.md](taxonomy.md) for the full mapping.

### 6. Synth seeder

**Input**: `id` (UUID)
**Output**: `is_claimed` bool, `friend_count` int.

Deterministic — same `id` always produces the same values across reruns.
Two independent uniform values per record, derived from MD5 of the id with
domain-separated salts.

```go
// In Go (mirrors the lemon_seed() SQL function).
func IsClaimed(id uuid.UUID, lemonScore *float64, photoCount int) bool {
    base := seed01(id.String() + ":claimed")
    threshold := 0.35
    if lemonScore != nil && *lemonScore >= 9.0 { threshold += 0.10 }
    if photoCount >= 3 { threshold += 0.10 }
    return base < threshold
}

func FriendCount(id uuid.UUID) int {
    if seed01(id.String()+":friends") >= 0.03 { return 0 }
    return 1 + int(seed01(id.String()+":friend_n") * 5)
}

func seed01(s string) float64 {
    sum := md5.Sum([]byte(s))
    return float64(binary.BigEndian.Uint32(sum[:4])) / 4294967296.0
}
```

**Target distributions** (verified by unit test over 10000 sampled IDs):
- `is_claimed = true`: 35–45% (correlated with quality)
- `friend_count > 0`: 2.7–3.3%
- Among rows with `friend_count > 0`, mean ≈ 3.0

### 7. COPY-stream loader

**Input**: stream of fully-prepared rows
**Output**: rows in `businesses`

Uses `pgx.CopyFrom` in batches of 500.

Pre-COPY:

```sql
CREATE TEMP TABLE stage_businesses (LIKE businesses INCLUDING ALL);
```

COPY into the temp table, then an `INSERT … ON CONFLICT (id) DO UPDATE`
into `businesses`. This makes the operation **idempotent**: re-running the
ingestion on the same input produces the same final state, regardless of
whether the table was empty or pre-populated.

```sql
INSERT INTO businesses (col1, col2, …)
SELECT col1, col2, … FROM stage_businesses
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  category = EXCLUDED.category,
  …  -- but NOT created_at (preserved from first insert)
;
```

Generated columns (`loc`, `photo_count`, `is_new`, `search_vector`) are
recomputed automatically by Postgres.

## Performance characteristics

- Throughput target: ≥ 5,000 records/sec on a Fly.io VM (no CPU pinning
  needed at 23k total rows).
- Memory: bounded by parser buffer + batch size. ~50 MB steady state.
- Time-to-load full file: ~5–10 seconds on `iad` Fly machine + same-region
  Supabase. On a laptop with cross-region Supabase: ~30–60 seconds.

## Failure handling

- **Parser fails on a record**: log a warning with byte offset + raw bytes,
  skip the record, continue. Counter is reported at the end.
- **Sanitize fails on a field**: log + default to zero/null, continue.
- **Taxonomy normalization fails**: bucket to `Other`, log.
- **Geo filter drops**: counted, not logged individually.
- **COPY fails on a batch**: re-tried once, then aborted with full context
  (no partial state — the temp table is rolled back).

End-of-run report (stdout):

```
ingest done in 7.2s
  read:                23537
  dropped (geo):         728
  dropped (no addr):     231
  dropped (cat empty):   287
  bucketed (Other):      157
  loaded:              22134
  is_claimed=true:      7891 (35.7%)
  friend_count > 0:      663 (3.0%)
```

## Re-ingestion

Idempotent by construction:

```bash
go run ./cmd/ingest -input businesses-2026-05-27.json
go run ./cmd/ingest -input businesses-2026-05-27.json   # same final state
```

The `created_at` of pre-existing rows is preserved; everything else is
refreshed. `is_claimed` and `friend_count` stay stable because of the
deterministic seeds.

## Cross-references

- The malformed-JSON gotcha is also captured in the memory store as a
  project-level note (`memory/project_data_file.md`).
- Schema: [schema.md](schema.md)
- Taxonomy: [taxonomy.md](taxonomy.md)
- Quality findings: [quality.md](quality.md)
- Implementation: `api/cmd/ingest/main.go` + `api/internal/ingest/`
