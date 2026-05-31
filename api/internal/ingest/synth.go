package ingest

import (
	"crypto/md5" //nolint:gosec // MD5 is a fast non-cryptographic hash here: deterministic seeding, never security.
	"encoding/binary"

	"github.com/google/uuid"
)

// The scraped Lemon data carries no social-graph signal and only ~10 businesses
// with a real is_claimed, so both friend_count and is_claimed are synthesized —
// the spec explicitly asks for this ("synthesize a small friends-reacted
// dataset"; "synthesize a claimed/unclaimed flag"), since neither has real source
// data (Lemon is pre-launch). Both draws are deterministic in the business id, so
// re-ingesting the same input yields identical values (the upsert is idempotent),
// and domain-separated salts keep the signals independent.

const (
	// friendNonzeroRate is the fraction of businesses that get a nonzero
	// friend_count. Kept low so the friend signal stays a sparse, meaningful
	// boost rather than ambient noise.
	friendNonzeroRate = 0.03
	// friendMax is the largest synthesized friend_count; values land in 1..5.
	friendMax = 5
	// claimedRate is the fraction of businesses synthesized as claimed. ~a fifth
	// keeps claimed a meaningful minority signal (the spec's "big boost") without
	// dominating result sets — at a third it crowded the top of every query.
	claimedRate = 0.20
	// uint32Range is 2^32, used to map a 4-byte hash slice into [0, 1).
	uint32Range = 4294967296.0
)

// Claimed returns a deterministic synthesized claimed flag. Roughly claimedRate
// of ids are claimed. Salt-separated from the friend draws so the two synthetic
// signals stay independent.
func Claimed(id uuid.UUID) bool {
	return seed01(id.String()+":claimed") < claimedRate
}

// FriendCount returns a deterministic synthesized friends-reacted count for a
// business. Roughly friendNonzeroRate of ids get a nonzero value; nonzero
// values are uniform in 1..friendMax. Two domain-separated salts keep the
// "does it react" and "how many" draws independent.
func FriendCount(id uuid.UUID) int {
	if seed01(id.String()+":friends") >= friendNonzeroRate {
		return 0
	}
	return 1 + int(seed01(id.String()+":friend_n")*friendMax)
}

// seed01 maps a string to a deterministic uniform value in [0, 1) via the first
// four bytes of its MD5 digest. Mirrors the lemon_seed() SQL function so the Go
// and Postgres sides agree on the synthesized distribution.
func seed01(s string) float64 {
	sum := md5.Sum([]byte(s)) //nolint:gosec // see import: non-cryptographic, deterministic seed.
	return float64(binary.BigEndian.Uint32(sum[:4])) / uint32Range
}
