package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// validYAML is a minimal-but-complete ranking config. Tests mutate a copy of
// this (via string replacement) to exercise individual validation failures.
const validYAML = `
signals:
  - distance
  - rating
  - popularity
  - friends
  - claimed
  - photos
  - open_status

signal_formulas:
  rating: literal
  bayesian_rating:
    prior_strength: 20
    global_mean: 4.3
    source: google_rating
  distance: literal
  distance_decay_km:
    utility_distance_dominant: 3
    low_stakes_fast_nearby: 8
  popularity:
    global_max_reviews: 10000
  photos:
    photo_demotion_below_3: 0.25
  friends:
    friends_full_credit: 5
  open_status:
    closed: 0.0
    opens_later: 0.3
    open_now: 1.0
    unknown_hours_signal: 0.7

archetypes:
  low_stakes_fast_nearby:
    weights:
      distance: 0.25
      rating: 0.18
    open_status: hard_filter
  utility_distance_dominant:
    weights:
      distance: 0.45
    open_status: soft

new_business:
  threshold_review_count: 10
  rating_demote_factor: 0.85
  pin_top: false
  swap_window: 0.05

exact_name:
  similarity_threshold: 0.85
`

func TestLoadValidRoundTrip(t *testing.T) {
	t.Parallel()

	cfg, err := Load(strings.NewReader(validYAML))
	if err != nil {
		t.Fatalf("Load returned error on valid config: %v", err)
	}

	if got, want := len(cfg.Signals), 7; got != want {
		t.Errorf("signals length = %d, want %d", got, want)
	}
	if got, want := cfg.Signals[0], "distance"; got != want {
		t.Errorf("signals[0] = %q, want %q", got, want)
	}
	if got, want := cfg.SignalFormulas.Rating, "literal"; got != want {
		t.Errorf("rating mode = %q, want %q", got, want)
	}
	if got, want := cfg.SignalFormulas.Distance, "literal"; got != want {
		t.Errorf("distance mode = %q, want %q", got, want)
	}
	if got, want := cfg.SignalFormulas.BayesianRating.PriorStrength, 20.0; got != want {
		t.Errorf("prior_strength = %v, want %v", got, want)
	}
	if got, want := cfg.SignalFormulas.Popularity.GlobalMaxReviews, 10000.0; got != want {
		t.Errorf("global_max_reviews = %v, want %v", got, want)
	}
	if got, want := cfg.SignalFormulas.OpenStatus.UnknownHoursSignal, 0.7; got != want {
		t.Errorf("unknown_hours_signal = %v, want %v", got, want)
	}
	if got, want := cfg.SignalFormulas.DistanceDecayKM["utility_distance_dominant"], 3.0; got != want {
		t.Errorf("distance_decay_km[utility] = %v, want %v", got, want)
	}
	if got, want := cfg.NewBusiness.ThresholdReviewCount, 10; got != want {
		t.Errorf("threshold_review_count = %d, want %d", got, want)
	}
	if got, want := cfg.NewBusiness.RatingDemoteFactor, 0.85; got != want {
		t.Errorf("rating_demote_factor = %v, want %v", got, want)
	}
	if got, want := cfg.ExactName.SimilarityThreshold, 0.85; got != want {
		t.Errorf("similarity_threshold = %v, want %v", got, want)
	}

	arch, ok := cfg.Archetypes[domain.ArchetypeLowStakesFastNearby]
	if !ok {
		t.Fatalf("missing archetype %q", domain.ArchetypeLowStakesFastNearby)
	}
	if got, want := arch.OpenStatus, "hard_filter"; got != want {
		t.Errorf("low_stakes open_status = %q, want %q", got, want)
	}
	if got, want := arch.Weights["distance"], 0.25; got != want {
		t.Errorf("low_stakes distance weight = %v, want %v", got, want)
	}
}

// Missing weight keys must default to 0, not error (contract C3).
func TestLoadMissingWeightDefaultsToZero(t *testing.T) {
	t.Parallel()

	cfg, err := Load(strings.NewReader(validYAML))
	if err != nil {
		t.Fatalf("Load returned error on valid config: %v", err)
	}

	arch := cfg.Archetypes[domain.ArchetypeLowStakesFastNearby]
	if got := arch.Weights["claimed"]; got != 0 {
		t.Errorf("absent claimed weight = %v, want 0", got)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		old     string
		new     string
		wantStr string
	}{
		{
			name:    "unknown archetype",
			old:     "low_stakes_fast_nearby:\n    weights:",
			new:     "made_up_archetype:\n    weights:",
			wantStr: "unknown archetype",
		},
		{
			name:    "bad open_status",
			old:     "open_status: hard_filter",
			new:     "open_status: sometimes",
			wantStr: "open_status",
		},
		{
			name:    "bad rating mode",
			old:     "rating: literal",
			new:     "rating: clever",
			wantStr: "signal_formulas.rating",
		},
		{
			name:    "bad distance mode",
			old:     "distance: literal",
			new:     "distance: nonlinear",
			wantStr: "signal_formulas.distance",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := strings.Replace(validYAML, tc.old, tc.new, 1)
			if src == validYAML {
				t.Fatalf("test setup: replacement %q not found", tc.old)
			}

			_, err := Load(strings.NewReader(src))
			if err == nil {
				t.Fatalf("Load accepted invalid config (%s)", tc.name)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("error %v is not ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), tc.wantStr) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantStr)
			}
		})
	}
}

func TestLoadMissingRequiredBlock(t *testing.T) {
	t.Parallel()

	// Drop the entire archetypes block (everything from "archetypes:" up to the
	// next top-level block).
	start := strings.Index(validYAML, "archetypes:")
	end := strings.Index(validYAML, "new_business:")
	if start < 0 || end < 0 {
		t.Fatal("test setup: archetypes/new_business markers not found")
	}
	src := validYAML[:start] + validYAML[end:]

	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("Load accepted config with no archetypes block")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("error %v is not ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "archetypes") {
		t.Errorf("error %q does not mention archetypes", err.Error())
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	t.Parallel()

	_, err := Load(strings.NewReader("signals: [unterminated"))
	if err == nil {
		t.Fatal("Load accepted malformed YAML")
	}
	if !strings.Contains(err.Error(), "decoding ranking config") {
		t.Errorf("error %q does not wrap decode failure", err.Error())
	}
}

// LoadFile must parse the real config/ranking.yaml so the committed file and
// the Go schema stay in lockstep.
func TestLoadFileRealConfig(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFile(realConfigPath(t))
	if err != nil {
		t.Fatalf("LoadFile(ranking.yaml) failed: %v", err)
	}
	if got, want := len(cfg.Signals), 7; got != want {
		t.Errorf("real config signals length = %d, want %d", got, want)
	}
	if got, want := len(cfg.Archetypes), 6; got != want {
		t.Errorf("real config archetypes count = %d, want %d", got, want)
	}
	for name := range cfg.Archetypes {
		if !validArchetype(name) {
			t.Errorf("real config has unknown archetype %q", name)
		}
	}
}

// realConfigPath resolves config/ranking.yaml relative to this test file so it
// works regardless of the caller's working directory.
func realConfigPath(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../api/internal/config/loader_test.go → repo root is three levels up.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(repoRoot, "config", "ranking.yaml")
}
