package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubEmbedder returns a fixed domain.EmbeddingDim-length vector per input text,
// recording the batch sizes it was called with. It needs no Ollama, so the
// orchestration is exercised in the unit tier (CI has no embedder).
type stubEmbedder struct {
	batchSizes []int
}

func (s *stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, domain.EmbeddingDim), nil
}

func (s *stubEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	s.batchSizes = append(s.batchSizes, len(texts))
	out := make([][]float32, len(texts))
	for i := range out {
		vec := make([]float32, domain.EmbeddingDim)
		vec[0] = float32(i) // distinct per index so id↔vector pairing is checkable
		out[i] = vec
	}
	return out, nil
}

// fakeStore is an in-memory embedReader + embedWriter: it pages from a fixed row
// slice and records the (id, vec) pairs written, asserting the orchestration
// without Postgres.
type fakeStore struct {
	rows    []embedRow
	written map[uuid.UUID][]float32
}

func newFakeStore(rows []embedRow) *fakeStore {
	return &fakeStore{rows: rows, written: make(map[uuid.UUID][]float32)}
}

func (f *fakeStore) page(_ context.Context, afterID uuid.UUID, limit int, _ bool) ([]embedRow, error) {
	start := 0
	if afterID != (uuid.UUID{}) {
		for i, r := range f.rows {
			if r.id == afterID {
				start = i + 1
				break
			}
		}
	}
	end := start + limit
	if end > len(f.rows) {
		end = len(f.rows)
	}
	return f.rows[start:end], nil
}

func (f *fakeStore) updateBatch(_ context.Context, ids []uuid.UUID, vecs [][]float32) error {
	for i, id := range ids {
		f.written[id] = vecs[i]
	}
	return nil
}

// orderedID makes an id whose byte order matches i, so the keyset page() in
// fakeStore (which advances past the last returned id) walks rows in order.
func orderedID(i int) uuid.UUID {
	var id uuid.UUID
	id[0] = byte(i >> 8)
	id[1] = byte(i)
	return id
}

func textRow(i int, name string) embedRow {
	return embedRow{id: orderedID(i), name: name, category: "Food & Drinks"}
}

func TestBackfillerEmbedsAllRowsInBatches(t *testing.T) {
	t.Parallel()

	const n = 10
	rows := make([]embedRow, n)
	for i := range rows {
		rows[i] = textRow(i+1, "Biz")
	}
	store := newFakeStore(rows)
	emb := &stubEmbedder{}

	stats, err := NewBackfiller(store, store, emb, discardLogger(), 4).Run(context.Background(), true, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Scanned != n || stats.Embedded != n || stats.Skipped != 0 {
		t.Errorf("stats = %+v, want scanned=%d embedded=%d skipped=0", stats, n, n)
	}
	if len(store.written) != n {
		t.Errorf("wrote %d rows, want %d", len(store.written), n)
	}
	// batchSize 4 over 10 rows → pages of 4,4,2.
	wantBatches := []int{4, 4, 2}
	if len(emb.batchSizes) != len(wantBatches) {
		t.Fatalf("batch calls = %v, want %v", emb.batchSizes, wantBatches)
	}
	for i, want := range wantBatches {
		if emb.batchSizes[i] != want {
			t.Errorf("batch %d size = %d, want %d", i, emb.batchSizes[i], want)
		}
	}
}

func TestBackfillerSkipsEmptyTextRows(t *testing.T) {
	t.Parallel()

	// Rows 2 and 4 have no embeddable text → skipped, never sent to the embedder.
	rows := []embedRow{
		textRow(1, "Has Name"),
		{id: orderedID(2)},
		textRow(3, "Also Named"),
		{id: orderedID(4), name: "   "},
	}
	store := newFakeStore(rows)
	emb := &stubEmbedder{}

	stats, err := NewBackfiller(store, store, emb, discardLogger(), 10).Run(context.Background(), true, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Scanned != 4 || stats.Embedded != 2 || stats.Skipped != 2 {
		t.Errorf("stats = %+v, want scanned=4 embedded=2 skipped=2", stats)
	}
	if len(store.written) != 2 {
		t.Errorf("wrote %d rows, want 2 (empties skipped)", len(store.written))
	}
	if _, ok := store.written[orderedID(2)]; ok {
		t.Errorf("empty-text row 2 must not be written")
	}
	// Only the 2 non-empty texts reach the embedder in the single page.
	if len(emb.batchSizes) != 1 || emb.batchSizes[0] != 2 {
		t.Errorf("batch sizes = %v, want [2]", emb.batchSizes)
	}
}

func TestBackfillerIDVectorPairing(t *testing.T) {
	t.Parallel()

	rows := []embedRow{textRow(1, "First"), textRow(2, "Second"), textRow(3, "Third")}
	store := newFakeStore(rows)

	if _, err := NewBackfiller(store, store, &stubEmbedder{}, discardLogger(), 10).Run(context.Background(), true, 0); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// stubEmbedder sets vec[0]=index within the batch; with one page of 3 the
	// i-th row must get vec[0]==i, proving ids and vectors stay aligned.
	for i, r := range rows {
		vec, ok := store.written[r.id]
		if !ok {
			t.Fatalf("row %d not written", i)
		}
		if vec[0] != float32(i) {
			t.Errorf("row %d got vec[0]=%v, want %v (misaligned id↔vector)", i, vec[0], float32(i))
		}
	}
}

func TestBackfillerRespectsLimit(t *testing.T) {
	t.Parallel()

	rows := make([]embedRow, 20)
	for i := range rows {
		rows[i] = textRow(i+1, "Biz")
	}
	store := newFakeStore(rows)

	stats, err := NewBackfiller(store, store, &stubEmbedder{}, discardLogger(), 6).Run(context.Background(), true, 5)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Scanned != 5 || stats.Embedded != 5 {
		t.Errorf("stats = %+v, want scanned=5 embedded=5 (limit honored)", stats)
	}
}

func TestBackfillerEmbedderErrorPropagates(t *testing.T) {
	t.Parallel()

	store := newFakeStore([]embedRow{textRow(1, "Biz")})
	_, err := NewBackfiller(store, store, errEmbedder{}, discardLogger(), 10).Run(context.Background(), true, 0)
	if err == nil {
		t.Fatal("expected error from embedder, got nil")
	}
}

type errEmbedder struct{}

func (errEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, errBoom }
func (errEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, errBoom
}

var errBoom = errors.New("boom")

func TestVectorLiteral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vec  []float32
		want string
	}{
		{"empty", nil, "[]"},
		{"single", []float32{0.5}, "[0.5]"},
		{"several", []float32{0.1, -0.25, 2}, "[0.1,-0.25,2]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := vectorLiteral(tc.vec); got != tc.want {
				t.Errorf("vectorLiteral(%v) = %q, want %q", tc.vec, got, tc.want)
			}
		})
	}
}
