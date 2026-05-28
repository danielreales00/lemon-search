# Architectural contracts — locked across stages

These are the abstractions every stage relies on. Once a stage commits one of
these, later stages may **extend** it (additive only) but must not break it.
Breaking changes require a versioned migration and a writeup note.

## C1. `domain.BusinessRepo` (locked Stage 1, used Stage 2+)

```go
// File: api/internal/domain/repo.go

type BusinessRepo interface {
    // Search returns up to opts.Limit candidates ordered by raw text score.
    // The returned slice contains the rich raw signals used by the ranker;
    // no scoring or filtering by archetype is performed here.
    Search(ctx context.Context, q string, opts SearchOpts) ([]Candidate, error)

    // ExactName returns at most one candidate whose name matches q at or
    // above the similarity threshold (Stage 2 uses 0.85). Found=false means
    // no pin — not an error.
    ExactName(ctx context.Context, q string) (c Candidate, found bool, err error)
}
```

`SearchOpts` (extensible — adding fields is allowed):

```go
type SearchOpts struct {
    Lat, Lng       float64
    Limit          int
    Now            time.Time              // for is_open_now
    Filters        IntentFilters          // added in Stage 3, zero-value in Stage 2
}
```

**Implemented by**: `internal/retrieve/postgres` (the only adapter at V1).

## C2. `domain.Candidate` (locked Stage 1, extended Stage 3)

```go
// File: api/internal/domain/types.go

type Candidate struct {
    ID                  uuid.UUID
    Name                string
    Category            string
    Subcategory         *string
    Archetype           Archetype
    Neighborhood        *string
    DistanceKM          float64           // from user location; capped at 48.28
    LemonScore          *float64          // 0..10
    GoogleRating        *float64          // 0..5
    GoogleReviewCount   int
    PriceRange          *string           // '$' | '$$' | '$$$' | '$$$$'
    PhotoCount          int
    PhotoURL            *string           // first photo (photos[1]); FE thumbnail; nil if none
    IsClaimed           bool
    FriendCount         int
    IsNew               bool
    IsOpenNow           *bool             // nil if hours unknown; false = closed at opts.Now
    OpensLater          bool              // closed now but reopens before midnight → 0.3 open-status
    Hours               json.RawMessage   // passthrough for FE display
    TextScore           float64           // ts_rank_cd
    NameTrigram         float64           // similarity(name, q)
}
```

The ranker reads these fields and writes a `Score`. Adding fields is fine;
renaming or removing breaks every stage downstream.

## C3. Ranking config schema (locked Stage 1; rebound at Stage 3)

Defined in `config/ranking.yaml`. The Go schema (`internal/config`) is the
source of truth; YAML is parsed into it.

- `signals` is the canonical order of the 7 signals; the ranker iterates this
  list when computing the linear sum.
- `signal_formulas` declares mode for `rating` (literal | bayesian) and
  `distance` (literal | decay).
- `archetypes.*.weights` is keyed by signal name; missing keys default to 0.
- `archetypes.*.open_status` ∈ {hard_filter, soft, ignore}.
- `new_business`, `exact_name` blocks own behavior knobs (no code branches).

Adding a new signal is a breaking change to this contract and requires bumping
a `config_version` field in the YAML (which we then read and validate).

## C4. HTTP response shape (locked Stage 2)

```jsonc
// GET /search?q=...&lat=...&lng=...&now=...
{
  "query":   "joes barbr near me",
  "results": [
    {
      "id":            "uuid",
      "name":          "Joe's Barber Shop",
      "category":      "Beauty",
      "subcategory":   "Barbershop",
      "archetype":     "low_stakes_fast_nearby",
      "neighborhood":  "Brickell",
      "distance_km":   1.2,
      "rating":        4.7,
      "review_count":  812,
      "price_range":   "$$",
      "photo_url":     "https://…",
      "is_claimed":    true,
      "is_new":        false,
      "is_open_now":   true,
      "score":         0.81
    }
    // … up to 15 results
  ],
  "timings": {
    "intent_ms":  0,
    "sql_ms":    18,
    "rerank_ms":  3,
    "total_ms":  27
  }
}
```

JSON keys are `snake_case` (enforced by `tagliatelle`). Adding new fields is
fine; renaming or removing requires a major version bump (we'll re-issue from
`/v2/search`).

## C5. `intent.Overlay` (locked Stage 3)

Output of the intent extractor; input to the SQL retrieval call.

```go
// File: api/internal/intent/types.go

type Overlay struct {
    CategoryFilter      *string     // e.g. "Food & Drinks"
    SubcategoryFilter   []string    // e.g. ["Photography & Video", "Weddings"]
    UniversalTagFilter  []string    // ANDed via array-overlap
    SpecificTagFilter   []string
    PriceFilter         []string    // {"$","$$"}
    RequireOpenNow      bool
    // (does NOT force archetype — see D6)
}
```

The Postgres adapter consumes this and adds equivalent `WHERE` clauses. The
ranker does **not** consume the overlay directly — it only sees candidates
that retrieval already narrowed.

## C6. Migrations

- Migrations are file-numbered SQL in `supabase/migrations/`, applied in
  lexicographic order.
- **Idempotent**: every `CREATE` uses `IF NOT EXISTS`; every `ALTER` checks
  state. CI applies the full set twice and asserts no error.
- **Forward-only**: no down-migrations in V1. If a column needs removal, write
  a new migration that drops it; existing migrations are immutable once on `main`.

## C7. Bench file

`bench/queries.json` is the curated test set. Schema:

```jsonc
{
  "user_location": { "lat": <float>, "lng": <float>, "label": "…" },
  "now_override":  "RFC3339 timestamp",
  "tests": [
    {
      "kind":             "typo" | "prefix" | "category" | "intent" | "exact_name" | "new_biz" | "geo" | "edge",
      "q":                "query string",
      "expected_top_3":   ["name1", "name2", "name3"],          // OR may contain "__FILL__"
      "user_location_override": { "lat": …, "lng": … },          // optional
      "note":             "freeform"                              // optional
    }
  ]
}
```

The bench runner skips pass/fail evaluation for any test whose
`expected_top_3` contains a `__FILL__` placeholder, but still records timings.

## C8. Coding conventions (cross-language)

| Concern | Convention |
|---|---|
| Logging | `slog` via `internal/observ`; structured key=value; one log per request lifecycle event |
| Errors | `fmt.Errorf("…: %w", err)` for wraps; `errors.Is/As` at boundaries; no `panic` outside `cmd/*/main` startup |
| Context | First param of every exported func that may block; never store on a struct |
| Time | `time.Time` everywhere; `now` injectable for tests + bench reproducibility |
| Numeric | All signals in `float64` between 0.0 and 1.0; out-of-range = bug |
| Naming | `snake_case` for SQL, JSON, YAML; `PascalCase` for Go exported, TypeScript types; `camelCase` for Go locals, TS values |
| Files | `kebab-case.ts` for TS; `lower_snake.go` for Go |

These are enforced by the lint configs (Go: `tagliatelle`, `revive`,
`stylecheck`, `importas`. TS: `unicorn/filename-case`, `import/order`).
