# Architecture

Compact reference. The full decision log is the plan file at
`~/.claude/plans/we-have-a-spec-scalable-pony.md`. This doc is the
quick-orient version that lives in the repo.

## Topology

```
┌──────────────────────────────────────┐
│ Next.js FE (Vercel)                  │
│  SearchBar (debounced ~50ms,         │
│   AbortController) → /api/search     │
└──────────────────────────────────────┘
                │ REST (JSON, gzip)
                ▼
┌──────────────────────────────────────┐
│ Go API (Fly.io, iad)                 │
│   cmd/api ────────────────────────┐  │
│   │  internal/api      (HTTP)     │  │
│   │  internal/intent   (overlay)  │  │
│   │  internal/domain   (port)     │  │
│   │  internal/retrieve/postgres   │  │
│   │  internal/rank     (7 sigs)   │  │
│   │  internal/config   (YAML)     │  │
│   │  internal/observ   (timings)  │  │
│   cmd/ingest ─────────────────────┘  │
└──────────────────────────────────────┘
                │ SQL (one round-trip per query)
                ▼
┌──────────────────────────────────────┐
│ Supabase Postgres (us-east-1)        │
│  businesses (GIN trgm, GIN tsvec,    │
│              GIST earth, GIN tags)   │
│  search_candidates() SQL fn          │
└──────────────────────────────────────┘
```

## Patterns

| # | Pattern | Why it earns its place |
|---|---|---|
| 1 | Two-phase retrieval | SQL recall (text + geo + filters), Go precision (7 signals × archetype). Decouples engines from scoring. |
| 2 | Hexagonal core (Ports & Adapters) | `BusinessRepo` interface lets Meilisearch slot in without touching the ranker. Easy unit tests. |
| 3 | Modular monolith | One Go binary, two `main`s (`api`, `ingest`). Microservices buy nothing at this scale. |
| 4 | Index-time precomputation | Generated columns: `loc`, `photo_count`, `is_new`, `search_vector`. Query path stays thin. |
| 5 | Ranking in Go, not pl/pgsql | Testability + YAML config beat ~3ms perf savings. |
| 6 | Stateless REST | Browser → Vercel → Fly → Supabase. No sessions, no cookies. AbortController handles "live". |
| 7 | Config-driven ranking | `config/ranking.yaml` owns archetype weights + behavior flags + formula switches. Tune w/o rebuild. |
| 8 | Intent extraction upstream | Lexicon-driven; produces filter/boost overlay; doesn't override archetype. |

## Data flow

```
"joes barbr near me open now"
  → intent.Extract            (lexicon, <1ms)
  → SQL retrieval (1 RTT)     (exact-name try + broad recall + raw signals)
  → Go re-rank                (hard filter → sigs → linear sum → demote → pin → tie → de-pin)
  → top 15 JSON               (with per-stage timings)
```

## Spec-faithfulness

Default `config/ranking.yaml`:

- `signal_formulas.rating: literal` → `lemon_score / 10` (per spec)
- `signal_formulas.distance: literal` → `max(1 - d/30mi, 0)` (per spec)
- Archetype strictly per-business (per spec)
- Intent: filter/boost overlay only, never overrides archetype

Behind switches:

- `signal_formulas.rating: bayesian` → IMDb-style smoothing of `google_rating`
- `signal_formulas.distance: decay` → per-archetype `exp(-d/k)`

The bench runner exercises both modes and produces a comparison table in the
writeup. We do not silently deviate.

## Spec ambiguities (resolved)

| Ambiguity | Resolution |
|---|---|
| Where does "reaction count" live? | Data has no such column. Use `google_review_count` (98% coverage). |
| "Inverse distance" formula | `max(1 - d/30mi, 0)` is the cleanest reading; archetype emphasis lives in the weight. |
| "Open status" hard filter, single SQL query | A query returns multi-archetype candidates → hard-filter runs in Go, not SQL. |
| Where do friend reactions live? | Demo-only: denormalized `friend_count` on `businesses`. Real Lemon needs a per-user join. |
| Rubric says 4 archetypes, body lists 6 | Implement 6 (per body). Flag in writeup. |
