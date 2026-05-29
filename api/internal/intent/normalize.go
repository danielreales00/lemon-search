package intent

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// tokenRe extracts word tokens: ASCII letters, digits, and the apostrophe
// (kept so "i'm" survives as one token). Everything else is a separator.
var tokenRe = regexp.MustCompile(`[a-z0-9']+`)

// normalize lowercases, strips diacritics (café → cafe, niño → nino), and
// splits into word tokens. Pure: same input → same output.
func normalize(q string) []string {
	q = strings.ToLower(q)
	q = unaccent(q)
	return tokenRe.FindAllString(q, -1)
}

// unaccent NFD-decomposes the string and drops nonspacing combining marks
// (Unicode category Mn), which removes accents while leaving the base letter.
func unaccent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
