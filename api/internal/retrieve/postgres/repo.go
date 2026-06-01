package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// queryTO bounds a single retrieval query. The API pool also sets a 1s
// statement_timeout, but binding it on the context here keeps the adapter
// correct regardless of how the pool was configured.
const queryTO = time.Second

// nameMatchCoverage is the minimum name-token coverage (from lemon_name_match)
// at/above which a query counts as an exact-name hit. Coverage decouples typos
// (per-word levenshtein) from how much of the name the query spans. See
// docs/ranking/semantics.md §"Exact-name pin".
const nameMatchCoverage = 0.8

// prefixMatchCoverage is the minimum prefix coverage (from lemon_prefix_match)
// at/above which a partial-name query (a typo-tolerant, in-order PREFIX of a
// name, >= 2 tokens) counts as a pin. Text relevance is not a ranking signal,
// so without this a prefix that uniquely names a business loses the top-3 to a
// more popular/closer unrelated token-sharer. 0.5 demands the prefix span at
// least half the name; with the >= 2-token floor and the cardinality +
// categorical back-offs, bare category words never reach it. See
// docs/ranking/semantics.md §"Exact-name pin".
const prefixMatchCoverage = 0.5

// maxNameMatches caps how many businesses may share a name before the
// exact-name pin backs off. A name matched by more than this many rows (e.g.
// "7-Eleven", shared by 25+ locations) is not a unique business name, so pinning
// an arbitrary one of them over-fires; ExactName returns found=false instead.
// The count comes from the same query via a window over the match predicate.
const maxNameMatches = 5

// candidateColumns is the projection shared by the search and exact-name
// queries: it matches the search_candidates() RETURNS TABLE order 1:1 so a
// single scanCandidate handles both paths.
const candidateColumns = `
	id, name, category, subcategory, archetype, neighborhood,
	distance_km, lemon_score, google_rating, google_review_count,
	price_range, photo_count, photo_url, is_claimed, friend_count,
	is_new, is_open_now, opens_later, hours`

// searchSQL invokes the retrieval function, threading the intent overlay
// (contract C5) as bound params $6–$11 and the optional query embedding as $12.
// A zero overlay yields no-op params (nil category, empty arrays, false
// require-open); a nil $12 (NULL::vector) disables the semantic channel — both
// make retrieval identical to passing nothing.
const searchSQL = `
	select ` + candidateColumns + `
	from search_candidates($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::vector)`

// exactNameSQL is the separate exact-name path: 0–1 rows. A trigram GIN
// pre-filter (name % $1, plus an ilike prefix arm) narrows candidates cheaply,
// then a row pins if EITHER it is a full-name match (lemon_name_match spans the
// name — typo'd full names pin, category prefixes don't) OR a confident
// in-order PREFIX (lemon_prefix_match: a multi-token name fragment like
// "best florida pest" -> "Best Florida Pest Control"). The pin ranks on the
// better of the two coverage scores. It carries no user location (the pin
// ignores distance), so distance_km is the ∞ sentinel and open-status uses
// wall-clock now() for display only. See docs/ranking/semantics.md §"Exact-name
// pin".
//
// match_count is count(*) over () — the number of businesses matching the same
// predicate, computed before LIMIT — so ExactName can back off when a name is
// shared by many locations (see maxNameMatches).
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
		count(*) over () as match_count
	from businesses b
	cross join lateral lemon_open_status(
		b.hours, (now() at time zone 'America/New_York')::timestamp) os
	where (b.name % $1 or b.name ilike $1 || '%')
		and (lemon_name_match($1, b.name) >= $2 or lemon_prefix_match($1, b.name) >= $3)
	order by greatest(lemon_name_match($1, b.name), lemon_prefix_match($1, b.name)) desc, b.id
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
// recall blend (text rank + name similarity). Scoring is the ranker's job. The
// overlay's filter fields are passed as bound params; a zero overlay is a no-op.
func (r *Repo) Search(ctx context.Context, q string, opts domain.SearchOpts) ([]domain.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTO)
	defer cancel()

	ov := opts.Overlay
	rows, err := r.pool.Query(
		ctx, searchSQL,
		q, opts.Lat, opts.Lng, opts.Now, opts.Limit,
		ov.CategoryFilter,
		// nilToEmpty: a nil slice encodes as SQL NULL, where cardinality(NULL)=0
		// is itself NULL (not true) and would wrongly drop every row. An empty
		// (non-nil) slice encodes as '{}', so the no-op clause fires correctly.
		nilToEmpty(ov.SubcategoryFilter),
		nilToEmpty(ov.UniversalTagFilter),
		nilToEmpty(ov.SpecificTagFilter),
		nilToEmpty(ov.PriceFilter),
		ov.RequireOpenNow,
		// vectorParam: nil query vec → NULL::vector → the semantic channel is a
		// no-op (the nearest-neighbour subquery short-circuits to empty).
		vectorParam(opts.QueryVec),
	)
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

// ExactName returns at most one candidate whose name matches q — either a
// full-name match (token coverage >= nameMatchCoverage, per-word typo-tolerant)
// or a confident in-order PREFIX (lemon_prefix_match >= prefixMatchCoverage, for
// partial-name fragments). found=false (with a nil error) means no pin — not an
// error. When the name is shared by more than maxNameMatches businesses it is
// ambiguous (not a unique name), so the pin backs off too.
func (r *Repo) ExactName(ctx context.Context, q string) (c domain.Candidate, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, queryTO)
	defer cancel()

	rows, err := r.pool.Query(ctx, exactNameSQL, q, nameMatchCoverage, prefixMatchCoverage)
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

	var matchCount int64
	cand, archetype, hours := newCandidate()
	if err = rows.Scan(append(candidateScanDests(cand, archetype, hours), &matchCount)...); err != nil {
		return domain.Candidate{}, false, fmt.Errorf("scanning exact-name candidate: %w", err)
	}
	if err = rows.Err(); err != nil {
		return domain.Candidate{}, false, fmt.Errorf("exact-name rows: %w", err)
	}
	if matchCount > maxNameMatches {
		return domain.Candidate{}, false, nil
	}
	return finishCandidate(cand, archetype, hours), true, nil
}

// nilToEmpty returns s, or a non-nil empty slice when s is nil, so pgx encodes
// it as the SQL array literal '{}' rather than NULL. See the call site for why
// the distinction matters to the overlay's cardinality()-based no-op clauses.
func nilToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// vectorParam encodes a query embedding as a pgvector text literal ("[0.1,0.2]")
// for the $12::vector bind, or returns nil (SQL NULL) when there is no vector —
// which makes the semantic recall channel a no-op. pgx has no native pgvector
// codec, so the text literal + cast is the registered path (mirrors the ingest
// writer). 'g'/-1 is the shortest round-trippable form of each float32.
func vectorParam(vec []float32) any {
	if len(vec) == 0 {
		return nil
	}
	buf := make([]byte, 0, len(vec)*12+2)
	buf = append(buf, '[')
	for i, v := range vec {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = strconv.AppendFloat(buf, float64(v), 'g', -1, 32)
	}
	buf = append(buf, ']')
	return string(buf)
}

// scanCandidate maps one row of candidateColumns into a domain.Candidate.
// Nullable source columns scan into pointer fields; archetype/hours are
// converted from their wire types.
func scanCandidate(rows pgx.Rows) (domain.Candidate, error) {
	c, archetype, hours := newCandidate()
	if err := rows.Scan(candidateScanDests(c, archetype, hours)...); err != nil {
		return domain.Candidate{}, fmt.Errorf("scanning candidate: %w", err)
	}
	return finishCandidate(c, archetype, hours), nil
}

// newCandidate allocates a candidate plus the two wire-typed sidecars
// (archetype string, hours bytes) that candidateScanDests / finishCandidate use.
func newCandidate() (c *domain.Candidate, archetype *string, hours *[]byte) {
	return &domain.Candidate{}, new(string), new([]byte)
}

// candidateScanDests returns the Scan destinations for the candidateColumns
// projection, in order. Both the search and exact-name paths use it so the
// 19-column scan lives in one place (the exact-name path appends match_count).
func candidateScanDests(c *domain.Candidate, archetype *string, hours *[]byte) []any {
	return []any{
		&c.ID,
		&c.Name,
		&c.Category,
		&c.Subcategory,
		archetype,
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
		hours,
	}
}

// finishCandidate converts the wire-typed sidecars onto c after a successful
// scan and returns the value.
func finishCandidate(c *domain.Candidate, archetype *string, hours *[]byte) domain.Candidate {
	c.Archetype = domain.Archetype(*archetype)
	if *hours != nil {
		c.Hours = json.RawMessage(*hours)
	}
	return *c
}
