package rank

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
)

func id(n int) uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-0000000000" + string(rune('0'+n/10)) + string(rune('0'+n%10)))
}

func defaultOpts() domain.SearchOpts { return domain.SearchOpts{Limit: 15} }

// runScores is a helper that asserts no error and returns results.
func runScores(t *testing.T, cands []domain.Candidate, pin *domain.Candidate, cfg *config.Ranking) []Result {
	t.Helper()
	got, err := Run(context.Background(), cands, pin, cfg, defaultOpts())
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return got
}

// TestRunWorkedExample reproduces the docs/ranking/semantics.md worked example.
func TestRunWorkedExample(t *testing.T) {
	cfg := loadCfg(t)
	c := domain.Candidate{
		ID:                id(1),
		Archetype:         domain.ArchetypeLowStakesFastNearby,
		DistanceKM:        1.5,
		LemonScore:        ptrF(9.2),
		GoogleReviewCount: 420,
		FriendCount:       2,
		IsClaimed:         true,
		PhotoCount:        8,
		IsOpenNow:         ptrB(true),
	}
	got := runScores(t, []domain.Candidate{c}, nil, cfg)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	const want = 0.8643
	if math.Abs(got[0].Score-want) > 0.001 {
		t.Fatalf("worked-example score = %v, want ≈ %v", got[0].Score, want)
	}
}

// TestHardFilterDropsClosedLowStakes: a closed low_stakes_fast_nearby is dropped.
func TestHardFilterDropsClosedLowStakes(t *testing.T) {
	cfg := loadCfg(t)
	cands := []domain.Candidate{
		{ID: id(1), Archetype: domain.ArchetypeLowStakesFastNearby, IsOpenNow: ptrB(false), LemonScore: ptrF(9)},
		{ID: id(2), Archetype: domain.ArchetypeLowStakesFastNearby, IsOpenNow: ptrB(true), LemonScore: ptrF(5)},
	}
	got := runScores(t, cands, nil, cfg)
	if len(got) != 1 {
		t.Fatalf("expected closed low_stakes dropped, got %d results", len(got))
	}
	if got[0].Candidate.ID != id(2) {
		t.Fatalf("expected the open candidate to survive, got %v", got[0].Candidate.ID)
	}
}

// TestHardFilterKeepsUnknownHours: nil hours is never dropped even for hard_filter.
func TestHardFilterKeepsUnknownHours(t *testing.T) {
	cfg := loadCfg(t)
	cands := []domain.Candidate{
		{ID: id(1), Archetype: domain.ArchetypeUtilityDistanceDominant, IsOpenNow: nil, LemonScore: ptrF(9)},
	}
	got := runScores(t, cands, nil, cfg)
	if len(got) != 1 {
		t.Fatalf("unknown-hours candidate must not be hard-filtered, got %d", len(got))
	}
}

// TestHardFilterIgnoresHighStakes: a closed high_stakes_one_time (ignore) is kept.
func TestHardFilterIgnoresHighStakes(t *testing.T) {
	cfg := loadCfg(t)
	cands := []domain.Candidate{
		{ID: id(1), Archetype: domain.ArchetypeHighStakesOneTime, IsOpenNow: ptrB(false), LemonScore: ptrF(9)},
	}
	got := runScores(t, cands, nil, cfg)
	if len(got) != 1 {
		t.Fatalf("closed high_stakes (ignore behavior) must NOT be filtered, got %d", len(got))
	}
}

// TestIgnoreArchetypeExcludesOpenStatus: the open_status weight is forced to 0
// for an "ignore" archetype, so open vs closed yields the same score.
func TestIgnoreArchetypeExcludesOpenStatus(t *testing.T) {
	cfg := loadCfg(t)
	open := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeHighStakesOneTime, IsOpenNow: ptrB(true), LemonScore: ptrF(8)}
	closed := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeHighStakesOneTime, IsOpenNow: ptrB(false), LemonScore: ptrF(8)}
	sOpen := scoreCandidate(&open, cfg)
	sClosed := scoreCandidate(&closed, cfg)
	if !almostEqual(sOpen, sClosed) {
		t.Fatalf("ignore archetype should exclude open_status: open=%v closed=%v", sOpen, sClosed)
	}
}

// TestSoftArchetypeIncludesOpenStatus is the contrast: a soft archetype lets
// open_status move the score.
func TestSoftArchetypeIncludesOpenStatus(t *testing.T) {
	cfg := loadCfg(t)
	open := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeMediumStakesOccasion, IsOpenNow: ptrB(true), LemonScore: ptrF(8)}
	closed := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeMediumStakesOccasion, IsOpenNow: ptrB(false), LemonScore: ptrF(8)}
	if almostEqual(scoreCandidate(&open, cfg), scoreCandidate(&closed, cfg)) {
		t.Fatalf("soft archetype should include open_status in the sum")
	}
}

// TestExactNamePinOverridesHigherScore: the pin beats a higher-scored candidate
// and lands at #1 with +Inf score.
func TestExactNamePinOverridesHigherScore(t *testing.T) {
	cfg := loadCfg(t)
	strong := domain.Candidate{
		ID: id(1), Archetype: domain.ArchetypeLowStakesFastNearby,
		DistanceKM: 0, LemonScore: ptrF(10), GoogleReviewCount: 9000,
		IsClaimed: true, PhotoCount: 10, IsOpenNow: ptrB(true), FriendCount: 9,
	}
	pin := domain.Candidate{
		ID: id(2), Archetype: domain.ArchetypeLowStakesFastNearby,
		DistanceKM: 40, LemonScore: ptrF(1), IsOpenNow: ptrB(true),
	}
	got := runScores(t, []domain.Candidate{strong}, &pin, cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].Candidate.ID != pin.ID {
		t.Fatalf("pin must be at position 0, got %v", got[0].Candidate.ID)
	}
	if !math.IsInf(got[0].Score, 1) {
		t.Fatalf("pin score must be +Inf, got %v", got[0].Score)
	}
}

// TestExactNamePinDedup: a pin that also appears in the candidate list is not
// duplicated.
func TestExactNamePinDedup(t *testing.T) {
	cfg := loadCfg(t)
	c := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeLowStakesFastNearby, LemonScore: ptrF(9), IsOpenNow: ptrB(true)}
	other := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeLowStakesFastNearby, LemonScore: ptrF(8), IsOpenNow: ptrB(true)}
	pin := c
	got := runScores(t, []domain.Candidate{c, other}, &pin, cfg)
	if len(got) != 2 {
		t.Fatalf("pin present in candidates must be deduped; got %d results", len(got))
	}
	if got[0].Candidate.ID != pin.ID || !math.IsInf(got[0].Score, 1) {
		t.Fatalf("deduped pin must hold #1 with +Inf")
	}
	for _, r := range got[1:] {
		if r.Candidate.ID == pin.ID {
			t.Fatalf("duplicate of pin found in tail")
		}
	}
}

// TestPinSurvivesTieBreak: even when other candidates tie, the +Inf pin is #1.
func TestPinSurvivesTieBreak(t *testing.T) {
	cfg := loadCfg(t)
	a := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeLowStakesFastNearby, LemonScore: ptrF(9), IsClaimed: true, IsOpenNow: ptrB(true)}
	b := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeLowStakesFastNearby, LemonScore: ptrF(9), IsClaimed: true, IsOpenNow: ptrB(true)}
	pin := domain.Candidate{ID: id(3), Archetype: domain.ArchetypeLowStakesFastNearby, LemonScore: ptrF(9), IsOpenNow: ptrB(true)}
	got := runScores(t, []domain.Candidate{a, b}, &pin, cfg)
	if got[0].Candidate.ID != pin.ID || !math.IsInf(got[0].Score, 1) {
		t.Fatalf("pin must remain #1 through tie-break, got %v score=%v", got[0].Candidate.ID, got[0].Score)
	}
}

// TestTieBreakOrder exercises each tie-break key in turn. All four candidates
// are constructed to score within tieEpsilon of one another.
func TestTieBreakOrder(t *testing.T) {
	cfg := loadCfg(t)
	// claimed beats unclaimed (all else equal).
	claimed := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, IsOpenNow: ptrB(true)}
	unclaimed := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: false, IsOpenNow: ptrB(true)}
	got := runScores(t, []domain.Candidate{unclaimed, claimed}, nil, cfg)
	if got[0].Candidate.ID != claimed.ID {
		t.Fatalf("tie-break: claimed should win, got %v", got[0].Candidate.ID)
	}

	// closer wins when claimed is equal.
	near := domain.Candidate{ID: id(3), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, DistanceKM: 1, IsOpenNow: ptrB(true)}
	far := domain.Candidate{ID: id(4), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, DistanceKM: 1.001, IsOpenNow: ptrB(true)}
	got = runScores(t, []domain.Candidate{far, near}, nil, cfg)
	if got[0].Candidate.ID != near.ID {
		t.Fatalf("tie-break: closer should win, got %v", got[0].Candidate.ID)
	}

	// more reviews wins when claimed + distance equal (distance kept tiny so
	// the popularity-driven score gap stays within tieEpsilon).
	fewer := domain.Candidate{ID: id(5), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, GoogleReviewCount: 10, IsOpenNow: ptrB(true)}
	more := domain.Candidate{ID: id(6), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, GoogleReviewCount: 12, IsOpenNow: ptrB(true)}
	got = runScores(t, []domain.Candidate{fewer, more}, nil, cfg)
	if got[0].Candidate.ID != more.ID {
		t.Fatalf("tie-break: more reviews should win, got %v", got[0].Candidate.ID)
	}

	// fully-equal candidates fall back to ID ascending.
	lo := domain.Candidate{ID: id(7), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, IsOpenNow: ptrB(true)}
	hi := domain.Candidate{ID: id(8), Archetype: domain.ArchetypeMediumStakesOccasion, IsClaimed: true, IsOpenNow: ptrB(true)}
	got = runScores(t, []domain.Candidate{hi, lo}, nil, cfg)
	if got[0].Candidate.ID != lo.ID {
		t.Fatalf("tie-break: lower ID should win on full tie, got %v", got[0].Candidate.ID)
	}
}

// TestDePinSwapsNewOutOfTop2: a new biz at #1 with a non-new within swap_window
// gets swapped down.
func TestDePinSwapsNewOutOfTop2(t *testing.T) {
	cfg := loadCfg(t)
	// newBiz scores slightly higher than estab (within swap_window) so the
	// swap, not the initial sort, is what moves it down. medium_stakes rating
	// weight 0.20, new demote 0.85: newBiz 0.20·(10/10·0.85)=0.17, estab
	// 0.20·(8/10)=0.16; both share open_now 0.10, gap 0.01 < swap_window 0.05.
	newBiz := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(10), IsNew: true, IsOpenNow: ptrB(true)}
	estab := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(8), IsNew: false, IsOpenNow: ptrB(true)}
	// Confirm the precondition: newBiz outranks estab before de-pin.
	if scoreCandidate(&newBiz, cfg) <= scoreCandidate(&estab, cfg) {
		t.Fatalf("test precondition: newBiz (%v) must outscore estab (%v)", scoreCandidate(&newBiz, cfg), scoreCandidate(&estab, cfg))
	}
	got := runScores(t, []domain.Candidate{newBiz, estab}, nil, cfg)
	if got[0].Candidate.ID != estab.ID {
		t.Fatalf("de-pin should swap the new biz below the close non-new, got #1=%v", got[0].Candidate.ID)
	}
	if !got[1].Candidate.IsNew {
		t.Fatalf("expected new biz at #2 after swap")
	}
}

// TestDePinNoSwapWhenDominant: a new biz that dominates (gap ≥ swap_window) stays.
func TestDePinNoSwapWhenDominant(t *testing.T) {
	cfg := loadCfg(t)
	newBiz := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(10), IsNew: true, IsOpenNow: ptrB(true)}
	weak := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(1), IsNew: false, IsOpenNow: ptrB(true)}
	got := runScores(t, []domain.Candidate{newBiz, weak}, nil, cfg)
	if got[0].Candidate.ID != newBiz.ID {
		t.Fatalf("dominant new biz should stay at #1, got %v", got[0].Candidate.ID)
	}
}

// TestDePinDoesNotMovePin: the +Inf pin at #0 is never swapped down even if new.
func TestDePinDoesNotMovePin(t *testing.T) {
	cfg := loadCfg(t)
	// pin is itself a new business; it must still stay at #0.
	pin := domain.Candidate{ID: id(1), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(9), IsNew: true, IsOpenNow: ptrB(true)}
	estab := domain.Candidate{ID: id(2), Archetype: domain.ArchetypeMediumStakesOccasion, LemonScore: ptrF(8.95), IsNew: false, IsOpenNow: ptrB(true)}
	got := runScores(t, []domain.Candidate{estab}, &pin, cfg)
	if got[0].Candidate.ID != pin.ID || !math.IsInf(got[0].Score, 1) {
		t.Fatalf("pin must remain #0 and not be de-pinned, got %v score=%v", got[0].Candidate.ID, got[0].Score)
	}
}

// TestRunErrorsOnNonLiteralRating: fail loud on an unimplemented mode.
func TestRunErrorsOnNonLiteralRating(t *testing.T) {
	cfg := loadCfg(t)
	cfg.SignalFormulas.Rating = "bayesian"
	_, err := Run(context.Background(), nil, nil, cfg, defaultOpts())
	if err == nil {
		t.Fatalf("expected error for bayesian rating mode")
	}
}

// TestRunErrorsOnNonLiteralDistance: fail loud on an unimplemented mode.
func TestRunErrorsOnNonLiteralDistance(t *testing.T) {
	cfg := loadCfg(t)
	cfg.SignalFormulas.Distance = "decay"
	_, err := Run(context.Background(), nil, nil, cfg, defaultOpts())
	if err == nil {
		t.Fatalf("expected error for decay distance mode")
	}
}

// TestRunRespectsCancelledContext: a cancelled ctx short-circuits Run.
func TestRunRespectsCancelledContext(t *testing.T) {
	cfg := loadCfg(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, nil, nil, cfg, defaultOpts())
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
}

// TestRunTruncatesToLimit caps the returned slice at opts.Limit.
func TestRunTruncatesToLimit(t *testing.T) {
	cfg := loadCfg(t)
	cands := make([]domain.Candidate, 5)
	for i := range cands {
		cands[i] = domain.Candidate{
			ID: id(i + 1), Archetype: domain.ArchetypeMediumStakesOccasion,
			LemonScore: ptrF(float64(i + 1)), IsOpenNow: ptrB(true),
		}
	}
	got, err := Run(context.Background(), cands, nil, cfg, domain.SearchOpts{Limit: 2})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected truncation to 2, got %d", len(got))
	}
	// highest lemon_score should sort first.
	if got[0].Candidate.ID != id(5) {
		t.Fatalf("expected highest score first, got %v", got[0].Candidate.ID)
	}
}

// TestRunEmptyCandidates returns an empty, non-nil slice.
func TestRunEmptyCandidates(t *testing.T) {
	cfg := loadCfg(t)
	got := runScores(t, []domain.Candidate{}, nil, cfg)
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

// TestLessWithinTie exercises each tie-break key directly in both directions so
// the comparator's branches are all covered (sort.SliceStable only calls it one
// way per pair).
func TestLessWithinTie(t *testing.T) {
	claimed := &domain.Candidate{ID: id(1), IsClaimed: true}
	unclaimed := &domain.Candidate{ID: id(2), IsClaimed: false}
	near := &domain.Candidate{ID: id(3), IsClaimed: true, DistanceKM: 1}
	far := &domain.Candidate{ID: id(4), IsClaimed: true, DistanceKM: 2}
	fewer := &domain.Candidate{ID: id(5), IsClaimed: true, GoogleReviewCount: 1}
	more := &domain.Candidate{ID: id(6), IsClaimed: true, GoogleReviewCount: 2}
	loID := &domain.Candidate{ID: id(7), IsClaimed: true}
	hiID := &domain.Candidate{ID: id(8), IsClaimed: true}

	tests := []struct {
		name string
		a, b *domain.Candidate
		want bool
	}{
		{"claimed before unclaimed", claimed, unclaimed, true},
		{"unclaimed after claimed", unclaimed, claimed, false},
		{"closer before farther", near, far, true},
		{"farther after closer", far, near, false},
		{"more reviews before fewer", more, fewer, true},
		{"fewer reviews after more", fewer, more, false},
		{"lower id before higher", loID, hiID, true},
		{"higher id after lower", hiID, loID, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lessWithinTie(tc.a, tc.b); got != tc.want {
				t.Fatalf("lessWithinTie = %v, want %v", got, tc.want)
			}
		})
	}
}
