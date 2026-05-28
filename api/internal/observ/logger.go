package observ

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a structured JSON logger writing to stdout. level is parsed
// case-insensitively from the usual slog names (debug, info, warn, error);
// anything unrecognized falls back to info.
func New(level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
