# Stage 3 — Intent, polish, perf (Day 3)

## Goal

Land the "smart" semantic intent layer, push p95 below 100ms, and tune
archetype weights against the bench until pass rate ≥ 80%. Implement the
alternative signal formulas behind their config switches so the writeup can
quote a real before/after comparison.

## Where this fits

- **Upstream**: Stage 2 — ranker, retrieval, HTTP shape, bench scaffolding.
- **Downstream**: Stage 4 reads the final config, the final bench results,
  and the loadtest report.

## Architectural commitments locked here

- **C5 `intent.Overlay`** struct is locked. Adding fields OK; renaming not.
- **Alternative signal-formula behavior** is locked: switching
  `signal_formulas.rating` / `signal_formulas.distance` in the YAML must
  produce different bench numbers without any code change.
- **Per-stage timings** semantics is locked: `intent_ms`, `sql_ms`,
  `rerank_ms`, `total_ms`. The sum must be ≤ `total_ms` (small slack for
  bookkeeping); the bench script asserts this invariant.

## Acceptance criteria

- [ ] Intent extractor handles 10–15 head intent families (price, time,
      audience, setting, domain pulls); each entry has a unit test.
- [ ] Overlay flows through retrieval SQL as `WHERE` clauses (tag `&&`,
      category `=`, price `IN`, `is_open_now`).
- [ ] `signal_formulas.rating: bayesian` and `signal_formulas.distance: decay`
      both produce results without rebuild; toggled via config only.
- [ ] Bench script runs in both modes; comparison table written to
      `bench/results-<date>.md`.
- [ ] p95 total latency ≤ 100ms in `scripts/loadtest.sh` against the
      deployed API at 50 RPS for 60s.
- [ ] Edge cases (empty / 1-char / whitespace / punctuation / accents /
      long queries) return sane results without 5xx.
- [ ] Bench pass rate ≥ 80% on the spec-literal default config.
- [ ] Fuzz tests for `intent.Extract` and signal computations pass under
      `go test -run=Fuzz -fuzz=. -fuzztime=30s`.

## Deliverables

| Artifact | Path | Notes |
|---|---|---|
| Intent lexicon | `api/internal/intent/lexicon.go` | Table-driven; one entry per intent term |
| Intent extractor | `api/internal/intent/extract.go` (replaces stub) | Tokenize → normalize → lexicon lookup → `Overlay` |
| Diacritic normalizer | `api/internal/intent/normalize.go` | `café` → `cafe` for matching; original preserved for trigram |
| Overlay wiring | `internal/retrieve/postgres/query.go` (extended) | Injects overlay into SQL params |
| Bayesian rating | `internal/rank/signals.go` (added path) | Behind `signal_formulas.rating: bayesian` |
| Decay distance | `internal/rank/signals.go` (added path) | Behind `signal_formulas.distance: decay`; per-archetype `decay_km` |
| Loadtest script | `scripts/loadtest.sh` | Wraps `hey`; writes markdown report |
| Comparison report | `bench/results-<date>.md` | Markdown; both formula modes; latency table |
| Day-3 note | `docs/progress/day-3.md` | |

## Sub-tasks (ordered)

1. **Lexicon entries** — write the 10–15 high-value families first.
2. **Tokenizer + diacritic normalizer** — lowercase + strip accents for
   matching; original query preserved for trigram.
3. **SQL overlay wiring** — add the nullable params (already declared in C4)
   and convert overlay → SQL args in the adapter.
4. **Alternative formulas** — wire `bayesian` and `decay` paths; same
   `signals.go`.
5. **Comparison bench** — `--formula` flag on the runner; emit a markdown
   table.
6. **Perf pass** — `EXPLAIN ANALYZE` the 10 worst queries; confirm GIN/GIST
   indexes used; trim response columns; gzip on; pool size = NumCPU.
7. **Edge-case audit** — every kind in `bench/queries.json` `edge` cluster
   passes without 5xx.

## Testing design

### Unit tests
| Subject | File | Cases |
|---|---|---|
| Lexicon | `internal/intent/lexicon_test.go` | one assertion per entry: query → expected `Overlay` (table-driven) |
| Normalizer | `internal/intent/normalize_test.go` | `café`→`cafe`, `niño`→`nino`, ascii passthrough, empty, very long |
| Extractor | `internal/intent/extract_test.go` | unigram + bigram match; no-match → empty overlay; precedence when two terms match (intent stays additive, not mutually exclusive) |
| Bayesian rating | `internal/rank/signals_test.go` (added) | n=0 → m/5; n=1 high r → close to m/5; n=10000 → close to r/5 |
| Decay distance | `internal/rank/signals_test.go` (added) | d=0 → 1.0; d=decay_km → ~0.37; per-archetype decay_km respected |
| Edge cases | `internal/api/search_edge_test.go` | empty / whitespace / 1-char / punctuation / very long → 200, `results=[]` or short list, no panics |

### Fuzz tests
- `internal/intent/extract_fuzz_test.go`: random bytes → never panic.
- `internal/rank/signals_fuzz_test.go`: random float inputs → all signals in
  `[0, 1]`; ranker output stable under random permutations.

### Integration tests
- Overlay flows through SQL: seed 50 candidates including a `wedding`
  photographer; `Search("wedding photographer")` returns only Events/photog
  candidates (intent filter applied).
- Alternative formula switch: with the same seed, `bayesian` and `literal`
  produce demonstrably different orderings; both stable across runs.

### Contract tests
- HTTP response shape unchanged from Stage 2 (same JSON Schema check); only
  values may differ.

### Bench
- Run runner in both modes; compare pass rates + median rank of the first
  expected hit.
- Pass rate ≥ 80% in default (literal) mode.

### Loadtest
- `scripts/loadtest.sh` runs `hey -z 60s -c 50 -q 10` against `/search` with
  a query pool (one keystroke per RPS). Captures p50/p95/p99 total and
  per-stage timings.
- Pass: p95_total ≤ 100ms, p99_total ≤ 150ms, zero non-2xx.

## Interface to next stage (Stage 4 reads this)

- `bench/results-<date>.md` is the source for the writeup comparison table.
- `scripts/loadtest.sh` is the source for the writeup latency table.
- Config switches are documented (toggle either formula; nothing else
  changes).
- All TODOs in the codebase resolved or moved to `docs/writeup.md` under
  "what I'd do next."

## Risks + mitigations

- **Lexicon over-/under-coverage.** **Mitigation**: keep entries conservative
  (no-overlay when uncertain); bench catches both directions.
- **p95 budget.** Three hops + DB call. **Mitigation**: prepared statements,
  minimize columns, gzip, pool tuned to CPU count, `EXPLAIN ANALYZE` worst
  queries; revisit GIN selectivity if needed.
- **Accents + `pg_trgm`.** Trigram is ASCII-friendly only.
  **Mitigation**: build `tsvector` and `name` trigrams over `unaccent(name)`
  in a new migration if measured weak.
- **Fuzz finding a real panic.** Treat as ship-blocker; fix before merge.

## Out of scope

- Embedding / vector search (`pgvector`). V2 writeup proposal.
- MMR / diversity. V2.
- Click-through learning. V2.
- A second adapter (Meilisearch). The port supports it; we don't need it.
