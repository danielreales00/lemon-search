// Command api is the Lemon Search HTTP service.
//
// It exposes a single endpoint, GET /search, that takes a free-text query
// plus an optional user location and returns up to 15 ranked businesses.
//
// The service is intentionally stateless; all state lives in Supabase Postgres.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		logger.Error("fatal", slog.String("err", err.Error()))
		os.Exit(1)
	}
}

func run(_ context.Context) error {
	slog.Info("lemon-search api starting (skeleton)")
	return nil
}
