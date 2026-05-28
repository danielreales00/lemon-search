// Command api is the Lemon Search HTTP service.
//
// It exposes the ranked-search endpoint plus /healthz, /readyz, and /version.
// The service is intentionally stateless; all state lives in Supabase Postgres.
//
// This is the composition root: it reads configuration from the environment,
// constructs the slog logger and pgx pool, wires the HTTP server, and runs it
// with graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/api"
	"github.com/danielreales00/lemon-search/api/internal/observ"
)

// Stamped at link time via -ldflags '-X main.version=... -X main.commit=... -X main.date=...'.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	defaultPort     = "8080"
	shutdownTimeout = 10 * time.Second
	readHeaderTO    = 5 * time.Second
	statementTO     = "1000" // ms; per-query Postgres statement_timeout (docs/api.md)
)

func main() {
	logger := observ.New(os.Getenv("LEMON_LOG_LEVEL"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	pinger, closePool := openPool(ctx, logger, os.Getenv("LEMON_DATABASE_URL"))
	defer closePool()

	build := api.BuildInfo{Version: version, Commit: commit, Date: date}
	srv := &http.Server{
		Addr:              addr(),
		Handler:           api.New(logger, pinger, build).Handler(),
		ReadHeaderTimeout: readHeaderTO,
	}

	return serve(ctx, logger, srv)
}

// openPool builds a pgx pool with a 1s per-statement timeout. When the URL is
// empty (e.g. CI smoke tests without a DB) it logs a warning and returns a
// stub Pinger that always reports not-ready, so /healthz still works and
// /readyz reports 503 without crashing the server.
func openPool(ctx context.Context, logger *slog.Logger, url string) (api.Pinger, func()) {
	if url == "" {
		logger.Warn("LEMON_DATABASE_URL is empty; starting without a database (/readyz will report not ready)")
		return notReady{}, func() {}
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		logger.Warn("invalid LEMON_DATABASE_URL; starting without a database", slog.String("err", err.Error()))
		return notReady{}, func() {}
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTO

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		logger.Warn("could not create database pool; starting without a database", slog.String("err", err.Error()))
		return notReady{}, func() {}
	}
	return pool, pool.Close
}

// serve runs srv until the context is canceled, then shuts it down gracefully.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server) error {
	errc := make(chan error, 1)
	go func() {
		logger.Info("lemon-search api listening", slog.String("addr", srv.Addr))
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining")
		// Fresh context on purpose: the signal ctx is already canceled, but
		// shutdown needs its own deadline to drain in-flight requests.
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil { //nolint:contextcheck // deliberate fresh deadline for drain
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	}
}

func addr() string {
	port := os.Getenv("LEMON_API_PORT")
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(os.Getenv("LEMON_API_HOST"), port)
}

// notReady is the Pinger used when no database is configured; it always fails
// so /readyz reports 503.
type notReady struct{}

func (notReady) Ping(context.Context) error {
	return errors.New("database not configured")
}
