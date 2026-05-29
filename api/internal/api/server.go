package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/search"
)

// readyTimeout bounds the DB ping behind /readyz so a stalled database never
// blocks the readiness probe (docs/api.md: SELECT 1 within 100ms).
const readyTimeout = 100 * time.Millisecond

// Pinger is the readiness dependency: a liveness check against the datastore.
// It is owned by this package (the consumer) so the HTTP layer stays free of
// any database driver import — *pgxpool.Pool satisfies it and is injected from
// cmd/api.
type Pinger interface {
	Ping(ctx context.Context) error
}

// BuildInfo carries the values stamped at link time and surfaced by /version.
// cmd/api populates it from -ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Server holds the HTTP dependencies and exposes the routed handler. It is thin
// transport: the search use-case lives in svc; this layer only parses requests,
// calls the service, and encodes the response.
type Server struct {
	log    *slog.Logger
	pinger Pinger
	svc    *search.Service
	build  BuildInfo
}

// New wires the HTTP server dependencies. It accepts interfaces and returns a
// concrete *Server; callers obtain the routed handler via Handler. svc may be
// nil — /search then reports 503, while the health endpoints still work (a
// server booted without a DB or ranking config has no usable search service).
func New(log *slog.Logger, pinger Pinger, svc *search.Service, build BuildInfo) *Server {
	return &Server{log: log, pinger: pinger, svc: svc, build: build}
}

// Handler returns the fully routed http.Handler with request logging applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /version", s.handleVersion)
	return s.logRequests(mux)
}

// logRequests emits one structured line per request with method, path, status,
// and latency.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.LogAttrs(
			r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	})
}

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
