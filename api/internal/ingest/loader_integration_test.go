//go:build integration

// These tests run against a LIVE local Postgres with migration 0001 applied:
//
//	make db-up && make db-reset
//	cd api && go test -tags integration ./internal/ingest/...
//
// They are gated behind the `integration` build tag so the default `go test
// ./...` (and CI) need no database.
package ingest

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

const defaultTestDB = "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"

// fixtureJSON is three records in the malformed "}\n{" separator format the real
// file uses (objects joined by "}\n{", no comma). Two are in Miami and survive;
// one is in France and is dropped by the geo filter. is_claimed is a real
// passthrough: only the second record sets it true.
const fixtureJSON = `[
{
  "id": "aaaaaaaa-0000-0000-0000-000000000001",
  "name": "Joe's Stone Crab",
  "category": "Food & Drinks",
  "subcategory": "Seafood",
  "address": "11 Washington Ave, Miami Beach, FL 33139",
  "latitude": 25.7689,
  "longitude": -80.1342,
  "lemon_score": 9.5,
  "google_rating": 4.5,
  "google_review_count": 5000,
  "price_range": "$$$",
  "hours": {"monday": {"closed": true}},
  "photos": ["x.jpg", "x.jpg", ""],
  "about": ["Iconic seafood.", "Since 1913."],
  "universal_tags": ["family-friendly"],
  "specific_tags": ["seafood"],
  "is_claimed": false
}
{
  "id": "aaaaaaaa-0000-0000-0000-000000000002",
  "name": "Royal Nails",
  "category": "Beauty",
  "subcategory": "Nails",
  "address": "1874 SW 57 Ave, Miami, FL 33155",
  "latitude": 25.74,
  "longitude": -80.29,
  "lemon_score": 10,
  "google_rating": 5,
  "google_review_count": 14,
  "price_range": null,
  "hours": null,
  "photos": null,
  "about": null,
  "universal_tags": null,
  "specific_tags": null,
  "is_claimed": true
}
{
  "id": "aaaaaaaa-0000-0000-0000-000000000003",
  "name": "Versailles Bike Tour",
  "category": "Activities & Experiences",
  "subcategory": "Other",
  "address": "78000, Versailles, France",
  "latitude": null,
  "longitude": null,
  "is_claimed": false
}
]`

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("LEMON_DATABASE_URL")
	if url == "" {
		url = defaultTestDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("no local Postgres (%s): %v", url, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("local Postgres not reachable (%s): %v", url, err)
	}
	return pool
}

// loadFixture runs the full pipeline (parse → sanitize → geo → taxonomy → synth
// → loader) over fixtureJSON and returns the rows loaded. It mirrors the wiring
// in cmd/ingest so the integration test exercises the same path.
func loadFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	p := New(strings.NewReader(fixtureJSON))
	rows := make(chan Business, CopyChannelBuffer)

	go func() {
		defer close(rows)
		for {
			raw, err := p.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Errorf("parser: %v", err)
				return
			}
			rb, err := Sanitize(raw)
			if err != nil {
				t.Errorf("sanitize: %v", err)
				return
			}
			addr := ""
			if rb.Address != nil {
				addr = *rb.Address
			}
			if GeoFilter(rb.Latitude, rb.Longitude, addr) != Keep {
				continue
			}
			tax := Normalize(rb.Category, derefStr(rb.Subcategory))
			if tax.Decision == TaxonomyDrop {
				continue
			}
			rows <- Business{
				ID: rb.ID, Name: rb.Name, Category: tax.Category,
				Subcategory: ptrOrNil(tax.Subcategory), Specialty: rb.Specialty,
				Archetype: tax.Archetype, Address: rb.Address, Neighborhood: rb.Neighborhood,
				Latitude: rb.Latitude, Longitude: rb.Longitude, LemonScore: rb.LemonScore,
				GoogleRating: rb.GoogleRating, GoogleReviewCount: rb.GoogleReviewCount,
				PriceRange: rb.PriceRange, Hours: rb.Hours, Photos: rb.Photos, About: rb.About,
				UniversalTags: rb.UniversalTags, SpecificTags: rb.SpecificTags,
				IsClaimed: rb.IsClaimed, FriendCount: FriendCount(rb.ID),
			}
		}
	}()

	loaded, err := NewLoader(pool).Load(ctx, rows)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

func TestLoaderEndToEnd(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	ids := []uuid.UUID{
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002"),
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000003"),
	}
	cleanup(ctx, t, pool, ids)
	defer cleanup(ctx, t, pool, ids)

	loaded := loadFixture(ctx, t, pool)
	if loaded != 2 {
		t.Fatalf("loaded = %d, want 2 (France record geo-dropped)", loaded)
	}

	// Row count for our fixture ids.
	var count int
	if err := pool.QueryRow(ctx,
		`select count(*) from businesses where id = any($1)`, ids).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("row count = %d, want 2", count)
	}

	// Archetype + is_claimed reflect real fixture values (not synthesized).
	assertRow(ctx, t, pool, ids[0], "low_stakes_fast_nearby", false)
	assertRow(ctx, t, pool, ids[1], "medium_stakes_occasion", true)

	// loc + search_vector were computed by the INSERT…SELECT.
	var hasLoc, hasVec bool
	if err := pool.QueryRow(ctx,
		`select loc is not null, search_vector is not null from businesses where id = $1`,
		ids[0]).Scan(&hasLoc, &hasVec); err != nil {
		t.Fatalf("loc/vec query: %v", err)
	}
	if !hasLoc || !hasVec {
		t.Errorf("computed columns: loc set=%v, search_vector set=%v, want both true", hasLoc, hasVec)
	}

	// is_claimed across the loaded fixture must be exactly the 1 real true row —
	// proves we did not synthesize a ~35% claimed rate.
	var claimedTrue int
	if err := pool.QueryRow(ctx,
		`select count(*) from businesses where id = any($1) and is_claimed`, ids).Scan(&claimedTrue); err != nil {
		t.Fatalf("claimed count: %v", err)
	}
	if claimedTrue != 1 {
		t.Errorf("is_claimed=true count = %d, want exactly 1 (real passthrough)", claimedTrue)
	}
}

func TestLoaderIdempotent(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	ids := []uuid.UUID{
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002"),
		uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000003"),
	}
	cleanup(ctx, t, pool, ids)
	defer cleanup(ctx, t, pool, ids)

	loadFixture(ctx, t, pool)

	var createdAt time.Time
	var friendsFirst int
	if err := pool.QueryRow(ctx,
		`select created_at, friend_count from businesses where id = $1`, ids[0]).
		Scan(&createdAt, &friendsFirst); err != nil {
		t.Fatalf("first read: %v", err)
	}

	loadFixture(ctx, t, pool) // re-run

	var count int
	var createdAt2 time.Time
	var friendsSecond int
	if err := pool.QueryRow(ctx,
		`select count(*) over (), created_at, friend_count from businesses where id = $1`, ids[0]).
		Scan(&count, &createdAt2, &friendsSecond); err != nil {
		t.Fatalf("second read: %v", err)
	}

	var total int
	if err := pool.QueryRow(ctx,
		`select count(*) from businesses where id = any($1)`, ids).Scan(&total); err != nil {
		t.Fatalf("total count: %v", err)
	}
	if total != 2 {
		t.Errorf("after re-run total = %d, want 2 (no duplicates)", total)
	}
	if !createdAt.Equal(createdAt2) {
		t.Errorf("created_at changed on re-ingest: %v → %v (must be preserved)", createdAt, createdAt2)
	}
	if friendsFirst != friendsSecond {
		t.Errorf("friend_count changed on re-ingest: %d → %d (deterministic seed)", friendsFirst, friendsSecond)
	}
}

func assertRow(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id uuid.UUID, wantArch string, wantClaimed bool) {
	t.Helper()
	var arch string
	var claimed bool
	if err := pool.QueryRow(ctx,
		`select archetype, is_claimed from businesses where id = $1`, id).Scan(&arch, &claimed); err != nil {
		t.Fatalf("row %s: %v", id, err)
	}
	if arch != wantArch {
		t.Errorf("row %s archetype = %q, want %q", id, arch, wantArch)
	}
	if claimed != wantClaimed {
		t.Errorf("row %s is_claimed = %v, want %v", id, claimed, wantClaimed)
	}
}

func cleanup(ctx context.Context, t *testing.T, pool *pgxpool.Pool, ids []uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `delete from businesses where id = any($1)`, ids); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
