# Stage 4 — Writeup + ship (Day 4)

## Goal

Ship the writeup and the polish. The work to-date is judged primarily on this
day's artifacts: the writeup, the public URL, the schema-share, and the final
bench numbers.

## Where this fits

- **Upstream**: Stages 1–3 — code feature-complete, bench passing, loadtest
  hitting target.
- **Downstream**: the submission. Nothing in the repo consumes Stage 4
  artifacts.

## Architectural commitments locked here

None new. Stage 4 polishes; it does not introduce new abstractions. If a UI
polish needs a new utility, it goes in `web/lib/` (a leaf).

## Acceptance criteria

- [ ] `docs/writeup.md` complete, covering every section the spec calls out
      (search engine, schema, ranking + archetypes, ambiguities, what's
      next, what's broken).
- [ ] Both formula modes ran on the final dataset; markdown comparison table
      embedded in the writeup.
- [ ] p95 latency report (from `scripts/loadtest.sh`) embedded.
- [ ] UI polish pass: loading state, empty state, error state. Zero console
      errors. Zero layout shift on results render.
- [ ] Final deploys verified live:
  - Next.js FE on Vercel
  - Go API on Fly.io
  - Supabase project read-share email confirmed for the grader
- [ ] Daily commits visible in git history with non-trivial messages
      (one commit per stage day minimum).
- [ ] CI green on the submission SHA.
- [ ] Lighthouse score ≥ 90 on the live URL.

## Deliverables

| Artifact | Path | Notes |
|---|---|---|
| Writeup | `docs/writeup.md` | The thing being graded |
| Final bench | `bench/results-final.md` | Both modes; pass rate + per-stage latency |
| Loadtest report | `bench/loadtest-final.md` | p50/p95/p99 vs RPS |
| Lighthouse report | `bench/lighthouse-final.html` | Saved from a real run |
| Day-4 note | `docs/progress/day-4.md` | Tiny; the writeup is the artifact |
| Submission thread/email | (out of repo) | URL, repo, Supabase invite, writeup link |

## Sub-tasks (ordered)

1. **Bench final** — run both modes on the deployed API; commit
   `bench/results-final.md`.
2. **Loadtest** — `scripts/loadtest.sh` at 50 RPS for 60s; commit
   `bench/loadtest-final.md`.
3. **Writeup draft** — pull facts from `docs/architecture.md`,
   `docs/roadmap/*.md`, bench output. Start at the *beginning* of Day 4.
4. **UI polish** — loading skeleton, empty state, error state, no console
   errors, no CLS.
5. **Lighthouse** — run against the live URL; save the report; iterate on the
   "Best Practices" + "Accessibility" findings.
6. **Final deploys** — tag, deploy, smoke-test in incognito on mobile and
   desktop.
7. **Submission** — compose response: live URL, repo URL, Supabase invite,
   writeup link, brief summary of the calls we made.

## Testing design

### Visual / UX
- Lighthouse ≥ 90 (Performance, Accessibility, Best Practices, SEO) on
  `/?q=coffee` against the live URL.
- Manual: SearchBar focus ring visible, debounce feels right, results
  animate in without layout shift.
- Mobile breakpoints at 360px / 768px / 1280px; no horizontal scroll.

### Smoke
- 5 manual queries on the live URL (typo, prefix, intent, exact name,
  edge). All return ≤ 200ms perceived; zero errors.
- `curl https://lemon-search-api.fly.dev/healthz` → 200 + version.
- Supabase grader-login dry-run: `psql -h ... -U lemon_grader -W` from a
  laptop; `SELECT count(*) FROM businesses;` works.

### Operational
- All CI jobs green on the submission SHA.
- `bench/results-final.md` and `bench/loadtest-final.md` committed and
  linked from the writeup.

### Accessibility (axe-core via Lighthouse)
- No serious or critical violations.
- Search input has a visible label and aria-label.
- Result rows are keyboard navigable; focus order is logical.

## Writeup outline (the artifact being graded)

```
# Lemon Search — 4-day build writeup

## TL;DR
- One paragraph: what shipped, p95, bench pass rate, biggest call.

## Stack
- Why Go on Fly + Supabase + Next on Vercel (the A3a path).
- Same-region pairing keeps the API↔DB hop ≤ 10ms.

## Search engine
- Postgres pg_trgm + tsvector + tag-array GIN + earthdistance.
- Considered: Algolia / Meilisearch / Typesense. Why Postgres won at 23k rows.

## Schema
- Generated columns do index-time work (loc, photo_count, is_new,
  search_vector). Trade-offs.
- The malformed JSON gotcha; the stream parser.

## Ranking + archetypes
- Default = spec-literal everywhere.
- Two alternative formulas (Bayesian rating, decay distance) behind config
  switches. Comparison table here ↓.

## Bench results (comparison)
- Markdown table: spec-literal vs bayesian-rating vs decay-distance.

## p95 latency
- Per-stage breakdown table; load-test results.

## Spec ambiguities + calls
- `reaction_count` ↔ `google_review_count`.
- "Inverse distance" interpretation.
- `lemon_score` skew → kept literal, surfaced Bayesian as a switch.
- Archetype is per-business; intent never overrides it.

## What's broken / known gaps
- Hours coverage 81% — soft-open fallback for the rest.
- ~3% non-Miami records dropped; 1% missing addresses dropped.
- Friend signal denormalized; real Lemon needs a per-user join.
- No diversity (MMR) — coffee chains can clump.

## What I'd do with another week
- pgvector + small embedding model for semantic recall.
- MMR diversification.
- Click-through learning loop.
- Per-user friends/relations table + join.
- A second adapter (Meilisearch) behind the same `BusinessRepo` port.
```

## Interface to next stage

There is no next stage. This is shipping. Anything not in the writeup or the
deployed system doesn't count.

## Risks + mitigations

- **Writeup time.** Easy to under-budget. **Mitigation**: start the draft at
  the *beginning* of Day 4, not the end.
- **Live URL freshness.** Vercel cache can lag. **Mitigation**: verify both
  deploys after the final commit; do a hard-reload smoke test in incognito.
- **Schema-share gotchas.** `lemon_grader` role must exist + have GRANT on
  `public` tables. **Mitigation**: dry-run with a throwaway password from a
  laptop the night before.

## Out of scope

- Anything that wouldn't make it into the writeup or the bench. Code without
  measurement does not help here.
