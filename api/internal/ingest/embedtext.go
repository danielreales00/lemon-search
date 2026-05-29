package ingest

import "strings"

// maxEmbedChars caps the composed embed text. all-MiniLM-L6-v2 truncates input
// at 256 word-pieces, and Ollama's /api/embed rejects the whole batch with a 400
// ("input length exceeds the context length") if any single input is too long.
// 1000 runes sits safely under that ceiling (empirically the cutoff is ~1100–
// 1200 chars for this corpus) while keeping the full text for ~98.5% of rows
// (p99 ≈ 1087). Because the salient identifying signal — name, category,
// subcategory, tags — is composed first, only the tail of a long `about` is
// dropped, which the model would have truncated anyway.
const maxEmbedChars = 1000

// EmbedText composes the single string fed to the sentence-embedding model for a
// business (ADR-0006: embeddings are computed from name + category + subcategory
// + tags + about). The fields are joined newline-separated, in descending
// salience order — name first, free-text about last — so the model sees the most
// identifying signal up front. Tags are space-joined within their line. The
// result is truncated to maxEmbedChars (on a rune boundary).
//
// It is pure and total: empty/blank fields are skipped (never emitting a blank
// line or stray separator), and every field is trimmed. A business with no text
// at all yields "" — the caller skips embedding it, leaving its column NULL
// (a harmless no-op for the recall query, per migration 0006).
func EmbedText(name, category, subcategory string, universalTags, specificTags []string, about string) string {
	// 6 source lines: name, category, subcategory, universal tags, specific tags, about.
	const sourceLines = 6
	lines := make([]string, 0, sourceLines)
	add := func(s string) {
		if t := strings.TrimSpace(s); t != "" {
			lines = append(lines, t)
		}
	}

	add(name)
	add(category)
	add(subcategory)
	add(joinTags(universalTags))
	add(joinTags(specificTags))
	add(about)

	return truncateRunes(strings.Join(lines, "\n"), maxEmbedChars)
}

// truncateRunes returns s capped at maxRunes, cutting on a rune boundary so a
// multi-byte UTF-8 character (accented names/about text) is never split.
func truncateRunes(s string, maxRunes int) string {
	if len(s) <= maxRunes { // byte len ≤ rune cap ⇒ already short enough (ASCII fast path)
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// joinTags trims each tag, drops blanks, and space-joins the rest, preserving
// order. An all-blank/empty input yields "" so EmbedText skips the line.
func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if t := strings.TrimSpace(tag); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}
