# ADR-0001: Stack — Go API on Fly.io + Supabase Postgres + Next.js on Vercel

- **Status**: Accepted — API host superseded by [ADR-0007](0007-api-host-ec2.md) (AWS EC2); Supabase + Vercel + Go stand.
- **Date**: 2026-05-27
- **Deciders**: Daniel

## Context

The spec asks for a 4-day build with the stated stack *"Next.js, Supabase
(Postgres, the stack Lemon runs on)"* and the deliverable *"the backend
project (Supabase), with read access shared so we can inspect schema and
data."*

We considered three paths:

| Path | Hops (browser → DB) | Spec match | Notes |
|---|---|---|---|
| A2: Next.js + Supabase (everything on Vercel) | 2 | ✅ exact | Search logic in pl/pgsql + a thin TS API route. Smallest moving parts. |
| A3a: Go API on Fly.io + Supabase + Next.js FE on Vercel | 3 | ✅ Supabase deliverable preserved | Backend in Go (the deciders' strength); same-region Fly↔Supabase. |
| A3b: Go API + self-hosted Postgres on the same Fly VM | 2 | ❌ misses the Supabase deliverable | Lowest theoretical latency, but the deliverable says Supabase. |

At ~23k rows the language doesn't decide whether we hit 100 ms — index
design and hop count do. Go vs. TS for the re-ranking math is a 1–5 ms
difference; trimming a hop saves 10–40 ms.

## Decision

**A3a**: Go API on Fly.io (`iad`) + Supabase Postgres (`us-east-1`) +
Next.js FE on Vercel.

Why A3a, not A2:

- Backend ergonomics. Strong types, single binary, the `cmd/api` /
  `cmd/ingest` split matches the workload shape. The deciders ship faster
  in Go than in TS.
- The Supabase deliverable is preserved (Supabase *is* Postgres; anything
  can connect to it).

Why not A3b:

- The deliverable text is specific: "backend project (Supabase), with
  read access shared." A self-hosted Postgres on a Fly VM misses that
  contract for no real latency win once same-region colocation is in
  place.

## Consequences

**Good**

- Backend is Go — fits the strongest tooling and ergonomics.
- Supabase deliverable shipped as-asked; graders log in to inspect.
- Same-region Fly + Supabase keeps API ↔ DB hop ≤ 10 ms.
- Vercel handles FE static + edge functions with zero ops.

**Bad / cost**

- Three deploy targets instead of two (Vercel + Fly + Supabase). More CI
  surface, more secrets to rotate.
- Three hops from browser to DB instead of two. ~30 ms more under load
  than the all-Vercel path; the budget still fits, but we documented it.

**Revisit when**

- If sub-100 ms p95 proves infeasible after Stage 3 tuning, fold the API
  into Next.js Route Handlers (drops to A2, two hops, loses Go).
- If multi-region demands arise, consider Fly multi-region + Supabase
  read-replicas. Not in scope for V1.
