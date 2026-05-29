package rank

import (
	"context"
	"math"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// loadFuzzCfg mirrors loadCfg but takes *testing.F (the fuzz harness type).
func loadFuzzCfg(f *testing.F) *config.Ranking {
	f.Helper()
	cfg, err := config.LoadFile("../../../config/ranking.yaml")
	if err != nil {
		f.Fatalf("loading default config: %v", err)
	}
	return cfg
}

// fuzzArchetype maps a fuzzed byte onto one of the six real archetypes, plus an
// out-of-set value to exercise the unknown-archetype path (weights default to a
// zero map; the scorer must not panic on a missing key).
func fuzzArchetype(b uint8) domain.Archetype {
	all := []domain.Archetype{
		domain.ArchetypeLowStakesFastNearby,
		domain.ArchetypeMediumStakesOccasion,
		domain.ArchetypeHighStakesOneTime,
		domain.ArchetypeExperiential,
		domain.ArchetypeRecurringService,
		domain.ArchetypeUtilityDistanceDominant,
		domain.Archetype("does_not_exist"),
	}
	return all[int(b)%len(all)]
}

func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// finiteCand reports whether every numeric candidate field is finite. The hot
// path must never emit a non-finite score for finite inputs, so this gates the
// finiteness assertions.
func finiteCand(c *domain.Candidate) bool {
	if !isFinite(c.DistanceKM) {
		return false
	}
	if c.LemonScore != nil && !isFinite(*c.LemonScore) {
		return false
	}
	return true
}

// inDomain reports whether the candidate's numeric fields fall inside the ranges
// the retrieval contract (C2) guarantees: lemon_score 0..10, distance ≥ 0,
// counts ≥ 0. The spec-literal signal formulas trust these bounds rather than
// re-clamping, so the signal∈[0,1] invariant is only asserted on in-domain draws
// — out-of-domain values are validated upstream, never by the ranker.
func inDomain(c *domain.Candidate) bool {
	if !finiteCand(c) || c.DistanceKM < 0 {
		return false
	}
	if c.LemonScore != nil && (*c.LemonScore < 0 || *c.LemonScore > 10) {
		return false
	}
	return c.GoogleReviewCount >= 0 && c.FriendCount >= 0 && c.PhotoCount >= 0
}

// FuzzScoreCandidate throws randomized candidate fields at the pure linear-sum
// scorer. For EVERY input the scorer must not panic; for finite inputs the
// composed score must stay finite; and for in-domain inputs each signal must
// land in [0,1] and the score must be non-negative (clamped signals × non-
// negative weights).
func FuzzScoreCandidate(f *testing.F) {
	cfg := loadFuzzCfg(f)

	// Seeds: a normal candidate, the zero candidate, and an in-domain extreme.
	f.Add(uint8(0), 1.5, 9.2, true, 420, 2, 3, true, false, true, false, false)
	f.Add(uint8(6), 0.0, 0.0, false, 0, 0, 0, false, false, false, false, false)
	f.Add(uint8(2), 48.28, 10.0, true, math.MaxInt32, math.MaxInt32, math.MaxInt32, true, true, true, true, true)
	f.Add(uint8(3), 0.0, 0.0, true, 0, 0, 0, false, true, false, true, false)

	f.Fuzz(func(t *testing.T,
		arch uint8, distKM, lemon float64, hasLemon bool,
		reviews, friends, photos int,
		claimed, isNew, hasOpen, openNow, opensLater bool,
	) {
		c := domain.Candidate{
			ID:                id(1),
			Archetype:         fuzzArchetype(arch),
			DistanceKM:        distKM,
			GoogleReviewCount: reviews,
			FriendCount:       friends,
			PhotoCount:        photos,
			IsClaimed:         claimed,
			IsNew:             isNew,
			OpensLater:        opensLater,
		}
		if hasLemon {
			c.LemonScore = ptrF(lemon)
		}
		if hasOpen {
			c.IsOpenNow = ptrB(openNow)
		}

		// Every signal must be computable without panic; on in-domain draws each
		// must fall within the spec's clamped [0,1] range.
		for _, name := range []string{
			signalDistanceName, signalRatingName, signalPopularityName,
			signalFriendsName, signalClaimedName, signalPhotosName, signalOpenStatusName,
		} {
			v := signalValue(name, &c, cfg)
			if inDomain(&c) && (math.IsNaN(v) || v < 0 || v > 1) {
				t.Fatalf("signal %q out of [0,1] for in-domain candidate %+v: got %v", name, c, v)
			}
		}

		score := scoreCandidate(&c, cfg)
		if finiteCand(&c) && !isFinite(score) {
			t.Fatalf("non-finite score %v for finite candidate %+v", score, c)
		}
		if inDomain(&c) && score < 0 {
			t.Fatalf("negative score %v for in-domain candidate %+v", score, c)
		}
	})
}

// FuzzRun drives the whole pipeline (filter → score → sort → pin → tie-break →
// de-pin → truncate) with two fuzzed candidates and an optional pin. The
// contract: no panic on any input, a result count within bounds, and a finite
// score on every NON-pinned result (the pin alone carries the sentinel +Inf,
// which the HTTP layer remaps).
func FuzzRun(f *testing.F) {
	cfg := loadFuzzCfg(f)

	f.Add(uint8(0), 1.5, 9.0, true, 100, true, true, uint8(1), 2.0, 8.0, false, 5, false, true, 15)
	f.Add(uint8(6), 0.0, 0.0, false, 0, false, false, uint8(0), 0.0, 0.0, true, 0, true, false, 0)
	f.Add(uint8(2), 40.0, 1.0, true, 9000, true, true, uint8(3), 1e6, 10.0, true, math.MaxInt32, true, true, 3)

	f.Fuzz(func(t *testing.T,
		archA uint8, distA, lemonA float64, hasLemonA bool, reviewsA int, isNewA, openA bool,
		archB uint8, distB, lemonB float64, hasLemonB bool, reviewsB int, isNewB, withPin bool,
		limit int,
	) {
		a := buildCand(2, fuzzArchetype(archA), distA, lemonA, hasLemonA, reviewsA, isNewA, openA)
		b := buildCand(3, fuzzArchetype(archB), distB, lemonB, hasLemonB, reviewsB, isNewB, openA)
		cands := []domain.Candidate{a, b}

		var pin *domain.Candidate
		if withPin {
			p := buildCand(1, fuzzArchetype(archB), distB, lemonB, hasLemonB, reviewsB, isNewB, openA)
			pin = &p
		}

		got, err := Run(context.Background(), cands, pin, cfg, domain.SearchOpts{Limit: limit})
		if err != nil {
			t.Fatalf("Run errored on valid literal config: %v", err)
		}
		if limit >= 0 && len(got) > limit {
			t.Fatalf("result count %d exceeds limit %d", len(got), limit)
		}
		allFinite := finiteCand(&a) && finiteCand(&b) && (pin == nil || finiteCand(pin))
		for i := range got {
			r := &got[i]
			isPin := pin != nil && r.Candidate.ID == pin.ID
			if isPin {
				continue // the pin's sentinel +Inf is intentional; remapped at the HTTP layer.
			}
			if math.IsNaN(r.Score) {
				t.Fatalf("NaN score for non-pinned result %+v (inputs a=%+v b=%+v)", r.Candidate, a, b)
			}
			if allFinite && !isFinite(r.Score) {
				t.Fatalf("non-finite score %v for non-pinned result from finite inputs", r.Score)
			}
		}
	})
}

func buildCand(n int, arch domain.Archetype, distKM, lemon float64, hasLemon bool, reviews int, isNew, open bool) domain.Candidate {
	c := domain.Candidate{
		ID:                id(n),
		Archetype:         arch,
		DistanceKM:        distKM,
		GoogleReviewCount: reviews,
		IsNew:             isNew,
		IsOpenNow:         ptrB(open),
	}
	if hasLemon {
		c.LemonScore = ptrF(lemon)
	}
	return c
}
