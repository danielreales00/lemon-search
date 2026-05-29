# Day 3 — 2026-05-28 progress note

## What I shipped

- **Intent extractor** (`api/internal/intent/`): pure `Extract(q) →
  domain.Overlay`, lexicon-driven (price / time / audience / setting /
  domain-pull / food families), diacritic-stripping tokenizer, unigram + bigram
  match. No LLM, no embeddings. Flag-gated behind `LEMON_FF_INTENT`. Narrows
  retrieval only; never overrides archetype (decision D6).
- **Exact-name over-fire fix — a hybrid** resolving the spec tension noted on
  Day 2 ("a name returns that business first, regardless" vs. a category
  "surfaces" matches): a **coverage** matcher (`lemon_name_match`, token
  coverage + per-word Levenshtein, `api/internal/retrieve/postgres/repo.go`),
  a **cardinality back-off** (always on — no pin when > 5 businesses share the
  matched name), and **categorical suppression** (flag-gated — no pin when the
  whole query is category/cuisine/domain terms, `intent.IsCategorical`).
- **Ranking-formula switches** (`config/ranking.yaml` `signal_formulas`):
  Bayesian rating + per-archetype decay distance, both opt-in, **default
  strictly spec-literal** (default scores unchanged).
- **Edge / fuzz tests** (`api/internal/.../fuzz_test.go`): ~3.5M fuzz execs
  found no panic / 5xx / NaN on degenerate input.
- Captured all of the above in `docs/writeup.md` and
  `docs/ranking/semantics.md`.

## What's in flight

- Threading the extracted `Overlay`'s category/tag/price filters into
  `search_candidates` retrieval. The extractor and the SQL params both exist;
  only the categorical guard is wired today (the filters are logged, not yet
  applied). Next issue.

## What I'm blocked on

- Nothing. Day-4 work (formal loadtest, ranking-mode bench sweep, writeup
  finalization) is unblocked.

## Numbers

- bench pass rate (local): `633/726` (`87%`) on the generated set (300
  businesses, seed 42, full Search + ExactName + rank pipeline).
  - over_fire `25/25` (`100%`, up from 76% — the hybrid fix)
  - typo `254/261` (`97%`, held — no regression)
  - accent `100%`; exact_name `100%`
  - partial `49/134` (`37%`, unchanged — standing weak spot)
- p95 (bench harness, local): `~26ms`. Formal `scripts/loadtest.sh` sweep is
  Day-4 work, not yet run.
- CI status on `main`: green.
- Migrations applied through: `0003_name_match.sql`.

## Decisions made today

- Exact-name pin is a **hybrid**, not a single threshold: coverage matcher +
  cardinality back-off + categorical suppression. Trigram similarity alone
  can't separate a typo'd full name from a category prefix (both ≈ 0.57), so we
  layer orthogonal discriminators instead of tuning one number.
- Cardinality back-off is **always on** (a property of the data); categorical
  suppression rides `LEMON_FF_INTENT` (it depends on the intent layer).
- Held the spec contract on ranking formulas: Bayesian rating + decay distance
  are config switches, default off; the bench will quote the comparison.

## Tomorrow's first move

- Run the ranking-mode bench sweep (literal vs. bayesian × literal vs. decay)
  and the formal loadtest, then fill the Day-4 tables in the writeup.

## Stage-3 acceptance criteria touched

- [x] Lexicon-driven intent extractor producing `domain.Overlay`, flag-gated.
- [x] Exact-name over-fire resolved (over_fire 76% → 100%, typo held at 97%).
- [x] Alternative ranking formulas behind config switches, default spec-literal.
- [x] Edge-case + fuzz coverage for degenerate input (no panic/5xx/NaN).
- [ ] Overlay filters threaded into `search_candidates` retrieval.
- [ ] Partial-name recall/ranking improvement (37% — deferred).
