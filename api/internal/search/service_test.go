package search

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
)

func ptr[T any](v T) *T { return &v }

// fakeRepo is an in-memory domain.BusinessRepo for service tests. It also
// records the SearchOpts it was called with, so a test can assert how the
// intent overlay was (or was not) threaded into retrieval.
type fakeRepo struct {
	candidates []domain.Candidate
	pin        *domain.Candidate
	searchErr  error
	exactErr   error
	gotOpts    domain.SearchOpts
}

func (f *fakeRepo) Search(_ context.Context, _ string, opts domain.SearchOpts) ([]domain.Candidate, error) {
	f.gotOpts = opts
	return f.candidates, f.searchErr
}

func (f *fakeRepo) ExactName(_ context.Context, _ string) (domain.Candidate, bool, error) {
	if f.exactErr != nil {
		return domain.Candidate{}, false, f.exactErr
	}
	if f.pin != nil {
		return *f.pin, true, nil
	}
	return domain.Candidate{}, false, nil
}

func loadConfig(t *testing.T) *config.Ranking {
	t.Helper()
	cfg, err := config.LoadFile("../../../config/ranking.yaml")
	if err != nil {
		t.Fatalf("load ranking config: %v", err)
	}
	return cfg
}

func newService(t *testing.T, repo domain.BusinessRepo, intentEnabled bool) *Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(log, repo, loadConfig(t), intentEnabled)
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

func pinned(ranked []rank.Result) bool {
	return len(ranked) > 0 && math.IsInf(ranked[0].Score, 1)
}

func TestServicePinFiresForRealName(t *testing.T) {
	pin := openCandidate("Joe's Barber Shop")
	repo := &fakeRepo{candidates: []domain.Candidate{openCandidate("Other Cafe")}, pin: &pin}
	svc := newService(t, repo, true) // intent on

	ranked, _, err := svc.Search(context.Background(), "joes barber shop", domain.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("want results, got none")
	}
	if ranked[0].Candidate.ID != pin.ID {
		t.Fatalf("non-categorical name must pin first, got %q", ranked[0].Candidate.Name)
	}
	if !math.IsInf(ranked[0].Score, 1) {
		t.Fatalf("pinned result must carry +Inf score, got %v", ranked[0].Score)
	}
}

func TestServiceSuppressesPinForCategoryWord(t *testing.T) {
	pin := openCandidate("Coffee")
	repo := &fakeRepo{
		candidates: []domain.Candidate{openCandidate("Panther Coffee"), openCandidate("Bean There")},
		pin:        &pin,
	}
	svc := newService(t, repo, true) // intent on

	ranked, _, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range ranked {
		if r.Candidate.ID == pin.ID {
			t.Fatalf("categorical query pinned %q; the guard should suppress it", r.Candidate.Name)
		}
		if math.IsInf(r.Score, 1) {
			t.Fatal("no result should carry the +Inf pin score for a categorical query")
		}
	}
}

// With intent on, the extracted overlay is threaded onto the retrieval
// SearchOpts. "coffee" maps to category Food & Drinks + specific tag coffee.
func TestServiceThreadsOverlayWhenIntentOn(t *testing.T) {
	repo := &fakeRepo{}
	svc := newService(t, repo, true)

	if _, _, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if repo.gotOpts.Overlay.CategoryFilter == nil || *repo.gotOpts.Overlay.CategoryFilter != "Food & Drinks" {
		t.Fatalf("CategoryFilter = %v, want Food & Drinks", repo.gotOpts.Overlay.CategoryFilter)
	}
	if repo.gotOpts.Limit != candidateLimit {
		t.Fatalf("retrieval Limit = %d, want candidateLimit %d", repo.gotOpts.Limit, candidateLimit)
	}
}

// With intent off, behavior matches the pre-extractor path: no overlay, no
// categorical guard, so a category word still pins, and intent time is zero.
func TestServicePinFiresForCategoryWordWhenIntentOff(t *testing.T) {
	pin := openCandidate("Coffee")
	repo := &fakeRepo{candidates: []domain.Candidate{openCandidate("Panther Coffee")}, pin: &pin}
	svc := newService(t, repo, false) // intent off

	ranked, timings, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !pinned(ranked) || ranked[0].Candidate.ID != pin.ID {
		t.Fatal("with intent off the pin must fire as before")
	}
	if timings.IntentMS != 0 {
		t.Fatalf("with intent off intent_ms must be 0, got %d", timings.IntentMS)
	}
	if !repo.gotOpts.Overlay.IsZero() {
		t.Fatalf("overlay must be zero with intent off, got %+v", repo.gotOpts.Overlay)
	}
}

func TestServiceTimingsPopulated(t *testing.T) {
	repo := &fakeRepo{candidates: []domain.Candidate{openCandidate("Joe's Coffee")}}
	svc := newService(t, repo, false)

	_, timings, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if timings.SQLMS < 0 || timings.RerankMS < 0 || timings.IntentMS < 0 {
		t.Fatalf("timings must be non-negative, got %+v", timings)
	}
}

func TestServiceRetrievalErrorIsWrapped(t *testing.T) {
	repo := &fakeRepo{searchErr: errors.New("db down")}
	svc := newService(t, repo, false)

	_, _, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{})
	if err == nil {
		t.Fatal("want error when retrieval fails")
	}
}

func TestServiceExactNameErrorIsWrapped(t *testing.T) {
	repo := &fakeRepo{exactErr: errors.New("trgm failure")}
	svc := newService(t, repo, false)

	_, _, err := svc.Search(context.Background(), "coffee", domain.SearchOpts{})
	if err == nil {
		t.Fatal("want error when exact-name lookup fails")
	}
}
