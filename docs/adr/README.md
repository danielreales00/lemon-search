# Architecture Decision Records

ADRs capture the *why* behind major decisions — the trade-offs at the time
the call was made. They're append-only: once an ADR is `Accepted` and on
`main`, edits are limited to the **Status** field (e.g., switching to
`Superseded by ADR-NNNN`). A new decision gets a new ADR.

## Format

Michael Nygard style — tight on purpose.

```markdown
# ADR-NNNN: <title>

- **Status**: Accepted | Proposed | Superseded by ADR-MMMM | Deprecated
- **Date**: YYYY-MM-DD
- **Deciders**: <names>

## Context
What's the situation and the trade-off we're navigating?

## Decision
What did we choose, in one or two sentences?

## Consequences
- Good ones
- Bad ones
- Things we'll have to revisit
```

## Index

| # | Title | Status |
|---|---|---|
| 0001 | [Stack — Go on Fly + Supabase + Next.js on Vercel](0001-stack-choice.md) | Accepted |
| 0002 | [Search engine — Postgres `pg_trgm` + `tsvector`](0002-search-engine.md) | Accepted |
| 0003 | [Ranking — two-phase, in-Go, config-driven](0003-ranking-strategy.md) | Accepted |
| 0004 | [Spec-contract discipline — alternatives via config switch](0004-spec-contract-discipline.md) | Accepted |
| 0005 | [Hexagonal core, light-touch](0005-hex-architecture.md) | Accepted |

## Adding a new ADR

1. Pick the next sequential number.
2. Copy an existing ADR as a template.
3. Set **Status** to `Proposed` while you draft.
4. After review, flip to `Accepted` and link it in the index above and in
   [`../README.md`](../README.md).
5. Reference the ADR from any code or doc that depends on the decision.
