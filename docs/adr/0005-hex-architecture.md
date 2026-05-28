# ADR-0005: Hexagonal core (Ports & Adapters), light-touch

- **Status**: Accepted
- **Date**: 2026-05-27
- **Deciders**: Daniel

## Context

The Go service has clear seams:

- **Domain** — types and the `BusinessRepo` port. Pure, no I/O.
- **Use cases** — `intent.Extract`, `rank.Run`, `config.Load`. Pure.
- **Adapters** — `retrieve/postgres` (the only one at V1).
- **Transport** — `internal/api` (HTTP only; no business logic).
- **Composition roots** — `cmd/api`, `cmd/ingest`.

We expect future work to land a `retrieve/meilisearch` adapter (ADR-0002
escape hatch). The ranker is the highest-value, most-graded code; it must
be unit-testable without spinning up Postgres.

Options:

| Approach | Pros | Cons |
|---|---|---|
| **Hexagonal (Ports & Adapters), no framework** | Standard pattern, isolates the ranker, swap-friendly | A bit of boilerplate per package |
| Full Clean Architecture with use-case interfaces | Maximum testability | Over-engineered for ~5 packages |
| No abstractions, direct DB calls everywhere | Less code today | Untestable, swap-hostile |

## Decision

**Hexagonal, but without a DI framework.** Go interfaces are the seam; no
Wire / Uber FX / IoC container. Concretely:

- `internal/domain/repo.go` declares `BusinessRepo` (the port) and its
  input/output types.
- `internal/retrieve/postgres/repo.go` implements `BusinessRepo` (the
  adapter).
- `internal/rank/scorer.go` consumes the port; never imports `pgx`.
- `internal/intent/extract.go` consumes nothing infrastructural; it
  returns a typed `Overlay` value the adapter knows how to translate.
- `cmd/api/main.go` is the composition root: it instantiates pgx, the
  config loader, the adapter, the ranker, and the HTTP server.

Boundaries are enforced by two layers:

1. **`api/.go-arch-lint.yml`** declares the components and explicit
   `mayDependOn` rules. `domain` has `anyVendorDeps: false` — not even
   `pgx`.
2. **`depguard` in `.golangci.yml`** restates the same boundaries as a
   per-commit baseline.

Both must be green or the hook / CI fails.

## Consequences

**Good**

- The ranker is testable against fixture candidate slices with zero
  infrastructure. Stage 2's unit-test target (90% coverage on `rank`) is
  reachable.
- Swapping in Meilisearch is mechanical: write `retrieve/meilisearch`
  implementing `BusinessRepo`; wire it in the composition root. The
  ranker doesn't change.
- The boundaries are *enforced*, not aspirational. Code review burden is
  lower because drift fails at commit time.

**Bad / cost**

- ~10 lines of interface + adapter glue per port. Trivial.
- Tempting to over-port (e.g., a `ConfigRepo` port for YAML loading).
  We resist that — one port per real seam.

**Revisit when**

- If a third I/O concern appears (e.g., Redis cache for hot queries),
  it becomes a port with its own adapter.
- If we adopt OpenTelemetry, instrumentation lives in the adapters; the
  ranker stays pure.

## Cross-references

- The boundary diagram: [../development.md](../development.md#architectural-boundaries-reference)
- Lint configs: `api/.golangci.yml`, `api/.go-arch-lint.yml`
- TS analogue (`eslint-plugin-boundaries`): `web/.eslintrc.json`
- Pattern rationale: [../architecture.md](../architecture.md#pattern-2-hexagonal-core-ports--adapters-light-touch--adopt)
