package intent

import (
	"slices"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// Extract maps head terms in a raw query to a filter/boost overlay that narrows
// retrieval. It walks every unigram and consecutive bigram against the lexicon,
// merging matches additively: *string fields take the last write, slices union
// (deduped), bools OR. No lexicon hit → zero value (broad search). Pure and
// sub-millisecond. See docs/ranking/intent.md.
func Extract(q string) domain.Overlay {
	tokens := normalize(q)
	var ov domain.Overlay
	for i, t := range tokens {
		mergeTerm(&ov, t)
		if i+1 < len(tokens) {
			mergeTerm(&ov, t+" "+tokens[i+1])
		}
	}
	return ov
}

func mergeTerm(ov *domain.Overlay, term string) {
	r, ok := lexicon[term]
	if !ok {
		return
	}
	if r.category != "" {
		c := r.category
		ov.CategoryFilter = &c
	}
	ov.SubcategoryFilter = appendUnique(ov.SubcategoryFilter, r.subcategory...)
	ov.UniversalTagFilter = appendUnique(ov.UniversalTagFilter, r.universal...)
	ov.SpecificTagFilter = appendUnique(ov.SpecificTagFilter, r.specific...)
	ov.PriceFilter = appendUnique(ov.PriceFilter, r.price...)
	if r.openNow {
		ov.RequireOpenNow = true
	}
}

func appendUnique(dst []string, vals ...string) []string {
	for _, v := range vals {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}
