// Package flags reads boolean feature flags from the environment. It is a leaf
// utility with no internal dependencies; the composition root (cmd/api) reads
// the flags once at startup and threads the resulting values down as plain
// arguments, so the rest of the code stays free of env lookups and global
// mutable state. See docs/operations/feature-flags.md.
package flags

import (
	"os"
	"strings"
)

// Flags holds the feature flags resolved once at startup.
type Flags struct {
	// Intent gates wiring the intent extractor into the search handler while the
	// lexicon is still incomplete (LEMON_FF_INTENT). Default off.
	Intent bool
}

// FromEnv resolves the feature flags from the process environment.
func FromEnv() Flags {
	return Flags{Intent: truthy(os.Getenv("LEMON_FF_INTENT"))}
}

// truthy reports whether v is a case-insensitive "1" or "true". Anything else
// (including empty) is false, so flags default off.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true":
		return true
	default:
		return false
	}
}
