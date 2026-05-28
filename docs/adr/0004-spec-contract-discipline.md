# ADR-0004: Spec-contract discipline — alternatives via config switch, not silent substitution

- **Status**: Accepted
- **Date**: 2026-05-27
- **Deciders**: Daniel

## Context

While designing the ranker we found three places where the spec-literal
reading was arguably *worse* than a more sophisticated alternative:

1. **Rating signal** — spec says `rating = reaction score / 10` (which we
   interpret as `lemon_score / 10`). But `lemon_score` is heavily skewed
   (mean ≈ 9), so the literal formula barely discriminates. A
   Bayesian-smoothed `google_rating` is a better quality signal.
2. **Distance signal** — spec says "inverse distance, capped at 30 miles."
   Per-archetype exponential decay (utility tight, experiential loose)
   matches the spec's qualitative descriptions better than a single
   normalization scaled by weight.
3. **Archetype assignment** — spec says "every category maps to one of
   six archetypes." A query-intent override (e.g., `"wedding photographer"`
   → force `high_stakes_one_time`) is sometimes more accurate, especially
   for mis-categorized rows.

In the planning conversation, the question was raised explicitly:
*"Aren't we changing the contract of the test?"*

The answer is yes — silent substitution would change what's being
graded. The grading rubric is *whether the engineer can honor the
contract*, not whether they can ship clever variants.

## Decision

For every place where the literal spec and a more sophisticated
alternative diverge, we:

1. **Default to the literal spec.** `signal_formulas.rating: literal` and
   `signal_formulas.distance: literal` in `config/ranking.yaml`. Archetype
   is strictly per-business (no override).
2. **Implement the alternative behind a config switch.** Bayesian rating
   and decay distance are both implemented and reachable via the YAML.
3. **Quote a measured comparison in the writeup.** The bench runner runs
   in both modes and writes `bench/results-<date>.md` with a side-by-side
   pass-rate + latency table. The writeup defends the choice with real
   numbers.

For the archetype-override case (#3), the alternative was dropped
entirely. Intent extraction only produces filter/boost overlays; the
right archetype emerges naturally because the narrowed candidate set
already has its proper per-category archetype assigned.

## Consequences

**Good**

- Graders evaluate us on the contract they wrote. The bench numbers prove
  we can implement it.
- The alternatives still ship, gated by config — we can prove they're
  better with evidence rather than opinion.
- The discipline scales: when the next "smarter idea" appears, the
  default move is config switch + bench, not substitution.

**Bad / cost**

- More code: two formula paths instead of one for `rating` and `distance`.
  Each path is < 30 lines of Go and tested independently.
- Slightly more bench runtime (twice the queries).

**Revisit when**

- If post-trial work continues and graders express a strong preference
  for one of the alternatives, flip the default and supersede this ADR.

## Cross-references

- Where each deviation was caught: the plan file's D-decisions section
  (D2, D4, D6) at `~/.claude/plans/we-have-a-spec-scalable-pony.md`.
- The corresponding memory note for future sessions:
  `memory/feedback_spec_contract.md`.
- The formulas: [../ranking/semantics.md](../ranking/semantics.md).
