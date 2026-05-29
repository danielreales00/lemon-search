package domain

// Overlay is the filter/boost narrowing produced by the intent extractor and
// consumed by the retrieval adapter. The zero value is a no-op (broad search).
// It does NOT force archetype assignment (see ranking decision D6) — it only
// narrows which candidates retrieval returns. Contract C5
// (docs/roadmap/05-architectural-contracts.md).
//
// It lives in domain (not intent) because the postgres adapter must consume it
// and arch-lint restricts retrieve/postgres to domain-only internal deps.
type Overlay struct {
	CategoryFilter     *string  // sole category, e.g. "Food & Drinks"; nil = any
	SubcategoryFilter  []string // any-of; empty = any
	UniversalTagFilter []string // array-overlap (&&); empty = any
	SpecificTagFilter  []string // array-overlap (&&); empty = any
	PriceFilter        []string // any-of, e.g. {"$","$$"}; empty = any
	RequireOpenNow     bool     // true → drop closed candidates
}

// IsZero reports whether the overlay adds no constraints (broad search).
func (o Overlay) IsZero() bool {
	return o.CategoryFilter == nil &&
		len(o.SubcategoryFilter) == 0 &&
		len(o.UniversalTagFilter) == 0 &&
		len(o.SpecificTagFilter) == 0 &&
		len(o.PriceFilter) == 0 &&
		!o.RequireOpenNow
}
