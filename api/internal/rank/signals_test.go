package rank

import (
	"math"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// loadCfg loads the real shipping default config so tests validate against it.
func loadCfg(t *testing.T) *config.Ranking {
	t.Helper()
	cfg, err := config.LoadFile("../../../config/ranking.yaml")
	if err != nil {
		t.Fatalf("loading default config: %v", err)
	}
	return cfg
}

func ptrF(v float64) *float64 { return &v }
func ptrB(v bool) *bool       { return &v }

const eps = 1e-9

func almostEqual(a, b float64) bool { return math.Abs(a-b) < eps }

// TestSignalDistanceLiteral pins the spec-literal default: closer is higher,
// capped at 30 mi. These are the values the shipping config must reproduce.
func TestSignalDistanceLiteral(t *testing.T) {
	cfg := loadCfg(t) // default config ships distance = literal
	tests := []struct {
		name string
		km   float64
		want float64
	}{
		{"at user location", 0, 1.0},
		{"halfway to cap", distanceCapKM / 2, 0.5},
		{"exactly at cap", distanceCapKM, 0.0},
		{"beyond cap floors to zero", 100, 0.0},
		{"infinite distance floors to zero", math.Inf(1), 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{DistanceKM: tc.km}
			if got := signalDistance(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalDistance(%v) = %v, want %v", tc.km, got, tc.want)
			}
		})
	}
}

// TestSignalDistanceDecay covers the per-archetype exponential mode and the
// graceful fallback when an archetype has no (or a non-positive) decay_km.
func TestSignalDistanceDecay(t *testing.T) {
	cfg := loadCfg(t)
	cfg.SignalFormulas.Distance = distanceDecayMode
	decay := cfg.SignalFormulas.DistanceDecayKM
	utilKM := decay["utility_distance_dominant"]
	tests := []struct {
		name string
		arch domain.Archetype
		km   float64
		want float64
	}{
		{"at user location scores one", domain.ArchetypeUtilityDistanceDominant, 0, 1.0},
		{"utility at one decay-length", domain.ArchetypeUtilityDistanceDominant, utilKM, math.Exp(-1)},
		{"experiential decays slower at same km", domain.ArchetypeExperiential, utilKM, math.Exp(-utilKM / decay["experiential"])},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{Archetype: tc.arch, DistanceKM: tc.km}
			if got := signalDistance(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalDistance(%s,%v) = %v, want %v", tc.arch, tc.km, got, tc.want)
			}
		})
	}

	// Missing/zero decay constant falls back to the literal cap.
	cfg.SignalFormulas.DistanceDecayKM = map[string]float64{}
	c := domain.Candidate{Archetype: domain.ArchetypeExperiential, DistanceKM: distanceCapKM / 2}
	if got := signalDistance(&c, cfg); !almostEqual(got, 0.5) {
		t.Fatalf("missing decay_km should fall back to literal 0.5, got %v", got)
	}
}

// TestSignalRatingLiteral pins the spec-literal default lemon_score/10 plus the
// new-business demotion. The shipping config must reproduce these values.
func TestSignalRatingLiteral(t *testing.T) {
	cfg := loadCfg(t) // default config ships rating = literal
	demote := cfg.NewBusiness.RatingDemoteFactor
	tests := []struct {
		name  string
		score *float64
		isNew bool
		want  float64
	}{
		{"nil lemon score scores zero", nil, false, 0},
		{"full score", ptrF(10), false, 1.0},
		{"mid score", ptrF(9.2), false, 0.92},
		{"zero score", ptrF(0), false, 0},
		{"new business demoted", ptrF(10), true, demote},
		{"new business mid demoted", ptrF(9.2), true, 0.92 * demote},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{LemonScore: tc.score, IsNew: tc.isNew}
			if got := signalRating(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalRating = %v, want %v", got, tc.want)
			}
		})
	}
}

// bayes computes the expected Bayesian rating signal for a test row, mirroring
// the formula so the table states intent rather than re-deriving it inline.
func bayes(r, n, c, m float64) float64 { return ((c*m + n*r) / (c + n)) / bayesianScale }

// TestSignalRatingBayesian covers the bayesian switch: the smoothing math, the
// source selector (google_rating vs lemon_score), nil sources, and that the
// new-business demotion still composes on top.
func TestSignalRatingBayesian(t *testing.T) {
	cfg := loadCfg(t)
	cfg.SignalFormulas.Rating = ratingBayesianMode
	b := cfg.SignalFormulas.BayesianRating // C=20, m=4.3, source=google_rating
	demote := cfg.NewBusiness.RatingDemoteFactor
	tests := []struct {
		name    string
		gRating *float64
		lemon   *float64
		reviews int
		isNew   bool
		want    float64
	}{
		{"nil source scores zero", nil, ptrF(9), 100, false, 0},
		{"zero reviews pulls to prior mean", ptrF(5.0), nil, 0, false, b.GlobalMean / bayesianScale},
		{"high reviews pull toward observed", ptrF(4.0), nil, 1000, false, bayes(4.0, 1000, b.PriorStrength, b.GlobalMean)},
		{"few reviews stay near prior", ptrF(5.0), nil, 5, false, bayes(5.0, 5, b.PriorStrength, b.GlobalMean)},
		{"new business demotion composes", ptrF(4.0), nil, 1000, true, bayes(4.0, 1000, b.PriorStrength, b.GlobalMean) * demote},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{GoogleRating: tc.gRating, LemonScore: tc.lemon, GoogleReviewCount: tc.reviews, IsNew: tc.isNew}
			if got := signalRating(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalRating(bayesian) = %v, want %v", got, tc.want)
			}
		})
	}

	// source: lemon_score reads LemonScore instead of GoogleRating.
	cfg.SignalFormulas.BayesianRating.Source = bayesianSourceLemon
	c := domain.Candidate{LemonScore: ptrF(8.0), GoogleRating: ptrF(2.0), GoogleReviewCount: 50}
	want := bayes(8.0, 50, b.PriorStrength, b.GlobalMean)
	if got := signalRating(&c, cfg); !almostEqual(got, want) {
		t.Fatalf("bayesian source=lemon_score = %v, want %v (must read lemon, not google)", got, want)
	}
}

func TestSignalPopularity(t *testing.T) {
	cfg := loadCfg(t)
	gmax := cfg.SignalFormulas.Popularity.GlobalMaxReviews
	tests := []struct {
		name string
		n    int
		want float64
	}{
		{"zero reviews", 0, 0},
		{"negative treated as zero", -5, 0},
		{"at global max scores one", int(gmax), 1.0},
		{"above global max clamps to one", int(gmax) + 5000, 1.0},
		{"fifty reviews mid-range", 50, math.Log1p(50) / math.Log1p(gmax)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{GoogleReviewCount: tc.n}
			if got := signalPopularity(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalPopularity(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
	// Spec sanity: 800 reactions should not bury 50.
	c50 := domain.Candidate{GoogleReviewCount: 50}
	c800 := domain.Candidate{GoogleReviewCount: 800}
	if signalPopularity(&c800, cfg)-signalPopularity(&c50, cfg) > 0.5 {
		t.Fatalf("popularity gap between 800 and 50 too large")
	}
}

func TestSignalFriends(t *testing.T) {
	cfg := loadCfg(t)
	full := cfg.SignalFormulas.Friends.FriendsFullCredit
	tests := []struct {
		name string
		n    int
		want float64
	}{
		{"no friends", 0, 0},
		{"below full credit", 2, 2 / full},
		{"one below full credit", int(full) - 1, (full - 1) / full},
		{"at full credit caps", int(full), 1.0},
		{"above full credit caps", int(full) + 4, 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{FriendCount: tc.n}
			if got := signalFriends(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalFriends(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}

func TestSignalClaimed(t *testing.T) {
	if got := signalClaimed(&domain.Candidate{IsClaimed: true}); got != 1.0 {
		t.Fatalf("claimed=true: got %v, want 1.0", got)
	}
	if got := signalClaimed(&domain.Candidate{IsClaimed: false}); got != 0.0 {
		t.Fatalf("claimed=false: got %v, want 0.0", got)
	}
}

func TestSignalPhotos(t *testing.T) {
	cfg := loadCfg(t)
	below := cfg.SignalFormulas.Photos.PhotoDemotionBelow3
	tests := []struct {
		name  string
		count int
		want  float64
	}{
		{"zero photos demoted", 0, below},
		{"two photos demoted", 2, below},
		{"exactly three full credit", 3, 1.0},
		{"many photos full credit", 99, 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{PhotoCount: tc.count}
			if got := signalPhotos(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalPhotos(%d) = %v, want %v", tc.count, got, tc.want)
			}
		})
	}
}

func TestSignalOpenStatus(t *testing.T) {
	cfg := loadCfg(t)
	os := cfg.SignalFormulas.OpenStatus
	tests := []struct {
		name       string
		isOpenNow  *bool
		opensLater bool
		want       float64
	}{
		{"nil hours unknown soft-open", nil, false, os.UnknownHoursSignal},
		{"open now", ptrB(true), false, os.OpenNow},
		{"open now ignores opens-later flag", ptrB(true), true, os.OpenNow},
		{"closed but opens later", ptrB(false), true, os.OpensLater},
		{"closed all day", ptrB(false), false, os.Closed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := domain.Candidate{IsOpenNow: tc.isOpenNow, OpensLater: tc.opensLater}
			if got := signalOpenStatus(&c, cfg); !almostEqual(got, tc.want) {
				t.Fatalf("signalOpenStatus = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSignalValueUnknown covers the dispatch default branch.
func TestSignalValueUnknown(t *testing.T) {
	cfg := loadCfg(t)
	c := domain.Candidate{}
	if got := signalValue("not_a_signal", &c, cfg); got != 0 {
		t.Fatalf("unknown signal: got %v, want 0", got)
	}
}
