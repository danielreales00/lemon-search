package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// EmbedBatchSize is the number of businesses embedded per Embedder.EmbedBatch
// call and written per UPDATE batch. It bounds both the HTTP payload to the
// embedder and the in-flight memory; ~64 keeps Ollama's batch endpoint busy
// without oversized requests over ~23k rows.
const EmbedBatchSize = 64

// embedRow is the minimal projection the embedding pass reads: the id to update
// plus the text fields EmbedText composes. Nullable columns are pointers so a
// SQL NULL stays empty rather than scanning into a zero value mid-row.
type embedRow struct {
	id            uuid.UUID
	name          string
	category      string
	subcategory   *string
	about         *string
	universalTags []string
	specificTags  []string
}

// embedReader pages businesses for the embedding pass. It is a narrow seam over
// the pool so the orchestration is unit-testable without a database: the real
// adapter (pageReader) keyset-paginates `businesses`; the test injects a fake.
//
// page returns up to limit rows with id strictly greater than afterID, ordered
// by id. onlyMissing restricts to rows whose embedding is still NULL (the
// idempotent default — a re-run skips already-embedded rows). A page shorter
// than limit signals the final page.
type embedReader interface {
	page(ctx context.Context, afterID uuid.UUID, limit int, onlyMissing bool) ([]embedRow, error)
}

// embedWriter persists embeddings for the embedding pass. updateBatch writes one
// vector per row in a single round-trip; ids and vecs are index-aligned. It is a
// seam for the same reason as embedReader — the unit test asserts the batching
// and id↔vector pairing without Postgres.
type embedWriter interface {
	updateBatch(ctx context.Context, ids []uuid.UUID, vecs [][]float32) error
}

// EmbedStats is the end-of-pass tally the Backfiller returns and logs.
type EmbedStats struct {
	Scanned  int // rows read from the DB
	Embedded int // rows that had text and were embedded + written
	Skipped  int // rows with no embeddable text (left NULL)
}

// Backfiller computes and stores per-business embeddings. It pages rows via an
// embedReader, builds each row's embed text (EmbedText), embeds a page in one
// Embedder.EmbedBatch call, and writes the page via an embedWriter — bounded,
// ctx-aware, and idempotent (re-running overwrites in place). It depends on the
// domain.Embedder PORT, never a concrete adapter.
type Backfiller struct {
	reader    embedReader
	writer    embedWriter
	embedder  domain.Embedder
	logger    *slog.Logger
	batchSize int
}

// NewBackfiller wires a Backfiller. embedder is the domain port (Ollama now). A
// non-positive batchSize falls back to EmbedBatchSize.
func NewBackfiller(reader embedReader, writer embedWriter, embedder domain.Embedder, logger *slog.Logger, batchSize int) *Backfiller {
	if batchSize <= 0 {
		batchSize = EmbedBatchSize
	}
	return &Backfiller{reader: reader, writer: writer, embedder: embedder, logger: logger, batchSize: batchSize}
}

// Run pages through businesses and backfills embeddings until exhausted (or
// limit rows have been scanned, when limit > 0 — used to bound a sample run).
// onlyMissing skips rows that already have an embedding, making a full re-run
// cheap and idempotent. It honors ctx cancellation between pages.
func (b *Backfiller) Run(ctx context.Context, onlyMissing bool, limit int) (EmbedStats, error) {
	var stats EmbedStats
	var afterID uuid.UUID // zero UUID sorts first; first page starts after it

	for {
		if err := ctx.Err(); err != nil {
			return stats, fmt.Errorf("embedding pass: %w", err)
		}

		pageSize := b.pageSize(limit, stats.Scanned)
		if pageSize <= 0 {
			break // limit reached
		}

		rows, err := b.reader.page(ctx, afterID, pageSize, onlyMissing)
		if err != nil {
			return stats, fmt.Errorf("reading page after %s: %w", afterID, err)
		}
		if len(rows) == 0 {
			break
		}
		stats.Scanned += len(rows)
		afterID = rows[len(rows)-1].id

		if err := b.embedPage(ctx, rows, &stats); err != nil {
			return stats, err
		}

		if len(rows) < pageSize {
			break // short page → no more rows
		}
	}

	b.logger.Info("embedding pass done",
		slog.Int("scanned", stats.Scanned),
		slog.Int("embedded", stats.Embedded),
		slog.Int("skipped", stats.Skipped))
	return stats, nil
}

// pageSize is the next page's row cap: the batch size, shrunk so a positive
// limit is not overshot. Returns 0 once limit rows have been scanned.
func (b *Backfiller) pageSize(limit, scanned int) int {
	if limit <= 0 {
		return b.batchSize
	}
	remaining := limit - scanned
	if remaining < b.batchSize {
		return remaining
	}
	return b.batchSize
}

// embedPage builds embed text for the page, embeds the non-empty texts in one
// EmbedBatch call, and writes the resulting vectors. Rows whose text is empty
// are skipped (their column stays NULL) and never sent to the embedder, so the
// batch holds only embeddable rows and stays index-aligned with its ids.
func (b *Backfiller) embedPage(ctx context.Context, rows []embedRow, stats *EmbedStats) error {
	ids := make([]uuid.UUID, 0, len(rows))
	texts := make([]string, 0, len(rows))
	for _, r := range rows {
		text := EmbedText(r.name, r.category, derefStr(r.subcategory), r.universalTags, r.specificTags, derefStr(r.about))
		if text == "" {
			stats.Skipped++
			continue
		}
		ids = append(ids, r.id)
		texts = append(texts, text)
	}
	if len(texts) == 0 {
		return nil
	}

	vecs, err := b.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return fmt.Errorf("embedding %d texts: %w", len(texts), err)
	}
	if len(vecs) != len(ids) {
		return fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(texts))
	}

	if err := b.writer.updateBatch(ctx, ids, vecs); err != nil {
		return fmt.Errorf("writing %d embeddings: %w", len(ids), err)
	}
	stats.Embedded += len(ids)
	return nil
}

// vectorLiteral encodes a []float32 as pgvector's text input format,
// "[0.1,0.2,...]". Callers bind it with an explicit $n::vector cast: pgx has no
// native pgvector codec, so the registered path is the text literal — this keeps
// the pass dependency-free (no pgvector-go). 'g' format with -1 precision is the
// shortest round-trippable form of each float32.
func vectorLiteral(vec []float32) string {
	if len(vec) == 0 {
		return "[]"
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

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
