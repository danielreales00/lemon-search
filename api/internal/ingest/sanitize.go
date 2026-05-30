package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// RawBusiness is the projection of a source JSON record onto the columns the
// businesses table needs. Only these fields are decoded; the ~70 other source
// fields (embedding, search_tsv, owner_id, …) are ignored. Nullable scalar
// columns use pointers so a JSON null survives as a SQL NULL rather than a zero
// value.
type RawBusiness struct {
	ID                uuid.UUID
	Name              string
	Category          string
	Subcategory       *string
	Specialty         *string
	Address           *string
	Neighborhood      *string
	Latitude          *float64
	Longitude         *float64
	LemonScore        *float64
	GoogleRating      *float64
	GoogleReviewCount *int
	PriceRange        *string
	Hours             json.RawMessage // null source → nil → SQL NULL
	Photos            []string        // deduped, empties dropped
	About             *string         // source is []string, joined
	UniversalTags     []string
	SpecificTags      []string
	IsClaimed         bool // real passthrough, default false
}

// rawRecord mirrors the source JSON field names and shapes for the subset we
// keep. about is an array of strings in the source; the rest map directly.
// Decoding into typed fields means malformed values for a field surface as a
// decode error rather than silently corrupting another column.
type rawRecord struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Category          string          `json:"category"`
	Subcategory       *string         `json:"subcategory"`
	Specialty         *string         `json:"specialty"`
	Address           *string         `json:"address"`
	Neighborhood      *string         `json:"neighborhood"`
	Latitude          *float64        `json:"latitude"`
	Longitude         *float64        `json:"longitude"`
	LemonScore        *float64        `json:"lemon_score"`
	GoogleRating      *float64        `json:"google_rating"`
	GoogleReviewCount *int            `json:"google_review_count"`
	PriceRange        *string         `json:"price_range"`
	Hours             json.RawMessage `json:"hours"`
	Photos            []string        `json:"photos"`
	About             []string        `json:"about"`
	UniversalTags     []string        `json:"universal_tags"`
	SpecificTags      []string        `json:"specific_tags"`
	IsClaimed         bool            `json:"is_claimed"`
}

// Sanitize decodes one source record into a RawBusiness: trims strings, maps
// JSON null to nil for nullable columns, joins the about array into one blob,
// dedupes photos (dropping empties), and carries is_claimed through verbatim
// (default false). A non-empty id and name are required; everything else is
// best-effort. hours is preserved as raw JSON for passthrough into the JSONB
// column (null stays nil → SQL NULL).
func Sanitize(raw json.RawMessage) (RawBusiness, error) {
	var rec rawRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return RawBusiness{}, fmt.Errorf("decoding record: %w", err)
	}

	id, err := uuid.Parse(strings.TrimSpace(rec.ID))
	if err != nil {
		return RawBusiness{}, fmt.Errorf("parsing id %q: %w", rec.ID, err)
	}
	name := strings.TrimSpace(rec.Name)
	if name == "" {
		return RawBusiness{}, fmt.Errorf("record %s has empty name", id)
	}

	return RawBusiness{
		ID:                id,
		Name:              name,
		Category:          strings.TrimSpace(rec.Category),
		Subcategory:       trimPtr(rec.Subcategory),
		Specialty:         trimPtr(rec.Specialty),
		Address:           trimPtr(rec.Address),
		Neighborhood:      trimPtr(rec.Neighborhood),
		Latitude:          rec.Latitude,
		Longitude:         rec.Longitude,
		LemonScore:        rec.LemonScore,
		GoogleRating:      rec.GoogleRating,
		GoogleReviewCount: rec.GoogleReviewCount,
		PriceRange:        normalizePrice(rec.PriceRange),
		Hours:             nullableJSON(rec.Hours),
		Photos:            dedupeNonEmpty(rec.Photos),
		About:             joinAbout(rec.About),
		UniversalTags:     trimEach(rec.UniversalTags),
		SpecificTags:      trimEach(rec.SpecificTags),
		IsClaimed:         rec.IsClaimed,
	}, nil
}

// trimPtr trims a nullable string, returning nil when the source was null or
// trims down to empty (so blank strings become SQL NULL, not "").
func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// normalizePrice maps the source's mixed price vocabulary onto the canonical
// $-tier the schema documents (docs/data/schema.md). The data ships a mix of
// "$$" and word tiers ("affordable"/"mid-range"/"premium"); the intent overlay's
// price filter ("cheap"→$/$$, "fancy"→$$$/$$$$) only matches the $-tiers, so
// without this any "cheap …" query returned zero rows. Unknown/blank → nil.
func normalizePrice(s *string) *string {
	if s == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(*s)) {
	case "$", "affordable", "cheap", "budget":
		return ptrTo("$")
	case "$$", "mid-range", "midrange", "moderate":
		return ptrTo("$$")
	case "$$$", "premium", "upscale":
		return ptrTo("$$$")
	case "$$$$", "luxury", "expensive":
		return ptrTo("$$$$")
	default:
		return nil
	}
}

func ptrTo(s string) *string { return &s }

// nullableJSON normalizes the hours passthrough: a JSON null literal (or an
// absent field) becomes nil so the loader writes SQL NULL rather than the
// 4-byte string "null" into the jsonb column.
func nullableJSON(r json.RawMessage) json.RawMessage {
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	return r
}

// dedupeNonEmpty trims each photo URL, drops empties, and removes duplicates
// while preserving first-seen order. Returns nil for an all-empty/absent input
// so the column is SQL NULL rather than an empty array.
func dedupeNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimEach trims each tag and drops empties, preserving order. Returns nil for
// an empty/absent input so the column is SQL NULL.
func trimEach(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// joinAbout collapses the source about array into one text blob (the column is
// scalar text, weight D in search_vector). Empty paragraphs are dropped; an
// all-empty/absent input yields nil → SQL NULL.
func joinAbout(in []string) *string {
	if len(in) == 0 {
		return nil
	}
	parts := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	joined := strings.Join(parts, "\n\n")
	return &joined
}
