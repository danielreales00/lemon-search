package main

import (
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

func TestScoreSemantic(t *testing.T) {
	t.Parallel()

	sub := func(s string) *string { return &s }
	res := func(cat string, sc *string) rank.Result {
		return rank.Result{Candidate: domain.Candidate{Category: cat, Subcategory: sc}}
	}

	tests := []struct {
		name   string
		expect []string
		ranked []rank.Result
		want   bool
	}{
		{"subcategory substring", []string{"coworking"}, []rank.Result{res("Co-working", sub("Coworking Space"))}, true},
		{"category match", []string{"beauty"}, []rank.Result{res("Beauty", sub("Spa"))}, true},
		{"partial token matches variant (caf → Café)", []string{"caf"}, []rank.Result{res("Food & Drinks", sub("Café"))}, true},
		{"case-insensitive", []string{"SPA"}, []rank.Result{res("Beauty", sub("Med Spa"))}, true},
		{"no match", []string{"tattoo"}, []rank.Result{res("Food & Drinks", sub("Pizzeria"))}, false},
		{"nil subcategory still checks category", []string{"co-working"}, []rank.Result{res("Co-working", nil)}, true},
		{"only the top-3 count", []string{"florist"}, []rank.Result{
			res("Food & Drinks", sub("Bar")),
			res("Beauty", sub("Spa")),
			res("Fitness & Wellness", sub("Gym")),
			res("Events", sub("Florist")), // 4th — must be ignored
		}, false},
		{"empty ranked", []string{"spa"}, nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scoreSemantic(semanticTest{Expect: tc.expect}, tc.ranked); got != tc.want {
				t.Errorf("scoreSemantic() = %v, want %v", got, tc.want)
			}
		})
	}
}
