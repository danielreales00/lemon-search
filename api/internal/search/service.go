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
	"strings"
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
	EmbedMS  int64
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
	embedder      domain.Embedder
}

// New wires the search service. intentEnabled gates the intent extractor
// (LEMON_FF_INTENT); when false the search path behaves exactly as it did
// before the extractor was wired in (no intent, no overlay, pin fires on a
// match). embedder gates semantic recall (LEMON_FF_SEMANTIC): nil means no
// query embedding and purely lexical retrieval; the composition root passes a
// non-nil embedder only when the flag is on. repo and cfg are required —
// callers that may lack them (a degraded server) should not construct a Service.
func New(log *slog.Logger, repo domain.BusinessRepo, cfg *config.Ranking, intentEnabled bool, embedder domain.Embedder) *Service {
	return &Service{log: log, repo: repo, cfg: cfg, intentEnabled: intentEnabled, embedder: embedder}
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
	embedMS, queryVec := s.embedQuery(ctx, q)

	retrieveOpts := opts
	retrieveOpts.Limit = candidateLimit
	retrieveOpts.Overlay = overlay
	retrieveOpts.QueryVec = queryVec

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

	// Rank the full pool, then dedupe by name and take the top resultLimit, so a
	// chain's repeated locations (e.g. several "Häagen-Dazs") don't crowd out
	// distinct businesses — the freed slots backfill from the ranked remainder.
	rankOpts := opts
	rankOpts.Limit = candidateLimit

	rerankStart := time.Now()
	ranked, err := rank.Run(ctx, candidates, pinPtr, s.cfg, rankOpts)
	if err != nil {
		return nil, Timings{}, fmt.Errorf("ranking: %w", err)
	}
	ranked = dedupeByName(ranked, resultLimit)
	rerankMS := sinceMS(rerankStart)

	return ranked, Timings{IntentMS: intentMS, EmbedMS: embedMS, SQLMS: sqlMS, RerankMS: rerankMS}, nil
}

// dedupeByName keeps the first (highest-ranked) result per normalized name and
// returns at most limit of them, so the top-N is N distinct businesses rather
// than several locations of one chain. Locations carrying distinct names
// ("Panther Coffee" vs "Panther Coffee - Wynwood") are kept.
func dedupeByName(ranked []rank.Result, limit int) []rank.Result {
	seen := make(map[string]struct{}, len(ranked))
	out := make([]rank.Result, 0, limit)
	for i := range ranked {
		key := strings.ToLower(strings.TrimSpace(ranked[i].Candidate.Name))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ranked[i])
		if len(out) == limit {
			break
		}
	}
	return out
}

// embedQuery embeds q for semantic recall when an embedder is wired (the
// LEMON_FF_SEMANTIC path). With no embedder — the default — it is a no-op: zero
// time, nil vector, retrieval stays purely lexical. An embed error degrades to
// lexical-only (logged, nil vector) rather than failing the whole search, so a
// flaky embedder never takes search down.
func (s *Service) embedQuery(ctx context.Context, q string) (embedMS int64, vec []float32) {
	if s.embedder == nil || strings.TrimSpace(q) == "" {
		return 0, nil
	}
	start := time.Now()
	v, err := s.embedder.Embed(ctx, q)
	embedMS = sinceMS(start)
	if err != nil {
		s.log.WarnContext(ctx, "query embed failed; falling back to lexical recall", "q", q, "err", err)
		return embedMS, nil
	}
	return embedMS, v
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
