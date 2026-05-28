package api

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
)

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	if err := s.pinger.Ping(ctx); err != nil {
		s.log.WarnContext(ctx, "readiness check failed", "err", err)
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"version":  s.build.Version,
		"commit":   s.build.Commit,
		"built_at": s.build.Date,
		"go":       runtime.Version(),
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.Error("encoding response", "err", err)
	}
}
