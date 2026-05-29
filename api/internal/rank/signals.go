package rank

import (
	"math"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// Named constants for spec values that are not config-tunable. The mnd linter
// would otherwise flag these as magic numbers; they are fixed by the spec.
const (
	// distanceCapKM is 30 miles in kilometers — the literal-mode distance cap.
	distanceCapKM = 48.28
	// ratingScale converts a 0..10 lemon_score into the 0..1 rating signal.
	ratingScale = 10.0
	// bayesianScale normalizes the Bayesian-smoothed rating (on the source's
	// own scale; google_rating, the default source, is 0..5) into 0..1.
	bayesianScale = 5.0
	// photosFullCredit is the photo count at/above which photos score 1.0.
	photosFullCredit = 3

	bayesianSourceLemon = "lemon_score"
)

// signalDistance dispatches to the configured distance formula. Both formulas
// return 0..1; a candidate with no location arrives with DistanceKM ≥ the cap
// (or +Inf) from retrieval, which floors both to 0.
func signalDistance(c *domain.Candidate, cfg *config.Ranking) float64 {
	if cfg.SignalFormulas.Distance == distanceDecayMode {
		return distanceDecay(c, cfg)
	}
	return math.Max(1-c.DistanceKM/distanceCapKM, 0)
}

// distanceDecay is per-archetype exponential decay: exp(-d / decay_km[arch]).
// A missing or non-positive decay constant falls back to the literal cap so a
// misconfigured archetype degrades gracefully instead of dividing by zero.
func distanceDecay(c *domain.Candidate, cfg *config.Ranking) float64 {
	km := cfg.SignalFormulas.DistanceDecayKM[string(c.Archetype)]
	if km <= 0 {
		return math.Max(1-c.DistanceKM/distanceCapKM, 0)
	}
	return math.Exp(-c.DistanceKM / km)
}

// signalRating dispatches to the configured rating formula, then applies the
// new-business demotion to whichever base value it produced.
func signalRating(c *domain.Candidate, cfg *config.Ranking) float64 {
	base := literalRating(c)
	if cfg.SignalFormulas.Rating == ratingBayesianMode {
		base = bayesianRating(c, cfg)
	}
	if c.IsNew {
		return base * cfg.NewBusiness.RatingDemoteFactor
	}
	return base
}

// literalRating is the spec-literal lemon_score/10. A nil lemon_score scores 0.
func literalRating(c *domain.Candidate) float64 {
	if c.LemonScore == nil {
		return 0
	}
	return *c.LemonScore / ratingScale
}

// bayesianRating smooths the rating source toward the global mean by review
// count: ((C*m + n*r) / (C + n)) / scale. A nil source value scores 0.
func bayesianRating(c *domain.Candidate, cfg *config.Ranking) float64 {
	b := cfg.SignalFormulas.BayesianRating
	r := c.GoogleRating
	if b.Source == bayesianSourceLemon {
		r = c.LemonScore
	}
	if r == nil {
		return 0
	}
	n := float64(c.GoogleReviewCount)
	return ((b.PriorStrength*b.GlobalMean + n*(*r)) / (b.PriorStrength + n)) / bayesianScale
}

// signalPopularity is log-scaled review confidence normalized against a global
// ceiling so 800 reactions do not bury 50. Non-positive counts score 0; counts
// above the ceiling clamp to 1.0.
func signalPopularity(c *domain.Candidate, cfg *config.Ranking) float64 {
	n := c.GoogleReviewCount
	if n <= 0 {
		return 0
	}
	v := math.Log1p(float64(n)) / math.Log1p(cfg.SignalFormulas.Popularity.GlobalMaxReviews)
	return math.Min(v, 1.0)
}

// signalFriends scales the friend count to full credit, capped at 1.0.
func signalFriends(c *domain.Candidate, cfg *config.Ranking) float64 {
	return math.Min(float64(c.FriendCount)/cfg.SignalFormulas.Friends.FriendsFullCredit, 1.0)
}

// signalClaimed is a pure step: claimed scores 1.0, unclaimed 0.0. The boost
// itself lives in the archetype weight.
func signalClaimed(c *domain.Candidate) float64 {
	if c.IsClaimed {
		return 1.0
	}
	return 0.0
}

// signalPhotos gives full credit at 3+ photos, a configured demotion below.
func signalPhotos(c *domain.Candidate, cfg *config.Ranking) float64 {
	if c.PhotoCount >= photosFullCredit {
		return 1.0
	}
	return cfg.SignalFormulas.Photos.PhotoDemotionBelow3
}

// signalOpenStatus maps the four open states to their configured signal values,
// reading both IsOpenNow (nil = unknown hours) and OpensLater.
func signalOpenStatus(c *domain.Candidate, cfg *config.Ranking) float64 {
	os := cfg.SignalFormulas.OpenStatus
	if c.IsOpenNow == nil {
		return os.UnknownHoursSignal
	}
	if *c.IsOpenNow {
		return os.OpenNow
	}
	if c.OpensLater {
		return os.OpensLater
	}
	return os.Closed
}
