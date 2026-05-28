// Command ingest streams the businesses-*.json file into Supabase Postgres.
//
// It is the composition root for the ingestion pipeline: it opens the data
// file, builds the pgx pool, and wires the pure stages (parser → sanitize →
// geo filter → taxonomy → synth) into the COPY-stream loader. The pipeline is
// idempotent: re-running on the same input yields the same final table state.
//
// Usage:
//
//	LEMON_DATABASE_URL=postgres://… go run ./cmd/ingest -input businesses-2026-05-27.json
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/ingest"
	"github.com/danielreales00/lemon-search/api/internal/observ"
)

// statementTO bounds any single ingest statement. The API pool uses 1s because
// it serves the latency-sensitive query path; ingest instead runs a bulk upsert
// over ~22k rows (computing loc + search_vector per row) that legitimately takes
// seconds, so it gets a generous batch-job cap rather than the 1s query cap.
const statementTO = "300000" // ms (5 min); batch ingest, not the API's 1s

func main() {
	logger := observ.New(os.Getenv("LEMON_LOG_LEVEL"))

	input := flag.String("input", "", "path to businesses-*.json")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger, *input); err != nil {
		logger.Error("fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, input string) error {
	if input == "" {
		return errors.New("missing -input path")
	}
	dbURL := os.Getenv("LEMON_DATABASE_URL")
	if dbURL == "" {
		return errors.New("LEMON_DATABASE_URL is empty")
	}

	f, err := os.Open(input) //nolint:gosec // operator-supplied path for a CLI loader
	if err != nil {
		return fmt.Errorf("opening input: %w", err)
	}
	defer func() { _ = f.Close() }()

	pool, err := openPool(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	start := time.Now()
	logger.Info("ingest starting", slog.String("input", input))

	// Derived cancel so a loader error unblocks the producer's channel send
	// (it selects on pipeCtx.Done()), guaranteeing prod.wait() returns.
	pipeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rows := make(chan ingest.Business, ingest.CopyChannelBuffer)
	prod := newProducer(ingest.New(f), logger)
	go prod.run(pipeCtx, rows)

	loaded, loadErr := ingest.NewLoader(pool).Load(pipeCtx, rows)
	cancel()
	stats := prod.wait()

	if loadErr != nil {
		return fmt.Errorf("loading: %w", loadErr)
	}
	if stats.err != nil {
		return fmt.Errorf("producing rows: %w", stats.err)
	}

	stats.loaded = loaded
	if _, err := io.WriteString(os.Stdout, report(stats, time.Since(start))); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}
	return nil
}

// openPool builds the ingest pgx pool with a generous batch statement timeout
// (see statementTO) — long enough for the bulk upsert over the full dataset.
func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parsing LEMON_DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTO

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}
	return pool, nil
}

// report renders the end-of-run summary in the format docs/data/ingestion.md
// specifies. Returned as a string so the single write to stdout is error-checked.
func report(s stats, elapsed time.Duration) string {
	pct := func(n int) float64 {
		if s.read == 0 {
			return 0
		}
		return 100 * float64(n) / float64(s.read)
	}
	return fmt.Sprintf("ingest done in %s\n", elapsed.Round(time.Millisecond)) +
		fmt.Sprintf("  read:                %6d\n", s.read) +
		fmt.Sprintf("  dropped (geo):       %6d\n", s.droppedGeo) +
		fmt.Sprintf("  dropped (no addr):   %6d\n", s.droppedNoAddr) +
		fmt.Sprintf("  dropped (cat empty): %6d\n", s.droppedCatEmpty) +
		fmt.Sprintf("  bucketed (Other):    %6d\n", s.bucketedOther) +
		fmt.Sprintf("  loaded:              %6d\n", s.loaded) +
		fmt.Sprintf("  is_claimed=true (source): %6d (%.2f%%)\n", s.claimedTrue, pct(s.claimedTrue)) +
		fmt.Sprintf("  friend_count > 0:    %6d (%.2f%%)\n", s.friendNonzero, pct(s.friendNonzero))
}
