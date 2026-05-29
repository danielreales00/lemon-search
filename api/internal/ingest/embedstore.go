package ingest

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

// pageSQL keyset-paginates businesses for the embedding pass: rows with id > $1,
// ordered by id, capped at $2. $3 toggles the "only NULL embeddings" filter
// (true → skip already-embedded rows, the idempotent re-run path). Keyset (not
// OFFSET) keeps each page cheap and the scan deterministic.
const pageSQL = `
	select id, name, category, subcategory, about, universal_tags, specific_tags
	from businesses
	where id > $1 and ($3 = false or embedding is null)
	order by id
	limit $2`

// updateSQL writes one embedding. The vector is bound as its text literal and
// cast with ::vector — pgx has no native pgvector codec, so this explicit cast
// is how a []float32 reaches a vector(384) column without a third-party codec.
const updateSQL = `update businesses set embedding = $1::vector where id = $2`

// EmbedStore is the Postgres adapter for the embedding pass: it pages rows to
// embed and writes the resulting vectors. It implements embedReader +
// embedWriter and holds only the pool. The composition root (cmd/ingest) owns
// pool construction.
type EmbedStore struct {
	pool *pgxpool.Pool
}

// NewEmbedStore returns an EmbedStore bound to pool.
func NewEmbedStore(pool *pgxpool.Pool) *EmbedStore {
	return &EmbedStore{pool: pool}
}

// page reads up to limit businesses with id > afterID, ordered by id. When
// onlyMissing is true it returns only rows whose embedding is still NULL.
func (s *EmbedStore) page(ctx context.Context, afterID uuid.UUID, limit int, onlyMissing bool) ([]embedRow, error) {
	rows, err := s.pool.Query(ctx, pageSQL, afterID, limit, onlyMissing)
	if err != nil {
		return nil, fmt.Errorf("query page: %w", err)
	}
	defer rows.Close()

	out := make([]embedRow, 0, limit)
	for rows.Next() {
		var r embedRow
		if err := rows.Scan(&r.id, &r.name, &r.category, &r.subcategory, &r.about, &r.universalTags, &r.specificTags); err != nil {
			return nil, fmt.Errorf("scan page row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("page rows: %w", err)
	}
	return out, nil
}

// updateBatch writes one vector per id in a single round-trip via pgx.Batch,
// all in one transaction so a page commits atomically (a mid-batch failure
// rolls the whole page back, never leaving it half-written). ids and vecs are
// index-aligned; each vector is encoded as a pgvector text literal.
func (s *EmbedStore) updateBatch(ctx context.Context, ids []uuid.UUID, vecs [][]float32) error {
	if len(ids) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for i, id := range ids {
		batch.Queue(updateSQL, vectorLiteral(vecs[i]), id)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful Commit

	results := tx.SendBatch(ctx, batch)
	for i := range ids {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("update embedding %d/%d: %w", i+1, len(ids), err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close update batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit update batch: %w", err)
	}
	return nil
}
