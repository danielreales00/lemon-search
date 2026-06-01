# Ranking-quality eval - 2026-05-31

Curated **25**-query ranking-quality set run through the real pipeline (intent overlay + single-round-trip retrieval + pure re-rank) in-process against the local Postgres (22,568 Miami businesses). Metrics are computed over the top-15 and are SPEC-DERIVED proxies for what each query should prioritize - not opinion. Dataset claimed base rate: **20.7%**.

The two arms differ ONLY in `signal_formulas.distance`:

- **literal** - spec default: `max(1 - d/30mi, 0)`
- **decay** - per-archetype `exp(-d/decay_km)` (3km utility … 80km high-stakes)

Every other knob (rating mode, weights, photo/friend/open constants) is shared, read from the `-config` file, so the table isolates the distance formula.

> **Note.** 3 free-form/vibe queries return zero results in this lexical baseline (no embedder wired): `fancy dinner`, `i'm hungry`, `somewhere to get pampered`. They are honest gaps the semantic layer (LEMON_FF_SEMANTIC, ADR-0006) is meant to close; they are excluded from the aggregate means.

## Headline - literal vs decay

| metric | literal | decay | Δ (decay−literal) |
|---|---|---|---|
| mean_distance_km | 6.83 | 5.17 | -1.66 |
| median_distance_km | 6.09 | 4.39 | -1.70 |
| mean_rating (0..1) | 0.919 | 0.914 | -0.006 |
| mean_log_reviews (0..1) | 0.720 | 0.678 | -0.043 |
| pct_open | 0.98 | 0.95 | -0.03 |
| category_precision | 0.867 | 0.863 | -0.003 |
| claimed_pct | 0.382 | 0.336 | -0.045 |
| diversity | 0.927 | 0.924 | -0.003 |
| golden_precision@5 | 0.500 | 0.500 | 0.000 |
| new_at_rank1 (count) | 0 | 0 | +0 |
| claimed base rate | 0.207 | 0.207 | - |

**Read.** Decay is worth flipping the spec default only if it improves locality (lower mean distance) *without* hurting category_precision or rating. Movement:

- distance: -1.66 km (better)
- category_precision: -0.003 (worse)
- rating: -0.006 (worse)

**Recommendation: keep `literal` as the default; ship `decay` as the documented opt-in switch.** Decay tightens locality but the trade against category/rating is not clean enough to override the spec contract by default.

## Per-query - literal

| query | kind | loc | dist km (mean/med) | rating | logrev | open | cat_prec | intent | claimed | new@1 | div | golden@5 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| "sushi" | category | Downtown Miami | 4.00 / 1.54 | 0.915 | 0.719 | 100% | 100% | - | 53% | no | 0.87 | 100% |
| "coffee" | category | Downtown Miami | 4.76 / 2.24 | 0.935 | 0.696 | 100% | 87% | - | 67% | no | 1.00 | 0% |
| "gym" | category | Downtown Miami | 6.14 / 4.24 | 0.912 | 0.636 | 87% | 100% | - | 33% | no | 0.80 | - |
| "barber" | category | Downtown Miami | 5.05 / 3.21 | 0.976 | 0.631 | 100% | 100% | - | 47% | no | 0.93 | - |
| "nail salon" | category | Downtown Miami | 11.32 / 11.24 | 0.914 | 0.568 | 87% | 100% | - | 53% | no | 1.00 | - |
| "tacos" | category | Downtown Miami | 3.85 / 3.56 | 0.943 | 0.931 | 100% | 100% | - | 40% | no | 0.87 | - |
| "pizza" | category | Downtown Miami | 3.51 / 2.26 | 0.908 | 0.758 | 100% | 100% | - | 20% | no | 1.00 | - |
| "spa" | category | Downtown Miami | 6.82 / 6.65 | 0.936 | 0.619 | 100% | 100% | - | 60% | no | 1.00 | - |
| "tattoo" | category | Downtown Miami | 8.09 / 6.37 | 0.968 | 0.729 | 100% | 100% | - | 53% | no | 0.80 | - |
| "ramen" | category | Downtown Miami | 3.35 / 1.34 | 0.895 | 0.763 | 100% | 40% | - | 13% | no | 0.87 | - |
| "steakhouse" | category | Downtown Miami | 5.36 / 5.74 | 0.944 | 0.895 | 100% | 100% | - | 27% | no | 0.87 | - |
| "cheap restaurants" | intent | Downtown Miami | 4.30 / 3.85 | 0.887 | 0.808 | 100% | 73% | cheap:100% | 47% | no | 1.00 | - |
| "open now" | intent | Downtown Miami | 5.90 / 3.17 | 0.887 | 0.764 | 100% | - | open_now:100% | 47% | no | 1.00 | - |
| "fancy dinner" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "date night" | intent | Downtown Miami | 8.97 / 9.74 | 0.842 | 0.512 | 80% | 100% | date_night:n/a | 0% | no | 1.00 | - |
| "i'm hungry" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "outdoor seating" | intent | Downtown Miami | 4.60 / 5.97 | 0.912 | 0.772 | 100% | 60% | outdoor:n/a | 33% | no | 1.00 | - |
| "chill place to work" | vibe | Downtown Miami | 22.27 / 22.27 | 0.900 | 0.643 | 100% | 0% | work:n/a | 0% | no | 1.00 | - |
| "somewhere to get pampered" | vibe | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "coffee" | geo | Brickell | 4.95 / 3.58 | 0.931 | 0.696 | 100% | 87% | - | 67% | no | 0.93 | - |
| "coffee" | geo | Miami Beach | 7.20 / 6.37 | 0.915 | 0.665 | 100% | 80% | - | 53% | no | 1.00 | - |
| "coffee" | geo | Hialeah | 11.34 / 11.43 | 0.920 | 0.734 | 100% | 93% | - | 40% | no | 0.87 | - |
| "sushi" | geo | Brickell | 3.81 / 2.93 | 0.923 | 0.730 | 100% | 100% | - | 40% | no | 0.93 | - |
| "sushi" | geo | Miami Beach | 5.61 / 6.34 | 0.928 | 0.755 | 100% | 100% | - | 20% | no | 0.87 | - |
| "sushi" | geo | Hialeah | 9.09 / 10.01 | 0.937 | 0.823 | 100% | 100% | - | 27% | no | 0.80 | - |

## Per-query - decay

| query | kind | loc | dist km (mean/med) | rating | logrev | open | cat_prec | intent | claimed | new@1 | div | golden@5 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| "sushi" | category | Downtown Miami | 1.65 / 1.07 | 0.911 | 0.690 | 100% | 100% | - | 53% | no | 0.93 | 100% |
| "coffee" | category | Downtown Miami | 1.36 / 0.92 | 0.917 | 0.606 | 100% | 93% | - | 40% | no | 1.00 | 0% |
| "gym" | category | Downtown Miami | 4.33 / 3.24 | 0.921 | 0.621 | 80% | 100% | - | 27% | no | 0.80 | - |
| "barber" | category | Downtown Miami | 4.40 / 2.24 | 0.976 | 0.623 | 100% | 100% | - | 47% | no | 0.93 | - |
| "nail salon" | category | Downtown Miami | 10.49 / 8.31 | 0.914 | 0.558 | 87% | 100% | - | 53% | no | 1.00 | - |
| "tacos" | category | Downtown Miami | 1.72 / 1.11 | 0.913 | 0.808 | 100% | 100% | - | 40% | no | 0.87 | - |
| "pizza" | category | Downtown Miami | 1.37 / 0.78 | 0.889 | 0.681 | 100% | 100% | - | 20% | no | 1.00 | - |
| "spa" | category | Downtown Miami | 4.68 / 4.88 | 0.952 | 0.598 | 93% | 100% | - | 47% | no | 1.00 | - |
| "tattoo" | category | Downtown Miami | 6.53 / 6.06 | 0.973 | 0.721 | 100% | 100% | - | 53% | no | 0.87 | - |
| "ramen" | category | Downtown Miami | 2.12 / 0.98 | 0.887 | 0.723 | 87% | 33% | - | 13% | no | 0.93 | - |
| "steakhouse" | category | Downtown Miami | 2.99 / 1.35 | 0.920 | 0.834 | 87% | 100% | - | 20% | no | 0.93 | - |
| "cheap restaurants" | intent | Downtown Miami | 2.63 / 1.80 | 0.876 | 0.763 | 93% | 73% | cheap:100% | 40% | no | 1.00 | - |
| "open now" | intent | Downtown Miami | 4.90 / 1.96 | 0.925 | 0.717 | 100% | - | open_now:100% | 33% | no | 0.87 | - |
| "fancy dinner" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "date night" | intent | Downtown Miami | 8.97 / 9.74 | 0.842 | 0.512 | 80% | 100% | date_night:n/a | 0% | no | 1.00 | - |
| "i'm hungry" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "outdoor seating" | intent | Downtown Miami | 4.01 / 3.78 | 0.911 | 0.742 | 100% | 47% | outdoor:n/a | 40% | no | 1.00 | - |
| "chill place to work" | vibe | Downtown Miami | 22.27 / 22.27 | 0.900 | 0.643 | 100% | 0% | work:n/a | 0% | no | 1.00 | - |
| "somewhere to get pampered" | vibe | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "coffee" | geo | Brickell | 1.14 / 0.69 | 0.907 | 0.593 | 100% | 93% | - | 33% | no | 0.93 | - |
| "coffee" | geo | Miami Beach | 4.96 / 3.00 | 0.883 | 0.635 | 93% | 93% | - | 40% | no | 0.80 | - |
| "coffee" | geo | Hialeah | 9.43 / 9.56 | 0.917 | 0.676 | 93% | 93% | - | 40% | no | 0.87 | - |
| "sushi" | geo | Brickell | 1.72 / 0.88 | 0.911 | 0.688 | 93% | 87% | - | 47% | no | 0.93 | - |
| "sushi" | geo | Miami Beach | 3.80 / 2.90 | 0.921 | 0.684 | 93% | 100% | - | 27% | no | 0.87 | - |
| "sushi" | geo | Hialeah | 8.28 / 9.05 | 0.933 | 0.789 | 100% | 100% | - | 27% | no | 0.80 | - |

## Metric definitions

- **dist km (mean/med)** - raw retrieval distance of the top-15. Lower = tighter locality.
- **rating** - mean `lemon_score/10` over rated results (0..1).
- **logrev** - mean `log(1+reviews)/log(1+10000)` (spec popularity signal, 0..1).
- **open** - fraction explicitly open now (unknown hours count as not-open).
- **cat_prec** - fraction whose subcategory/category matches an expected token (category-aware matching).
- **intent** - per-intent adherence: `cheap`=frac $/$$, `fancy`=frac $$$+, `open_now`=frac open; vibe intents are n/a (judged on cat_prec).
- **claimed** - fraction claimed; compare to the ~20.7% base rate (≈base good, ~2x = dominating).
- **new@1** - is the #1 result a new business? Spec: must be `no`.
- **div** - distinct name-stems / 15 (chains clumping lowers it).
- **golden@5** - precision@5 vs hand-picked anchors (- when none).
