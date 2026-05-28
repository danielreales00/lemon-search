# Data quality — what we know about the Lemon catalog

Profile of `businesses-2026-05-27.json` (626 MB, 23,537 records) — the
canonical input. Everything below is empirical; counts were measured by
stream-parsing the entire file (the file is malformed JSON, see
[ingestion.md](ingestion.md)).

The spec asks us to "flag data-quality issues (SEO-spam names, bad categories,
broken hours)" in the writeup. This is that flag.

## Headline numbers

| Field | Coverage | Notes |
|---|---|---|
| `name` | 100% | Free text; SEO-spam suspected in a small minority (see below) |
| `category` | 98.8% (287 missing) | Mixed spec + Google taxonomy; ~5% needs normalization |
| `subcategory` | 95% | Variant naming (`TimeSavers` vs `Time Savers`, etc.) |
| `latitude`/`longitude` | 97.0% | 717 rows have at least one null |
| `neighborhood` | 91.1% | Often consistent with `address` |
| `hours` | **81.1%** | Per-day structured; remaining 19% are `null` |
| `google_rating` | 98.0% | 0–5 scale |
| `google_review_count` | 98.1% | Treated as the "reaction count" signal |
| `lemon_score` | 98.0% | 0–10 scale; **mean ≈ 9** (skewed high) |
| `price_range` | 74.6% | `$`/`$$`/`$$$`/`$$$$` |
| `photos` | 97.5% have ≥1 photo, 79.4% have ≥3 | URLs from Cloudinary / Airbnb / Google |
| `about` | 80.4% | Long-form text; mixed quality |
| `universal_tags` | 99.7% | Coarse tags |
| `specific_tags` | 94.8% | Fine-grained tags |
| `is_claimed=true` | **0.04% (10 rows)** | Effectively zero — we synthesize |
| `is_verified=true` | 0% | We don't use this field |

## What we drop

### Non-Miami records (~3.1%, 728 rows)

The catalog includes records from outside Miami (e.g., Versailles, France
appears as a *Lemon experience* in Activities & Experiences). Filter via
bounding box around Miami-Dade County + manual blocklist for known foreign
cities.

Bounding box (Miami-Dade-ish):

```
latitude  ∈ [25.10, 26.10]
longitude ∈ [-80.95, -80.05]
```

Rows outside the bbox **and** with addresses that don't match a US-Florida
pattern (`, FL`) are dropped.

### Empty-category records (~1.2%, 287 rows)

Rows with `category` null after normalization. Drop rather than bucket as
"Other" — these are typically scraping errors with sparse data overall.

### No-address records (~1.0%, 231 rows)

Rows with both `address` empty and `latitude`/`longitude` null. Drop.

### Test businesses

The data has an `is_test_business` flag; all 23,537 rows have it `false`,
so no drops here. We still check during ingestion in case future feeds
add real test rows.

### Foreign records (a handful)

E.g., the Versailles bike-tour example. The bbox + `, FL` filter catches
these.

Result: after ingestion, the table holds ≈ 22,000 rows (target ≥ 22,000 in
Stage 1 acceptance criteria).

## What we synthesize

### `is_claimed` (~35% target)

Spec requires synthesis. The data ships with only 10 claimed rows.
Synthesis is deterministic per `id` via `lemon_seed(id)`:

```
is_claimed = lemon_seed(id) < 0.35
  · correlated by boost: +0.1 if lemon_score >= 9.0
  · correlated by boost: +0.1 if photo_count >= 3
```

The correlation makes claimed businesses tend to be the higher-quality
ones (which matches real platform behavior). Documented as a design
choice in the writeup.

### `friend_count` (~3% of rows have 1–5)

Spec: "synthesize a small friends-reacted dataset." Deterministic per `id`:

```
if lemon_seed(id + ':friends') < 0.03:
    friend_count = 1 + int(lemon_seed(id + ':friend_n') * 5)
else:
    friend_count = 0
```

(Real Lemon needs a per-user join; this is a demo-only denormalization,
flagged in writeup.)

### "Reaction count" mapping

The spec talks about "reaction count" but the data has no such column. We
use `google_review_count` (98% coverage) as the proxy. Documented as a
spec-ambiguity call.

### Open-status fallback

For the 19% of rows with `hours = null`, the open-status signal defaults
to `0.7` ("soft-open") and **never** triggers a `hard_filter` drop. Honest
mitigation; the alternative (drop those rows) would lose ~4k results.

## What we flag in the writeup

### SEO-spam names

A small number of records have names that look optimized for search rather
than human readability:

- All-caps or repeated keywords (`"BEST PIZZA MIAMI BEACH OPEN NOW"`).
- Trailing keyword strings (`"Joe's Plumbing - Plumber, Drain Cleaning, Water Heater Repair"`).

We **don't** filter these out — they're real businesses — but they hurt
exact-name match quality. The 0.85 similarity threshold for the hard-pin
path absorbs the worst of it; a name like
`"Joe's Plumbing - Plumber, Drain Cleaning"` still requires the user to
type close to the prefix.

Future mitigation (V2): a name-cleaner that strips trailing punctuated
keyword runs before indexing.

### Category drift

The data has a clean spec taxonomy in 95% of rows but ~5% bleed Google-API
categories (`Tobacco shop`, `Insurance agency`, `Trucking company`,
`Children's amusement center`, etc.). See [taxonomy.md](taxonomy.md) for
the normalization rules. Unmapped values go to an `Other` archetype with
reduced weights.

### Subcategory naming variants

The same logical subcategory appears with different casing/spacing:
`TimeSavers` vs `Time Savers`, `Coworking` vs `Co-working`, `Auto` vs
`Car/Auto`, `Home` vs `Home Improvement`. Normalized at ingest.

### `lemon_score` skew

Mean ≈ 9.0 over the population, range 1.6–10. Almost everyone scores
8.5–9.8. This makes raw `lemon_score / 10` a weak discriminator. The
literal formula stays as the spec contract; Bayesian-smoothed
`google_rating` is the alternative behind a config switch.

### `status` distribution

| Status | Count |
|---|---|
| `scraped` | 22,581 |
| `discovered` | 737 |
| `unclaimed` | 209 |
| `pending` | 10 |

All four indicate pre-launch / pre-user-reactions state. No `live` or
`active` status — meaning the "reactions" we score on are really Google
reviews. We use `google_review_count` as reaction count and note this in
the writeup.

### Hours quirks

When `hours` is present (81%), the structure is consistent and clean:

```json
{
  "monday":    {"open": "9:00 AM", "close": "6:30 PM"},
  "tuesday":   {"closed": true},
  …
}
```

Edge cases we handle:

- A day with `"closed": true` → that day's open-now check returns false.
- A day missing entirely → treated as closed for that day.
- 24h businesses (`open: "12:00 AM", close: "11:59 PM"`) → handled normally.
- Cross-midnight close (rare, e.g., bars): not in the data; if encountered,
  we treat as open through midnight.

### Photos

97.5% have at least one photo; 79.4% have ≥3. The 2.5% with zero photos
take the photo demotion (signal 0.25 vs 1.0). No photo-quality scoring
(no detection of stock images, watermark spam, etc.).

## What we don't validate

- Phone numbers (free text, format varies)
- Website URLs (we don't crawl; some are broken)
- Address format (free text; not geocoded against lat/lng for consistency)

## Cross-references

- Schema: [schema.md](schema.md)
- Ingestion pipeline: [ingestion.md](ingestion.md)
- Taxonomy + archetype: [taxonomy.md](taxonomy.md)
- The writeup section that consumes this: `../writeup.md#whats-broken--known-gaps`
