//go:build integration

// These tests run against a LIVE local Postgres with migrations through 0006
// applied (the embedding vector(384) column). They use a STUB embedder — no
// Ollama — so CI's integration tier (Postgres, no Ollama) can run them:
//
//	make db-up && make db-reset
//	cd api && go test -tags integration ./internal/ingest/...
package ingest

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// fixtureVec builds a recognizable domain-dim vector whose first component is
// first, so the integration test can read it back from pgvector and confirm the
// encoding round-tripped.
func fixtureVec(first float32) []float32 {
	v := make([]float32, domain.EmbeddingDim)
	v[0] = first
	v[domain.EmbeddingDim-1] = 1
	return v
}

// TestEmbedStoreRoundTrip writes embeddings for disposable fixture rows via the
// real EmbedStore.updateBatch (pgvector text-literal + ::vector cast) and reads
// them back, proving the pgx↔pgvector encoding works against the real vector(384)
// column. It targets only its own fixture ids — never the real dataset, and
// never the table-walking Run loop (that path is covered by the unit tests).
func TestEmbedStoreRoundTrip(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	ids := []uuid.UUID{
		uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001"),
		uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002"),
	}
	cleanup(ctx, t, pool, ids)
	defer cleanup(ctx, t, pool, ids)

	for i, id := range ids {
		if _, err := pool.Exec(ctx,
			`insert into businesses (id, name, category, archetype)
			 values ($1, $2, 'Food & Drinks', 'low_stakes_fast_nearby')`,
			id, []string{"Cafe One", "Cafe Two"}[i]); err != nil {
			t.Fatalf("insert fixture %d: %v", i, err)
		}
	}

	// Write distinct vectors for the two fixtures in one batch — exercises the
	// id↔vector pairing and the encoding against real Postgres.
	store := NewEmbedStore(pool)
	vecs := [][]float32{fixtureVec(0.5), fixtureVec(0.25)}
	if err := store.updateBatch(ctx, ids, vecs); err != nil {
		t.Fatalf("updateBatch: %v", err)
	}

	wantFirst := []float32{0.5, 0.25}
	for i, id := range ids {
		var dims int
		var first float32
		if err := pool.QueryRow(ctx,
			`select vector_dims(embedding), (embedding::float4[])[1] from businesses where id = $1`,
			id).Scan(&dims, &first); err != nil {
			t.Fatalf("read back %s: %v", id, err)
		}
		if dims != domain.EmbeddingDim {
			t.Errorf("row %s dims = %d, want %d", id, dims, domain.EmbeddingDim)
		}
		if first != wantFirst[i] {
			t.Errorf("row %s embedding[1] = %v, want %v (encoding round-trip)", id, first, wantFirst[i])
		}
	}
}

// TestEmbedStoreOnlyMissing proves the idempotent re-run path: with onlyMissing,
// an already-embedded fixture row is not re-scanned.
func TestEmbedStoreOnlyMissing(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	// Two fixtures with the smallest-possible ids (real v4 UUIDs never collide
	// with these) so paging from the zero UUID with limit 2 returns exactly them
	// and nothing from the real dataset — keeping the scan bounded. One already
	// has an embedding; one is NULL.
	embedded := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	missing := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	ids := []uuid.UUID{embedded, missing}
	cleanup(ctx, t, pool, ids)
	defer cleanup(ctx, t, pool, ids)

	if _, err := pool.Exec(ctx,
		`insert into businesses (id, name, category, archetype, embedding)
		 values ($1, 'Already Embedded', 'Beauty', 'low_stakes_fast_nearby', $2::vector)`,
		embedded, vectorLiteral(fixtureVec(9))); err != nil {
		t.Fatalf("insert pre-embedded fixture: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`insert into businesses (id, name, category, archetype)
		 values ($1, 'Needs Embedding', 'Beauty', 'low_stakes_fast_nearby')`,
		missing); err != nil {
		t.Fatalf("insert missing fixture: %v", err)
	}

	// page(zero, 2, onlyMissing=true) returns the two smallest-id rows filtered
	// to NULL embeddings: the missing fixture, not the embedded one.
	rows, err := NewEmbedStore(pool).page(ctx, uuid.UUID{}, 2, true)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	var sawMissing, sawEmbedded bool
	for _, r := range rows {
		switch r.id {
		case missing:
			sawMissing = true
		case embedded:
			sawEmbedded = true
		}
	}
	if !sawMissing {
		t.Errorf("onlyMissing page did not return the NULL-embedding fixture")
	}
	if sawEmbedded {
		t.Errorf("onlyMissing page returned the already-embedded fixture")
	}
}
