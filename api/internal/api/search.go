package api

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

const (
	// resultCap pre-sizes the response slice; the service caps the returned
	// results to the same top-N (the limit policy lives there, not here).
	resultCap = 15

	// Fixed Miami fallback used when the client sends no location (downtown).
	defaultLat = 25.7617
	defaultLng = -80.1918
)

// searchResponse is the GET /search payload — contract C4. JSON keys are
// snake_case (tagliatelle-enforced); Results is always a non-nil array.
type searchResponse struct {
	Query   string         `json:"query"`
	Results []searchResult `json:"results"`
	Timings searchTimings  `json:"timings"`
}

type searchResult struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Subcategory  *string  `json:"subcategory"`
	Archetype    string   `json:"archetype"`
	Neighborhood *string  `json:"neighborhood"`
	DistanceKM   float64  `json:"distance_km"`
	Rating       *float64 `json:"rating"`
	ReviewCount  int      `json:"review_count"`
	PriceRange   *string  `json:"price_range"`
	PhotoURL     *string  `json:"photo_url"`
	IsClaimed    bool     `json:"is_claimed"`
	IsNew        bool     `json:"is_new"`
	IsOpenNow    *bool    `json:"is_open_now"`
	Score        float64  `json:"score"`
}

type searchTimings struct {
	IntentMS int64 `json:"intent_ms"`
	SQLMS    int64 `json:"sql_ms"`
	RerankMS int64 `json:"rerank_ms"`
	TotalMS  int64 `json:"total_ms"`
}

// handleSearch parses the request, delegates the search use-case to the
// service, and encodes the top results with per-stage timings. The orchestration
// (intent → retrieval → pin → re-rank) lives in search.Service.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if s.svc == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "search is not configured"})
		return
	}

	params := r.URL.Query()
	q := strings.TrimSpace(params.Get("q"))
	lat, lng, err := parseLatLng(params)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	now, err := parseNow(params)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := searchResponse{Query: q, Results: make([]searchResult, 0, resultCap)}
	if q == "" {
		resp.Timings.TotalMS = sinceMS(start)
		s.writeJSON(w, http.StatusOK, resp)
		return
	}

	ctx := r.Context()
	ranked, timings, err := s.svc.Search(ctx, q, domain.SearchOpts{Lat: lat, Lng: lng, Now: now})
	if err != nil {
		s.log.ErrorContext(ctx, "search failed", "err", err)
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
		return
	}

	for i := range ranked {
		resp.Results = append(resp.Results, toResult(&ranked[i]))
	}
	resp.Timings = searchTimings{
		IntentMS: timings.IntentMS,
		SQLMS:    timings.SQLMS,
		RerankMS: timings.RerankMS,
		TotalMS:  sinceMS(start),
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// toResult maps a ranked result onto the C4 DTO. The exact-name pin carries a
// +Inf score (which JSON cannot encode); it surfaces as 1.0 — a perfect match.
func toResult(r *rank.Result) searchResult {
	c := &r.Candidate
	score := r.Score
	if math.IsInf(score, 1) {
		score = 1.0
	}
	return searchResult{
		ID:           c.ID.String(),
		Name:         c.Name,
		Category:     c.Category,
		Subcategory:  c.Subcategory,
		Archetype:    string(c.Archetype),
		Neighborhood: c.Neighborhood,
		DistanceKM:   c.DistanceKM,
		Rating:       c.GoogleRating,
		ReviewCount:  c.GoogleReviewCount,
		PriceRange:   c.PriceRange,
		PhotoURL:     c.PhotoURL,
		IsClaimed:    c.IsClaimed,
		IsNew:        c.IsNew,
		IsOpenNow:    c.IsOpenNow,
		Score:        score,
	}
}

// parseLatLng reads lat/lng, falling back to the fixed Miami location, and
// rejects out-of-range coordinates.
func parseLatLng(q url.Values) (lat, lng float64, err error) {
	if lat, err = floatParam(q, "lat", defaultLat); err != nil {
		return 0, 0, err
	}
	if lng, err = floatParam(q, "lng", defaultLng); err != nil {
		return 0, 0, err
	}
	if lat < -90 || lat > 90 {
		return 0, 0, fmt.Errorf("lat %g out of range [-90,90]", lat)
	}
	if lng < -180 || lng > 180 {
		return 0, 0, fmt.Errorf("lng %g out of range [-180,180]", lng)
	}
	return lat, lng, nil
}

func floatParam(q url.Values, key string, def float64) (float64, error) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", key, raw)
	}
	return v, nil
}

// parseNow reads an optional RFC3339 timestamp; absent means wall-clock now.
// An injectable now keeps is_open_now and bench runs reproducible.
func parseNow(q url.Values) (time.Time, error) {
	raw := strings.TrimSpace(q.Get("now"))
	if raw == "" {
		return time.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid now %q (want RFC3339)", raw)
	}
	return t, nil
}

func sinceMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
