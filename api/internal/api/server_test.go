package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestServer(p Pinger) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	build := BuildInfo{Version: "1.2.3", Commit: "abc123", Date: "2026-05-28T00:00:00Z"}
	return New(log, p, nil, build).Handler()
}

func doGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestHealthz(t *testing.T) {
	h := newTestServer(fakePinger{})
	rec := doGet(t, h, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := decodeBody(t, rec)["status"]; got != "ok" {
		t.Fatalf("status field = %q, want %q", got, "ok")
	}
}

func TestVersion(t *testing.T) {
	h := newTestServer(fakePinger{})
	rec := doGet(t, h, "/version")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := decodeBody(t, rec)
	for _, key := range []string{"version", "commit", "built_at", "go"} {
		if body[key] == "" {
			t.Errorf("missing or empty field %q in %v", key, body)
		}
	}
	if body["version"] != "1.2.3" {
		t.Errorf("version = %q, want %q", body["version"], "1.2.3")
	}
}

func TestReadyzOK(t *testing.T) {
	h := newTestServer(fakePinger{err: nil})
	rec := doGet(t, h, "/readyz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := decodeBody(t, rec)["status"]; got != "ok" {
		t.Fatalf("status field = %q, want %q", got, "ok")
	}
}

func TestReadyzUnavailable(t *testing.T) {
	h := newTestServer(fakePinger{err: errors.New("connection refused")})
	rec := doGet(t, h, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := decodeBody(t, rec)["status"]; got != "db_unavailable" {
		t.Fatalf("status field = %q, want %q", got, "db_unavailable")
	}
}
