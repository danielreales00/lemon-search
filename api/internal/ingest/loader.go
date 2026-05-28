package ingest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// copyBatchSize is the number of rows buffered before each pgx.CopyFrom into the
// staging table. ~22k total rows means a handful of batches; 500 keeps memory
// bounded without paying per-call overhead on every row.
const copyBatchSize = 500

// CopyChannelBuffer is the recommended buffer for the Business channel feeding
// Load. It lets the producing pipeline run a batch ahead of the COPY without
// growing unbounded, preserving backpressure.
const CopyChannelBuffer = copyBatchSize

// Business is a fully-prepared row ready for the businesses table: sanitized,
// geo-filtered, taxonomy-normalized, archetype-assigned, and synth-seeded. The
// loader maps it onto the COPY column list. loc and search_vector are NOT here;
// they are computed in SQL during the INSERT…SELECT. The generated columns
// (photo_count, is_new) are likewise omitted — Postgres recomputes them.
type Business struct {
	ID                uuid.UUID
	Name              string
	Category          string
	Subcategory       *string
	Specialty         *string
	Archetype         domain.Archetype
	Address           *string
	Neighborhood      *string
	Latitude          *float64
	Longitude         *float64
	LemonScore        *float64
	GoogleRating      *float64
	GoogleReviewCount *int
	PriceRange        *string
	Hours             json.RawMessage
	Photos            []string
	About             *string
	UniversalTags     []string
	SpecificTags      []string
	IsClaimed         bool
	FriendCount       int
}

// copyColumns is the COPY target column list, in the order copyValues emits.
// It deliberately excludes: the generated columns photo_count and is_new
// (Postgres computes them — you cannot COPY into a GENERATED column); loc and
// search_vector (computed by the INSERT…SELECT below); and created_at (left to
// its default on insert and preserved on conflict, which is what makes the
// upsert idempotent).
var copyColumns = []string{
	"id", "name", "category", "subcategory", "specialty", "archetype",
	"address", "neighborhood", "latitude", "longitude",
	"lemon_score", "google_rating", "google_review_count", "price_range",
	"hours", "photos", "about", "universal_tags", "specific_tags",
	"is_claimed", "friend_count",
}

// copyValues flattens a Business into the COPY tuple, matching copyColumns
// order exactly. Nullable scalars stay as pointers so pgx writes SQL NULL; the
// hours raw JSON is passed as bytes for the jsonb column (nil → NULL).
func copyValues(b Business) []any {
	var hours any
	if b.Hours != nil {
		hours = []byte(b.Hours)
	}
	return []any{
		b.ID, b.Name, b.Category, b.Subcategory, b.Specialty, string(b.Archetype),
		b.Address, b.Neighborhood, b.Latitude, b.Longitude,
		b.LemonScore, b.GoogleRating, b.GoogleReviewCount, b.PriceRange,
		hours, b.Photos, b.About, b.UniversalTags, b.SpecificTags,
		b.IsClaimed, b.FriendCount,
	}
}

// Loader upserts prepared rows into the businesses table via a staging temp
// table + pgx.CopyFrom. It holds only the pool; each Load call acquires a
// single connection and runs the whole COPY → INSERT in one transaction so the
// connection-scoped temp table is visible to the final INSERT and the operation
// is atomic (a failed batch rolls everything back — no partial state).
type Loader struct {
	pool *pgxpool.Pool
}

// NewLoader returns a Loader bound to the given pool.
func NewLoader(pool *pgxpool.Pool) *Loader {
	return &Loader{pool: pool}
}

// Load drains rows from the channel and upserts them. It buffers into batches
// of copyBatchSize, COPYs each batch into a staging table, then runs one
// idempotent INSERT…SELECT into businesses. Returns the number of rows loaded.
// The whole operation is a single transaction on one connection: the temp table
// lives for that connection, and any error rolls back without partial state.
func (l *Loader) Load(ctx context.Context, rows <-chan Business) (int, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful Commit

	if _, err = tx.Exec(ctx, createStageSQL); err != nil {
		return 0, fmt.Errorf("creating stage table: %w", err)
	}

	total, err := l.copyAll(ctx, tx, rows)
	if err != nil {
		return 0, err
	}

	if _, err = tx.Exec(ctx, upsertSQL); err != nil {
		return 0, fmt.Errorf("upserting from stage: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing: %w", err)
	}
	return total, nil
}

// copyAll buffers rows into fixed-size batches and COPYs each into the stage
// table, returning the total copied. Honors context cancellation between rows.
func (l *Loader) copyAll(ctx context.Context, tx pgx.Tx, rows <-chan Business) (int, error) {
	batch := make([]Business, 0, copyBatchSize)
	total := 0
	for {
		select {
		case <-ctx.Done():
			return total, fmt.Errorf("loading rows: %w", ctx.Err())
		case b, ok := <-rows:
			if !ok {
				n, err := copyBatch(ctx, tx, batch)
				return total + n, err
			}
			batch = append(batch, b)
			if len(batch) < copyBatchSize {
				continue
			}
			n, err := copyBatch(ctx, tx, batch)
			if err != nil {
				return total, err
			}
			total += n
			batch = batch[:0]
		}
	}
}

// copyBatch COPYs one buffered batch into the stage table. An empty batch is a
// no-op (e.g. when the final flush has nothing left).
func copyBatch(ctx context.Context, tx pgx.Tx, batch []Business) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	n, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"stage_businesses"},
		copyColumns,
		pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
			return copyValues(batch[i]), nil
		}),
	)
	if err != nil {
		return 0, fmt.Errorf("copying batch of %d: %w", len(batch), err)
	}
	if n != int64(len(batch)) {
		return 0, fmt.Errorf("copied %d rows, expected %d", n, len(batch))
	}
	return len(batch), nil
}

const createStageSQL = `create temp table stage_businesses
	(like businesses including all) on commit drop`

// upsertSQL moves the staged rows into businesses, computing loc and the
// weighted search_vector in SQL. ON CONFLICT refreshes every column EXCEPT
// created_at (preserved from the first insert), which is what makes re-ingestion
// idempotent. The search_vector weighting (name=A, subcategory=B,
// category/specialty/specific_tags=C, about=D) is copied verbatim from
// docs/data/ingestion.md and docs/data/schema.md.
const upsertSQL = `
insert into businesses (
	id, name, category, subcategory, specialty, archetype,
	address, neighborhood, latitude, longitude,
	lemon_score, google_rating, google_review_count, price_range,
	hours, photos, about, universal_tags, specific_tags,
	is_claimed, friend_count,
	loc, search_vector
)
select
	id, name, category, subcategory, specialty, archetype,
	address, neighborhood, latitude, longitude,
	lemon_score, google_rating, google_review_count, price_range,
	hours, photos, about, universal_tags, specific_tags,
	is_claimed, friend_count,
	case
		when latitude is not null and longitude is not null
		then ll_to_earth(latitude, longitude)
	end,
	setweight(to_tsvector('english', coalesce(name, '')), 'A')
		|| setweight(to_tsvector('english', coalesce(subcategory, '')), 'B')
		|| setweight(to_tsvector('english', coalesce(category, '')), 'C')
		|| setweight(to_tsvector('english', coalesce(specialty, '')), 'C')
		|| setweight(to_tsvector('english',
			array_to_string(coalesce(specific_tags, '{}'), ' ')), 'C')
		|| setweight(to_tsvector('english', coalesce(about, '')), 'D')
from stage_businesses
on conflict (id) do update set
	name = excluded.name,
	category = excluded.category,
	subcategory = excluded.subcategory,
	specialty = excluded.specialty,
	archetype = excluded.archetype,
	address = excluded.address,
	neighborhood = excluded.neighborhood,
	latitude = excluded.latitude,
	longitude = excluded.longitude,
	lemon_score = excluded.lemon_score,
	google_rating = excluded.google_rating,
	google_review_count = excluded.google_review_count,
	price_range = excluded.price_range,
	hours = excluded.hours,
	photos = excluded.photos,
	about = excluded.about,
	universal_tags = excluded.universal_tags,
	specific_tags = excluded.specific_tags,
	is_claimed = excluded.is_claimed,
	friend_count = excluded.friend_count,
	loc = excluded.loc,
	search_vector = excluded.search_vector`
