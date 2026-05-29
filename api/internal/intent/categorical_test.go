package intent

import "testing"

func TestIsCategorical(t *testing.T) {
	tests := []struct {
		name string
		q    string
		want bool
	}{
		{"single cuisine word", "coffee", true},
		{"single subcategory word", "spa", true},
		{"two category words", "sushi tacos", true},
		{"accented category word", "Café", false}, // "cafe" is not a lexicon entry
		{"category word with trailing space", "  pizza  ", true},
		{"bigram universal tag", "date night", true},

		{"business name", "joes barber shop", false}, // "joes"/"shop" not category
		{"foreign business name", "rinconcito peruano", false},
		{"price-only modifier is not categorical", "cheap", false},
		{"time-only modifier is not categorical", "open now", false},
		{"category word plus an unknown word", "best coffee", false}, // "best" unknown
		{"empty", "", false},
		{"punctuation only", "!!!", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCategorical(tc.q); got != tc.want {
				t.Errorf("IsCategorical(%q) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}
