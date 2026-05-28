// Command ingest streams the businesses-*.json file into Supabase Postgres.
//
// Responsibilities:
//   - Stream-parse malformed JSON (objects separated by "}\n{" instead of "},\n{").
//   - Normalize categories/subcategories to the spec taxonomy.
//   - Assign one of six archetypes per row.
//   - Drop non-Miami records and rows with empty category.
//   - Synthesize is_claimed and friend_count (deterministic by id).
//   - Bulk-insert via pgx COPY for speed.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	input := flag.String("input", "", "path to businesses-*.json")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *input); err != nil {
		logger.Error("fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(_ context.Context, input string) error {
	slog.Info("lemon-search ingest starting (skeleton)", slog.String("input", input))
	return nil
}
