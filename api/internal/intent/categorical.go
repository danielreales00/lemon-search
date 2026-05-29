package intent

// IsCategorical reports whether q is composed entirely of category/cuisine/
// domain words — i.e. every normalized token is covered by a lexicon entry that
// narrows the candidate set (category, subcategory, specific tag, or universal
// tag). It is the guard the search handler uses to suppress the exact-name pin:
// a query like "coffee" or "spa" names a category, not one business, so pinning
// a business literally named that over-fires.
//
// A query of only price/time modifiers (e.g. "cheap", "open now") is NOT
// categorical — those entries narrow by price/open-now, not by category, so the
// query is still a free-text search that the pin may legitimately serve.
//
// Pure: same query → same answer. Empty query → false.
func IsCategorical(q string) bool {
	tokens := normalize(q)
	if len(tokens) == 0 {
		return false
	}
	for i, t := range tokens {
		if categoryLike(t) {
			continue
		}
		// A bigram entry (e.g. "date night") covers tokens its unigrams don't.
		if i > 0 && categoryLike(tokens[i-1]+" "+t) {
			continue
		}
		if i+1 < len(tokens) && categoryLike(t+" "+tokens[i+1]) {
			continue
		}
		return false
	}
	return true
}

// categoryLike reports whether term is a lexicon entry that contributes a
// category-shaped narrowing (category / subcategory / specific tag / universal
// tag). Price-only and open-now-only entries return false.
func categoryLike(term string) bool {
	r, ok := lexicon[term]
	if !ok {
		return false
	}
	return r.category != "" ||
		len(r.subcategory) > 0 ||
		len(r.specific) > 0 ||
		len(r.universal) > 0
}
