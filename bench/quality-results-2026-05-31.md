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
| mean_distance_km | 8.13 | 6.12 | -2.01 |
| median_distance_km | 7.79 | 5.54 | -2.25 |
| mean_rating (0..1) | 0.916 | 0.908 | -0.008 |
| mean_log_reviews (0..1) | 0.686 | 0.651 | -0.035 |
| pct_open | 0.97 | 0.95 | -0.03 |
| category_precision | 0.860 | 0.854 | -0.006 |
| claimed_pct | 0.661 | 0.548 | -0.112 |
| diversity | 0.927 | 0.948 | 0.021 |
| golden_precision@5 | 0.500 | 0.500 | 0.000 |
| new_at_rank1 (count) | 0 | 0 | +0 |
| claimed base rate | 0.207 | 0.207 | - |

**Read.** Decay is worth flipping the spec default only if it improves locality (lower mean distance) *without* hurting category_precision or rating. Movement:

- distance: -2.01 km (better)
- category_precision: -0.006 (worse)
- rating: -0.008 (worse)

**Recommendation: keep `literal` as the default; ship `decay` as the documented opt-in switch.** Decay tightens locality but the trade against category/rating is not clean enough to override the spec contract by default.

_Side-observation._ claimed_pct sits at 66% (literal) / 55% (decay) against a ~20.7% base rate - above the ~2x line. Decay pulls it toward the base rate by surfacing nearby (often unclaimed) places; under literal, claimed weight + popularity skew co-select for established, claimed businesses. Worth a follow-up claimed-weight sweep with this same harness.

## Per-query - literal

| query | kind | loc | dist km (mean/med) | rating | logrev | open | cat_prec | intent | claimed | new@1 | div | golden@5 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| "sushi" | category | Downtown Miami | 3.06 / 1.54 | 0.907 | 0.700 | 100% | 100% | - | 73% | no | 0.87 | 100% |
| "coffee" | category | Downtown Miami | 5.33 / 5.88 | 0.936 | 0.678 | 100% | 87% | - | 80% | no | 1.00 | 0% |
| "gym" | category | Downtown Miami | 11.70 / 12.15 | 0.905 | 0.513 | 80% | 100% | - | 100% | no | 0.87 | - |
| "barber" | category | Downtown Miami | 12.48 / 12.04 | 0.957 | 0.549 | 100% | 100% | - | 100% | no | 1.00 | - |
| "nail salon" | category | Downtown Miami | 13.92 / 13.65 | 0.915 | 0.520 | 87% | 100% | - | 93% | no | 1.00 | - |
| "tacos" | category | Downtown Miami | 4.57 / 6.03 | 0.941 | 0.888 | 100% | 100% | - | 67% | no | 0.93 | - |
| "pizza" | category | Downtown Miami | 4.93 / 5.60 | 0.921 | 0.707 | 100% | 100% | - | 67% | no | 0.93 | - |
| "spa" | category | Downtown Miami | 8.45 / 7.74 | 0.900 | 0.558 | 100% | 100% | - | 100% | no | 1.00 | - |
| "tattoo" | category | Downtown Miami | 8.93 / 6.06 | 0.968 | 0.583 | 93% | 100% | - | 100% | no | 0.93 | - |
| "ramen" | category | Downtown Miami | 5.15 / 1.97 | 0.903 | 0.766 | 100% | 33% | - | 33% | no | 0.80 | - |
| "steakhouse" | category | Downtown Miami | 6.88 / 6.19 | 0.939 | 0.859 | 100% | 93% | - | 67% | no | 0.80 | - |
| "cheap restaurants" | intent | Downtown Miami | 5.36 / 4.74 | 0.888 | 0.818 | 100% | 87% | cheap:100% | 73% | no | 1.00 | - |
| "open now" | intent | Downtown Miami | 7.13 / 6.71 | 0.880 | 0.760 | 100% | - | open_now:100% | 60% | no | 1.00 | - |
| "fancy dinner" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "date night" | intent | Downtown Miami | 8.97 / 9.74 | 0.842 | 0.512 | 80% | 100% | date_night:n/a | 0% | no | 1.00 | - |
| "i'm hungry" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "outdoor seating" | intent | Downtown Miami | 4.42 / 5.89 | 0.912 | 0.749 | 100% | 53% | outdoor:n/a | 47% | no | 1.00 | - |
| "chill place to work" | vibe | Downtown Miami | 22.27 / 22.27 | 0.900 | 0.643 | 100% | 0% | work:n/a | 0% | no | 1.00 | - |
| "somewhere to get pampered" | vibe | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "coffee" | geo | Brickell | 5.48 / 5.26 | 0.936 | 0.678 | 100% | 87% | - | 80% | no | 1.00 | - |
| "coffee" | geo | Miami Beach | 8.28 / 6.66 | 0.929 | 0.679 | 100% | 80% | - | 80% | no | 1.00 | - |
| "coffee" | geo | Hialeah | 12.09 / 12.12 | 0.925 | 0.694 | 100% | 87% | - | 67% | no | 0.87 | - |
| "sushi" | geo | Brickell | 3.54 / 2.39 | 0.905 | 0.732 | 100% | 100% | - | 67% | no | 0.87 | - |
| "sushi" | geo | Miami Beach | 6.73 / 6.63 | 0.916 | 0.686 | 100% | 100% | - | 67% | no | 0.80 | - |
| "sushi" | geo | Hialeah | 9.06 / 10.01 | 0.928 | 0.815 | 100% | 100% | - | 33% | no | 0.73 | - |

## Per-query - decay

| query | kind | loc | dist km (mean/med) | rating | logrev | open | cat_prec | intent | claimed | new@1 | div | golden@5 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| "sushi" | category | Downtown Miami | 1.50 / 1.07 | 0.913 | 0.651 | 100% | 100% | - | 60% | no | 0.93 | 100% |
| "coffee" | category | Downtown Miami | 1.68 / 0.92 | 0.919 | 0.632 | 100% | 93% | - | 47% | no | 1.00 | 0% |
| "gym" | category | Downtown Miami | 9.97 / 10.09 | 0.905 | 0.543 | 80% | 100% | - | 93% | no | 0.87 | - |
| "barber" | category | Downtown Miami | 11.02 / 11.39 | 0.948 | 0.548 | 93% | 100% | - | 100% | no | 1.00 | - |
| "nail salon" | category | Downtown Miami | 12.04 / 11.49 | 0.895 | 0.516 | 87% | 100% | - | 93% | no | 1.00 | - |
| "tacos" | category | Downtown Miami | 2.09 / 1.16 | 0.925 | 0.803 | 100% | 100% | - | 60% | no | 0.87 | - |
| "pizza" | category | Downtown Miami | 1.35 / 0.78 | 0.892 | 0.662 | 100% | 100% | - | 27% | no | 1.00 | - |
| "spa" | category | Downtown Miami | 8.45 / 7.74 | 0.900 | 0.558 | 100% | 100% | - | 100% | no | 1.00 | - |
| "tattoo" | category | Downtown Miami | 8.21 / 5.39 | 0.968 | 0.565 | 100% | 100% | - | 100% | no | 0.93 | - |
| "ramen" | category | Downtown Miami | 2.12 / 0.98 | 0.887 | 0.723 | 87% | 33% | - | 13% | no | 0.93 | - |
| "steakhouse" | category | Downtown Miami | 3.16 / 1.36 | 0.923 | 0.828 | 93% | 93% | - | 27% | no | 0.87 | - |
| "cheap restaurants" | intent | Downtown Miami | 2.92 / 1.80 | 0.876 | 0.756 | 93% | 73% | cheap:100% | 53% | no | 1.00 | - |
| "open now" | intent | Downtown Miami | 3.53 / 1.54 | 0.928 | 0.663 | 100% | - | open_now:100% | 53% | no | 1.00 | - |
| "fancy dinner" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "date night" | intent | Downtown Miami | 8.97 / 9.74 | 0.842 | 0.512 | 80% | 100% | date_night:n/a | 0% | no | 1.00 | - |
| "i'm hungry" | intent | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "outdoor seating" | intent | Downtown Miami | 5.48 / 3.78 | 0.903 | 0.718 | 100% | 53% | outdoor:n/a | 53% | no | 1.00 | - |
| "chill place to work" | vibe | Downtown Miami | 22.27 / 22.27 | 0.900 | 0.643 | 100% | 0% | work:n/a | 0% | no | 1.00 | - |
| "somewhere to get pampered" | vibe | Downtown Miami | (no results, lexical baseline) | | | | | | | | | |
| "coffee" | geo | Brickell | 1.29 / 0.71 | 0.905 | 0.634 | 100% | 93% | - | 47% | no | 1.00 | - |
| "coffee" | geo | Miami Beach | 4.88 / 5.93 | 0.892 | 0.651 | 93% | 80% | - | 73% | no | 0.93 | - |
| "coffee" | geo | Hialeah | 10.57 / 11.43 | 0.919 | 0.666 | 93% | 87% | - | 60% | no | 0.87 | - |
| "sushi" | geo | Brickell | 1.53 / 0.88 | 0.907 | 0.661 | 93% | 87% | - | 53% | no | 0.93 | - |
| "sushi" | geo | Miami Beach | 3.81 / 2.90 | 0.921 | 0.625 | 93% | 100% | - | 53% | no | 0.93 | - |
| "sushi" | geo | Hialeah | 7.80 / 8.48 | 0.912 | 0.759 | 93% | 100% | - | 40% | no | 0.80 | - |

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
