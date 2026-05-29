# Day 2 — 2026-05-28 progress note

> Backfilled from the commit history. Stages 2 and 3 both landed on 2026-05-28
> (a compressed build); this note covers the Stage-2 search core, Day 3 covers
> the Stage-3 intent layer.

## What I shipped

- **Retrieval** (`supabase/migrations/0002_search_candidates.sql` +
  `api/internal/retrieve/postgres`, #73): the `search_candidates` SQL function —
  one round-trip, recall via trigram + weighted `tsvector` + tag arrays + bbox
  geo, returning rich raw signals — and the postgres `BusinessRepo` adapter
  (`Search()` + `ExactName()`). The seam: **SQL returns raw signals, Go composes
  the score.**
- **Ranker** (`api/internal/rank`, #72): pure 7-signal × archetype-weight
  linear-sum scorer + pipeline (hard-filter → sum → exact-name pin → tie-break →
  de-pin), spec-literal formulas, ≥90% fixture coverage. No DB dependency.
- **API** (`api/internal/api`, #74): `GET /search` wiring intent (no-op at
  Stage 2) → retrieval → re-rank, per-stage timings, the C4 DTO; `photo_url` +
  `opens_later` carried on the candidate (#70).
- **Web** (`web/`, #71): debounced search bar (`AbortController`), typed API
  client, results list + chips — a thin, functional UI.
- **Exact-name matcher**: first pass pinned on a bare `ILIKE` prefix, which
  over-fired (`coffee` → "Coffee To Go"); realigned to `similarity ≥ 0.85`
  (#75), then replaced with a **coverage + per-word-levenshtein** matcher
  (`lemon_name_match`, #76). Ran a measured 3-way A/B (Postgres vs default vs
  tuned Meilisearch) — **tie at 86%**; chose Postgres for single-system
  simplicity (ADR-0002).

## What's in flight

- The exact-name **vs category-aware** tension surfaced here (a typo'd full name
  and a category prefix score in the same trigram band, ≈0.57) — full resolution
  (the hybrid pin) is Stage 3.
- Intent extractor (Stage 3).

## What I'm blocked on

- Nothing.

## Numbers

- Search core live end-to-end (`/search` → ranked top-15).
- Matcher bench: typo recall 69% → **97%** with the coverage matcher;
  tuned-Meili A/B tied at 86% overall.
- p95: ~25–40ms local.
- CI status on `main`: green.
- Migrations applied through: `0003_name_match.sql`.

## Decisions made today

- **Retrieval/ranking seam**: SQL does recall + raw-signal extraction; Go does
  normalization + the archetype-weighted sum. Keeps the ranker unit-testable
  against fixtures with no DB.
- **Exact-name pin = `+Inf` override** (the spec's "regardless"); mapped to 1.0
  in JSON.
- **Matcher = coverage + per-word levenshtein**, not raw trigram similarity —
  the only thing that separates a typo'd full name from a category prefix.
  Meilisearch is a *validated* (not assumed) escape hatch.

## Tomorrow's first move

- Build the lexicon intent extractor and resolve the exact-name over-fire with a
  hybrid pin (coverage + cardinality + categorical suppression).

## Stage-2 acceptance criteria touched

- [x] One-round-trip SQL retrieval returning raw signals; postgres adapter.
- [x] Pure 7-signal archetype-weighted ranker (≥90% coverage).
- [x] `/search` endpoint + thin debounced web UI, end-to-end.
- [x] Typo tolerance (coverage + levenshtein matcher) measured and chosen.
- [ ] Exact-name over-fire fully resolved (carried to Stage 3).
