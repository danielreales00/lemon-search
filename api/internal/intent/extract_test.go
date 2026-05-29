package intent

import (
	"slices"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

func strptr(s string) *string { return &s }

func overlayEqual(a, b domain.Overlay) bool {
	if (a.CategoryFilter == nil) != (b.CategoryFilter == nil) {
		return false
	}
	if a.CategoryFilter != nil && *a.CategoryFilter != *b.CategoryFilter {
		return false
	}
	return slices.Equal(a.SubcategoryFilter, b.SubcategoryFilter) &&
		slices.Equal(a.UniversalTagFilter, b.UniversalTagFilter) &&
		slices.Equal(a.SpecificTagFilter, b.SpecificTagFilter) &&
		slices.Equal(a.PriceFilter, b.PriceFilter) &&
		a.RequireOpenNow == b.RequireOpenNow
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		q    string
		want domain.Overlay
	}{
		{"price unigram", "cheap", domain.Overlay{PriceFilter: []string{"$", "$$"}}},
		{
			"cheap restaurants → price + category",
			"cheap restaurants",
			domain.Overlay{CategoryFilter: strptr("Food & Drinks"), PriceFilter: []string{"$", "$$"}},
		},
		{
			"fancy → price + upscale tag",
			"fancy",
			domain.Overlay{PriceFilter: []string{"$$$", "$$$$"}, UniversalTagFilter: []string{"upscale"}},
		},
		{"open now bigram", "open now", domain.Overlay{RequireOpenNow: true}},
		{
			"idiomatic i'm hungry → food + open now",
			"I'm hungry",
			domain.Overlay{CategoryFilter: strptr("Food & Drinks"), RequireOpenNow: true},
		},
		{
			"date night bigram (trailing word ignored)",
			"date night spot",
			domain.Overlay{UniversalTagFilter: []string{"date-night"}},
		},
		{
			"wedding photographer → events + subcats, deduped",
			"wedding photographer",
			domain.Overlay{
				CategoryFilter: strptr("Events"),
				SubcategoryFilter: []string{
					"Weddings", "Photography & Video", "DJ / Music", "Florist", "Catering",
				},
			},
		},
		{
			"emergency tow → open now + towing subcat",
			"emergency tow",
			domain.Overlay{RequireOpenNow: true, SubcategoryFilter: []string{"Towing & Roadside"}},
		},
		{
			"multi-family union: cheap kid friendly tacos",
			"cheap kid friendly tacos",
			domain.Overlay{
				CategoryFilter:     strptr("Food & Drinks"),
				UniversalTagFilter: []string{"kid-friendly", "family-friendly"},
				SpecificTagFilter:  []string{"tacos"},
				PriceFilter:        []string{"$", "$$"},
			},
		},
		{"accented non-lexicon term → zero", "Café", domain.Overlay{}},
		{"real business name → zero (no false intent)", "joes barber shop", domain.Overlay{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Extract(tc.q); !overlayEqual(got, tc.want) {
				t.Fatalf("Extract(%q) =\n  %+v\nwant\n  %+v", tc.q, got, tc.want)
			}
		})
	}
}

func TestExtractIsZero(t *testing.T) {
	if !Extract("joes barber shop").IsZero() {
		t.Fatal("non-lexicon query should yield a zero overlay")
	}
	if Extract("cheap").IsZero() {
		t.Fatal("lexicon hit should yield a non-zero overlay")
	}
}
