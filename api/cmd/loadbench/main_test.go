package main

import (
	"math/rand"
	"strings"
	"testing"
)

func TestPctl(t *testing.T) {
	t.Parallel()
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[int]float64{50: 6, 95: 10, 99: 10}
	for p, want := range cases {
		if got := pctl(xs, p); got != want {
			t.Errorf("pctl(p%d) = %v, want %v", p, got, want)
		}
	}
	if got := pctl(nil, 95); got != 0 {
		t.Errorf("pctl(empty) = %v, want 0", got)
	}
}

func TestParseRates(t *testing.T) {
	t.Parallel()
	got, err := parseRates(" 25, 50 ,100")
	if err != nil {
		t.Fatalf("parseRates: %v", err)
	}
	if len(got) != 3 || got[0] != 25 || got[2] != 100 {
		t.Fatalf("parseRates = %v", got)
	}
	if _, err := parseRates("25,nope"); err == nil {
		t.Error("parseRates(bad) = nil error, want error")
	}
	if _, err := parseRates("0"); err == nil {
		t.Error("parseRates(0) = nil error, want error")
	}
}

func TestURLFor(t *testing.T) {
	t.Parallel()
	u := urlFor("http://x:8080/", request{q: "chill place to work", lat: 25.77, lng: -80.19, now: "2026-05-30T13:00:00-04:00"})
	if !strings.HasPrefix(u, "http://x:8080/search?") {
		t.Fatalf("bad prefix: %s", u)
	}
	for _, want := range []string{"q=chill+place+to+work", "lat=25.770000", "lng=-80.190000", "now=2026"} {
		if !strings.Contains(u, want) {
			t.Errorf("url %s missing %q", u, want)
		}
	}
}

// TestPickRespectsWeights checks the weighted sampler lands near the configured
// proportions: a weight-9 query should be sampled ~9x more than a weight-1 one.
func TestPickRespectsWeights(t *testing.T) {
	t.Parallel()
	c := &corpus{
		Points:  []point{{Lat: 1, Lng: 2, Label: "p"}},
		Nows:    []string{"2026-05-30T13:00:00-04:00"},
		Queries: []weightedQuery{{Q: "rare", Weight: 1}, {Q: "common", Weight: 9}},
	}
	rng := rand.New(rand.NewSource(7))
	total := weightTotal(c)
	if total != 10 {
		t.Fatalf("weightTotal = %d, want 10", total)
	}
	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		counts[c.pick(rng, total).q]++
	}
	ratio := float64(counts["common"]) / float64(counts["rare"])
	if ratio < 6 || ratio > 13 {
		t.Errorf("common/rare ratio = %.1f, want ~9 (6..13)", ratio)
	}
}
