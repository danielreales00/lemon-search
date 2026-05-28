# Glossary

Terms used across the docs. Alphabetical.

## Terms

### Archetype
One of six labels assigned to every business at ingest time, based on its
spec category. Determines the per-signal weights used when scoring. Values:
`low_stakes_fast_nearby`, `medium_stakes_occasion`, `high_stakes_one_time`,
`experiential`, `recurring_service`, `utility_distance_dominant`. See
[data/taxonomy.md](data/taxonomy.md) for the category → archetype mapping.

### Bayesian rating
The IMDb-style smoothed rating: `(C·m + n·r) / (C + n)`, where `r` is
`google_rating`, `n` is `google_review_count`, `m` is the global mean rating
and `C` is a prior weight. Available behind the
`signal_formulas.rating: bayesian` config switch; spec-literal
(`lemon_score / 10`) is the default. See [ranking/semantics.md](ranking/semantics.md).

### Boundaries
ESLint plugin (`eslint-plugin-boundaries`) that enforces import rules between
`app`, `component`, and `lib` element types in `web/`. The TypeScript-side
analogue of `go-arch-lint`.

### Candidate
A business returned from the retrieval phase with all the raw signal columns
attached. The ranker consumes a slice of `Candidate` and produces a slice of
`Result`. Defined in `api/internal/domain/types.go` (C2 contract).

### Cognitive complexity
Sonar's metric for how hard a function is to read (nested control flow
counts more than flat sequential logic). Capped at 15 by `gocognit`
(Go) and `sonarjs/cognitive-complexity` (TS).

### Conventional Commits
The commit-message format `type(scope): subject`. Enforced by `commitlint`.
Allowed types and scopes are listed in `commitlint.config.mjs`.

### Cyclomatic complexity
Number of linearly independent paths through a function. Capped at 12 by
`gocyclo` (Go) and the `complexity` ESLint rule (TS).

### Decay distance
Per-archetype exponential decay of the distance signal:
`exp(-distance_km / decay_km[archetype])`. Available behind
`signal_formulas.distance: decay`; spec-literal `max(1 - d/30mi, 0)` is the
default. See [ranking/semantics.md](ranking/semantics.md).

### De-pin pass
After top-K sorting, if a new business (`is_new = true`) lands at position 1
or 2 and a non-new candidate's score is within `swap_window` (~0.05), they
swap. Implements the spec's "don't surface at the very top" without
hard-banning new businesses.

### Depguard
Built-in golangci-lint module that blocks specific imports. We use it as
the cheap per-commit baseline for architectural drift; richer rules live in
`api/.go-arch-lint.yml`.

### Drift (architectural)
A change that violates the import boundaries declared in `.go-arch-lint.yml`
or the `boundaries` ESLint rules — e.g., `domain` importing `pgx`. Always
fails the hook + CI.

### earthdistance
Postgres contrib module that provides the `earth` type and the
`earth_distance(loc1, loc2)` function. We use it to compute distance from
the fixed user location in the SQL retrieval phase.

### Exact-name hard pin
When a query closely matches a business name (similarity ≥ 0.85 or `name
ILIKE q || '%'`), the ranker prepends that business at position #1 with
`score = +∞`. Spec text: "regardless of other ranking signals." See
ranking decision D5.

### Filter (hard / soft / ignore)
Per-archetype behavior flag for the open-status signal:
- **hard_filter**: closed businesses are removed pre-scoring.
- **soft**: closed contributes 0 to the open-status signal but still scores.
- **ignore**: open-status weight is zero; the signal doesn't participate.

### Forbidigo
golangci-lint module that forbids identifier patterns. We use it to ban
`fmt.Print*` (forces structured logging via `observ`) and bare `panic`
(force-return-error idiom).

### Gitleaks
Secrets scanner. Runs on staged diff (pre-commit), local push (pre-push), and
full history (CI). Config: `.gitleaks.toml` with default rules + project
rules for Supabase JWTs and Fly tokens.

### Go-arch-lint
Project [fe3dback/go-arch-lint](https://github.com/fe3dback/go-arch-lint).
Declares components and their `mayDependOn` rules. The primary architectural-
drift enforcer for the Go service. Config: `api/.go-arch-lint.yml`.

### Hexagonal architecture
Same as Ports & Adapters. The pattern: pure domain at the center, ports
(interfaces) at its boundary, adapters implementing those ports for I/O.
Lets us swap `retrieve/postgres` for `retrieve/meilisearch` without
touching the ranker. See [adr/0005-hex-architecture.md](adr/0005-hex-architecture.md).

### Intent overlay
The output of `intent.Extract(query)`. A typed struct (see C5 contract) of
optional filters (category, subcategory, tags, price, `is_open_now`) that
the retrieval layer ANDs into its `WHERE` clause. The overlay does **not**
override archetype assignment.

### Knip
Node.js tool that finds unused files, exports, dependencies, devDependencies,
binaries, and types. Runs on pre-push and CI. Config: `web/knip.json`.

### Lefthook
Single-binary git hook runner. Replaces husky/pre-commit. Config:
`lefthook.yml`. Layers: `commit-msg`, `pre-commit`, `pre-push`.

### lemon_seed(id)
Postgres function returning a stable `[0, 1)` value from a UUID. Used to make
`friend_count` synthesis deterministic across re-ingests. No longer used for
`is_claimed`, which is kept as real source data (not synthesized).

### Madge
Node.js tool that finds circular dependencies. Runs on pre-commit and CI.

### New business
A business with `google_review_count < 10` (the `is_new` generated column).
The ranker applies a rating-demote (`rating_signal *= 0.85` by default) and
the de-pin pass (above) prevents it from surfacing at #1 or #2 unless its
score dominates.

### Overlay
See **Intent overlay**.

### Port (Ports & Adapters)
An interface declared in `domain` (e.g., `BusinessRepo`) that adapters
implement (e.g., `retrieve/postgres.Repo`). Use cases depend on the port,
never on the adapter. See **Hexagonal architecture**.

### Reaction count / reaction score (spec)
The spec uses these terms. Our data has no separate "reaction" columns; we
treat `google_review_count` as the reaction count (98% coverage) and
`lemon_score` (0–10) as the reaction score. Documented in
[data/quality.md](data/quality.md).

### Repo (the port)
`domain.BusinessRepo` (C1 contract). `Search(...)` and `ExactName(...)`. The
only path the API takes to talk to data.

### Search vector
Postgres `tsvector` generated from the business's `name` (weight A),
`subcategory` (B), `category` and `specialty` and `specific_tags` (C), and
`about` (D), built once at ingest as a `STORED` generated column. Indexed
with GIN. The text-relevance side of retrieval.

### Sonarjs
ESLint plugin that adds cognitive-complexity, no-duplicate-string,
no-identical-functions, single-return, and other code-quality rules.

### Spec contract
The 7-signals × 6-archetype-weights × linear-sum framework from the trial
spec. We honor it literally; deviations live behind config switches.
See [adr/0004-spec-contract-discipline.md](adr/0004-spec-contract-discipline.md).

### Specific tags
Fine-grained tags on each business (`sushi`, `gel-manicures`,
`personal-training`, `comfort-food`, `fine-dining`, `cocktails`). 94.8%
coverage. Indexed with GIN; used for intent overlays.

### Stored generated column
A Postgres column whose value is computed from other columns at write time
and persisted. We use them for `loc`, `photo_count`, `is_new`, and
`search_vector` so the query path stays thin.

### Tagliatelle
golangci-lint module that enforces struct-tag naming. We use it to force
`snake_case` for JSON and YAML keys (matches the spec's external
conventions).

### Trigram (pg_trgm)
3-character substring index in Postgres. Powers typo tolerance:
`similarity(name, q) > threshold` and `name % q`. Indexed with GIN.

### Two-phase retrieval
The standard ranked-search pattern: a broad **recall** phase (SQL with
text + geo + filters), then a precise **re-rank** phase (Go with the 7
signals × archetype weights). See [adr/0003-ranking-strategy.md](adr/0003-ranking-strategy.md).

### Universal tags
Coarse tags on each business (`casual`, `upscale`, `kid-friendly`,
`date-night`, `instagrammable`, `outdoor-seating`, `latino-owned`).
99.7% coverage. The primary lever for "smart semantic" queries.
