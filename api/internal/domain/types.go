package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Archetype is the demand-shape bucket a business belongs to. It selects which
// signal weights the ranker applies. Values match the businesses.archetype
// CHECK constraint in 0001_initial_schema.sql exactly.
type Archetype string

// The six valid archetypes.
const (
	ArchetypeLowStakesFastNearby     Archetype = "low_stakes_fast_nearby"
	ArchetypeMediumStakesOccasion    Archetype = "medium_stakes_occasion"
	ArchetypeHighStakesOneTime       Archetype = "high_stakes_one_time"
	ArchetypeExperiential            Archetype = "experiential"
	ArchetypeRecurringService        Archetype = "recurring_service"
	ArchetypeUtilityDistanceDominant Archetype = "utility_distance_dominant"
)

// Candidate is one retrieval result carrying the rich raw signals the ranker
// composes into a score. Retrieval fills every field; the ranker reads them
// and writes a separate Score. Pointer fields are nil when the source column
// is null. See contract C2 (docs/roadmap/05-architectural-contracts.md).
type Candidate struct {
	ID                uuid.UUID
	Name              string
	Category          string
	Subcategory       *string
	Archetype         Archetype
	Neighborhood      *string
	DistanceKM        float64  // from user location; capped at 48.28
	LemonScore        *float64 // 0..10
	GoogleRating      *float64 // 0..5
	GoogleReviewCount int
	PriceRange        *string // '$' | '$$' | '$$$' | '$$$$'
	PhotoCount        int
	PhotoURL          *string // first photo (photos[1]); FE thumbnail; nil if none
	IsClaimed         bool
	FriendCount       int
	IsNew             bool
	IsOpenNow         *bool           // nil if hours unknown; false = closed at opts.Now
	OpensLater        bool            // closed now but reopens before midnight → 0.3 open-status
	Hours             json.RawMessage // passthrough for FE display
}

// SearchOpts carries the per-request retrieval parameters. Now is injected so
// is_open_now and bench runs are reproducible. Overlay is the intent-derived
// narrowing the adapter ANDs into the retrieval WHERE clause; the zero value is
// a no-op (broad search) — see contract C5.
type SearchOpts struct {
	Lat, Lng float64
	Limit    int
	Now      time.Time
	Overlay  Overlay
}
