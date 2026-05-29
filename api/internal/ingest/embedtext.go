package ingest

import "strings"

// maxEmbedChars caps the composed embed text. all-MiniLM-L6-v2 truncates input
// at 256 word-pieces, and Ollama's /api/embed rejects the WHOLE batch with a 400
// ("input length exceeds the context length") if any single input overflows —
// so one dense row poisons its entire EmbedBatch page. Char count is a poor
// proxy for token count: dense text (hyphenated tags + menu prose) blows past
// 256 tokens well under 1000 chars, which is why the first ingest pass 400'd and
// left ~21k rows unembedded. 512 runes is the verified-safe cap — a corpus-wide
// stress test (all 22,568 rows, embedded individually) found zero 400s at 512,
// covering the worst case of ~2 chars/token. Because the salient identifying
// signal — name, category, subcategory, tags — is composed first, a tighter cap
// only drops the tail of a long `about`, which the model would truncate anyway.
const maxEmbedChars = 512

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
