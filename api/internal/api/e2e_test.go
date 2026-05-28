//go:build e2e

// End-to-end smoke test: boots the fully-routed handler behind a real httptest
// HTTP server and drives it over the wire against a LIVE Postgres (the *pgxpool
// pool satisfies Pinger). Unlike server_test.go — which uses a fake Pinger and
// an in-process recorder — /readyz here exercises a real database round-trip,
// i.e. the path cmd/api runs in production.
//
//	make db-up && make db-reset
//	cd api && go test -tags e2e ./internal/api/...
//
// Gated behind the `e2e` build tag so the default `go test ./...` needs no DB.
package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

const e2eDefaultDB = "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"

// e2ePool dials the live Postgres the same way the ingest integration suite
// does: LEMON_DATABASE_URL when set, else the local Supabase default. It skips
// (not fails) when no database is reachable, so a bare `-tags e2e` run off-CI
// is a no-op rather than a red failure.
func e2ePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("LEMON_DATABASE_URL")
	if url == "" {
		url = e2eDefaultDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("no live Postgres (%s): %v", url, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("live Postgres not reachable (%s): %v", url, err)
	}
	return pool
}

// getJSON performs a context-bound GET and decodes the flat string body.
func getJSON(t *testing.T, client *http.Client, url string) (status int, body map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return resp.StatusCode, body
}

func TestE2EHealthReadinessVersion(t *testing.T) {
	pool := e2ePool(t)
	defer pool.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	build := BuildInfo{Version: "e2e", Commit: "e2e", Date: "2026-05-28T00:00:00Z"}
	srv := httptest.NewServer(New(log, pool, build).Handler())
	defer srv.Close()
	client := srv.Client()

	if code, body := getJSON(t, client, srv.URL+"/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("/healthz = %d %v, want 200 {status: ok}", code, body)
	}
	// The end-to-end distinction: a real DB ping must succeed.
	if code, body := getJSON(t, client, srv.URL+"/readyz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("/readyz = %d %v, want 200 {status: ok} against the live DB", code, body)
	}
	if code, body := getJSON(t, client, srv.URL+"/version"); code != http.StatusOK || body["version"] == "" {
		t.Fatalf("/version = %d %v, want 200 with a version", code, body)
	}
}
