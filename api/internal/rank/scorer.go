package rank

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// Result pairs a scored candidate with its final score. The exact-name pin
// carries a Score of +Inf; the HTTP layer maps that for JSON.
type Result struct {
	Candidate domain.Candidate
	Score     float64
}

const (
	literalMode = "literal"
	// tieEpsilon is the score band within which two candidates are treated as
	// tied and resolved by the deterministic tie-break keys.
	tieEpsilon = 0.005
	// dePinTopN is how many leading positions the de-pin pass guards.
	dePinTopN = 2

	signalDistanceName   = "distance"
	signalRatingName     = "rating"
	signalPopularityName = "popularity"
	signalFriendsName    = "friends"
	signalClaimedName    = "claimed"
	signalPhotosName     = "photos"
	signalOpenStatusName = "open_status"

	openStatusIgnore     = "ignore"
	openStatusHardFilter = "hard_filter"
)

// Run executes the full scoring pipeline and returns the top opts.Limit
// results. pin is the optional exact-name hit (nil if none); when present it is
// pinned to position 0 with a +Inf score regardless of other signals.
func Run(
	ctx context.Context,
	candidates []domain.Candidate,
	pin *domain.Candidate,
	cfg *config.Ranking,
	opts domain.SearchOpts,
) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.SignalFormulas.Rating != literalMode || cfg.SignalFormulas.Distance != literalMode {
		return nil, fmt.Errorf(
			"rank: only literal formulas implemented at Stage 2; got rating=%q distance=%q",
			cfg.SignalFormulas.Rating, cfg.SignalFormulas.Distance,
		)
	}

	results := scoreAll(hardFilter(candidates, cfg), cfg)
	sortByScore(results)
	results = applyPin(results, pin)
	tieBreak(results)
	dePin(results, cfg.NewBusiness.SwapWindow)
	return truncate(results, opts.Limit), nil
}

// hardFilter drops candidates whose archetype demands open-status hard
// filtering and which are explicitly closed. nil/unknown hours are never
// dropped; open candidates proceed.
func hardFilter(candidates []domain.Candidate, cfg *config.Ranking) []domain.Candidate {
	kept := make([]domain.Candidate, 0, len(candidates))
	for i := range candidates {
		c := &candidates[i]
		behavior := cfg.Archetypes[c.Archetype].OpenStatus
		closed := c.IsOpenNow != nil && !*c.IsOpenNow
		if behavior == openStatusHardFilter && closed {
			continue
		}
		kept = append(kept, *c)
	}
	return kept
}

// scoreAll computes the linear-sum score for every candidate.
func scoreAll(candidates []domain.Candidate, cfg *config.Ranking) []Result {
	results := make([]Result, len(candidates))
	for i := range candidates {
		results[i] = Result{
			Candidate: candidates[i],
			Score:     scoreCandidate(&candidates[i], cfg),
		}
	}
	return results
}

// scoreCandidate sums weight_i · signal_i over the canonical signal order. An
// archetype whose open_status behavior is "ignore" excludes that signal.
func scoreCandidate(c *domain.Candidate, cfg *config.Ranking) float64 {
	arch := cfg.Archetypes[c.Archetype]
	ignoreOpen := arch.OpenStatus == openStatusIgnore
	var score float64
	for _, name := range cfg.Signals {
		if name == signalOpenStatusName && ignoreOpen {
			continue
		}
		score += arch.Weights[name] * signalValue(name, c, cfg)
	}
	return score
}

// signalValue dispatches one named signal to its pure implementation. An
// unknown name contributes 0.
func signalValue(name string, c *domain.Candidate, cfg *config.Ranking) float64 {
	switch name {
	case signalDistanceName:
		return signalDistance(c)
	case signalRatingName:
		return signalRating(c, cfg)
	case signalPopularityName:
		return signalPopularity(c, cfg)
	case signalFriendsName:
		return signalFriends(c, cfg)
	case signalClaimedName:
		return signalClaimed(c)
	case signalPhotosName:
		return signalPhotos(c, cfg)
	case signalOpenStatusName:
		return signalOpenStatus(c, cfg)
	default:
		return 0
	}
}

func sortByScore(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

// applyPin removes any duplicate of pin by ID, sets the pin's score to +Inf,
// and prepends it at position 0.
func applyPin(results []Result, pin *domain.Candidate) []Result {
	if pin == nil {
		return results
	}
	deduped := results[:0]
	for _, r := range results {
		if r.Candidate.ID != pin.ID {
			deduped = append(deduped, r)
		}
	}
	pinned := make([]Result, 0, len(deduped)+1)
	pinned = append(pinned, Result{Candidate: *pin, Score: math.Inf(1)})
	return append(pinned, deduped...)
}

// tieBreak applies the deterministic multi-key ordering: higher score, then
// within tieEpsilon claimed-beats-unclaimed, closer, more reviews, ID ascending.
// The +Inf pin stays first naturally.
func tieBreak(results []Result) {
	sort.SliceStable(results, func(i, j int) bool {
		a, b := &results[i], &results[j]
		if math.Abs(a.Score-b.Score) >= tieEpsilon {
			return a.Score > b.Score
		}
		return lessWithinTie(&a.Candidate, &b.Candidate)
	})
}

// lessWithinTie resolves two candidates whose scores are within tieEpsilon.
func lessWithinTie(a, b *domain.Candidate) bool {
	if a.IsClaimed != b.IsClaimed {
		return a.IsClaimed
	}
	if a.DistanceKM != b.DistanceKM {
		return a.DistanceKM < b.DistanceKM
	}
	if a.GoogleReviewCount != b.GoogleReviewCount {
		return a.GoogleReviewCount > b.GoogleReviewCount
	}
	return a.ID.String() < b.ID.String()
}

// dePin keeps a new business out of the top dePinTopN positions when a non-new
// candidate is within swapWindow of its score. The +Inf pin is never moved.
func dePin(results []Result, swapWindow float64) {
	for i := 0; i < dePinTopN && i < len(results); i++ {
		if math.IsInf(results[i].Score, 1) || !results[i].Candidate.IsNew {
			continue
		}
		for j := i + 1; j < len(results); j++ {
			if !results[j].Candidate.IsNew && results[i].Score-results[j].Score < swapWindow {
				results[i], results[j] = results[j], results[i]
				break
			}
		}
	}
}

func truncate(results []Result, limit int) []Result {
	if limit >= 0 && limit < len(results) {
		return results[:limit]
	}
	return results
}
