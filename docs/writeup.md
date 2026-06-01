# Lemon Search: 4-day build writeup

A search and ranking engine over ~22k real Miami businesses. Type a query,
get the right businesses back, ranked well, in under 100 ms at the 95th
percentile.

- **Live app**: <https://lemon-search-web.vercel.app>
- **Live API**: <https://lemonapi.danielreales.lat> (try `/search?q=coffee`)
- **Backend**: Supabase Postgres 17, read-only `lemon_grader` role shared for
  schema + data inspection.
- **Repo**: <https://github.com/danielreales00/lemon-search> (full commit
  history; daily progress notes in `docs/`).

This document is written for two audiences. Each major section opens with a
short **For business readers** plain-language pass, then a **For engineers**
deep dive. Skim the first lines of each section for the story; read on for the
how and why.

---

## TL;DR

**For business readers.** Typing a query returns the 15 best Miami businesses
in well under a tenth of a second. Search is forgiving (typos, accents, and
partial names mostly just work) and a little bit smart (it understands
"cheap restaurants" and "i'm hungry" without an AI bill on every keystroke).
Ranking follows Lemon's spec exactly: seven quality signals (distance, rating,
popularity, friends, claimed, photos, open-now) combined per "archetype" (a
restaurant is ranked differently from a tow truck). Everything tunable lives in
a config file, so non-engineers can re-weight the ranking without a code change.
The whole thing runs on the stack Lemon already uses (Postgres + Next.js), so
there is no second system to keep in sync.

**For engineers.** One Go binary (hexagonal core, ports + adapters) over
Supabase Postgres, plus a Next.js front end on Vercel. Retrieval is two-phase:
a single SQL round-trip recalls ≤150 candidates with rich raw signals
(`pg_trgm` fuzzy + weighted `tsvector` + `earthdistance` geo + tag-array GIN +
pgvector HNSW for semantic recall), then a **pure** Go ranker composes the
7-signal archetype-weighted score. The ranker has no DB dependency and is
fixture-tested. The spec contract is honored literally by default; smarter
variants (Bayesian rating, distance decay) live behind config switches, off by
default, measured in a bench. Measured single-query server p95 is **~80 ms**
(p50 ~50 ms) after a short-query optimization; embedding is ~6 ms and SQL is
the dominant stage. Findability bench (`bench-runner -generate`, 726 cases) is
**87% pass@3**: exact-name 100%, typo 97%, accent 100%, partial 37% (the known
weak spot). Biggest judgment call: holding the spec contract literally and
surfacing every "smarter" idea as an opt-in switch with a measured comparison,
rather than silently substituting it.

---

## System architecture

**For business readers.** Think of three boxes. The **front end** (the search
bar you see, hosted on Vercel) sends what you type to the **engine** (a small
Go service on an Amazon server in Virginia). The engine asks the **database**
(Supabase Postgres, also in Virginia) one question per keystroke: "give me the
businesses that could match, with all the raw facts about each one." The engine
then scores those candidates and returns the top 15. Two design choices make
this fast and trustworthy: the engine and database sit in the same region (so
the conversation between them is a few milliseconds, not a transatlantic call),
and the scoring logic is a small, isolated, heavily-tested piece of code that
never talks to the database directly. That separation is why we can change how
ranking feels (re-weight signals in a config file) without risking the parts
that fetch data.

**For engineers.** The shape is a **hexagonal core (ports + adapters)** with
**two-phase retrieval**. Import rules are enforced by `go-arch-lint` +
`depguard` (Go) and `eslint-plugin-boundaries` (TS), so the boundaries are not
aspirational, they fail CI if violated.

```
  Browser  ──REST(JSON,gzip)──▶  Go API (AWS EC2 c7i.xlarge, us-east-1)
  Next.js                          cmd/api  (composition root)
  (Vercel)                          ├─ internal/api       HTTP only
                                    ├─ internal/intent    lexicon → Overlay (pure)
                                    ├─ internal/domain     types + ports (PURE, no pgx)
                                    ├─ internal/retrieve/postgres   the ONLY DB adapter
                                    ├─ internal/rank      7 signals × archetype (pure)
                                    ├─ internal/config    YAML weights
                                    └─ internal/observ    timings/logging
                                  cmd/ingest (composition root)
                                          │ one SQL round-trip per query
                                          ▼
                          Supabase Postgres 17 (us-east-1)
                            businesses (GIN trgm, GIN tsvector,
                              GIST earth, GIN tags, HNSW vector)
                            search_candidates() SQL function
```

The load-bearing ideas:

- **The seam: SQL returns rich raw signals; Go composes them into the score.**
  Retrieval recalls candidates and hands back every raw fact the ranker needs
  (distance in km, review count, photo count, open-now status, claimed flag,
  friend count). The ranker is a **pure function** of those candidates plus the
  config: same input, same output, no I/O. That is what makes it unit-testable
  against fixtures and safe to tune.
- **One round-trip per query.** Text relevance, geo, tag filters, and semantic
  recall all resolve in a single `search_candidates()` call. No N+1, no fan-out.
- **Stateless API.** All state lives in Postgres. Any API instance serves any
  request; there are no in-process caches that must stay coherent. The browser's
  `AbortController` handles "live as you type," not server sessions.
- **Composition root.** Only `cmd/api` and `cmd/ingest` construct dependencies
  (pgx pool, config, adapters, ranker, embedder). Everything else receives what
  it needs as constructor arguments. No globals, no `init()`, no service
  locators.
- **Index-time over query-time.** Anything computable at ingest is precomputed:
  generated columns (`photo_count`, `is_new`), the `loc` earth point and the
  weighted `search_vector` set in the ingest INSERT, archetype assignment,
  taxonomy normalization, and the 384-dim embedding. The hot path stays thin.
- **Config over code.** Archetype weights, formula choice, and thresholds live
  in `config/ranking.yaml`. Incomplete work hides behind feature flags
  (`LEMON_FF_*`). Neither is hardcoded.

Why an engineer should respect it and a non-engineer should trust it: the
expensive, risky logic (scoring) is isolated and tested; the data path is a
single well-indexed query; and the two halves talk through a narrow, documented
contract, so either can be swapped (a different search engine, a different host)
without touching the other.

Reference: `docs/architecture.md`, ADR-0005 (hexagonal), ADR-0003 (two-phase
retrieval).

---

## Search engine choice and why

**For business readers.** We searched *inside* the database we already use
(Postgres) rather than bolting on a separate search product like Algolia or
Meilisearch. The reason is not dogma: we ran a head-to-head bench, and a
properly-tuned Meilisearch and our Postgres approach **tied on quality (86%
each)** with different strengths. Given a 4-day budget, one system that does
search, geo, filters, and storage beats two systems that must be kept in sync.
We kept Meilisearch as a proven fallback we can switch to without rewriting the
ranking.

**For engineers.** Postgres with four index types: `pg_trgm` GIN on `name`
(fuzzy/typo), GIN on a weighted `tsvector` (full text over
name/sub/category/specialty/tags/about), GIST on `loc` (`earthdistance` geo),
and GIN on the tag arrays (intent overlay via `@>` / `&&`). pgvector HNSW
(`vector_cosine_ops`) adds semantic recall (below). The Go ranker does the
precision pass, so engine-side "advanced ranking" features would be wasted.

**Measured, not asserted.** We ran the shadow A/B *before* committing (726
cases generated from 300 real businesses, identical set across engines, same
ranker and same pin logic, only the retrieval engine differs):

| dimension | Postgres trgm 0.85 | Postgres cov+leven | Meili (default) | Meili (tuned) |
|---|---|---|---|---|
| exact | 100% | 100% | 100% | 100% |
| typo | 69% | **97%** | 77% | 92% |
| accent | 67% | 100% | 83% | 100% |
| over_fire | 88% | 76% | 100% | **92%** |
| partial | 37% | 37% | 39% | **43%** |
| **overall** | 76% | **86%** | 80% | **86%** |
| search p95 (local) | ~105 ms | ~40 ms | ~11 ms | ~8 ms |

Tuned Meilisearch ties the Postgres matcher overall, with a different profile:
Meili wins partials and over-fire (ranking beats hard-pinning on bare category
words) and raw latency; Postgres wins typo (its per-word Levenshtein budget
exceeds Meili's 2-typo cap). Meili's *defaults* under-perform (80%, typo 77%),
so the comparison is only fair after tuning typo tolerance and matching
strategy. **We chose Postgres for single-system simplicity under a 4-day budget,
not superiority**, and keep Meili as a validated escape hatch behind the
`BusinessRepo` port. A real Meili deployment also adds an API→engine network hop
that partially offsets its faster raw search. Full table and caveats:
ADR-0002.

---

## Schema decisions and trade-offs

**For business readers.** We do the heavy lifting once, at load time, not on
every search. Distance math, photo counts, the "new business" flag, the
searchable text, and the meaning-vector for each business are all computed and
stored when we ingest the data, so a live search just reads them. The source
data is messy (it is a 626 MB malformed JSON file), so the loader is careful: it
streams the file, drops records that are not really Miami businesses, normalizes
inconsistent category names, and synthesizes the two signals the spec explicitly
asks us to invent.

**For engineers.** Full column-by-column reference: `docs/data/schema.md`. The
decisions that matter:

- **Generated columns for index-time precompute.** `photo_count`
  (`cardinality(photos)`) and `is_new` (`review_count < 10`) are
  `GENERATED ALWAYS AS … STORED`. `loc` (`ll_to_earth`) and the weighted
  `search_vector` are set in the ingest `INSERT…SELECT` rather than as STORED
  generated columns, because `ll_to_earth` / `to_tsvector` with a text-literal
  config are not immutable enough for a PG generation expression but are fine
  inside an INSERT. The embedding (`vector(384)`) is computed at ingest via the
  same model the query path uses (see semantic section). Net effect: the hot
  query reads stored values; it computes only distance and open-status against
  the request's location and `now`.
- **The malformed-JSON ingestion.** `businesses-2026-05-27.json` is
  pretty-printed objects separated by `}\n{`, not a JSON array. We **stream-parse
  with a depth counter** and never `json.Unmarshal` the whole file. Details:
  `docs/data/ingestion.md`.
- **What we drop (~3k rows).** Non-Miami records (~3.1%, bbox + `, FL` filter,
  including a Versailles, France "experience"), empty-category rows (~1.2%),
  and no-address rows (~1.0%). Result: ~22,000 rows, self-contained in Supabase.
- **What we synthesize, deterministically.** The spec asks us to synthesize two
  signals because Lemon is pre-launch and the data carries no real source for
  them. Both are seeded off the business `id` with domain-separated salts, so
  re-ingesting is idempotent: `friend_count` (~3% of rows get 1-5) and
  `is_claimed` (~20% claimed). See the "Spec ambiguities" section for the
  history of the claimed call.
- **What we keep messy on purpose.** SEO-spam names
  (`"BEST PIZZA MIAMI BEACH OPEN NOW"`) and trailing keyword runs are real
  businesses, so we index them as-is and flag them; we do not fabricate cleaner
  data. Category drift (~5% bleed Google-API categories) normalizes at ingest;
  unmapped values go to an `Other` archetype with reduced weights.

Trade-off we accepted: denormalizing `friend_count` onto `businesses` is a
demo-only shortcut. Real multi-user Lemon needs a per-user `friend_reactions`
join (flagged below).

Reference: `docs/data/schema.md`, `docs/data/quality.md`,
`docs/data/ingestion.md`.

---

## How ranking and archetypes are implemented

**For business readers.** Every result gets a score: seven quality signals,
each scaled to a 0-1 value, multiplied by a weight, and added up. The weights
depend on the business's **archetype**. A tow truck (utility) is ranked almost
entirely on distance and whether it is open; a wedding photographer
(high-stakes, one-time) is ranked on rating, reviews, whether the business is
claimed, and photo quality, with distance barely mattering. There are six
archetypes, all defined in a config file, so the "feel" of ranking is tunable
without touching code.

**For engineers.** The math reference is `docs/ranking/semantics.md`; the Go
implementation in `api/internal/rank/` is the source of truth. The pipeline:

```
candidates (≤150) → [1] hard-filter (drop closed-now where archetype demands)
                  → [2] 7 signals, each ∈ [0,1]
                  → [3] linear sum  score = Σ wᵢ[archetype]·signalᵢ
                  → [4] new-biz rating demote (×0.85)
                  → [5] sort desc
                  → [6] exact-name pin (score=+Inf, prepend)
                  → [7] deterministic tie-break
                  → [8] de-pin (keep new biz out of top-2 unless dominant)
                  → top 15
```

The seven signals, spec-literal by default:

1. **Distance**: `max(1 - d/30mi, 0)`.
2. **Rating**: `lemon_score / 10`.
3. **Popularity**: `log(1+n) / log(1+10000)` (so 800 reviews do not bury 50).
4. **Friends**: `min(friend_count / 5, 1)`.
5. **Claimed**: `1.0` if claimed else `0.0`.
6. **Photos**: `1.0` at ≥3 photos, else `0.25`.
7. **Open status**: open-now `1.0` > opens-later `0.3` > closed `0.0`;
   hours-unknown defaults to `0.7` (soft-open).

Six archetypes (`low_stakes_fast_nearby`, `medium_stakes_occasion`,
`high_stakes_one_time`, `experiential`, `recurring_service`,
`utility_distance_dominant`), each with a weight vector and an `open_status`
behavior (`hard_filter` | `soft` | `ignore`) in `config/ranking.yaml`. Archetype
is a **per-business** property assigned at ingest from the category, never
overridden by the query.

**Spec-contract discipline.** Default config is spec-literal everywhere. Two
alternatives live behind `signal_formulas` switches, default off: **Bayesian
rating** (IMDb-style smoothing of `google_rating`, which counters the skewed
`lemon_score`) and **distance decay** (per-archetype `exp(-d/k)`). The bench
runner exercises both modes; we do not silently substitute a "smarter" formula
for the spec's literal one. Why: ADR-0004.

The hard-filter and exact-name pin run in Go, not SQL, because a single query
returns candidates of mixed archetypes (a "sushi" query matches both a
restaurant and a sushi-making class), and one `WHERE` clause cannot express
archetype-specific filter logic.

Reference: `docs/ranking/semantics.md`, ADR-0003, ADR-0004.

### Smart semantic intent

**For business readers.** "cheap restaurants" should prioritize affordable food;
"i'm hungry" should surface nearby open places to eat. We do this two ways
without an LLM bill per keystroke: a fast dictionary that recognizes intent
words, and (for open-ended "vibe" queries) a lightweight meaning-vector match.

**For engineers.** Two channels, both flag-gated:

- **Lexicon intent (`LEMON_FF_INTENT`)**: `intent.Extract(q) → domain.Overlay`,
  a pure, sub-millisecond extractor. A diacritic-stripping tokenizer feeds
  unigram + bigram lookups across six families (price, time, audience, setting,
  domain, food). Matches merge additively into an `Overlay` that **narrows
  retrieval, never overrides archetype** (decision D6). Today its wired consumer
  is the categorical guard (`intent.IsCategorical`) that suppresses the
  exact-name pin for pure category queries; threading the overlay's
  category/tag/price filters fully into `search_candidates` is partly wired (the
  SQL accepts the params) and is the next step.
- **Dense recall (`LEMON_FF_SEMANTIC`)**: the lexicon nails the spec's examples
  for free but cannot scale to open-vocabulary vibe queries ("chill place to
  work", "somewhere to get pampered"). A sentence-embedding recall channel
  (`all-MiniLM-L6-v2`, 384-dim, cosine HNSW) generalizes over vibe vocabulary.
  It stays **in retrieval, additive, flag-gated**, never an 8th ranking signal
  (the spec's 7×archetype sum is untouched, per ADR-0006). One model serves both
  ingest and query (cosine recall needs both vectors in the same space). On 24
  hand-labeled NL/vibe queries it lifts pass@3 from 50% → 88% (+38pp, 0
  regressions). Query-embed is ~6 ms; the HNSW probe is ~1 ms. The runtime is
  in-process ONNX Runtime (ORT) behind an `Embedder` port: we benchmarked
  pure-Go GoMLX (~67 ms/embed, fails the gate), an Ollama sidecar (~15 ms), and
  in-process ORT (~1-2 ms) at cosine-1.0 parity, and chose ORT (tag-gated build,
  native libs only in the deploy image).

Reference: `docs/ranking/intent.md`, ADR-0006.

---

## Speed: sub-100ms p95

**For business readers.** Every keystroke returns in well under a tenth of a
second. We measured this honestly (not a single lucky query) using a load tool
that mimics real typing, run from a machine in the same data-center region as
the server, so the numbers reflect the system and not someone's home wifi. We
also found and fixed a real slowdown on very short queries.

**For engineers.** Single-query server-side **p95 ≈ 80 ms, p50 ≈ 50 ms**. The
stage split: embed is flat at ~6 ms; **SQL is the dominant stage**; re-rank is a
couple of ms (pure CPU over ≤150 candidates). Methodology: an open-loop load
bench (`api/cmd/loadbench`) against the deployed box from a same-region EC2
instance, with a corpus weighted for search-as-you-type. Open-loop (constant
arrival rate, latency measured from intended send time) surfaces coordinated
omission a closed-loop harness would hide.

**The short-query fix (caught by the live load bench).** Spot-checks hid it, but
the realistic mix showed single-query p95 at ~146 ms, entirely from short
lexical prefixes (`s`=156 ms, `c`=147 ms) while everything ≥3 chars (`coffee`
73 ms) and all semantic queries (`chill place to work` 23 ms) were comfortable.
Root cause: `pg_trgm` similarity is meaningless below 3 chars (a 1-char query
matches 0 rows via `name % q` yet costs ~30 ms to scan, and the ranker then
computes `similarity()` for the ~1,700 rows an `ilike 'c%'` recalls: pure
waste). Fix, two parts, both measured:

- **A length gate (migration 0009)** skips the trigram recall arm *and* the
  `similarity()` rank term for queries under 3 chars. Queries ≥3 chars are
  byte-identical (bench pass@3 held at 629/726 before and after); 2-char queries
  fell 121 → 70 ms.
- **A frontend min-length-2 gate**: the client does not fire until the 2nd
  keystroke. We checked the data first: the only 1-char "names" are `.` and `d`,
  both malformed records, so this costs nothing real, and it is a reversible
  frontend gate (the SQL still matches a 1-char `ilike` prefix if we ever lower
  it).

Together: single-query p95 **146 → ~80 ms**, under target.

**The remaining ceiling is compute, not query cost.** With per-query cost fixed,
the p95 climb under sustained concurrency (≥~20 rps) is purely the Supabase
**2-vCPU tier** throughput wall (Micro/Small/Medium/Large are all 2 vCPU; CPU
scales only at XL+). That is a separate compute-scaling lever, not a per-query
problem. We attribute it cleanly because `loadbench` records the server's
`sql`/`embed` split per request, and the knee sits exactly where the 2-core math
predicts (~80 rps). A live preview against the dev box:

```
rate  25 → wall p95   70ms | sql  67  embed  2          ok
rate  50 → wall p95   71ms | sql  66  embed  2          ok
rate 100 → wall p95  169ms | sql 157  embed  8          knee
rate 200 → wall p95 1837ms | sql 1749 embed 27 | 503s   DB saturates
```

`sql` is the wall at every step; `embed` never breaks a sweat. On a co-scaled
Supabase the knee moves out proportionally to cores, and the *next* wall becomes
the EC2 embed pool (~900 req/s on `c7i.xlarge`, validated near-linearly by the
pool bench in ADR-0007). Honesty over a single big number: the per-query budget
is met; sustained throughput is a known, named, single-lever scaling story.

Reference: `docs/bench/plan.md`, ADR-0007 (capacity model).

---

## Findability and ranking-quality evaluation

**For business readers.** We test search quality with hundreds of generated
queries that have known right answers, and report the pass rate by category
(exact names, typos, accents, partial names). Ranking quality (does the *order*
feel right) is being measured by a second, purpose-built harness.

**For engineers.**

### Findability (search quality by mode)

`bench-runner -generate`, 726 cases from 300 real businesses (seed 42), full
Search + ExactName + rank pipeline:

| Mode | Pass@3 | Notes |
|---|---|---|
| exact_name | **100%** | unique-name pins land |
| typo | **97%** (254/261) | per-word Levenshtein budget |
| accent | **100%** | diacritic-stripping tokenizer |
| over_fire | **100%** (25/25) | after the hybrid pin fix (was 76%) |
| partial | **37%** (49/134) | the standing weak spot |
| **overall** | **87%** (629/726) | |

Read: the over-fire hybrid (below) closed the false-pin gap without costing typo
recall, and partial-name matching is the next lever (it is a recall/ranking gap
the over-fire work deliberately did not touch).

### Ranking-quality evaluation

> **Placeholder, in progress.** A spec-derived metric harness is being built in
> parallel to measure ranking *quality* (as distinct from findability) along
> the dimensions the spec actually cares about: **locality, rating, category
> precision, intent adherence, claimed-vs-base-rate, and diversity.** It will
> also produce the **literal-vs-decay distance** comparison (and
> literal-vs-Bayesian rating) on the same harness, so the config-switch
> recommendation is measured rather than asserted. Numbers will be added here
> when the harness lands; we are not fabricating them in advance.

---

## Spec ambiguities and the calls we made

**For business readers.** A few places in the spec were underspecified or did
not match the data. Each time, we made a documented call, defaulting to the most
literal reading and surfacing the alternative as an opt-in switch rather than
quietly deviating.

**For engineers.**

- **"Reaction count" → `google_review_count`.** The data carries a
  `lemon_reaction_count`, but it is ~92% zero with a max around 27, so as the
  spec's "reaction count" (the signal whose whole point is "800 should not bury
  50") it is a dead signal. `google_review_count` (98% coverage) is the field
  that actually has the spec's magnitude and distribution, so we proxy with it
  and document the call. The pre-launch `status` distribution (all rows
  `scraped`/`discovered`/etc., no `live`) confirms there were never real Lemon
  reactions to count.
- **"Inverse distance, capped at 30 miles" → `max(1 - d/30mi, 0)`.** The
  cleanest reading; per-archetype emphasis lives entirely in the weight, not the
  curve. A decay-curve alternative is behind a config switch. Note the
  consequence: a 30-mile linear cap makes distance a **weak discriminator within
  a single metro** (most of Miami-Dade is well inside 30 miles, so scores bunch
  near 1.0). The decay switch exists precisely to sharpen locality if the
  ranking-quality harness shows it helps.
- **Claimed and friends → synthesize both, deterministically.** The spec lists
  *two* signals to synthesize ("synthesize a small friends-reacted dataset";
  "synthesize a claimed/unclaimed flag"). We initially synthesized only friends
  and kept `is_claimed` as the ~10 real passthrough rows (a data-fidelity
  instinct: do not fabricate what graders inspect live). That left the claimed
  signal boosting almost nothing, so we now synthesize both, deterministically
  off the `id` with separate salts. **The tuning story is itself a judgment
  exhibit:** synthesizing claimed at 35% exercised the claimed weight for the
  first time and exposed it as miscalibrated. For "coffee," top-15 scores
  cluster in a ~0.14 band, and a 0.08 low-stakes claimed weight was 57% of that
  spread, acting as a near-binary sort key: a 14-review claimed coffee cart
  outranked Panther Coffee (2,550 reviews), violating "more reactions = more
  confidence." Two coordinated fixes: low-stakes claimed weight 0.08 → 0.05 (a
  tiebreaker, not an override), with the freed 0.03 moved into popularity
  0.12 → 0.15; and the synth rate 35% → 20% (a meaningful minority, not a third
  of every result set). Higher-stakes archetypes keep their larger claimed
  weight, which is correct per spec (claimed/verified matters more for weddings
  and contractors).
  - **Follow-up: the full claimed-weight sweep (`cmd/bench-runner -quality`).**
    The ranking-quality harness then showed the over-domination was not confined
    to low-stakes: aggregate `claimed_pct` over the 25-query set sat at **66%**
    (literal), ~3x the 20.7% base rate, driven mostly by the *other* archetypes,
    whose claimed weights were still large (medium 0.15, high-stakes 0.25,
    recurring 0.22). Because the synthesized flag is **independent of every other
    signal** (a hash of the `id`), a large claimed weight pulls unrelated claimed
    businesses to the top of every result set — the harness floor with claimed
    weight zeroed everywhere is ~28%, so the claimed weight was the entire lever
    between 28% and 66%. We used the harness as an A/B rig and trimmed claimed
    across the board, moving the freed weight into the spec's quality signals
    (`popularity`, `rating`): low 0.05 → 0.01, medium 0.15 → 0.03, high-stakes
    0.25 → 0.12, recurring 0.22 → 0.10, experiential 0.10 → 0.07 (utility already
    low at 0.08). The spec ordering still holds — claimed matters more for
    high-stakes/recurring than for food — but it is now a tiebreaker, not a sort
    key. Result: aggregate `claimed_pct` **66% → 38%** (literal) / **55% → 34%**
    (decay), with `category_precision` **86% → 87%** and `mean_rating`
    **0.916 → 0.919** (both slightly *up*, i.e. no quality cost). The
    medium/high-stakes probe queries that came back near-fully-claimed now do
    not: gym 100% → ~30%, barber 100% → ~47%, spa 100% → 60%, nail 93% → 60%,
    tattoo 100% → 53%, each at 100% category precision.
- **`lemon_score` skew (mean ≈ 9).** Kept the literal `lemon_score / 10` as the
  contract; surfaced Bayesian-smoothed `google_rating` as an opt-in switch.
- **Exact-name "boost" vs category-aware matching (the over-fire fix).** The
  spec lists these as separate behaviors with deliberate verbs: a name "returns
  that business first, **regardless** of other ranking signals" (a hard
  override) vs a category prefix that "**surfaces**" a match (rank, do not
  override). Our first pass pinned on `name ILIKE q || '%'`, conflating them
  (`coffee` pinned "Coffee To Go", `sushi` pinned "Sushi Joe"). The deeper
  tension: trigram similarity cannot separate a typo'd *full name* from a
  *category prefix* (the spec's own `joes barbr shop → Joe's Barber Shop` scores
  ~0.57, the same band as the false positives), so no single threshold separates
  them. The Stage-3 resolution stops relying on one number and layers three
  orthogonal conditions: (1) a **coverage** matcher (`lemon_name_match`, token
  coverage + per-word Levenshtein ≥ 0.8) asking "does the query span most of the
  *full name*?"; (2) a **cardinality back-off** (always on) suppressing the pin
  when >5 businesses share the name (a chain, not a unique business); and (3)
  **categorical suppression** (flag-gated) dropping the pin when the whole query
  is category/cuisine/domain terms. Measured: `over_fire` 76% → 100% with typo
  held at 97%.
- **Rubric says 4 archetypes, body lists 6.** We implemented 6 per the body, and
  flag the discrepancy here.

Reference: `docs/ranking/semantics.md`, `docs/data/quality.md`, ADR-0004.

---

## What is broken, and what I would fix first

**For business readers.** A few honest gaps. Most are data-quality issues we
chose to flag rather than paper over. The single biggest one to fix first is
partial-name search.

**For engineers.** In priority order:

1. **Partial-name matching is weak (37%).** Fixing first. A query that is a
   *fragment* of a real name (not a typo of the whole name) is a recall +
   ranking gap; the over-fire hybrid tightened the *pin*, not recall. The fix is
   a dedicated prefix/substring recall arm plus a coverage-aware rank term, or
   adopting the Meili adapter (which already wins partials, 43% vs 37%) behind
   the existing port. This is the clearest lever on the headline quality number.
2. **Dead Google photo URLs.** A subset of source photo URLs (Google-hosted) are
   stale and 404 at display time. We count photos but do not validate URL
   liveness. Fix: a liveness sweep at ingest (HEAD check, drop dead URLs before
   computing `photo_count`) or a front-end `onerror` fallback so a dead URL never
   renders as a broken image.
3. **Hours coverage 81%.** For the 19% missing hours, open-status defaults to
   0.7 (soft-open) and `hard_filter` archetypes never drop them. Honest
   mitigation; the alternative (dropping ~4k businesses) is worse. Real fix:
   re-scrape hours from Google Places to push coverage toward 99% and make
   open-status fully reliable.
4. **Throughput is bound by the Supabase 2-vCPU tier.** Per-query latency is
   fine; sustained concurrency walls at the DB's 2 effective cores. Fix is a
   single lever: scale Supabase compute (XL+ adds cores), at which point the next
   wall is the EC2 embed pool. Named and modeled, not mysterious.
5. **Friend signal is denormalized.** `friend_count` on `businesses` is a
   demo-only shortcut. Real multi-user Lemon needs a per-user `friend_reactions`
   join.
6. **Overlay filters only partly threaded into retrieval.** `intent.Extract`
   produces a full `Overlay`, but only the categorical guard is consumed in the
   default path; category/tag/price filters are wired into `search_candidates`
   behind the flag and not yet on by default.
7. **No diversity (MMR).** Coffee chains can clump near the top of `coffee`.
8. **Bayesian-rating scale guard.** The opt-in `source: lemon_score` (0-10) path
   is not yet scale-corrected (it divides by 5, correct for `google_rating` 0-5).
   Default config is unaffected; this only bites if you flip both the formula and
   the source.

Per the spec: an unflagged bug we find ourselves is worse than one you flagged.
These are flagged.

---

## What I would do with another week

**For business readers.** Make partial-name search as good as the rest, diversify
the top results so one chain does not dominate, learn from clicks to keep
improving, and harden the data (photos, hours).

**For engineers**, concretely:

- **Close the partial-name gap.** A prefix/substring recall arm with a
  coverage-aware rank term, benched against the Meili adapter (already a proven
  +6pp on partials) wired behind the `BusinessRepo` port. Target: partial
  37% → 70%+, overall 87% → ~92%.
- **Ranking-quality harness to green.** Finish the spec-derived metric harness
  (locality, rating, category precision, intent adherence, claimed-vs-base-rate,
  diversity), publish the literal-vs-decay-distance and literal-vs-Bayesian-
  rating comparisons, and let the data pick the default config rather than the
  spec-literal default.
- **MMR diversity** over the top-15 to break chain/owner clumping.
- **Click-through learning loop.** Log `(query, result, clicked)` and nightly
  nudge ranker weights or learn a re-rank, feeding the per-user loop the spec's
  appendix anticipates.
- **Per-user friends.** Replace the denormalized `friend_count` with a
  `friend_reactions` join so the friend signal is real and per-user.
- **Data hardening.** Photo-URL liveness sweep, hours re-scrape, and a
  name-cleaner that strips trailing punctuated keyword runs from SEO-spam names
  before indexing.

---

## How to extend this for the rest of the product

**For business readers.** The spec says the full Lemon product eventually powers
four surfaces (the search bar we built, category browse, AI natural-language
search, and a recommended feed) plus per-user learning and logging. We built the
search bar, but we built the *engine* so the other three plug in without a
rewrite. The same scoring brain serves all of them; only the way candidates are
gathered changes.

**For engineers.** The architecture was chosen so the appendix surfaces are
additions, not rewrites. The extension points:

- **Category browse** is the existing retrieval with the **intent `Overlay`**
  set explicitly (a category filter) and the text-relevance term dropped. The
  `Overlay` type and the `search_candidates` filter params already exist; browse
  is "empty query + category filter + same ranker." No new ranking code.
- **AI natural-language search** plugs into the **dense-recall channel** already
  behind `LEMON_FF_SEMANTIC`. A heavier NL layer (LLM query-rewrite or
  function-calling that emits an `Overlay`) feeds the *same* retrieval and the
  *same* pure ranker. The 7×archetype sum stays the scoring contract; the LLM
  only shapes recall and the overlay, never an 8th signal.
- **Recommended feed** is retrieval with a *different candidate source* (a user's
  neighborhood, history, or friends) flowing into the **same `BusinessRepo` port
  and the same ranker**. Because the ranker is a pure function of candidates +
  config, a feed is "different recall, identical scoring."
- **Per-user learning loop** rides on the **stateless, config-driven** ranker:
  weights live in `config/ranking.yaml`, so a learning loop becomes
  per-user/per-segment weight overlays without touching ranking code. Logging is
  already structured per request (`slog` via `internal/observ`, with the
  per-stage timings the API returns), so the `(query, result, clicked)` event
  stream the loop needs is a small addition, not new infrastructure.

The common thread: the **hexagonal seam** (SQL recalls raw signals, Go composes
them) means every future surface reuses the scoring core and swaps only the
candidate-gathering adapter. That is the whole reason we held the ports + the
spec contract under a 4-day clock.

---

## Future roadmap (forward-looking)

| Horizon | Work | Why |
|---|---|---|
| Next | Partial-name recall arm; ranking-quality harness to green | Biggest quality lever; turns "feels right" into measured |
| Near | MMR diversity; photo-liveness + hours re-scrape; overlay filters on by default | Visible polish + data trust |
| Mid | Category browse + recommended-feed surfaces on the same core | Spec appendix; near-free given the seam |
| Mid | Click-through logging → nightly weight nudges | Foundation for the learning loop |
| Later | Per-user friend join; per-user/segment weight overlays | Real multi-user Lemon |
| Later | Meili adapter or co-scaled Supabase as load + multilingual/semantic demand grows | Validated escape hatches, already designed for |

---

## Appendices

- **Architecture**: `docs/architecture.md` (patterns adopted/rejected,
  topology).
- **Cross-stage contracts (C1-C8)**: `docs/roadmap/05-architectural-contracts.md`
  (Repo, Candidate, HTTP shape, Overlay, migrations, bench).
- **Decisions (ADRs)**: `docs/adr/` (0001 stack, 0002 search engine, 0003
  ranking, 0004 spec-contract discipline, 0005 hexagonal, 0006 semantic
  embeddings, 0007 EC2 host).
- **Quality stack**: `docs/development.md` (correctness, complexity, dead code,
  drift, duplication, cycles, secrets, conventions). CI mirrors pre-push and
  adds migration idempotency, web build, commitlint, markdownlint, and gitleaks.
