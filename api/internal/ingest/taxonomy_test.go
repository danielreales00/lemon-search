package ingest

import (
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

func TestNormalizeCategoryVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"spec food identity", "Food & Drinks", "Food & Drinks"},
		{"spec beauty identity", "Beauty", "Beauty"},
		{"fitness expands", "Fitness", "Fitness & Wellness"},
		{"home to home improvement", "Home", "Home Improvement"},
		{"home services to home improvement", "Home Services", "Home Improvement"},
		{"timesavers spaced", "TimeSavers", "Time Savers"},
		{"time savers identity", "Time Savers", "Time Savers"},
		{"car slash auto", "Car/Auto", "Car"},
		{"auto title case", "Auto", "Car"},
		{"auto lower case", "auto", "Car"},
		{"coworking compact", "Coworking", "Co-working"},
		{"co-working hyphen", "Co-working", "Co-working"},
		{"whitespace tolerant", "  Beauty  ", "Beauty"},
		{"case tolerant", "FOOD & DRINKS", "Food & Drinks"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.raw, "")
			if got.Decision != TaxonomyKeep {
				t.Fatalf("Normalize(%q) decision = %v, want TaxonomyKeep", tc.raw, got.Decision)
			}
			if got.Category != tc.want {
				t.Errorf("Normalize(%q) category = %q, want %q", tc.raw, got.Category, tc.want)
			}
		})
	}
}

func TestNormalizeSubcategoryVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rawCat string
		rawSub string
		want   string
	}{
		{"nails to nail salon", "Beauty", "Nails", "Nail Salon"},
		{"hair to hair salon", "Beauty", "Hair", "Hair Salon"},
		{"studio under beauty to spa", "Beauty", "Studio", "Spa"},
		{"studio under fitness unchanged", "Fitness", "Studio", "Studio"},
		{"photography expands", "Events", "Photography", "Photography & Video"},
		{"dj to dj music", "Events", "DJ", "DJ / Music"},
		{"towing expands", "Car", "Towing", "Towing & Roadside"},
		{"unknown subcategory passthrough", "Beauty", "Eyebrow Threading", "Eyebrow Threading"},
		{"subcategory trimmed", "Beauty", "  Nails  ", "Nail Salon"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.rawCat, tc.rawSub)
			if got.Subcategory != tc.want {
				t.Errorf("Normalize(%q,%q) subcategory = %q, want %q",
					tc.rawCat, tc.rawSub, got.Subcategory, tc.want)
			}
		})
	}
}

func TestNormalizeArchetypeByCategory(t *testing.T) {
	t.Parallel()

	// Every spec category resolves to exactly one of the six archetypes via the
	// category fallback (no subcategory given).
	cases := []struct {
		raw  string
		want domain.Archetype
	}{
		{"Food & Drinks", domain.ArchetypeLowStakesFastNearby},
		{"Beauty", domain.ArchetypeMediumStakesOccasion},
		{"Fitness & Wellness", domain.ArchetypeMediumStakesOccasion},
		{"Home Improvement", domain.ArchetypeHighStakesOneTime},
		{"Time Savers", domain.ArchetypeRecurringService},
		{"Pets", domain.ArchetypeRecurringService},
		{"Events", domain.ArchetypeHighStakesOneTime},
		{"Car", domain.ArchetypeMediumStakesOccasion},
		{"Activities & Experiences", domain.ArchetypeExperiential},
		{"Co-working", domain.ArchetypeRecurringService},
		{"Grocery", domain.ArchetypeUtilityDistanceDominant},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.raw, "")
			if got.Decision != TaxonomyKeep {
				t.Fatalf("Normalize(%q) decision = %v, want TaxonomyKeep", tc.raw, got.Decision)
			}
			if got.Archetype != tc.want {
				t.Errorf("Normalize(%q) archetype = %q, want %q", tc.raw, got.Archetype, tc.want)
			}
		})
	}
}

func TestNormalizeArchetypeBySubcategory(t *testing.T) {
	t.Parallel()

	// Subcategory splits: these override the category default.
	cases := []struct {
		name   string
		rawCat string
		rawSub string
		want   domain.Archetype
	}{
		{"pets grooming recurring", "Pets", "Grooming", domain.ArchetypeRecurringService},
		{"pets walking recurring", "Pets", "Walking", domain.ArchetypeRecurringService},
		{"pets boarding recurring", "Pets", "Boarding & Sitting", domain.ArchetypeRecurringService},
		{"pets vet medium", "Pets", "Vet", domain.ArchetypeMediumStakesOccasion},
		{"pets training medium", "Pets", "Training", domain.ArchetypeMediumStakesOccasion},

		{"car repair medium", "Car", "Repair", domain.ArchetypeMediumStakesOccasion},
		{"car body paint medium", "Car", "Body & Paint", domain.ArchetypeMediumStakesOccasion},
		{"car detailing low", "Car", "Detailing", domain.ArchetypeLowStakesFastNearby},
		{"car tires utility", "Car", "Tires", domain.ArchetypeUtilityDistanceDominant},
		{"car towing utility", "Car", "Towing & Roadside", domain.ArchetypeUtilityDistanceDominant},
		{"car glass utility", "Car", "Glass Repair", domain.ArchetypeUtilityDistanceDominant},

		{"coworking day pass utility", "Co-working", "Day Pass", domain.ArchetypeUtilityDistanceDominant},
		{"coworking meeting room utility", "Co-working", "Meeting Room", domain.ArchetypeUtilityDistanceDominant},
		{"coworking hot desk recurring", "Co-working", "Hot Desk", domain.ArchetypeRecurringService},
		{"coworking private office recurring", "Co-working", "Private Office", domain.ArchetypeRecurringService},
		{"coworking virtual office recurring", "Co-working", "Virtual Office", domain.ArchetypeRecurringService},

		{"grocery supermarket utility", "Grocery", "Supermarket", domain.ArchetypeUtilityDistanceDominant},
		{"grocery convenience utility", "Grocery", "Convenience Store", domain.ArchetypeUtilityDistanceDominant},
		{"grocery organic low", "Grocery", "Organic / Health Food", domain.ArchetypeLowStakesFastNearby},
		{"grocery international low", "Grocery", "International / Ethnic", domain.ArchetypeLowStakesFastNearby},
		{"grocery butcher low", "Grocery", "Butcher / Seafood", domain.ArchetypeLowStakesFastNearby},
		{"grocery farmers market low", "Grocery", "Farmers Market", domain.ArchetypeLowStakesFastNearby},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.rawCat, tc.rawSub)
			if got.Decision != TaxonomyKeep {
				t.Fatalf("Normalize(%q,%q) decision = %v, want TaxonomyKeep",
					tc.rawCat, tc.rawSub, got.Decision)
			}
			if got.Archetype != tc.want {
				t.Errorf("Normalize(%q,%q) archetype = %q, want %q",
					tc.rawCat, tc.rawSub, got.Archetype, tc.want)
			}
		})
	}
}

func TestNormalizeSubcategoryNormalizedBeforeArchetype(t *testing.T) {
	t.Parallel()

	// The archetype override is keyed by the normalized subcategory, so a raw
	// "Towing" must still resolve to the utility archetype after being expanded
	// to "Towing & Roadside".
	got := Normalize("Car", "Towing")
	if got.Subcategory != "Towing & Roadside" {
		t.Fatalf("subcategory = %q, want %q", got.Subcategory, "Towing & Roadside")
	}
	if got.Archetype != domain.ArchetypeUtilityDistanceDominant {
		t.Errorf("archetype = %q, want %q", got.Archetype, domain.ArchetypeUtilityDistanceDominant)
	}
}

func TestNormalizeDrop(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"none literal", "None"},
		{"none lower", "none"},
		{"none mixed case", "NoNe"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.raw, "Whatever")
			if got.Decision != TaxonomyDrop {
				t.Errorf("Normalize(%q) decision = %v, want TaxonomyDrop", tc.raw, got.Decision)
			}
		})
	}
}

func TestNormalizeBucketed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		rawCat string
		rawSub string
	}{
		{"tobacco shop", "Tobacco shop", "Cigars"},
		{"insurance agency", "Insurance agency", "Auto Insurance"},
		{"trucking company", "Trucking company", ""},
		{"pinball supplier", "Pinball machine supplier", "Parts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Normalize(tc.rawCat, tc.rawSub)
			if got.Decision != TaxonomyBucketed {
				t.Fatalf("Normalize(%q) decision = %v, want TaxonomyBucketed", tc.rawCat, got.Decision)
			}
			if got.Category != "Other" {
				t.Errorf("Normalize(%q) category = %q, want %q", tc.rawCat, got.Category, "Other")
			}
			if got.Archetype != domain.ArchetypeLowStakesFastNearby {
				t.Errorf("Normalize(%q) archetype = %q, want %q",
					tc.rawCat, got.Archetype, domain.ArchetypeLowStakesFastNearby)
			}
			if got.Subcategory != tc.rawSub {
				t.Errorf("Normalize(%q,%q) subcategory = %q, want raw %q preserved",
					tc.rawCat, tc.rawSub, got.Subcategory, tc.rawSub)
			}
			if got.RawCategory != tc.rawCat {
				t.Errorf("Normalize(%q) raw category = %q, want %q", tc.rawCat, got.RawCategory, tc.rawCat)
			}
		})
	}
}

func TestNormalizeExplicitOtherKept(t *testing.T) {
	t.Parallel()

	// A literal "Other" category is a known spec value (not a leak), so it is
	// kept rather than bucketed, but resolves to the same conservative archetype.
	got := Normalize("Other", "Misc")
	if got.Decision != TaxonomyKeep {
		t.Fatalf("decision = %v, want TaxonomyKeep", got.Decision)
	}
	if got.Category != "Other" {
		t.Errorf("category = %q, want %q", got.Category, "Other")
	}
	if got.Archetype != domain.ArchetypeLowStakesFastNearby {
		t.Errorf("archetype = %q, want %q", got.Archetype, domain.ArchetypeLowStakesFastNearby)
	}
}
