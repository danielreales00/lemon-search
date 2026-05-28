# ADR-0003: Ranking — two-phase retrieval + Go re-ranker + YAML config

- **Status**: Accepted
- **Date**: 2026-05-27
- **Deciders**: Daniel

## Context

The spec defines a 7-signal × archetype-weighted linear sum. The signals
mix concerns: some are cheap to compute in SQL (distance, photo_count,
is_claimed), some need scaled math (rating, popularity), some are
behavior-flagged per archetype (open_status hard / soft / ignore), and
one is a "regardless" hard pin (exact-name).

Architecture options:

| Where ranking lives | Pros | Cons |
|---|---|---|
| Pure `pl/pgsql` function | One round-trip, ~3–5 ms faster | Hard to test; YAML config awkward; archetype hard-filter behavior gets gnarly across mixed archetypes |
| Pure Go, retrieve everything matching | Easiest to test | "Everything matching" can be huge; query is slow |
| **Two-phase: SQL retrieves top-N raw, Go re-ranks** | Testable, fast enough, evolvable | One more layer to maintain |

The deciding facts:

- The Go ranker must be unit-tested against fixture candidates (no DB).
  Otherwise we can't have confidence in the math.
- `config/ranking.yaml` owns archetype weights so tuning is rebuild-free.
  pl/pgsql cannot consume YAML easily; you'd end up with hardcoded
  constants or a `weights` table.
- A single SQL query returns multi-archetype candidates (a "sushi" query
  matches restaurants *and* a sushi-making class). The
  `open_status: hard_filter` behavior differs per archetype; a single
  `WHERE` clause can't express that. Go is the cleanest place for it.

## Decision

**Two-phase retrieval**:

1. **Recall (SQL)**: `ranking.search_candidates(q, lat, lng, now, lim, …overlay)`
   returns ≤ 150 candidates with rich raw signal columns. One round-trip.
2. **Precision (Go)**: pipeline of hard-filter → signal computation →
   linear sum → exact-name pin → tie-break → de-pin pass. Pure functions.

Plus:

- A separate SQL exact-name path returning at most one row; if it fires,
  Go prepends with `score = +∞`.
- `config/ranking.yaml` owns archetype weights, behavior flags, and the
  literal/alternative formula switches. Loaded once at startup; held in
  memory.

Details of each step are in [../ranking/semantics.md](../ranking/semantics.md).

## Consequences

**Good**

- Ranker is unit-testable in isolation against fixture candidate slices.
  ~60 test cases cover the pipeline.
- Tuning weights or flipping formula modes is a YAML edit + restart.
- The seam between SQL and Go is clean: "SQL returns raw signals; Go
  composes them." Easy to evolve either side.
- The 7-signal math is in one language (Go), with strong typing and
  bounded-arithmetic invariants (every signal ∈ [0, 1]).

**Bad / cost**

- One more network round-trip and ~50 KB transfer per query vs. the
  pl/pgsql path. Measured ~2–5 ms; affordable.
- Two SQL queries (broad recall + exact-name path) instead of one. Both
  prepared statements; same connection.

**Revisit when**

- If we ever add per-user features (friends per *current user*), the SQL
  function gains a `user_id` parameter and pulls friend joins inside the
  same call. The contract (raw signals out) does not change.
- If a future signal is purely set-relational (e.g., "appeared in user's
  recent search history"), it joins in SQL too.

## Cross-references

- The 13 ranking deep-dive decisions (D1–D13) are in the plan file at
  `~/.claude/plans/we-have-a-spec-scalable-pony.md`.
- Implementation: `api/internal/rank/` + `supabase/migrations/0002_search_candidates.sql`.
- Why we *don't* substitute "smarter" alternatives silently:
  [0004-spec-contract-discipline.md](0004-spec-contract-discipline.md).
