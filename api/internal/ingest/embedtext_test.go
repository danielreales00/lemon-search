package ingest

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEmbedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		bizName       string
		category      string
		subcategory   string
		universalTags []string
		specificTags  []string
		about         string
		want          string
	}{
		{
			name:          "all fields",
			bizName:       "Joe's Stone Crab",
			category:      "Food & Drinks",
			subcategory:   "Seafood",
			universalTags: []string{"family-friendly"},
			specificTags:  []string{"seafood", "stone crab"},
			about:         "Iconic seafood since 1913.",
			want:          "Joe's Stone Crab\nFood & Drinks\nSeafood\nfamily-friendly\nseafood stone crab\nIconic seafood since 1913.",
		},
		{
			name:     "name and category only",
			bizName:  "Royal Nails",
			category: "Beauty",
			want:     "Royal Nails\nBeauty",
		},
		{
			name:        "empty subcategory is skipped, no stray separator",
			bizName:     "Cafe",
			category:    "Food & Drinks",
			subcategory: "",
			about:       "Cozy spot.",
			want:        "Cafe\nFood & Drinks\nCozy spot.",
		},
		{
			name:          "blank fields and tags are trimmed and dropped",
			bizName:       "  Spa  ",
			category:      " Beauty ",
			subcategory:   "   ",
			universalTags: []string{" ", "wellness", ""},
			specificTags:  []string{"   "},
			about:         "  Relax.  ",
			want:          "Spa\nBeauty\nwellness\nRelax.",
		},
		{
			name:    "name only",
			bizName: "Solo",
			want:    "Solo",
		},
		{
			name: "all empty yields empty string",
			want: "",
		},
		{
			name:         "only tags",
			specificTags: []string{"vegan", "gluten-free"},
			want:         "vegan gluten-free",
		},
		{
			name:          "nil tag slices are fine",
			bizName:       "X",
			universalTags: nil,
			specificTags:  nil,
			want:          "X",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EmbedText(tc.bizName, tc.category, tc.subcategory, tc.universalTags, tc.specificTags, tc.about)
			if got != tc.want {
				t.Errorf("EmbedText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmbedTextTruncatesLongText(t *testing.T) {
	t.Parallel()

	about := strings.Repeat("a", maxEmbedChars*2)
	got := EmbedText("Spot", "Food & Drinks", "", nil, nil, about)

	if utf8.RuneCountInString(got) != maxEmbedChars {
		t.Fatalf("rune len = %d, want %d (truncated)", utf8.RuneCountInString(got), maxEmbedChars)
	}
	// The salient prefix (name then category) survives truncation.
	if !strings.HasPrefix(got, "Spot\nFood & Drinks\n") {
		t.Errorf("prefix not preserved: %q…", got[:20])
	}
}

func TestEmbedTextTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Multi-byte runes (é is 2 bytes) must never be split by truncation.
	about := strings.Repeat("é", maxEmbedChars*2)
	got := EmbedText("X", "", "", nil, nil, about)

	if !utf8.ValidString(got) {
		t.Fatalf("truncation split a multi-byte rune: invalid UTF-8")
	}
	if utf8.RuneCountInString(got) != maxEmbedChars {
		t.Errorf("rune len = %d, want %d", utf8.RuneCountInString(got), maxEmbedChars)
	}
}
