# Ranking semantics - the math

The spec contract: every candidate is scored by 7 signals, each normalized to
[0, 1], multiplied by an archetype weight, and summed. This doc is the
formulas - what each signal is, edge cases, and how the pipeline composes
them. The Go implementation in `api/internal/rank/` is the single source of
truth; this doc is the spec it implements.

## The scoring pipeline

```
candidates (≤ 150 from retrieval)
   │
[1] hard-filter pre-pass        - drop closed-now where archetype demands it
   │
[2] compute per-signal values   - 7 signals, each ∈ [0, 1]
   │
[3] linear sum                  - score = Σ weight_i · signal_i
   │
[4] new-biz rating demote       - rating_signal *= 0.85 when is_new
                                    (applied INSIDE step 2 in practice;
                                     surfaced here for clarity)
   │
[5] sort descending by score
   │
[6] exact-name pin              - prepend the name-match hit if any
   │
[7] tie-break                   - deterministic ordering within ε
   │
[8] de-pin pass                 - keep new biz out of top-2 unless dominant
   │
top 15
```

## Final score formula

```
score(c) = Σ_{i ∈ signals}  w_i[archetype(c)] · signal_i(c)
```

- **signals** = `[distance, rating, popularity, friends, claimed, photos, open_status]`
- **w_i[archetype]** comes from `config/ranking.yaml`. Archetype is `c.Archetype` (the per-business value).
- **signal_i(c)** ∈ [0, 1]. Out-of-range = bug.

`score` is itself **not** normalized; the maximum theoretical value is the
sum of weights for that archetype. For tie-breaking and de-pin distance, we
work in absolute score space, not percentile.

## The 7 signals

### 1. Distance - `signal_distance`

**Spec**: "inverse distance from a fixed user location, capped at 30 miles. Closer is higher."

**Mode `literal` (default)**:

```
signal_distance(c) =
    max(1 - distance_km(c) / 48.28, 0)        # 48.28 km ≈ 30 mi
```

Where `distance_km(c)` is computed by Postgres at retrieval time using
`earth_distance(c.loc, user_loc) / 1000`.

**Mode `decay` (config switch)**:

```
signal_distance(c) =
    exp(- distance_km(c) / decay_km[archetype(c)])
```

Per-archetype `decay_km` from config:

| Archetype | decay_km |
|---|---|
| `utility_distance_dominant` | 3 |
| `low_stakes_fast_nearby` | 8 |
| `medium_stakes_occasion` | 16 |
| `recurring_service` | 16 |
| `experiential` | 48 |
| `high_stakes_one_time` | 80 |

**Edge cases**:
- `distance_km = 0` → both modes return 1.0.
- `distance_km` ≥ 48.28 in `literal` → 0.0.
- `latitude/longitude` null in candidate → retrieval returns `distance_km = ∞`,
  signal is forced to 0.0.

### 2. Rating - `signal_rating`

**Spec**: "reaction score / 10".

**Mode `literal` (default)**:

```
signal_rating(c) = (c.lemon_score / 10) · (0.85 if c.is_new else 1.0)
```

The `0.85` factor implements the spec's "slight rating-signal demote" for
new businesses. The constant lives in `config.new_business.rating_demote_factor`.

**Mode `bayesian` (config switch)**:

```
signal_rating(c) =
    ( (C · m + n · r) / (C + n) ) / 5   · (0.85 if c.is_new else 1.0)
```

Where:
- `r = c.google_rating` (defaults to global mean if null)
- `n = c.google_review_count`
- `m = config.bayesian_rating.global_mean` (default 4.3, over `google_rating`)
- `C = config.bayesian_rating.prior_strength` (default 20)

**Edge cases**:
- `lemon_score` null (literal): signal = 0.
- `google_rating` null (bayesian): treat as `m`; result = m/5 (asymptote).
- `n = 0` (bayesian): signal = m/5 (full prior pull).
- `n → ∞`: signal → r/5.

### 3. Popularity - `signal_popularity`

**Spec**: "reaction count, log-scaled confidence. 800 reactions should not bury 50."

```
signal_popularity(c) =
    log(1 + n) / log(1 + GLOBAL_MAX_REVIEWS)
```

Where `n = c.google_review_count` and `GLOBAL_MAX_REVIEWS = 10000`
(from config; held constant across data updates so behavior is stable).

**Edge cases**:
- `n` null or 0: signal = 0.
- `n > GLOBAL_MAX`: clamped to 1.0.
- `n = 50`: ≈ 0.43. `n = 800`: ≈ 0.73. (Spec: 800 should not bury 50.)

### 4. Friends - `signal_friends`

**Spec**: "synthesize a small friends-reacted dataset. Any friend reacted positively boosts; more friends, bigger boost."

```
signal_friends(c) = min(c.friend_count / FRIENDS_FULL_CREDIT, 1.0)
```

Where `FRIENDS_FULL_CREDIT = 5` from config.

**Edge cases**:
- `friend_count = 0`: signal = 0.
- `friend_count ≥ 5`: signal = 1.0 (capped).
- Demo-only: in real Lemon this is a per-user lookup, not a column.

### 5. Claimed - `signal_claimed`

**Spec**: "Claimed gets a big boost, unclaimed gets none."

```
signal_claimed(c) = 1.0 if c.is_claimed else 0.0
```

Pure step. The "big boost" lives entirely in the archetype weight, ordered by
how much trust matters per demand-shape: `high_stakes_one_time` (0.12) >
`recurring_service` (0.10) > `medium_stakes_occasion` (0.03) >
`low_stakes_fast_nearby` (0.01). Weights are kept small on purpose - claimed is
a tiebreaker, not a dominator. Because the synthesized flag is independent of
every other signal (a hash of the id, ~20.7% base rate), a large weight pulls
unrelated claimed businesses to the top of every result set; the
ranking-quality harness (`cmd/bench-runner -quality`) measured `claimed_pct`
falling from ~66% to ~38% (literal) once the weights were trimmed, with no loss
of category precision or mean rating.

### 6. Photos - `signal_photos`

**Spec**: "3+ photos full eligibility, under 3 a significant demotion."

```
signal_photos(c) =
    1.0                                  if c.photo_count >= 3
    PHOTO_DEMOTION_BELOW_3 (≈ 0.25)      otherwise
```

`PHOTO_DEMOTION_BELOW_3` from config (default 0.25).

### 7. Open status - `signal_open_status`

**Spec**: "open now beats opens-later beats closed, computed from the structured hours and a fixed current time."

```
signal_open_status(c) =
    1.0      if c.is_open_now is true
    0.3      if c is "opens later today" (closed now but opens before midnight)
    0.0      if explicitly closed all day
    0.7      if c.hours is null (unknown - soft-open default)
```

The `is_open_now` and "opens later" status are computed by Postgres in the
retrieval phase against a fixed `now` value (passed from the API as a
query parameter; defaults to wall-clock).

**Archetype behavior** (from `archetypes.*.open_status` config):
- `hard_filter`: candidates with `is_open_now = false` are dropped in
  pre-pass (step 1 above); they never see step 2.
- `soft`: signal participates in the linear sum normally.
- `ignore`: weight is forced to 0; signal does not participate.

Hours-unknown rows (`signal_open_status = 0.7`) are **never** hard-filtered.
Documented as a conservative call in [data/quality.md](../data/quality.md).

## Hard-filter pre-pass (step 1)

Before scoring, drop any candidate `c` where:

```
c.archetype.open_status_behavior == hard_filter
AND
c.is_open_now == false           # not null, not true - explicitly false
```

Archetypes that hard-filter: `low_stakes_fast_nearby`, `utility_distance_dominant`.

The drop happens in Go (not SQL) because a single query can return
candidates of mixed archetypes (a "sushi" query matches both restaurants
and a sushi-making class). One `WHERE` clause can't express
archetype-specific filter logic.

## Exact-name pin (step 6)

The retrieval phase runs a separate SQL query for the exact-name path. A
trigram GIN pre-filter (`name % $q`) narrows candidates cheaply, then
`lemon_name_match($q, name)` - token **coverage** plus per-word Levenshtein -
scores how much of the *full name* the query spans, typo-tolerantly:

```sql
SELECT …, count(*) OVER () AS match_count
FROM businesses
WHERE name % $q AND lemon_name_match($q, name) >= 0.8
ORDER BY lemon_name_match($q, name) DESC, id
LIMIT 1
```

If that path returns a row and it survives the two back-offs below, the ranker:

1. Removes that row from the broad-recall result (dedup by `id`).
2. Sets its `score` to `+Inf` (positive infinity in Go).
3. Prepends it at position #1.

Spec text: "regardless of other ranking signals." The `+Inf` is how we
honor "regardless" literally - sort order puts it first; tie-break never
kicks in.

The `0.8` coverage bar is `nameMatchCoverage` in
`api/internal/retrieve/postgres/repo.go`.

### Partial-name (prefix) pin

`lemon_name_match` requires the query to *span* the whole name, so a strict
**prefix** ("best florida pest" → "Best Florida Pest Control") scores 0 and never
pins. But text relevance is not a ranking signal - the 7-signal sum ignores how
well a candidate's name matches the query - so a prefix that uniquely names a
business was losing the top-3 to a more popular/closer unrelated token-sharer.
This was the weakest findability mode (partial pass@3 ≈ 33%).

`lemon_prefix_match(q, nm)` closes that gap: it returns name coverage in (0,1]
when `q` is an **in-order, typo-tolerant prefix** of `nm` - every query token
matches the name token at the *same position* within per-word Levenshtein
tolerance - and 0 otherwise. The exact-name query pins a row when *either*
matcher clears its bar:

```sql
WHERE (name % $q OR name ILIKE $q || '%')
  AND (lemon_name_match($q, name) >= 0.8 OR lemon_prefix_match($q, name) >= 0.5)
ORDER BY greatest(lemon_name_match($q, name), lemon_prefix_match($q, name)) DESC, id
```

The prefix path is precision-guarded the same way as the full-name path:

- **≥ 2 query tokens** - a single token is a category word, not a name fragment,
  so a bare "coffee"/"sushi" can never reach the prefix matcher (over_fire stays
  100%).
- **In-order, same-position** matching - "florida pest" does not match
  "South Florida Pet Sitter"; only a genuine leading prefix counts.
- **Cardinality back-off** (`maxNameMatches = 5`) and **categorical
  suppression** (`LEMON_FF_INTENT`) apply unchanged, so a chain prefix shared by
  many locations does not pin an arbitrary sibling.

The `0.5` prefix bar is `prefixMatchCoverage` in
`api/internal/retrieve/postgres/repo.go`. Measured effect: partial pass@3
**33% → 68%** with exact-name/typo/accent/over_fire all held (100/97/100/100).
The remaining partial "misses" are almost entirely large chains whose prefix
maps to many equally-valid siblings (the bench samples one arbitrarily) - the
engine returns a correct sibling; the ground truth is ambiguous, not absent.
The spec contract is untouched: ranking is still the 7-signal linear sum; this
only widens the max-cost name pin to recognize a confident prefix.

### The hybrid: coverage matcher + two back-offs (Stage 3)

The Stage-2 version pinned on trigram `similarity ≥ 0.85`. Trigram alone cannot
separate a typo'd *full name* from a *category prefix* - measured on the data,
the spec's own example `joes barbr shop → Joe's Barber Shop` scores ≈ **0.57**,
the *same band* as the false positives (`coffee`→"Coffee To Go" 0.54,
`sushi`→"Sushi Joe" 0.60), so no single similarity threshold separates them.
Stage 3 stops trying to discriminate on one number and layers three orthogonal
conditions instead; a high coverage score is now *necessary* but not
*sufficient* to honor "returns that business first, regardless".

**Coverage, not raw similarity.** `lemon_name_match` measures how much of the
business's name the query covers (token coverage) while tolerating per-word
typos (Levenshtein). A typo'd full name like `joes barbr shop` covers
"Joe's Barber Shop" and clears 0.8; a category word like `coffee` covers only a
fraction of "Coffee To Go" and does not. This is the "spans most of the full
name" discriminator the Stage-2 note deferred. `similarity(name, $q)` survives
only as the `name_trigram` display field.

**Back-off 1 - cardinality (always on).** The query also returns
`match_count = count(*) OVER ()`; the pin is suppressed when more than **5**
businesses share the matched name (`maxNameMatches`). A name that resolves to
many rows ("7-Eleven", "Subway") is a *chain*, not a unique business, and
pinning one arbitrary location ahead of a better-ranked sibling violates the
spec's intent more than it serves it. This is a property of the data, not the
query, so it needs no feature flag and is on unconditionally.

**Back-off 2 - categorical suppression (flag-gated, `LEMON_FF_INTENT`).** The
search handler drops the pin when the *entire* query is composed of category /
cuisine / domain terms - `intent.IsCategorical(q)`. So `coffee` and `spa` rank
their candidates instead of pinning a business literally named that, while
`joes barbr shop` (no categorical token) still pins. The intent lexicon already
knows which tokens are taxonomy leaves (Café→Coffee Shop, …), so this is the
"suppress when the query is a known taxonomy term" discriminator the Stage-2
note called for. It rides behind `LEMON_FF_INTENT` because it depends on the
intent layer; with the flag off, only the cardinality back-off applies. A query
of *only* price/time modifiers (`cheap`, `open now`) is **not** categorical -
those entries narrow by price/open-now, not category - so the pin may still
serve it.

The pin remains a max-cost override (a false positive is catastrophic; a false
negative merely demotes a still-ranked result), so all three conditions are
tuned to favor precision. Measured effect: the `over_fire` bench mode went from
**76% → 100%** (25/25) while typo held at **97%** - see
[the over-fire resolution in the writeup](../writeup.md#spec-ambiguities--calls).

The pin still folds *into* the main retrieval (for real distance and one fewer
round-trip) only once the overlay is threaded through `search_candidates`; until
then it stays a separate query carrying a distance sentinel.

## Tie-breaking (step 7)

After step 5 sort, equal scores (within `tie_epsilon = 0.005`) are broken
deterministically:

1. Higher final score.
2. Within ε: `is_claimed` true beats false.
3. Still tied: smaller `distance_km`.
4. Still tied: larger `google_review_count`.
5. Stable by `id` (UUID string compare).

Implemented as a single `sort.SliceStable` with a multi-key comparator.

## De-pin pass (step 8)

After the top-K is selected, walk positions 1 and 2:

```
for i in [0, 1]:
    if results[i].is_new:
        # find the highest-scored non-new candidate not yet in top-2
        for j in [i+1, …]:
            if not results[j].is_new and (results[i].score - results[j].score) < swap_window:
                swap(results[i], results[j])
                break
```

Where `swap_window = config.new_business.swap_window` (default 0.05).
Implements spec text "don't surface at the very top" without hard-banning
new businesses entirely.

## Worked example

Query: `"sushi near brickell"`, archetype defaults to `low_stakes_fast_nearby` for matched candidates.

Candidate: a sushi restaurant 1.5 km away, `lemon_score = 9.2`,
`google_review_count = 420`, `friend_count = 2`, `is_claimed = true`,
`photo_count = 8`, `is_open_now = true`, `is_new = false`.

Signals (literal mode):

```
distance      = max(1 - 1.5/48.28, 0)         = 0.969
rating        = 9.2/10                         = 0.920
popularity    = log(421)/log(10001)            = 0.656
friends       = min(2/5, 1)                    = 0.400
claimed       = 1.0                            = 1.000
photos        = 1.0  (>= 3)                    = 1.000
open_status   = 1.0                            = 1.000
```

Weights for `low_stakes_fast_nearby`:

```
distance: 0.25  · 0.969 = 0.2422
rating:   0.18  · 0.920 = 0.1656
popular:  0.19  · 0.656 = 0.1247
friends:  0.12  · 0.400 = 0.0480
claimed:  0.01  · 1.000 = 0.0100
photos:   0.10  · 1.000 = 0.1000
open:     0.15  · 1.000 = 0.1500
                          ──────
score                    ≈ 0.8405
```

Compare to a similar sushi restaurant 4 km away with `google_review_count = 50`
and `friend_count = 0`:

```
distance      = 1 - 4/48.28                    = 0.917
popularity    = log(51)/log(10001)             = 0.426
friends       = 0
… others equal …

distance: 0.25 · 0.917 = 0.2293
popular:  0.19 · 0.427 = 0.0811
friends:  0
… others same …

score ≈ 0.7360
```

First candidate wins by ~0.10 - distance dominates within the
neighborhood-tight `low_stakes` weights, but the `popularity` and
`friends` contributions matter as differentiators between two close
restaurants.

## Config-switch behavior summary

| Switch | Default | Alternative |
|---|---|---|
| `signal_formulas.rating` | `literal` (`lemon_score / 10`) | `bayesian` (smoothed `google_rating`) |
| `signal_formulas.distance` | `literal` (`max(1 - d/30mi, 0)`) | `decay` (per-archetype `exp(-d/k)`) |

Switching is config-only (no rebuild). The bench runner runs both and the
writeup quotes a comparison.

## Cross-references

- Config schema: `config/ranking.yaml`
- Architecture: [../architecture.md](../architecture.md)
- ADR for ranking strategy: [../adr/0003-ranking-strategy.md](../adr/0003-ranking-strategy.md)
- ADR for keeping the spec contract: [../adr/0004-spec-contract-discipline.md](../adr/0004-spec-contract-discipline.md)
- Intent overlay (consumed *before* ranking, narrows the candidate set): [intent.md](intent.md)
