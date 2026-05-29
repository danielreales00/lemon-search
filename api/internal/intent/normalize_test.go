package intent

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"lowercase + split", "Best Steakh", []string{"best", "steakh"}},
		{"strip accents", "Café Bea", []string{"cafe", "bea"}},
		{"spanish tilde + enye", "Señor niño", []string{"senor", "nino"}},
		{"apostrophe preserved", "i'm hungry!", []string{"i'm", "hungry"}},
		{"punctuation separates", "tacos, burgers & wine", []string{"tacos", "burgers", "wine"}},
		{"collapses whitespace", "  open    now  ", []string{"open", "now"}},
		{"digits preserved", "open 24", []string{"open", "24"}},
		{"empty", "", nil},
		{"only punctuation", "!!! ???", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalize(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("normalize(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
