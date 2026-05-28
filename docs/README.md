# Docs

This directory is the spec for the Lemon Search build. Each file is reference
material; the writeup at the end (`writeup.md`) draws from it.

## Start here

| Doc | What's in it |
|---|---|
| [../CLAUDE.md](../CLAUDE.md) | Always-loaded agent context: stack, boundaries, working model, conventions. Read first. |
| [architecture.md](architecture.md) | The patterns we adopted and why, with the topology diagram. |
| [development.md](development.md) | Local setup, quality stack, hooks, conventions. |
| [operations/workflow.md](operations/workflow.md) | How we chunk work and run parallel agents in worktrees. |
| [glossary.md](glossary.md) | Terms used across the docs. |
| [writeup.md](writeup.md) | Stage 4 deliverable — drafted on Day 4. |

## Roadmap

| Stage | Doc |
|---|---|
| Overview + global testing strategy | [roadmap/00-overview.md](roadmap/00-overview.md) |
| Day 1 — Foundation | [roadmap/01-foundation.md](roadmap/01-foundation.md) |
| Day 2 — Search core | [roadmap/02-search-core.md](roadmap/02-search-core.md) |
| Day 3 — Intent + polish + perf | [roadmap/03-intent-polish.md](roadmap/03-intent-polish.md) |
| Day 4 — Writeup + ship | [roadmap/04-writeup-ship.md](roadmap/04-writeup-ship.md) |
| Cross-stage interface contracts | [roadmap/05-architectural-contracts.md](roadmap/05-architectural-contracts.md) |

## Data

| Doc | Purpose |
|---|---|
| [data/schema.md](data/schema.md) | Column-by-column data dictionary for `businesses`. |
| [data/quality.md](data/quality.md) | Findings from profiling 23,537 records; what's dropped, synthesized, flagged. |
| [data/taxonomy.md](data/taxonomy.md) | Spec category taxonomy + raw→spec normalization + archetype assignment. |
| [data/ingestion.md](data/ingestion.md) | The JSON-to-Postgres pipeline, step by step. |

## Search

| Doc | Purpose |
|---|---|
| [ranking/semantics.md](ranking/semantics.md) | The math of the 7 signals — formulas, normalization, edges. |
| [ranking/intent.md](ranking/intent.md) | Intent extractor lexicon + tokenization + precedence. |
| [api.md](api.md) | HTTP endpoint contract (`/search`, `/healthz`, `/version`). |

## Operations

| Doc | Purpose |
|---|---|
| [operations/workflow.md](operations/workflow.md) | Chunked work, worktrees, the project board, parallel agents. |
| [operations/feature-flags.md](operations/feature-flags.md) | Flag conventions + registry (keep `main` deployable). |
| [operations/deployment.md](operations/deployment.md) | Runbook: Supabase, Fly.io, Vercel — setup, deploy, rollback. |
| [operations/observability.md](operations/observability.md) | Logging, per-stage timing, performance budget. |

## Decisions (ADRs)

Numbered. New decisions get a new file; superseded ADRs stay in place with
their status updated.

| # | Title | Status |
|---|---|---|
| 0001 | [Stack — Go on Fly + Supabase + Next.js on Vercel](adr/0001-stack-choice.md) | Accepted |
| 0002 | [Search engine — Postgres `pg_trgm` + `tsvector`](adr/0002-search-engine.md) | Accepted |
| 0003 | [Ranking — two-phase, in-Go, config-driven](adr/0003-ranking-strategy.md) | Accepted |
| 0004 | [Spec-contract discipline — alternatives via config switch](adr/0004-spec-contract-discipline.md) | Accepted |
| 0005 | [Hexagonal core, light-touch](adr/0005-hex-architecture.md) | Accepted |

See [adr/README.md](adr/README.md) for the ADR format and how to add one.

## Progress notes

Daily build notes (one per stage) land in [`progress/`](progress/). Template:
[`progress/_template.md`](progress/_template.md).

## Conventions across these docs

- Code references use `path/to/file.go:42` form for grep-friendliness.
- "Spec text" in quotes means a verbatim quote from the Lemon trial spec.
- "C1/C2/…" references the interface contracts in
  [roadmap/05-architectural-contracts.md](roadmap/05-architectural-contracts.md).
- "D1/D2/…" references the ranking-design deep-dives in the plan file at
  `~/.claude/plans/we-have-a-spec-scalable-pony.md`.
