package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// queryTO bounds a single retrieval query. The API pool also sets a 1s
// statement_timeout, but binding it on the context here keeps the adapter
// correct regardless of how the pool was configured.
const queryTO = time.Second

// exactNameThreshold is the fixed trigram similarity at/above which a name
// counts as an exact-name hit (Stage 2). See docs/ranking/semantics.md §"pin".
const exactNameThreshold = 0.85

// candidateColumns is the projection shared by the search and exact-name
// queries: it matches the search_candidates() RETURNS TABLE order 1:1 so a
// single scanCandidate handles both paths.
const candidateColumns = `
	id, name, category, subcategory, archetype, neighborhood,
	distance_km, lemon_score, google_rating, google_review_count,
	price_range, photo_count, photo_url, is_claimed, friend_count,
	is_new, is_open_now, opens_later, hours, text_score, name_trigram`

// searchSQL invokes the retrieval function. Overlay params are passed NULL/false
// at Stage 2; Stage 3 fills them without changing this call's shape.
const searchSQL = `
	select ` + candidateColumns + `
	from search_candidates($1, $2, $3, $4, $5, null, null, null, null, false)`

// exactNameSQL is the separate exact-name path: 0–1 rows, ordered by name
// similarity. It pins ONLY on high trigram similarity (no bare ILIKE prefix),
// so generic category words ("coffee", "sushi") can't hijack the pin —
// prefix/partial recall is search_candidates' job (ranked, not pinned). See
// docs/ranking/semantics.md §"Exact-name pin". It carries no user location
// (the pin ignores distance), so distance_km is the ∞ sentinel and open-status
// uses wall-clock now() for display only.
const exactNameSQL = `
	select
		b.id, b.name, b.category, b.subcategory, b.archetype, b.neighborhood,
		1e9 as distance_km,
		b.lemon_score::float8, b.google_rating::float8,
		coalesce(b.google_review_count, 0) as google_review_count,
		b.price_range, b.photo_count,
		case when b.photos is not null and cardinality(b.photos) >= 1
			then b.photos[1] else null end as photo_url,
		b.is_claimed, b.friend_count, b.is_new,
		os.is_open_now, coalesce(os.opens_later, false) as opens_later,
		b.hours,
		0::float8 as text_score,
		similarity(b.name, $1)::float8 as name_trigram
	from businesses b
	cross join lateral lemon_open_status(
		b.hours, (now() at time zone 'America/New_York')::timestamp) os
	where similarity(b.name, $1) >= $2
	order by similarity(b.name, $1) desc, b.id
	limit 1`

// Repo is the Supabase Postgres adapter implementing domain.BusinessRepo. It
// holds only the pool; pgx caches prepared statements per connection
// (QueryExecModeCacheStatement, the pool default), so the parameterized queries
// above are prepared on first use and reused.
type Repo struct {
	pool *pgxpool.Pool
}

// New returns a Repo bound to pool. The composition root (cmd/api) owns pool
// construction, including the 1s statement_timeout.
func New(pool *pgxpool.Pool) (*Repo, error) {
	if pool == nil {
		return nil, errors.New("postgres.New: nil pool")
	}
	return &Repo{pool: pool}, nil
}

// Search returns up to opts.Limit raw candidates for q, ordered by the SQL
// recall blend (text rank + name similarity). Scoring is the ranker's job.
func (r *Repo) Search(ctx context.Context, q string, opts domain.SearchOpts) ([]domain.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTO)
	defer cancel()

	rows, err := r.pool.Query(ctx, searchSQL, q, opts.Lat, opts.Lng, opts.Now, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Candidate, 0, opts.Limit)
	for rows.Next() {
		c, scanErr := scanCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("search rows: %w", err)
	}
	return out, nil
}

// ExactName returns at most one candidate whose name matches q at or above the
// 0.85 trigram similarity threshold. found=false (with a nil error) means no
// pin — not an error.
func (r *Repo) ExactName(ctx context.Context, q string) (c domain.Candidate, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, queryTO)
	defer cancel()

	rows, err := r.pool.Query(ctx, exactNameSQL, q, exactNameThreshold)
	if err != nil {
		return domain.Candidate{}, false, fmt.Errorf("exact-name query: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return domain.Candidate{}, false, fmt.Errorf("exact-name rows: %w", err)
		}
		return domain.Candidate{}, false, nil
	}

	c, err = scanCandidate(rows)
	if err != nil {
		return domain.Candidate{}, false, err
	}
	if err = rows.Err(); err != nil {
		return domain.Candidate{}, false, fmt.Errorf("exact-name rows: %w", err)
	}
	return c, true, nil
}

// scanCandidate maps one row of candidateColumns into a domain.Candidate.
// Nullable source columns scan into pointer fields; archetype/hours are
// converted from their wire types.
func scanCandidate(rows pgx.Rows) (domain.Candidate, error) {
	var (
		c         domain.Candidate
		archetype string
		hours     []byte
	)
	if err := rows.Scan(
		&c.ID,
		&c.Name,
		&c.Category,
		&c.Subcategory,
		&archetype,
		&c.Neighborhood,
		&c.DistanceKM,
		&c.LemonScore,
		&c.GoogleRating,
		&c.GoogleReviewCount,
		&c.PriceRange,
		&c.PhotoCount,
		&c.PhotoURL,
		&c.IsClaimed,
		&c.FriendCount,
		&c.IsNew,
		&c.IsOpenNow,
		&c.OpensLater,
		&hours,
		&c.TextScore,
		&c.NameTrigram,
	); err != nil {
		return domain.Candidate{}, fmt.Errorf("scanning candidate: %w", err)
	}
	c.Archetype = domain.Archetype(archetype)
	if hours != nil {
		c.Hours = json.RawMessage(hours)
	}
	return c, nil
}
