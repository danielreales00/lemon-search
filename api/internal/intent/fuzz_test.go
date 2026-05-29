package intent

import (
	"slices"
	"testing"
)

// fuzzSeeds spans the input classes intent.Extract must tolerate: blank,
// whitespace, lexicon hits, accents, idioms, invalid UTF-8, control chars,
// emoji, and pathologically long strings.
var fuzzSeeds = []string{
	"",
	" ",
	"\t\n",
	"cheap",
	"cheap kid friendly tacos",
	"open now",
	"I'm hungry",
	"wedding photographer",
	"emergency tow",
	"café",
	"niño",
	"crème brûlée",
	"joes barber shop",
	"!@#$%^&*()",
	"🍋🍕☕️",
	"best 🍕 near me",
	"拉麵 寿司",
	"'; DROP TABLE businesses;--",
	"' OR '1'='1",
	"a\x00b\x01c",
	"\xff\xfe\xfd", // invalid UTF-8
	"caf\xc3",      // truncated multibyte sequence
	"coffee\tshop\tnear",
}

// FuzzExtract asserts intent.Extract never panics and returns a structurally
// usable Overlay for ANY input. It does not assert specific overlay contents
// (the lexicon is still in flux); it asserts robustness + internal invariants:
// IsZero is callable, and the dedup contract on every filter slice holds.
func FuzzExtract(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, q string) {
		ov := Extract(q)

		// IsZero must be safe to call on the result of any input.
		_ = ov.IsZero()

		// Every filter slice is built via appendUnique, so it must stay
		// duplicate-free regardless of how degenerate the query was.
		assertNoDup(t, q, "subcategory", ov.SubcategoryFilter)
		assertNoDup(t, q, "universal_tag", ov.UniversalTagFilter)
		assertNoDup(t, q, "specific_tag", ov.SpecificTagFilter)
		assertNoDup(t, q, "price", ov.PriceFilter)

		// A non-nil CategoryFilter must point at a non-empty string: mergeTerm
		// only ever assigns a non-empty lexicon category.
		if ov.CategoryFilter != nil && *ov.CategoryFilter == "" {
			t.Fatalf("Extract(%q): CategoryFilter set to empty string", q)
		}

		// Extract is pure: a second call on the same input must match.
		if got := Extract(q); !overlayEqual(ov, got) {
			t.Fatalf("Extract(%q) not deterministic", q)
		}
	})
}

func assertNoDup(t *testing.T, q, field string, vals []string) {
	t.Helper()
	for i := range vals {
		if slices.Contains(vals[i+1:], vals[i]) {
			t.Fatalf("Extract(%q): duplicate %s filter entry %q in %v", q, field, vals[i], vals)
		}
	}
}
