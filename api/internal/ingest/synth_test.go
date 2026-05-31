package ingest

import (
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// genID produces a reproducible UUID for sample i, so the statistical
// assertions below are stable across runs and machines.
func genID(i int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(strconv.Itoa(i)))
}

func TestClaimedDeterministicAndDistribution(t *testing.T) {
	t.Parallel()

	const n = 10000
	claimed := 0
	for i := 0; i < n; i++ {
		id := genID(i)
		want := Claimed(id)
		if Claimed(id) != want {
			t.Fatalf("Claimed(%s) not stable", id)
		}
		if want {
			claimed++
		}
	}
	// claimedRate = 0.20; allow a few points of sampling slack.
	rate := float64(claimed) / n
	if rate < 0.17 || rate > 0.23 {
		t.Errorf("claimed rate = %.3f, want ~%.2f", rate, claimedRate)
	}
}

func TestFriendCountDeterministic(t *testing.T) {
	t.Parallel()

	for i := 0; i < 50; i++ {
		id := genID(i)
		want := FriendCount(id)
		for call := 0; call < 100; call++ {
			if got := FriendCount(id); got != want {
				t.Fatalf("FriendCount(%s) call %d = %d, want %d (must be stable)", id, call, got, want)
			}
		}
	}
}

func TestFriendCountDistribution(t *testing.T) {
	t.Parallel()

	const n = 10000

	var (
		nonzero int
		sum     int
	)
	for i := 0; i < n; i++ {
		c := FriendCount(genID(i))
		if c == 0 {
			continue
		}
		if c < 1 || c > friendMax {
			t.Fatalf("FriendCount out of range: got %d, want 1..%d", c, friendMax)
		}
		nonzero++
		sum += c
	}

	rate := float64(nonzero) / float64(n)
	if rate < 0.027 || rate > 0.033 {
		t.Errorf("nonzero rate = %.4f over %d ids, want within [0.027, 0.033]", rate, n)
	}

	if nonzero == 0 {
		t.Fatal("no nonzero friend_count values; cannot check mean")
	}
	mean := float64(sum) / float64(nonzero)
	if mean < 2.7 || mean > 3.3 {
		t.Errorf("mean of nonzero friend_count = %.3f (n=%d), want within [2.7, 3.3]", mean, nonzero)
	}
}

func TestSeed01Range(t *testing.T) {
	t.Parallel()

	for i := 0; i < 1000; i++ {
		v := seed01(genID(i).String() + ":probe")
		if v < 0.0 || v >= 1.0 {
			t.Fatalf("seed01 out of [0,1): got %v", v)
		}
	}
}
