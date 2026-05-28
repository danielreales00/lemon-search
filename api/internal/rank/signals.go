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
	// photosFullCredit is the photo count at/above which photos score 1.0.
	photosFullCredit = 3
)

// signalDistance implements the literal distance formula: closer is higher,
// capped at 30 miles. A candidate with no location arrives with DistanceKM set
// to a value ≥ the cap (or +Inf) by retrieval, which naturally floors to 0.
func signalDistance(c *domain.Candidate) float64 {
	return math.Max(1-c.DistanceKM/distanceCapKM, 0)
}

// signalRating is lemon_score/10, demoted by the new-business factor when the
// candidate is new. A nil lemon_score scores 0.
func signalRating(c *domain.Candidate, cfg *config.Ranking) float64 {
	if c.LemonScore == nil {
		return 0
	}
	demote := 1.0
	if c.IsNew {
		demote = cfg.NewBusiness.RatingDemoteFactor
	}
	return (*c.LemonScore / ratingScale) * demote
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
