package ingest

import (
	"strings"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// TaxonomyDecision is the outcome of normalizing one record's raw
// category/subcategory. The three outcomes drive different ingest paths: Keep
// rows are stored with their spec taxonomy, Bucketed rows are stored under
// "Other" and counted in an end-of-run histogram, and Drop rows are discarded.
type TaxonomyDecision int

const (
	// TaxonomyKeep means the raw category mapped to a spec category.
	TaxonomyKeep TaxonomyDecision = iota
	// TaxonomyBucketed means the raw category matched no spec category and was
	// filed under "Other" (a Google-API leak). RawCategory carries the original.
	TaxonomyBucketed
	// TaxonomyDrop means the category was empty/missing/"None"; the caller
	// discards the row.
	TaxonomyDrop
)

// Taxonomy is the normalized taxonomy for one record plus the decision and the
// original raw category (kept so the caller can log an Other-histogram).
type Taxonomy struct {
	Category    string
	Subcategory string
	Archetype   domain.Archetype
	Decision    TaxonomyDecision
	RawCategory string
}

const categoryOther = "Other"

// categoryVariants maps a lower-cased, trimmed raw category to its spec
// category. Identity rows are included so a single lookup both validates and
// normalizes; absence from this map (and from the empty/None check) is what
// triggers bucketing to "Other".
var categoryVariants = map[string]string{
	"food & drinks":            "Food & Drinks",
	"beauty":                   "Beauty",
	"fitness":                  "Fitness & Wellness",
	"fitness & wellness":       "Fitness & Wellness",
	"activities & experiences": "Activities & Experiences",
	"home":                     "Home Improvement",
	"home services":            "Home Improvement",
	"home improvement":         "Home Improvement",
	"grocery":                  "Grocery",
	"timesavers":               "Time Savers",
	"time savers":              "Time Savers",
	"car/auto":                 "Car",
	"auto":                     "Car",
	"car":                      "Car",
	"events":                   "Events",
	"pets":                     "Pets",
	"co-working":               "Co-working",
	"coworking":                "Co-working",
	"other":                    categoryOther,
}

// subcategoryVariants normalizes noisy raw subcategories. It is keyed by
// "specCategory|trimmedRawSubcategory" because the same raw value maps
// differently per category (e.g. "Studio" → "Spa" under Beauty, but stays
// "Studio" under Fitness & Wellness, which has no entry here).
var subcategoryVariants = map[string]string{
	"Beauty|Nails":       "Nail Salon",
	"Beauty|Hair":        "Hair Salon",
	"Beauty|Studio":      "Spa",
	"Events|Photography": "Photography & Video",
	"Events|DJ":          "DJ / Music",
	"Car|Towing":         "Towing & Roadside",
}

// categoryArchetype is the per-category fallback archetype, used when no
// (category, subcategory) override applies.
var categoryArchetype = map[string]domain.Archetype{
	"Food & Drinks":            domain.ArchetypeLowStakesFastNearby,
	"Beauty":                   domain.ArchetypeMediumStakesOccasion,
	"Fitness & Wellness":       domain.ArchetypeMediumStakesOccasion,
	"Home Improvement":         domain.ArchetypeHighStakesOneTime,
	"Time Savers":              domain.ArchetypeRecurringService,
	"Pets":                     domain.ArchetypeRecurringService,
	"Events":                   domain.ArchetypeHighStakesOneTime,
	"Car":                      domain.ArchetypeMediumStakesOccasion,
	"Activities & Experiences": domain.ArchetypeExperiential,
	"Co-working":               domain.ArchetypeRecurringService,
	"Grocery":                  domain.ArchetypeUtilityDistanceDominant,
	categoryOther:              domain.ArchetypeLowStakesFastNearby,
}

// subcategoryArchetype overrides the category default for subcategories whose
// demand shape differs from their category's. Keyed by "category|subcategory"
// using normalized (spec) values. Categories with a single archetype across all
// subcategories are absent here and resolve via categoryArchetype.
var subcategoryArchetype = map[string]domain.Archetype{
	"Pets|Vet":      domain.ArchetypeMediumStakesOccasion,
	"Pets|Training": domain.ArchetypeMediumStakesOccasion,

	"Car|Repair":            domain.ArchetypeMediumStakesOccasion,
	"Car|Body & Paint":      domain.ArchetypeMediumStakesOccasion,
	"Car|Detailing":         domain.ArchetypeLowStakesFastNearby,
	"Car|Tires":             domain.ArchetypeUtilityDistanceDominant,
	"Car|Towing & Roadside": domain.ArchetypeUtilityDistanceDominant,
	"Car|Glass Repair":      domain.ArchetypeUtilityDistanceDominant,

	"Co-working|Day Pass":       domain.ArchetypeUtilityDistanceDominant,
	"Co-working|Meeting Room":   domain.ArchetypeUtilityDistanceDominant,
	"Co-working|Hot Desk":       domain.ArchetypeRecurringService,
	"Co-working|Private Office": domain.ArchetypeRecurringService,
	"Co-working|Virtual Office": domain.ArchetypeRecurringService,

	"Grocery|Supermarket":            domain.ArchetypeUtilityDistanceDominant,
	"Grocery|Convenience Store":      domain.ArchetypeUtilityDistanceDominant,
	"Grocery|Organic / Health Food":  domain.ArchetypeLowStakesFastNearby,
	"Grocery|International / Ethnic": domain.ArchetypeLowStakesFastNearby,
	"Grocery|Butcher / Seafood":      domain.ArchetypeLowStakesFastNearby,
	"Grocery|Farmers Market":         domain.ArchetypeLowStakesFastNearby,
}

// Normalize maps a record's raw category/subcategory to the spec taxonomy and
// assigns an archetype. It is pure (no I/O) so the ingest pipeline can apply it
// per row and the result can be unit-tested against fixtures.
//
// An empty/"none" category drops the row. A category that matches no spec
// category is bucketed under "Other" with its raw subcategory preserved, so the
// caller can keep a histogram of leaked Google-API categories.
func Normalize(rawCategory, rawSubcategory string) Taxonomy {
	raw := strings.TrimSpace(rawCategory)
	sub := strings.TrimSpace(rawSubcategory)

	if raw == "" || strings.EqualFold(raw, "none") {
		return Taxonomy{Decision: TaxonomyDrop, RawCategory: raw}
	}

	category, ok := categoryVariants[strings.ToLower(raw)]
	if !ok {
		return Taxonomy{
			Category:    categoryOther,
			Subcategory: sub,
			Archetype:   domain.ArchetypeLowStakesFastNearby,
			Decision:    TaxonomyBucketed,
			RawCategory: raw,
		}
	}

	subcategory := normalizeSubcategory(category, sub)
	return Taxonomy{
		Category:    category,
		Subcategory: subcategory,
		Archetype:   archetypeFor(category, subcategory),
		Decision:    TaxonomyKeep,
		RawCategory: raw,
	}
}

func normalizeSubcategory(category, rawSubcategory string) string {
	if norm, ok := subcategoryVariants[category+"|"+rawSubcategory]; ok {
		return norm
	}
	return rawSubcategory
}

func archetypeFor(category, subcategory string) domain.Archetype {
	if a, ok := subcategoryArchetype[category+"|"+subcategory]; ok {
		return a
	}
	return categoryArchetype[category]
}
