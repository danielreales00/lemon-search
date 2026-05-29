// Package search owns the search use-case: the orchestration of intent →
// retrieval → exact-name pin → re-rank that turns a raw query into ranked
// results. It is the single seam both the HTTP handler and the bench-runner
// call, so a guard added here (e.g. the categorical pin suppression) applies to
// both without hand-copying. A future semantic layer or extra surface plugs in
// here too. The service is pure orchestration over the domain ports; it holds
// no transport concerns and no DB driver.
package search

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/intent"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

const (
	// candidateLimit caps the recall set retrieval returns; the ranker trims it
	// down to resultLimit after scoring.
	candidateLimit = 150
	resultLimit    = 15
)

// Timings reports the wall time spent in each stage of a search. It is
// domain-side (no JSON tags): transport layers map it onto their own DTO.
type Timings struct {
	IntentMS int64
	SQLMS    int64
	RerankMS int64
}

// Service runs the search use-case against a BusinessRepo and a ranking config.
// It is constructed once at the composition root and is safe for concurrent use
// (it holds no per-request state).
type Service struct {
	log           *slog.Logger
	repo          domain.BusinessRepo
	cfg           *config.Ranking
	intentEnabled bool
}

// New wires the search service. intentEnabled gates the intent extractor
// (LEMON_FF_INTENT); when false the search path behaves exactly as it did
// before the extractor was wired in (no intent, no overlay, pin fires on a
// match). repo and cfg are required — callers that may lack them (a degraded
// server) should not construct a Service.
func New(log *slog.Logger, repo domain.BusinessRepo, cfg *config.Ranking, intentEnabled bool) *Service {
	return &Service{log: log, repo: repo, cfg: cfg, intentEnabled: intentEnabled}
}

// Search runs the full pipeline: flag-gated intent (extract overlay + detect a
// categorical query), single-round-trip retrieval narrowed by the overlay, an
// exact-name lookup, the categorical pin guard, and the re-rank. It returns the
// top resultLimit ranked results plus per-stage timings.
//
// opts carries the caller's Lat/Lng/Now; the limit policy lives here, so any
// Limit the caller sets is ignored (candidateLimit for recall, resultLimit for
// the returned slice). The Overlay is filled in from intent and must not be set
// by the caller.
func (s *Service) Search(ctx context.Context, q string, opts domain.SearchOpts) ([]rank.Result, Timings, error) {
	intentMS, categorical, overlay := s.runIntent(ctx, q)

	retrieveOpts := opts
	retrieveOpts.Limit = candidateLimit
	retrieveOpts.Overlay = overlay

	sqlStart := time.Now()
	candidates, err := s.repo.Search(ctx, q, retrieveOpts)
	if err != nil {
		return nil, Timings{}, fmt.Errorf("retrieval: %w", err)
	}
	pin, found, err := s.repo.ExactName(ctx, q)
	if err != nil {
		return nil, Timings{}, fmt.Errorf("exact-name lookup: %w", err)
	}
	sqlMS := sinceMS(sqlStart)

	// A categorical query (e.g. "coffee", "spa") names a category, not one
	// business, so suppress the pin even when a literally-named business matched.
	var pinPtr *domain.Candidate
	if found && !categorical {
		pinPtr = &pin
	}

	rankOpts := opts
	rankOpts.Limit = resultLimit

	rerankStart := time.Now()
	ranked, err := rank.Run(ctx, candidates, pinPtr, s.cfg, rankOpts)
	if err != nil {
		return nil, Timings{}, fmt.Errorf("ranking: %w", err)
	}
	rerankMS := sinceMS(rerankStart)

	return ranked, Timings{IntentMS: intentMS, SQLMS: sqlMS, RerankMS: rerankMS}, nil
}

// runIntent runs the flag-gated intent extractor and reports the time spent,
// whether the query is categorical (used to suppress the exact-name pin), and
// the overlay (threaded into retrieval to narrow candidates). With the flag off
// it is a no-op — zero time, not categorical, zero overlay — so the search path
// behaves exactly as it did before the extractor was wired in.
func (s *Service) runIntent(ctx context.Context, q string) (intentMS int64, categorical bool, overlay domain.Overlay) {
	if !s.intentEnabled {
		return 0, false, domain.Overlay{}
	}
	intentStart := time.Now()
	overlay = intent.Extract(q)
	categorical = intent.IsCategorical(q)
	intentMS = sinceMS(intentStart)
	s.log.DebugContext(ctx, "intent extracted",
		"q", q, "categorical", categorical, "overlay_zero", overlay.IsZero())
	return intentMS, categorical, overlay
}

func sinceMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
