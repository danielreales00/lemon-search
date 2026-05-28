# Taxonomy — categories, subcategories, archetypes

The spec defines a clean three-level taxonomy
(Category → Subcategory → Specialty/Preference). The data partly matches,
partly drifts. This doc is the source of truth for:

1. The canonical spec taxonomy
2. How dirty raw values map to it
3. How each Category gets one of six archetypes

The `internal/ingest/taxonomy.go` Go file is the implementation; this
document is the spec it implements.

## Canonical taxonomy (from the spec)

| Category | Subcategories |
|---|---|
| **Food & Drinks** | Restaurant, Casual / Fast, Bar, Café, Catering |
| **Beauty** | Hair Salon, Barbershop, Nail Salon, Spa, Skincare, Makeup & Lashes, Tattoo & Piercing |
| **Fitness & Wellness** | Gym, Studio, Personal Training, Wellness |
| **Home Improvement** | Contractor, Outdoor & Garden, Electrician, Plumber, HVAC, Painting, Flooring, Roofing, Interior Design, Carpentry, Windows & Doors, Masonry & Concrete, Handyman |
| **Time Savers** | Cleaning, Moving, Laundry, Errands, Organization, Private Chef |
| **Pets** | Grooming, Vet, Boarding & Sitting, Training, Walking |
| **Events** | Weddings, Venue, Photography & Video, Catering, DJ / Music, Event Planning, Florist, Rentals |
| **Car** | Repair, Detailing, Body & Paint, Tires, Towing & Roadside, Glass Repair |
| **Activities & Experiences** | Bowling, Golf, Racquet Sports, Action Sports, Arcades & Games, Water Sports, Arts & Culture, Parks & Nature, Boat Tour, Food Tour, Cooking Class |
| **Co-working** | Hot Desk, Private Office, Meeting Room, Day Pass, Virtual Office |
| **Grocery** | Supermarket, Organic / Health Food, International / Ethnic, Butcher / Seafood, Farmers Market, Convenience Store |

Specialties (the third level) are too varied to enumerate here; the
spec lists them per subcategory. Stored as free text in the `specialty`
column.

## Archetype assignment (per Category)

Spec text quoted; mapping to one of six archetypes.

| Category | Archetype | Spec rationale |
|---|---|---|
| Food & Drinks | `low_stakes_fast_nearby` | "restaurants, cafés, bars, fast food" |
| Beauty | `medium_stakes_occasion` | "salons, barbers, nail salons, spas" |
| Fitness & Wellness | `medium_stakes_occasion` | "gyms, salons, barbers, nail salons, spas, tattoo, studios, personal training, wellness" |
| Home Improvement | `high_stakes_one_time` | "all home improvement" |
| Time Savers | `recurring_service` | "cleaners, dog walkers, meal prep, errands" |
| Pets — Grooming/Walking/Boarding | `recurring_service` | "pet grooming/walking" |
| Pets — Vet | `medium_stakes_occasion` | "vets, pet boarding/training" |
| Pets — Training | `medium_stakes_occasion` | Same line as above |
| Events | `high_stakes_one_time` | "weddings, event venues, catering, photography, DJs, event planning, florists, rentals" |
| Car — Repair / Body & Paint | `medium_stakes_occasion` | "car repair, vets, pet boarding/training" |
| Car — Detailing | `low_stakes_fast_nearby` | "car detailing" |
| Car — Tires / Towing & Roadside / Glass Repair | `utility_distance_dominant` | "towing, tires, glass repair" |
| Activities & Experiences | `experiential` | "boats, beach clubs, theaters, hotels, destination spas, all activities and experiences" |
| Co-working — Day Pass / Meeting Room | `utility_distance_dominant` | "meeting rooms, day passes" |
| Co-working — Hot Desk / Private Office / Virtual Office | `recurring_service` | "coworking" |
| Grocery — Supermarket / Convenience Store | `utility_distance_dominant` | "supermarket, convenience store" |
| Grocery — Organic / International / Butcher / Farmers Market | `low_stakes_fast_nearby` | "farmers markets, ethnic/organic grocery, butcher" |

### `Other` archetype fallback

Categories that don't map to any of the above (Google-API drift like
"Tobacco shop", "Insurance agency", "Trucking company") get bucketed as:

```
category = 'Other'
archetype = 'low_stakes_fast_nearby'   # conservative default
weights   = (slightly reduced from low_stakes defaults)
```

Reasoning: these are mostly small local businesses; treating them as
low-stakes-fast-nearby with reduced weights is closer to right than
picking a wrong archetype. Counts in the histogram (logged at ingest)
inform a Stage 3 decision: keep, retire, or build a richer map.

## Raw → spec normalization rules

The data ships with category names in three styles:

1. **Spec-matching**: `Food & Drinks`, `Beauty`, `Events` (~85% of rows)
2. **Compact variants**: `TimeSavers`, `Coworking`, `Auto`, `Home`
3. **Google-API leaks**: `Tobacco shop`, `Insurance agency`, …

### Variant table (category)

| Raw | Normalized to |
|---|---|
| `Food & Drinks` | `Food & Drinks` |
| `Beauty` | `Beauty` |
| `Fitness` | `Fitness & Wellness` |
| `Activities & Experiences` | `Activities & Experiences` |
| `Home` | `Home Improvement` |
| `Home Services` | `Home Improvement` |
| `Grocery` | `Grocery` |
| `TimeSavers` | `Time Savers` |
| `Time Savers` | `Time Savers` |
| `Car/Auto` | `Car` |
| `Auto` | `Car` |
| `auto` | `Car` |
| `Events` | `Events` |
| `Pets` | `Pets` |
| `Co-working` | `Co-working` |
| `Coworking` | `Co-working` |
| `Other` | `Other` |
| `<empty>` / `<missing>` / `None` | (drop row) |

### Variant table (subcategory — examples)

The data has noisier subcategories. Normalized at ingest:

| Raw subcategory | Normalized |
|---|---|
| `Nails` | `Nail Salon` |
| `Studio` (under Beauty) | `Spa` |
| `Hair` | `Hair Salon` |
| `Wellness` | `Wellness` |
| `Photography` | `Photography & Video` |
| `DJ` | `DJ / Music` |
| `Towing` | `Towing & Roadside` |

The full normalization table lives in `api/internal/ingest/taxonomy.go`
as a `map[string]string` keyed by `(rawCategory, rawSubcategory)`.

### Google-API leak handling

Raw categories like `Tobacco shop`, `Cigar shop`, `Smoke shop`, `Insurance
agency`, `Trucking company`, `Pinball machine supplier` (one row each) all
get bucketed:

```
category    = 'Other'
subcategory = (original raw subcategory, preserved as-is)
archetype   = 'low_stakes_fast_nearby'
```

A histogram of every raw → "Other" mapping is logged at ingest. If any
category has > 20 rows, it's a candidate for a real spec mapping in Stage
3 (revisit during the polish pass).

## Specialty (third level)

Spec mentions Specialty / Preference / Sub-Sub. Examples:
- under Restaurant: `American, Mexican, Italian, Japanese, Chinese, Thai, …`
- under Gym: `Commercial, Boutique, CrossFit, …`

Stored as free text in `specialty` column. Indexed indirectly via
`search_vector` (weight C). Not normalized.

## Tags vs taxonomy

The data also has `universal_tags` and `specific_tags` arrays (see
[schema.md](schema.md)). These are not part of the spec taxonomy but are
the primary lever for the intent overlay (`cheap`, `kid-friendly`,
`date-night`, etc.). They're orthogonal to category/subcategory.

## Cross-references

- Schema: [schema.md](schema.md)
- Ingestion: [ingestion.md](ingestion.md)
- Data quality: [quality.md](quality.md)
- Ranking semantics: [../ranking/semantics.md](../ranking/semantics.md)
- Implementation: `api/internal/ingest/taxonomy.go` (lands at Stage 1)
