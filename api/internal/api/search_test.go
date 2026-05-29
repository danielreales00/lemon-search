package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

func ptr[T any](v T) *T { return &v }

// fakeRepo is an in-memory domain.BusinessRepo for handler tests.
type fakeRepo struct {
	candidates []domain.Candidate
	pin        *domain.Candidate
	searchErr  error
	exactErr   error
}

func (f fakeRepo) Search(_ context.Context, _ string, _ domain.SearchOpts) ([]domain.Candidate, error) {
	return f.candidates, f.searchErr
}

func (f fakeRepo) ExactName(_ context.Context, _ string) (domain.Candidate, bool, error) {
	if f.exactErr != nil {
		return domain.Candidate{}, false, f.exactErr
	}
	if f.pin != nil {
		return *f.pin, true, nil
	}
	return domain.Candidate{}, false, nil
}

func loadTestConfig(t *testing.T) *config.Ranking {
	t.Helper()
	cfg, err := config.LoadFile("../../../config/ranking.yaml")
	if err != nil {
		t.Fatalf("load ranking config: %v", err)
	}
	return cfg
}

func newSearchServer(t *testing.T, repo domain.BusinessRepo, cfg *config.Ranking) http.Handler {
	t.Helper()
	return newSearchServerFF(t, repo, cfg, false)
}

func newSearchServerFF(t *testing.T, repo domain.BusinessRepo, cfg *config.Ranking, intentEnabled bool) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	build := BuildInfo{Version: "test", Commit: "test", Date: "2026-05-28T00:00:00Z"}
	return New(log, fakePinger{}, repo, cfg, build, intentEnabled).Handler()
}

func decodeSearch(t *testing.T, rec *httptest.ResponseRecorder) searchResponse {
	t.Helper()
	var sr searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return sr
}

func openCandidate(name string) domain.Candidate {
	return domain.Candidate{
		ID:                uuid.New(),
		Name:              name,
		Category:          "Food & Drinks",
		Archetype:         domain.ArchetypeLowStakesFastNearby,
		DistanceKM:        1.0,
		LemonScore:        ptr(9.0),
		GoogleRating:      ptr(4.5),
		GoogleReviewCount: 200,
		PhotoCount:        5,
		IsOpenNow:         ptr(true),
	}
}

func TestSearchUnavailableWithoutDeps(t *testing.T) {
	h := newSearchServer(t, nil, nil)
	rec := doGet(t, h, "/search?q=coffee")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSearchEmptyQueryReturnsEmptyArray(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"results":[]`) {
		t.Fatalf("empty query must encode results as [], got %q", rec.Body.String())
	}
	if sr := decodeSearch(t, rec); len(sr.Results) != 0 {
		t.Fatalf("want 0 results, got %d", len(sr.Results))
	}
}

func TestSearchInvalidLat(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee&lat=notanumber")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSearchLatOutOfRange(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee&lat=120")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSearchInvalidNow(t *testing.T) {
	h := newSearchServer(t, fakeRepo{}, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee&now=not-a-timestamp")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSearchReturnsRankedResults(t *testing.T) {
	repo := fakeRepo{candidates: []domain.Candidate{openCandidate("Joe's Coffee"), openCandidate("Bean There")}}
	h := newSearchServer(t, repo, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee&lat=25.76&lng=-80.19")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	sr := decodeSearch(t, rec)
	if sr.Query != "coffee" {
		t.Fatalf("query = %q, want coffee", sr.Query)
	}
	if len(sr.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(sr.Results))
	}
	if sr.Results[0].Score <= 0 {
		t.Fatalf("want a positive score, got %v", sr.Results[0].Score)
	}
	if sr.Results[0].Name == "" || sr.Results[0].ID == "" {
		t.Fatalf("result missing name/id: %+v", sr.Results[0])
	}
}

func TestSearchExactNamePinIsFirstWithFiniteScore(t *testing.T) {
	pin := openCandidate("Exact Match Cafe")
	repo := fakeRepo{
		candidates: []domain.Candidate{openCandidate("Other Cafe")},
		pin:        &pin,
	}
	h := newSearchServer(t, repo, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=Exact+Match+Cafe")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	sr := decodeSearch(t, rec)
	if sr.Results[0].ID != pin.ID.String() {
		t.Fatalf("pin must be first, got %q", sr.Results[0].Name)
	}
	if sr.Results[0].Score != 1.0 {
		t.Fatalf("pinned +Inf score must surface as 1.0, got %v", sr.Results[0].Score)
	}
}

// With intent on, a categorical query ("coffee") must NOT pin the
// literally-named business — the categorical guard suppresses it.
func TestSearchIntentSuppressesPinForCategoryWord(t *testing.T) {
	pin := openCandidate("Coffee")
	repo := fakeRepo{
		candidates: []domain.Candidate{openCandidate("Panther Coffee"), openCandidate("Bean There")},
		pin:        &pin,
	}
	h := newSearchServerFF(t, repo, loadTestConfig(t), true)
	rec := doGet(t, h, "/search?q=coffee")
	sr := decodeSearch(t, rec)
	for _, r := range sr.Results {
		if r.ID == pin.ID.String() {
			t.Fatalf("categorical query pinned %q; the guard should suppress it", r.Name)
		}
	}
	for _, r := range sr.Results {
		if r.Score == 1.0 {
			t.Fatalf("no result should carry the pinned 1.0 score for a categorical query")
		}
	}
}

// With intent on, a non-categorical query (a real business name) still pins.
func TestSearchIntentKeepsPinForRealName(t *testing.T) {
	pin := openCandidate("Joe's Barber Shop")
	repo := fakeRepo{candidates: []domain.Candidate{openCandidate("Other Cafe")}, pin: &pin}
	h := newSearchServerFF(t, repo, loadTestConfig(t), true)
	rec := doGet(t, h, "/search?q=joes+barber+shop")
	sr := decodeSearch(t, rec)
	if sr.Results[0].ID != pin.ID.String() {
		t.Fatalf("non-categorical name must still pin first, got %q", sr.Results[0].Name)
	}
	if sr.Timings.IntentMS < 0 {
		t.Fatalf("intent_ms must be non-negative, got %d", sr.Timings.IntentMS)
	}
}

// With intent OFF, behavior is exactly as before: even a category word pins
// (the guard is gated by the flag).
func TestSearchPinFiresForCategoryWordWhenIntentOff(t *testing.T) {
	pin := openCandidate("Coffee")
	repo := fakeRepo{candidates: []domain.Candidate{openCandidate("Panther Coffee")}, pin: &pin}
	h := newSearchServer(t, repo, loadTestConfig(t)) // flag off
	rec := doGet(t, h, "/search?q=coffee")
	sr := decodeSearch(t, rec)
	if sr.Results[0].ID != pin.ID.String() {
		t.Fatalf("with intent off the pin must fire as before, got %q", sr.Results[0].Name)
	}
	if sr.Timings.IntentMS != 0 {
		t.Fatalf("with intent off intent_ms must be 0, got %d", sr.Timings.IntentMS)
	}
}

func TestSearchRetrievalErrorIs500(t *testing.T) {
	repo := fakeRepo{searchErr: errors.New("db down")}
	h := newSearchServer(t, repo, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSearchExactNameErrorIs500(t *testing.T) {
	repo := fakeRepo{exactErr: errors.New("trgm failure")}
	h := newSearchServer(t, repo, loadTestConfig(t))
	rec := doGet(t, h, "/search?q=coffee")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
