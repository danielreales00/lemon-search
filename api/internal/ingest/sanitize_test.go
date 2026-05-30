package ingest

import (
	"encoding/json"
	"testing"
)

func TestSanitizeFullRecord(t *testing.T) {
	t.Parallel()

	const raw = `{
		"id": "00046d68-70af-451d-8d11-898d0a05ed3b",
		"name": "  Royal Nails Salon  ",
		"category": " Beauty ",
		"subcategory": "Nails",
		"specialty": null,
		"address": "1874 SW 57 Ave, Miami, FL 33155",
		"neighborhood": "Coral Gables",
		"latitude": 25.74,
		"longitude": -80.29,
		"lemon_score": 10,
		"google_rating": 5,
		"google_review_count": 14,
		"price_range": "mid-range",
		"hours": {"monday": {"closed": true}},
		"photos": ["a.jpg", "a.jpg", "  ", "b.jpg"],
		"about": ["First paragraph.", "  ", "Second paragraph."],
		"universal_tags": ["family-friendly", " "],
		"specific_tags": ["manicure"],
		"is_claimed": true,
		"owner_id": "ignored",
		"embedding": [0.1, 0.2]
	}`

	rb, err := Sanitize(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}

	if rb.Name != "Royal Nails Salon" {
		t.Errorf("Name = %q, want trimmed %q", rb.Name, "Royal Nails Salon")
	}
	if rb.Category != "Beauty" {
		t.Errorf("Category = %q, want trimmed %q", rb.Category, "Beauty")
	}
	if rb.Specialty != nil {
		t.Errorf("Specialty = %v, want nil for null source", rb.Specialty)
	}
	if rb.About == nil || *rb.About != "First paragraph.\n\nSecond paragraph." {
		t.Errorf("About = %v, want joined non-empty paragraphs", rb.About)
	}
	if got := rb.Photos; len(got) != 2 || got[0] != "a.jpg" || got[1] != "b.jpg" {
		t.Errorf("Photos = %v, want deduped non-empty [a.jpg b.jpg]", got)
	}
	if got := rb.UniversalTags; len(got) != 1 || got[0] != "family-friendly" {
		t.Errorf("UniversalTags = %v, want empties dropped", got)
	}
	if !rb.IsClaimed {
		t.Error("IsClaimed = false, want true (real passthrough)")
	}
	if rb.Hours == nil || string(rb.Hours) != `{"monday": {"closed": true}}` {
		t.Errorf("Hours = %s, want raw passthrough", rb.Hours)
	}
	if rb.GoogleReviewCount == nil || *rb.GoogleReviewCount != 14 {
		t.Errorf("GoogleReviewCount = %v, want 14", rb.GoogleReviewCount)
	}
}

func TestSanitizeNullsToNil(t *testing.T) {
	t.Parallel()

	const raw = `{
		"id": "11111111-1111-1111-1111-111111111111",
		"name": "Nullsville",
		"category": "Food & Drinks",
		"subcategory": null,
		"specialty": null,
		"address": null,
		"neighborhood": null,
		"latitude": null,
		"longitude": null,
		"lemon_score": null,
		"google_rating": null,
		"google_review_count": null,
		"price_range": null,
		"hours": null,
		"photos": null,
		"about": null,
		"universal_tags": null,
		"specific_tags": null
	}`

	rb, err := Sanitize(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}

	cases := map[string]bool{
		"Subcategory":  rb.Subcategory == nil,
		"Specialty":    rb.Specialty == nil,
		"Address":      rb.Address == nil,
		"Neighborhood": rb.Neighborhood == nil,
		"Latitude":     rb.Latitude == nil,
		"Longitude":    rb.Longitude == nil,
		"LemonScore":   rb.LemonScore == nil,
		"GoogleRating": rb.GoogleRating == nil,
		"ReviewCount":  rb.GoogleReviewCount == nil,
		"PriceRange":   rb.PriceRange == nil,
		"Hours":        rb.Hours == nil,
		"Photos":       rb.Photos == nil,
		"About":        rb.About == nil,
		"UniTags":      rb.UniversalTags == nil,
		"SpecTags":     rb.SpecificTags == nil,
	}
	for field, isNil := range cases {
		if !isNil {
			t.Errorf("%s: want nil for null source", field)
		}
	}
	if rb.IsClaimed {
		t.Error("IsClaimed = true, want false when absent")
	}
}

func TestSanitizeIsClaimedPassthrough(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want bool
	}{
		{"explicit true", `"is_claimed": true,`, true},
		{"explicit false", `"is_claimed": false,`, false},
		{"missing defaults false", ``, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := `{"id":"22222222-2222-2222-2222-222222222222","name":"Claim Test","category":"Beauty",` +
				tc.body + `"latitude":25.7,"longitude":-80.2}`
			rb, err := Sanitize(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("Sanitize() error = %v", err)
			}
			if rb.IsClaimed != tc.want {
				t.Errorf("IsClaimed = %v, want %v", rb.IsClaimed, tc.want)
			}
		})
	}
}

func TestSanitizeBlankStringsToNil(t *testing.T) {
	t.Parallel()

	const raw = `{
		"id": "33333333-3333-3333-3333-333333333333",
		"name": "Blanks",
		"category": "Pets",
		"subcategory": "   ",
		"price_range": ""
	}`

	rb, err := Sanitize(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	if rb.Subcategory != nil {
		t.Errorf("Subcategory = %v, want nil for whitespace-only", rb.Subcategory)
	}
	if rb.PriceRange != nil {
		t.Errorf("PriceRange = %v, want nil for empty string", rb.PriceRange)
	}
}

func TestSanitizeHoursEmptyObjectKept(t *testing.T) {
	t.Parallel()

	const raw = `{"id":"44444444-4444-4444-4444-444444444444","name":"Empty Hours","category":"Grocery","hours":{}}`
	rb, err := Sanitize(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	if rb.Hours == nil || string(rb.Hours) != `{}` {
		t.Errorf("Hours = %s, want empty object kept (only null → nil)", rb.Hours)
	}
}

func TestSanitizeRejectsBadRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"invalid json", `{"id": "x"`},
		{"missing id", `{"name":"No ID","category":"Beauty"}`},
		{"bad uuid", `{"id":"not-a-uuid","name":"Bad","category":"Beauty"}`},
		{"empty name", `{"id":"55555555-5555-5555-5555-555555555555","name":"  ","category":"Beauty"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Sanitize(json.RawMessage(tc.raw)); err == nil {
				t.Errorf("Sanitize(%s) error = nil, want error", tc.raw)
			}
		})
	}
}

func TestNormalizePrice(t *testing.T) {
	t.Parallel()
	p := func(s string) *string { return &s }
	cases := []struct {
		in   *string
		want *string
	}{
		{nil, nil},
		{p(""), nil},
		{p("  "), nil},
		{p("affordable"), p("$")},
		{p("Mid-Range"), p("$$")},
		{p("premium"), p("$$$")},
		{p("luxury"), p("$$$$")},
		{p("$$"), p("$$")},
		{p("unknown-tier"), nil},
	}
	for _, c := range cases {
		got := normalizePrice(c.in)
		switch {
		case got == nil && c.want != nil:
			t.Errorf("normalizePrice(%v) = nil, want %q", deref(c.in), *c.want)
		case got != nil && c.want == nil:
			t.Errorf("normalizePrice(%v) = %q, want nil", deref(c.in), *got)
		case got != nil && c.want != nil && *got != *c.want:
			t.Errorf("normalizePrice(%v) = %q, want %q", deref(c.in), *got, *c.want)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
