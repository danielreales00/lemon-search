package main

import (
	"math"
	"sort"
	"strings"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

// qualityMetrics are the spec-derived quality measures computed over one query's
// top-K results. Every field is a measurable proxy for what the spec says that
// query should prioritize (locality, result quality, open-status, category
// matching, intent adherence, claimed restraint, new-biz suppression, diversity).
// Fields are exported so the report renderer and tests can read them.
type qualityMetrics struct {
	N int // number of ranked results scored (top-K)

	MeanDistanceKM   float64
	MedianDistanceKM float64
	MeanRating       float64 // mean lemon_score/10 over results with a score, 0..1
	MeanLogReviews   float64 // mean log(1+reviews)/log(1+10000), 0..1
	PctOpen          float64 // fraction with is_open_now == true

	CategoryPrecision float64 // fraction whose subcategory/category matches an expected token
	CategoryHasExpect bool    // false when the query carries no category_match tokens

	IntentAdherence float64 // per-intent proxy (see intentAdherence); -1 when N/A
	IntentLabel     string  // the intent this query expresses ("" when none)

	ClaimedPct  float64 // fraction is_claimed
	NewAtRank1  bool    // is the #1 result a new business? (spec: should be false)
	Diversity   float64 // distinct name-stems / N (chains clumping lowers this)
	DistinctNum int     // count of distinct name-stems
}

const (
	popularityGlobalMax = 10000.0 // GLOBAL_MAX_REVIEWS from config; held constant
	intentNA            = -1.0    // intentAdherence sentinel: not applicable to this query
	// noLocDistanceKM is the retrieval sentinel for a candidate with a null loc
	// (search_candidates emits 1e9). Such rows carry no locality signal, so the
	// distance metrics exclude them rather than blow the mean to ~1e9.
	noLocDistanceKM = 1e9
	// lemonScoreMax normalizes lemon_score (0..10) into the rating signal's 0..1
	// range, matching the spec's literal `lemon_score / 10`.
	lemonScoreMax = 10.0
)

// computeMetrics derives all per-query quality metrics from a ranked top-K.
// It is pure: same ranked slice + same query spec => same metrics. The pin's
// +Inf score does not affect any metric (every metric reads candidate fields,
// not scores), so a fired exact-name pin is measured like any other result.
func computeMetrics(q qualityQuery, ranked []rank.Result) qualityMetrics {
	cands := candidatesOf(ranked)
	m := qualityMetrics{N: len(cands)}
	if len(cands) == 0 {
		m.IntentAdherence = intentNA
		m.IntentLabel = q.Intent
		return m
	}

	m.MeanDistanceKM, m.MedianDistanceKM = distanceStats(cands)
	m.MeanRating = meanRating(cands)
	m.MeanLogReviews = meanLogReviews(cands)
	m.PctOpen = pctOpen(cands)

	m.CategoryHasExpect = len(q.CategoryMatch) > 0
	m.CategoryPrecision = categoryPrecision(cands, q.CategoryMatch)

	m.IntentLabel = q.Intent
	m.IntentAdherence = intentAdherence(q.Intent, cands)

	m.ClaimedPct = claimedPct(cands)
	m.NewAtRank1 = cands[0].IsNew
	m.DistinctNum, m.Diversity = diversity(cands)
	return m
}

// candidatesOf flattens the ranked results to their candidates (the only thing
// the metrics read). Order is preserved so rank-1 stays first.
func candidatesOf(ranked []rank.Result) []domain.Candidate {
	out := make([]domain.Candidate, 0, len(ranked))
	for i := range ranked {
		out = append(out, ranked[i].Candidate)
	}
	return out
}

// distanceStats returns the mean and median raw retrieval distance in km over
// the located results (a locality proxy - lower is better for nearby-leaning
// archetypes). Candidates with a null loc (the 1e9 sentinel) carry no locality
// signal and are excluded. Returns 0,0 when no result is located.
func distanceStats(cands []domain.Candidate) (mean, median float64) {
	ds := make([]float64, 0, len(cands))
	var sum float64
	for i := range cands {
		if cands[i].DistanceKM >= noLocDistanceKM {
			continue
		}
		ds = append(ds, cands[i].DistanceKM)
		sum += cands[i].DistanceKM
	}
	if len(ds) == 0 {
		return 0, 0
	}
	mean = sum / float64(len(ds))
	sort.Float64s(ds)
	mid := len(ds) / 2
	if len(ds)%2 == 1 {
		median = ds[mid]
	} else {
		median = (ds[mid-1] + ds[mid]) / 2
	}
	return mean, median
}

// meanRating averages lemon_score/10 over results that have a score. Results
// with a nil lemon_score are skipped (not counted as 0) so the metric reflects
// the quality of the rated places at the top.
func meanRating(cands []domain.Candidate) float64 {
	var sum float64
	var n int
	for i := range cands {
		if cands[i].LemonScore != nil {
			sum += *cands[i].LemonScore / lemonScoreMax
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// meanLogReviews averages the spec's log-scaled popularity signal,
// log(1+n)/log(1+GLOBAL_MAX), over the results (a trust/popularity proxy).
func meanLogReviews(cands []domain.Candidate) float64 {
	denom := math.Log(1 + popularityGlobalMax)
	var sum float64
	for i := range cands {
		n := float64(cands[i].GoogleReviewCount)
		sum += math.Log(1+n) / denom
	}
	return sum / float64(len(cands))
}

// pctOpen is the fraction explicitly open now. Unknown hours (nil) count as
// not-open here (a conservative read: the metric measures *confirmed* open).
func pctOpen(cands []domain.Candidate) float64 {
	var n int
	for i := range cands {
		if cands[i].IsOpenNow != nil && *cands[i].IsOpenNow {
			n++
		}
	}
	return float64(n) / float64(len(cands))
}

// categoryPrecision is the fraction of results whose subcategory or category
// contains any expected token (case-insensitive substring). With no expected
// tokens it returns 0 (callers gate on CategoryHasExpect).
func categoryPrecision(cands []domain.Candidate, want []string) float64 {
	if len(want) == 0 {
		return 0
	}
	var n int
	for i := range cands {
		if matchesCategory(&cands[i], want) {
			n++
		}
	}
	return float64(n) / float64(len(cands))
}

// matchesCategory reports whether a candidate's subcategory+category contains
// any of the wanted lowercase tokens.
func matchesCategory(c *domain.Candidate, want []string) bool {
	hay := strings.ToLower(c.Category)
	if c.Subcategory != nil {
		hay += " " + strings.ToLower(*c.Subcategory)
	}
	for _, w := range want {
		if strings.Contains(hay, strings.ToLower(w)) {
			return true
		}
	}
	return false
}

// intentAdherence is a per-intent measurable proxy for the "smart semantic"
// requirement. It returns intentNA for queries that express no measurable
// price/time intent (the report shows "n/a" there):
//
//   - "cheap"   : fraction priced $ or $$ (over results that carry a price).
//   - "open_now": fraction explicitly open now.
//   - "fancy"   : fraction priced $$$ or $$$$ (over results that carry a price).
//
// Other intent labels are vibe/semantic and have no crisp price/time proxy, so
// they return intentNA and are judged on category_precision instead.
func intentAdherence(label string, cands []domain.Candidate) float64 {
	switch label {
	case "cheap":
		return priceFraction(cands, map[string]bool{"$": true, "$$": true})
	case "fancy":
		return priceFraction(cands, map[string]bool{"$$$": true, "$$$$": true})
	case "open_now":
		return pctOpen(cands)
	default:
		return intentNA
	}
}

// priceFraction returns the fraction of price-carrying results whose price is in
// the wanted set. Results with no price_range are excluded from the denominator
// (they carry no price signal), so the metric reads "of the places we know the
// price of, how many match the intent". Returns intentNA when none carry a price.
func priceFraction(cands []domain.Candidate, want map[string]bool) float64 {
	var n, denom int
	for i := range cands {
		p := cands[i].PriceRange
		if p == nil {
			continue
		}
		denom++
		if want[*p] {
			n++
		}
	}
	if denom == 0 {
		return intentNA
	}
	return float64(n) / float64(denom)
}

// claimedPct is the fraction of claimed results. Compared against the ~20.7%
// dataset base rate in the report: near base rate is healthy, ~2x means claimed
// is dominating the ranking against the spec's "tiebreaker, not override" intent.
func claimedPct(cands []domain.Candidate) float64 {
	var n int
	for i := range cands {
		if cands[i].IsClaimed {
			n++
		}
	}
	return float64(n) / float64(len(cands))
}

// diversity is distinct name-stems / N. The stem is the first two whitespace
// tokens of the lowercased name, so several locations of one chain ("Starbucks
// Coffee Company - …", "Panther Coffee - …") collapse to one stem and lower the
// score when they clump. Returns the distinct count and the ratio.
func diversity(cands []domain.Candidate) (distinct int, ratio float64) {
	seen := make(map[string]struct{}, len(cands))
	for i := range cands {
		seen[nameStem(cands[i].Name)] = struct{}{}
	}
	distinct = len(seen)
	return distinct, float64(distinct) / float64(len(cands))
}

// nameStem reduces a business name to its first two alphanumeric tokens,
// lowercased, so chain siblings collapse. "Panther Coffee - Wynwood" and
// "Panther Coffee - Brickell" both stem to "panther coffee".
func nameStem(name string) string {
	toks := strings.Fields(strings.ToLower(strings.TrimSpace(name)))
	if len(toks) > 2 {
		toks = toks[:2]
	}
	return strings.Join(toks, " ")
}

// goldenPrecisionAt5 returns the fraction of golden anchors that appear in the
// top-5 by name-stem (precision@5 against the hand-picked anchors). Returns -1
// when the query has no golden anchors (the report shows "-"). The match is by
// name-stem so "Sokai Sushi Bar" matches "Sokai Sushi Bar Doral".
func goldenPrecisionAt5(golden []string, ranked []rank.Result) float64 {
	if len(golden) == 0 {
		return intentNA
	}
	top := ranked
	if len(top) > goldenK {
		top = top[:goldenK]
	}
	stems := make(map[string]struct{}, len(top))
	for i := range top {
		stems[nameStem(top[i].Candidate.Name)] = struct{}{}
	}
	var hit int
	for _, g := range golden {
		if _, ok := stems[nameStem(g)]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(golden))
}

const goldenK = 5
