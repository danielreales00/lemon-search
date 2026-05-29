package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/ingest"
	ollama "github.com/danielreales00/lemon-search/api/internal/retrieve/embed/ollama"
)

const (
	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "all-minilm"
)

// embedOpts are the embedding-pass knobs parsed from CLI flags. all re-embeds
// every row (otherwise only NULL embeddings are filled); limit caps the rows
// processed (for a bounded sample run; 0 = all).
type embedOpts struct {
	all   bool
	limit int
}

// runEmbed is the composition root for the embedding pass: it builds the pgx
// pool and the Ollama-backed domain.Embedder from env, wires them into the
// ingest.Backfiller, and runs it. The Backfiller depends on the domain.Embedder
// port — the Ollama adapter is constructed only here.
func runEmbed(ctx context.Context, logger *slog.Logger, opts embedOpts) error {
	dbURL := os.Getenv("LEMON_DATABASE_URL")
	if dbURL == "" {
		return errors.New("LEMON_DATABASE_URL is empty")
	}

	// Typed as the domain port so the backfiller depends on the interface, not
	// the Ollama adapter (which is constructed only here, the composition root).
	var embedder domain.Embedder
	oll, err := ollama.New(ollamaURL(), nil, ollamaModel())
	if err != nil {
		return fmt.Errorf("building embedder: %w", err)
	}
	embedder = oll

	pool, err := openPool(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	start := time.Now()
	logger.Info("embedding pass starting",
		slog.String("ollama", ollamaURL()),
		slog.String("model", ollamaModel()),
		slog.Bool("only_missing", !opts.all),
		slog.Int("limit", opts.limit))

	store := ingest.NewEmbedStore(pool)
	stats, err := ingest.NewBackfiller(store, store, embedder, logger, ingest.EmbedBatchSize).
		Run(ctx, !opts.all, opts.limit)
	if err != nil {
		return fmt.Errorf("embedding pass: %w", err)
	}

	if _, err := os.Stdout.WriteString(embedReport(stats, time.Since(start))); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	return nil
}

func ollamaURL() string {
	if v := os.Getenv("LEMON_OLLAMA_URL"); v != "" {
		return v
	}
	return defaultOllamaURL
}

func ollamaModel() string {
	if v := os.Getenv("LEMON_OLLAMA_MODEL"); v != "" {
		return v
	}
	return defaultOllamaModel
}

// embedReport renders the end-of-pass summary, including throughput (ms/row over
// the embedded rows) to size the full run.
func embedReport(s ingest.EmbedStats, elapsed time.Duration) string {
	perRow := "n/a"
	if s.Embedded > 0 {
		perRow = fmt.Sprintf("%.1f ms/row", float64(elapsed.Milliseconds())/float64(s.Embedded))
	}
	return fmt.Sprintf("embedding pass done in %s\n", elapsed.Round(time.Millisecond)) +
		fmt.Sprintf("  scanned:   %6d\n", s.Scanned) +
		fmt.Sprintf("  embedded:  %6d\n", s.Embedded) +
		fmt.Sprintf("  skipped:   %6d (no embeddable text)\n", s.Skipped) +
		fmt.Sprintf("  throughput: %s\n", perRow)
}
