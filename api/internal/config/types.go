package config

import "github.com/danielreales00/lemon-search/api/internal/domain"

// Ranking is the typed view of config/ranking.yaml. It is the source of truth
// for the ranker (contract C3); the YAML is parsed into it and validated
// fail-fast. See docs/roadmap/05-architectural-contracts.md.
type Ranking struct {
	Signals        []string                       `yaml:"signals"`
	SignalFormulas SignalFormulas                 `yaml:"signal_formulas"`
	Archetypes     map[domain.Archetype]Archetype `yaml:"archetypes"`
	NewBusiness    NewBusiness                    `yaml:"new_business"`
	ExactName      ExactName                      `yaml:"exact_name"`
}

// SignalFormulas selects spec-literal formulas vs. alternatives and carries the
// tunable constants each formula needs. Rating and Distance are mode switches;
// the rest are scalar knobs.
type SignalFormulas struct {
	Rating          string             `yaml:"rating"`
	BayesianRating  BayesianRating     `yaml:"bayesian_rating"`
	Distance        string             `yaml:"distance"`
	DistanceDecayKM map[string]float64 `yaml:"distance_decay_km"`
	Popularity      Popularity         `yaml:"popularity"`
	Photos          Photos             `yaml:"photos"`
	Friends         Friends            `yaml:"friends"`
	OpenStatus      OpenStatus         `yaml:"open_status"`
}

// BayesianRating holds the prior used when SignalFormulas.Rating is "bayesian".
type BayesianRating struct {
	PriorStrength float64 `yaml:"prior_strength"`
	GlobalMean    float64 `yaml:"global_mean"`
	Source        string  `yaml:"source"`
}

// Popularity normalizes the review-count signal against a global ceiling.
type Popularity struct {
	GlobalMaxReviews float64 `yaml:"global_max_reviews"`
}

// Photos sets the demotion applied when a business has fewer than three photos.
type Photos struct {
	PhotoDemotionBelow3 float64 `yaml:"photo_demotion_below_3"`
}

// Friends sets the friend count at which the friends signal reaches 1.0.
type Friends struct {
	FriendsFullCredit float64 `yaml:"friends_full_credit"`
}

// OpenStatus maps each open-status bucket to its signal value, including the
// fallback used when hours are missing entirely.
type OpenStatus struct {
	Closed             float64 `yaml:"closed"`
	OpensLater         float64 `yaml:"opens_later"`
	OpenNow            float64 `yaml:"open_now"`
	UnknownHoursSignal float64 `yaml:"unknown_hours_signal"`
}

// Archetype carries one archetype's per-signal weights and its open-status
// behavior. Weights is keyed by signal name; missing keys default to 0.
type Archetype struct {
	Weights    map[string]float64 `yaml:"weights"`
	OpenStatus string             `yaml:"open_status"`
}

// NewBusiness holds the global handling for businesses below the review-count
// threshold (demotion and the de-pin swap pass).
type NewBusiness struct {
	ThresholdReviewCount int     `yaml:"threshold_review_count"`
	RatingDemoteFactor   float64 `yaml:"rating_demote_factor"`
	PinTop               bool    `yaml:"pin_top"`
	SwapWindow           float64 `yaml:"swap_window"`
}

// ExactName holds the similarity threshold for the exact-name hard-pin path.
type ExactName struct {
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
}
