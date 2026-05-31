# Lemon Search — 4-day build writeup

> Status: **DRAFT — landed on Day 4**. This stub is committed at
> Stage 1 so the structure is visible from the start. Each section is filled
> in during Stage 4 against real numbers from `bench/results-final.md` and
> `bench/loadtest-final.md`.

## TL;DR

_One paragraph: what shipped, p95 latency, bench pass rate, biggest call._

## Stack

- Backend: Go API + in-process ORT embedder on AWS EC2 (`c7i.xlarge`, `us-east-1`)
- DB: Supabase Postgres (`us-east-1`)
- Frontend: Next.js 15 on Vercel
- Why this combination: see [adr/0001-stack-choice.md](adr/0001-stack-choice.md); the
  API host moved Fly→EC2 (SSH/profiling of the CGo embedder, vCPUs for the embed
  pool) in [adr/0007-api-host-ec2.md](adr/0007-api-host-ec2.md).

## Search engine

- Choice: Postgres `pg_trgm` + weighted `tsvector` + GIN on tag arrays +
  `earthdistance`, with a coverage + per-word-levenshtein exact-name matcher.
- Alternatives considered: Algolia, Meilisearch, Typesense. Why Postgres
  won at 23k rows: see [adr/0002-search-engine.md](adr/0002-search-engine.md).
- **Measured head-to-head, not asserted** (726 generated cases, identical set,
  same ranker + pin logic): our Postgres coverage+levenshtein matcher and a
  *properly-tuned* Meilisearch v1.11 **tie at 86% overall** (vs 76% trigram
  baseline). Different strengths — Postgres edges typo (97% vs 92%); Meili wins
  partials (43% vs 37%) and avoids the over-fire (ranking vs pinning) and is
  faster raw. Meili's *defaults* under-perform (80%, typo 77%) — the comparison
  is only fair after tuning typo tolerance + matching strategy. We chose Postgres
  for single-system simplicity under a 4-day budget — not superiority — and keep
  Meili as a validated escape hatch behind the `BusinessRepo` port. Full table +
  caveats in ADR-0002.

## Schema

- See [data/schema.md](data/schema.md) for the column-by-column reference.
- Generated columns do the index-time precomputation:
  `loc` (earth), `photo_count`, `is_new`, `search_vector`.
- The malformed-JSON ingestion gotcha and how we handle it:
  [data/ingestion.md](data/ingestion.md).

## Ranking + archetypes

- Default = spec-literal everywhere
  (`rating = lemon_score / 10`, `distance = max(1 - d/30mi, 0)`, archetype
  strictly per-business).
- Two alternative formulas (Bayesian rating, decay distance) live behind
  config switches for the comparison below.
- Math reference: [ranking/semantics.md](ranking/semantics.md).
- Architecture: [adr/0003-ranking-strategy.md](adr/0003-ranking-strategy.md).
- Why we held the spec contract instead of silently substituting smarter
  variants: [adr/0004-spec-contract-discipline.md](adr/0004-spec-contract-discipline.md).

## Smart semantic intent

- `intent.Extract(q) → domain.Overlay` — a **pure**, lexicon-driven extractor.
  No LLM, no embeddings; sub-millisecond (the lexicon is a static Go map frozen
  at startup). A diacritic-stripping tokenizer (`café` → `cafe`) feeds
  unigram + bigram lookups against six families: price (`cheap`, `fancy`),
  time (`open now`, `brunch`), audience (`date night`, `kid friendly`), setting
  (`rooftop`, `quiet`), domain pulls (`wedding`, `tow`), and food
  (`sushi`, `tacos`). Matching entries merge additively into the `Overlay`.
- **Flag-gated behind `LEMON_FF_INTENT`** while the lexicon is still partial —
  with the flag off the search path behaves exactly as it did before.
- It **narrows retrieval, never overrides archetype** (decision D6): archetype
  stays a per-business property; intent only filters the candidate set.
- Today the wired consumer is the **categorical guard** (`intent.IsCategorical`)
  that suppresses the exact-name pin for category queries (see below). Threading
  the overlay's category/tag/price filters into `search_candidates` is the next
  step — the recall SQL already accepts those params (passed NULL until wired).
- Reference: [ranking/intent.md](ranking/intent.md).

### Dense recall for open-vocabulary intent (V2, flag-gated)

- The lexicon nails the spec's examples for free (`cheap restaurants` → price;
  `i'm hungry` → category). It **can't scale to open-vocabulary vibe** queries —
  `chill place to work`, `romantic dinner spot` — where every new vibe word would
  need a hand-mapping. That's the one place a sentence-embedding recall channel
  (ADR-0006) is load-bearing: it generalizes over the vibe vocabulary. It stays
  **in retrieval, additive, flag-gated** (`LEMON_FF_SEMANTIC`), never an 8th
  ranking signal (the spec's 7×archetype sum is untouched).
- **One model, both sides — the call we made.** Cosine recall needs business and
  query vectors in the same space, so a single model serves ingest *and* query;
  there is no "big offline model + small online model" option (different models =
  different spaces = noise). We keep `all-MiniLM-L6-v2` (384d) because it is the
  only choice that embeds a query in-process **per keystroke (~5–10ms)** and holds
  the sub-100ms p95 gate — a heavier long-context model (~40–80ms) would not, and
  its only gain (more `about` tail) doesn't move vibe recall, since the vibe
  signal (tags + lead sentences) is front-loaded inside MiniLM's 256-token window.
  Any upgrade is gated on the E5 recall-vs-latency bench.
- **Bug found + fixed.** The first ingest pass capped embed-text at 1000 *chars*,
  but the model limit is 256 *tokens*, and Ollama 400s the *whole batch* if any
  input overflows — so one dense row poisoned its page and only 1,472/22,568 rows
  embedded. Caught by a coverage check; fixed by lowering the cap to 512 runes
  (corpus-verified to clear the ceiling — zero 400s across all rows) and
  re-embedding (#100).
- **Measured — it clears the go/no-go gate (E5).** On 24 hand-labeled NL/vibe
  queries, semantic recall lifts pass@3 from **50% → 88% (+38pp), 0 regressions**,
  for **~15–25ms** of query-embed (ON engine p95 **~40ms**, under the 60ms local
  budget that leaves room for ~30–40ms network toward the 100ms end-to-end gate).
  Vibe queries with no keyword hook — "chill place to work", "somewhere to get
  pampered", "thinking about getting some ink" — flip from miss to hit; lexical
  controls don't regress. Full table: `bench/semantic-results-2026-05-29.md`. The
  three residual misses are ground-truth labeling edges (e.g. "Naked Farmer"
  categorized generically), not recall failures.
- **Production runtime — measured three, chose in-process ORT (E6).** Behind the
  `Embedder` port, `LEMON_EMBED_BACKEND` selects the runtime; we benchmarked all
  three on the same model/harness: **pure-Go GoMLX ~67ms/embed (fails the 100ms
  gate — it's an interpreter), Ollama sidecar ~15ms, in-process ONNX-Runtime
  ~1-2ms** (ON-pipeline p95 **~17ms**), all at cosine-1.0 parity. The hop is
  ~1-2ms, so compute dominates and only native onnxruntime kernels are fast
  enough — at the cost of CGo + two native libs (libonnxruntime + libtokenizers).
  We took that build cost for the ~9× speedup + no sidecar. The pure-Go path was
  tempting (zero deps, static binary) but ~40× slower, so it's rejected as the
  runtime. The ORT build is tag-gated (`-tags ORT`), so default/CI builds compile
  via a stub with no native libs; only the deploy image bundles them
  (`docs/operations/deployment.md`). Required the Go 1.23 → 1.26 toolchain bump
  (hugot floor). Verdict: ships default-on via ORT.

## Bench results

### Search quality by mode (Stage 3, measured)

Generated set: 300 businesses, seed 42, 726 cases, full
Search + ExactName + rank pipeline (local). These are the day-3 numbers after
the over-fire hybrid landed; the Day-4 table below sweeps the ranking-formula
switches against the same harness.

| Mode | Pass rate | Notes |
|---|---|---|
| over_fire | **76% → 100%** (25/25) | after the hybrid pin fix (was 76%) |
| typo | **97%** (254/261) | held — no regression from the pin change |
| accent | **100%** | diacritic-stripping tokenizer |
| exact_name | **100%** | unique-name pins land |
| partial | **37%** (49/134) | **unchanged** — the standing weak spot |
| **overall** | **87%** (633/726) | latency p95 ≈ 26 ms local |

Read: the hybrid closed the over-fire gap (the spec-faithfulness fix below)
without costing typo recall, and partial-name matching is the next lever — it
is a recall/ranking gap the over-fire work deliberately did not touch.

### Ranking-mode sweep

_Filled on Day 4 from `bench/results-final.md`._

| Mode | Pass rate (top-3) | p50 total | p95 total | p99 total |
|---|---|---|---|---|
| `rating: literal`, `distance: literal` (default) | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: bayesian`, `distance: literal` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: literal`, `distance: decay` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |
| `rating: bayesian`, `distance: decay` | _TBD_ | _TBD_ | _TBD_ | _TBD_ |

Commentary: _which mode performed best, why, and what we'd recommend at V2._

## p95 latency

_Filled on Day 4 from `bench/loadtest-final.md`._

| Stage | p50 | p95 | p99 |
|---|---|---|---|
| `intent_ms` | _TBD_ | _TBD_ | _TBD_ |
| `sql_ms` | _TBD_ | _TBD_ | _TBD_ |
| `rerank_ms` | _TBD_ | _TBD_ | _TBD_ |
| `total_ms` (server) | _TBD_ | _TBD_ | _TBD_ |
| End-to-end (with network) | _TBD_ | _TBD_ | _TBD_ |

Methodology: `scripts/loadtest.sh` (`hey -z 60s -c 50 -q 10`) against the
deployed `/search` endpoint with a query pool simulating one-keystroke-per-RPS.

### Short-query latency + the 1-char tradeoff (caught by the live load bench)

Running the open-loop load bench (`cmd/loadbench`, from a same-region EC2 against
the deployed box — see [bench/plan.md](bench/plan.md)) surfaced what single-query
spot-checks hid: on the realistic search-as-you-type mix, **single-query p95 was
~146ms — over the 100ms target** — and it was *entirely* short lexical prefixes.
The stage split (`embed` flat at ~6ms, `sql` carrying all of it) pinned it to the
DB, and per-query timing pinned it to 1–2 char queries: `s`=156ms, `c`=147ms,
`b`=128ms — while everything ≥3 chars (`coffee` 73ms) and all semantic queries
(`chill place to work` 23ms) were comfortably under.

**Root cause.** `pg_trgm` similarity is useless below 3 chars: a 1-char query
matches **0 rows** via `name % q` yet costs ~30ms to scan, and the ranker then
computes `similarity(name, q)` for the ~1,700 rows an `ilike 'c%'` recalls — pure
waste.

**Fix (two parts, both measured).**

- **Migration 0009** gates the trigram recall arm *and* the `similarity()` rank
  term to queries ≥3 chars. Queries ≥3 chars are byte-identical — `bench-runner`
  pass@3 held at **629/726 (87%)** before and after (exact/typo/accent all flat);
  2-char results stayed sensible (popular prefix matches, only mid-word fuzzy
  noise dropped). 2-char queries fell 121→70ms.
- **Frontend min-length-2** — the client doesn't fire until the 2nd keystroke
  (a 1-char query returns ~150 prefix-random names; useless *and* a wide recall).

Together: single-query p95 **146 → ~80ms**, under target — and the throughput knee
moved out too (cheaper queries per core).

**The 1-char tradeoff — eyes open.** min-2 means you can't find a business by
typing a single-letter *name*. We checked the data: the only 1-char "names" are
`.` and `d` — both malformed records (a stray punctuation; a truncated "Time
Savers"), not real searchable names — and the 2-char names (`bp`, `qr`, grocery
codes) still work. So it costs nothing here. And it's a *frontend* gate, not a
backend limit — the SQL still matches a 1-char query by `ilike` prefix — so if the
data ever gained a genuine single-letter business, lowering the threshold (or
special-casing exact 1-char name hits) restores it. We kept min-2: a deliberate,
reversible call backed by the data, not an oversight.

**What it left.** With per-query cost fixed, the remaining p95 climb under load
(≥~20 rps) is purely Supabase Small's **2-vCPU throughput** ceiling — a separate
compute-scaling lever (XL+ adds cores), not a query-cost problem. The bench
attributes it cleanly because `loadbench` records the server's `sql`/`embed` split
per request.

## Spec ambiguities + calls

- **"reaction count"** — no such column in the data. We use
  `google_review_count` (98% coverage). Documented in [data/quality.md](data/quality.md).
- **"inverse distance, capped at 30 miles"** — interpreted as
  `max(1 - d/30mi, 0)`. Per-archetype emphasis lives entirely in the weight,
  not in the curve. Decay-curve alternative behind config switch.
- **`lemon_score` skew (mean ≈ 9)** — kept the literal formula
  (`lemon_score / 10`) as the contract; surfaced Bayesian-google as an
  opt-in switch and quoted the comparison.
- **Archetype assignment** — strictly per-business via category mapping.
  Intent extraction does *not* override archetype; it only narrows
  retrieval. (See ranking decision D6.)
- **Exact-name "boost" vs. category-aware matching — RESOLVED (Stage 3).** The
  spec lists these as *separate* behaviors with deliberate verbs: a name
  "returns that business first, **regardless** of other ranking signals" (a hard
  override) vs. a prefix "**surfaces**" a match (rank, don't override). Our first
  pass pinned on `name ILIKE q || '%'`, which conflated them — `coffee` pinned
  "Coffee To Go", `sushi` pinned "Sushi Joe" over far better results, though both
  are *category* leaves in the taxonomy (Café→Coffee Shop, Casual/Fast→Sushi).
  **The deeper tension:** trigram similarity can't separate a typo'd *full name*
  from a *category prefix* — measured, the spec's own `joes barbr shop → Joe's
  Barber Shop` scores **0.57**, the same band as the false positives
  (`coffee` 0.54, `sushi` 0.60), so *no single threshold separates them*.
  Stage 2 took a conservative high-precision stopgap (pin only on
  `similarity ≥ 0.85`). **Stage 3 resolved it with a hybrid** that stops relying
  on one similarity number and layers three orthogonal conditions:
  (1) a **coverage** matcher (`lemon_name_match`, token coverage + per-word
  Levenshtein, ≥ 0.8) that asks "does the query span most of the *full name*?",
  so a typo'd full name pins but a category word does not;
  (2) a **cardinality back-off** (always on) — no pin when > 5 businesses share
  the matched name, since an ambiguous name like "7-Eleven" is not a unique
  business; and
  (3) **categorical suppression** (flag-gated, `LEMON_FF_INTENT`) — no pin when
  the whole query is category/cuisine/domain terms (`intent.IsCategorical`), so
  `coffee`/`spa` rank instead of pinning, while `joes barbr shop` still pins.
  Measured: `over_fire` **76% → 100%** (25/25) with typo held at **97%** (no
  regression). Detail: [ranking/semantics.md](ranking/semantics.md) §"Exact-name
  pin". Folding the pin into main retrieval (real distance, one fewer
  round-trip) waits on the overlay being threaded through `search_candidates`.
- **Rubric says 4 archetypes, body lists 6** — we implemented 6 per the body.

## What's broken / known gaps

- **Hours coverage 81%** — for the 19% with missing hours, the open-status
  signal defaults to 0.7 ("soft-open") and `hard_filter` archetypes never
  drop them. Honest mitigation; alternative is to drop those businesses
  (more honest data) at the cost of ~4k results.
- **Categories drift** — ~5% of raw values don't map to the spec taxonomy
  ("Tobacco shop", "Trucking company", etc.). They go to an `Other`
  archetype with reduced weights. ~280 rows have empty category and are
  dropped.
- **~3% non-Miami records dropped** at ingestion (including Versailles, FR).
- **Friend signal denormalized** as `friend_count` on `businesses`. Real
  multi-user Lemon needs a per-user join.
- **Partial-name matching is weak — 37%** (49/134), unchanged through Stage 3.
  Partial-name queries (a fragment of a real name, not a typo of the whole
  name) are a recall + ranking gap; the over-fire hybrid deliberately did not
  touch them (it tightened the *pin*, not recall). This is the next quality
  lever — see "another week" below.
- **Bayesian rating divides by 5**, correct for the default `google_rating`
  (0–5) source. The opt-in `source: lemon_score` (0–10) path is **not yet
  scale-corrected** — it would need a ÷10 normalizer. Default config (literal
  `lemon_score / 10`) is unaffected; this only bites if you flip both the
  formula *and* the source.
- **Overlay filters not yet threaded into retrieval** — `intent.Extract`
  produces a full `Overlay`, but only the categorical guard is consumed today;
  the category/tag/price filters are logged, not yet applied to
  `search_candidates`. Next step (the SQL already accepts the params).
- **No diversity (MMR)** — coffee chains can clump near the top of `coffee`.
- **No personalization** — no learning loop, no per-user history.

## What I'd do with another week

- **`pgvector` + sentence-transformer embedding** for semantic recall on
  hard queries (`a place to study quietly with good wifi`).
- **MMR / Maximum Marginal Relevance** to diversify the top-15 across
  chains/owners.
- **Click-through learning loop** — log `(query, result, clicked)` and use
  it to nudge ranker weights nightly.
- **Per-user friends / relations** — proper join table, swap the
  denormalized `friend_count`.
- **Second adapter (Meilisearch)** wired behind the same `BusinessRepo`
  port — the architecture supports the swap without touching the ranker.
- **Better hours data** — re-scrape from Google Places to push 81% → 99%
  and turn open-status into a fully-reliable signal.

## Architecture appendix

- [architecture.md](architecture.md): patterns adopted, patterns rejected,
  topology diagram.
- [roadmap/05-architectural-contracts.md](roadmap/05-architectural-contracts.md):
  cross-stage interface contracts (Repo, Candidate, HTTP shape, Overlay,
  migrations, bench).

## Quality stack appendix

See [development.md](development.md) for the full quality stack —
correctness, complexity, dead code, drift, duplication, cycles, secrets,
conventions. CI mirrors pre-push and adds migration idempotency, web build,
PR commitlint, markdownlint, and gitleaks history scan.
