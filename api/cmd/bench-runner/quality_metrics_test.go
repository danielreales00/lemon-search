package main

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

// cand builds a minimal candidate; the helpers below set only the fields the
// metric under test reads, so each test case stays focused.
func cand() domain.Candidate { return domain.Candidate{ID: uuid.New()} }

func ptrF(f float64) *float64 { return &f }
func ptrS(s string) *string   { return &s }
func ptrB(b bool) *bool       { return &b }

func ranked(cs ...domain.Candidate) []rank.Result {
	out := make([]rank.Result, len(cs))
	for i, c := range cs {
		out[i] = rank.Result{Candidate: c}
	}
	return out
}

const eps = 1e-9

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestDistanceStats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		dists             []float64
		wantMean, wantMed float64
	}{
		{"empty", nil, 0, 0},
		{"single", []float64{4}, 4, 4},
		{"odd", []float64{1, 3, 8}, 4, 3},
		{"even", []float64{2, 4, 6, 8}, 5, 5},
		{"excludes-noloc", []float64{2, 4, noLocDistanceKM}, 3, 3},
		{"all-noloc", []float64{noLocDistanceKM, noLocDistanceKM}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cs := make([]domain.Candidate, 0, len(tc.dists))
			for _, d := range tc.dists {
				c := cand()
				c.DistanceKM = d
				cs = append(cs, c)
			}
			mean, med := distanceStats(cs)
			approx(t, "mean", mean, tc.wantMean)
			approx(t, "median", med, tc.wantMed)
		})
	}
}

func TestMeanRating(t *testing.T) {
	t.Parallel()
	c1, c2, c3 := cand(), cand(), cand()
	c1.LemonScore = ptrF(9.0)
	c2.LemonScore = ptrF(8.0)
	c3.LemonScore = nil // skipped, not counted as 0
	approx(t, "two-rated-one-nil", meanRating([]domain.Candidate{c1, c2, c3}), 0.85)

	cn := cand()
	cn.LemonScore = nil
	approx(t, "all-nil", meanRating([]domain.Candidate{cn}), 0)
}

func TestMeanLogReviews(t *testing.T) {
	t.Parallel()
	c := cand()
	c.GoogleReviewCount = 0
	approx(t, "zero-reviews", meanLogReviews([]domain.Candidate{c}), 0)

	cMax := cand()
	cMax.GoogleReviewCount = int(popularityGlobalMax)
	// log(1+10000)/log(1+10000) = 1.0 at the ceiling.
	approx(t, "ceiling", meanLogReviews([]domain.Candidate{cMax}), 1.0)
}

func TestPctOpen(t *testing.T) {
	t.Parallel()
	open, closed, unknown := cand(), cand(), cand()
	open.IsOpenNow = ptrB(true)
	closed.IsOpenNow = ptrB(false)
	unknown.IsOpenNow = nil
	// 1 of 3 explicitly open; unknown counts as not-open.
	approx(t, "one-of-three", pctOpen([]domain.Candidate{open, closed, unknown}), 1.0/3.0)
}

func TestCategoryPrecision(t *testing.T) {
	t.Parallel()
	sushi, pizza := cand(), cand()
	sushi.Category = "Food & Drinks"
	sushi.Subcategory = ptrS("Japanese")
	pizza.Subcategory = ptrS("Pizzeria")
	want := []string{"japanese", "sushi"}
	approx(t, "half", categoryPrecision([]domain.Candidate{sushi, pizza}, want), 0.5)
	approx(t, "no-tokens", categoryPrecision([]domain.Candidate{sushi}, nil), 0)
}

func TestIntentAdherence(t *testing.T) {
	t.Parallel()
	cheapA, cheapB, pricey := cand(), cand(), cand()
	cheapA.PriceRange = ptrS("$")
	cheapB.PriceRange = ptrS("$$")
	pricey.PriceRange = ptrS("$$$$")
	cs := []domain.Candidate{cheapA, cheapB, pricey}

	approx(t, "cheap", intentAdherence("cheap", cs), 2.0/3.0)
	approx(t, "fancy", intentAdherence("fancy", cs), 1.0/3.0)

	noPrice := cand()
	noPrice.PriceRange = nil
	approx(t, "cheap-no-price", intentAdherence("cheap", []domain.Candidate{noPrice}), intentNA)
	approx(t, "vibe-na", intentAdherence("work", cs), intentNA)

	open, closed := cand(), cand()
	open.IsOpenNow = ptrB(true)
	closed.IsOpenNow = ptrB(false)
	approx(t, "open_now", intentAdherence("open_now", []domain.Candidate{open, closed}), 0.5)
}

func TestClaimedPct(t *testing.T) {
	t.Parallel()
	a, b := cand(), cand()
	a.IsClaimed = true
	b.IsClaimed = false
	approx(t, "half", claimedPct([]domain.Candidate{a, b}), 0.5)
}

func TestDiversity(t *testing.T) {
	t.Parallel()
	p1, p2, s := cand(), cand(), cand()
	p1.Name = "Panther Coffee - Wynwood"
	p2.Name = "Panther Coffee - Brickell"
	s.Name = "Sokai Sushi Bar"
	distinct, ratio := diversity([]domain.Candidate{p1, p2, s})
	if distinct != 2 {
		t.Errorf("distinct = %d, want 2 (Panther siblings collapse)", distinct)
	}
	approx(t, "ratio", ratio, 2.0/3.0)
}

func TestNameStem(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Panther Coffee - Wynwood": "panther coffee",
		"Sokai Sushi Bar Doral":    "sokai sushi",
		"Starbucks":                "starbucks",
		"  El  Sitio  ":            "el sitio",
	}
	for in, want := range cases {
		if got := nameStem(in); got != want {
			t.Errorf("nameStem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoldenPrecisionAt5(t *testing.T) {
	t.Parallel()
	mk := func(name string) domain.Candidate { c := cand(); c.Name = name; return c }
	top := ranked(
		mk("Sokai Sushi Bar Doral"),
		mk("Bonsai Sushi Bar"),
		mk("Other A"),
		mk("Other B"),
		mk("Other C"),
		mk("Taikin Sushi"), // position 6, outside top-5
	)
	// "Sokai Sushi Bar" matches by stem (in top-5); "Taikin" is at rank 6 → miss.
	approx(t, "one-of-two", goldenPrecisionAt5([]string{"Sokai Sushi Bar", "Taikin Sushi Asian Cuisine"}, top), 0.5)
	approx(t, "no-anchors", goldenPrecisionAt5(nil, top), intentNA)
}

func TestComputeMetricsEmpty(t *testing.T) {
	t.Parallel()
	m := computeMetrics(qualityQuery{Intent: "cheap"}, nil)
	if m.N != 0 {
		t.Errorf("N = %d, want 0", m.N)
	}
	if m.IntentAdherence != intentNA {
		t.Errorf("IntentAdherence = %v, want NA for empty", m.IntentAdherence)
	}
}

func TestComputeMetricsNewAtRank1(t *testing.T) {
	t.Parallel()
	first, second := cand(), cand()
	first.IsNew = true
	first.LemonScore = ptrF(9)
	second.LemonScore = ptrF(8)
	m := computeMetrics(qualityQuery{}, ranked(first, second))
	if !m.NewAtRank1 {
		t.Error("NewAtRank1 = false, want true when rank-1 is new")
	}
}

func TestAggregateQuality(t *testing.T) {
	t.Parallel()
	good := cand()
	good.LemonScore = ptrF(9)
	good.DistanceKM = 4
	good.Category = "Food & Drinks"
	good.Subcategory = ptrS("Japanese")
	good.IsClaimed = true

	rows := []qualityRow{
		{query: qualityQuery{CategoryMatch: []string{"japanese"}}, metrics: computeMetrics(qualityQuery{CategoryMatch: []string{"japanese"}}, ranked(good)), golden: 1.0},
		{err: errSkip},                  // errored row excluded
		{metrics: qualityMetrics{N: 0}}, // empty row excluded
	}
	a := aggregateQuality(rows)
	if a.n != 1 {
		t.Fatalf("n = %d, want 1 (error + empty excluded)", a.n)
	}
	approx(t, "meanDistance", a.meanDistanceKM, 4)
	approx(t, "categoryPrecision", a.categoryPrecision, 1.0)
	approx(t, "golden", a.goldenAt5, 1.0)
}

var errSkip = errSentinel("skip")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }
