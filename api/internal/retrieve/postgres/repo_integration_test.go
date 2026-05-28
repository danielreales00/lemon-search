//go:build integration

// These tests run against a LIVE Postgres with migrations 0001 + 0002 applied.
// They are hermetic: they seed their own ZZFIXTURE rows, assert, and delete
// them — they do NOT depend on any ingested data. Gated behind the
// `integration` build tag so the default `go test ./...` needs no database.
//
//	make db-up && make db-reset
//	cd api && go test -tags integration ./internal/retrieve/postgres/...
package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

const defaultTestDB = "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"

// fixedNow is Monday 2026-05-25 14:00:00 EDT (Miami 2pm). Open-status fixtures
// set their Monday hours relative to this instant.
var fixedNow = time.Date(2026, 5, 25, 18, 0, 0, 0, time.UTC) // 14:00-04:00

// Brickell-ish anchor the test "user" searches from.
const (
	anchorLat = 25.7617
	anchorLng = -80.1918
)

// fixture is one seeded business. lat/lng/hours drive the geo and open-status
// assertions; name carries the ZZFIXTURE sentinel so assertions stay scoped.
type fixture struct {
	id    uuid.UUID
	name  string
	cat   string
	sub   string
	arch  domain.Archetype
	lat   *float64
	lng   *float64
	hours string // JSON literal, or "" for SQL NULL
	tags  []string
}

func f64(v float64) *float64 { return &v }

// fixtures: a near sushi spot (open Mon), a far sushi spot (~50mi, opens later),
// a closed-all-day spot, and a null-hours spot.
func fixtures() []fixture {
	return []fixture{
		{
			id:   uuid.MustParse("dddddddd-0000-0000-0000-000000000001"),
			name: "ZZFIXTURE Sushi Near",
			cat:  "Food & Drinks",
			sub:  "Sushi",
			arch: domain.ArchetypeLowStakesFastNearby,
			lat:  f64(25.7620), lng: f64(-80.1925), // ~0.1 km from anchor
			hours: `{"monday":{"open":"9:00 AM","close":"9:00 PM"}}`,
			tags:  []string{"zzfixture-tag"},
		},
		{
			id:   uuid.MustParse("dddddddd-0000-0000-0000-000000000002"),
			name: "ZZFIXTURE Sushi Far",
			cat:  "Food & Drinks",
			sub:  "Sushi",
			arch: domain.ArchetypeLowStakesFastNearby,
			lat:  f64(26.4900), lng: f64(-80.1918), // ~50 mi north
			hours: `{"monday":{"open":"5:00 PM","close":"11:00 PM"}}`,
		},
		{
			id:   uuid.MustParse("dddddddd-0000-0000-0000-000000000003"),
			name: "ZZFIXTURE Closed Barber",
			cat:  "Beauty",
			sub:  "Barber",
			arch: domain.ArchetypeRecurringService,
			lat:  f64(25.7700), lng: f64(-80.1900),
			hours: `{"monday":{"closed":true}}`,
		},
		{
			id:   uuid.MustParse("dddddddd-0000-0000-0000-000000000004"),
			name: "ZZFIXTURE Unknown Hours Spa",
			cat:  "Beauty",
			sub:  "Spa",
			arch: domain.ArchetypeRecurringService,
			lat:  f64(25.7650), lng: f64(-80.1950),
			hours: "", // NULL
		},
	}
}

func TestSearchSurfacesSeededSushi(t *testing.T) {
	pool, ctx := setup(t)

	got := mustSearch(ctx, t, pool, "ZZFIXTURE sushi")
	names := candidateNames(got)
	if !names["ZZFIXTURE Sushi Near"] || !names["ZZFIXTURE Sushi Far"] {
		t.Errorf("search %q missing seeded sushi rows; got %v", "ZZFIXTURE sushi", keys(names))
	}
	// Recall is intentionally inclusive (the barber shares the "ZZFIXTURE"
	// trigram), so the barber may appear — but the "sushi" lexeme must give the
	// sushi rows a higher text_score than the non-sushi barber.
	byName := candidateByName(got)
	barber, ok := byName["ZZFIXTURE Closed Barber"]
	if ok {
		for _, n := range []string{"ZZFIXTURE Sushi Near", "ZZFIXTURE Sushi Far"} {
			if sushi := byName[n]; sushi.TextScore <= barber.TextScore {
				t.Errorf("%s text_score %.3f should beat barber %.3f for a sushi query",
					n, sushi.TextScore, barber.TextScore)
			}
		}
	}
}

func TestSearchNonsenseReturnsNoFixtures(t *testing.T) {
	pool, ctx := setup(t)

	got := mustSearch(ctx, t, pool, "zzfixture-qwopzxnonsense")
	for _, c := range got {
		if isFixture(c.Name) {
			t.Errorf("nonsense query surfaced fixture %q (text=%v trgm=%v)",
				c.Name, c.TextScore, c.NameTrigram)
		}
	}
}

func TestExactNameReturnsSeededRow(t *testing.T) {
	pool, ctx := setup(t)

	repo, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c, found, err := repo.ExactName(ctx, "ZZFIXTURE Sushi Near")
	if err != nil {
		t.Fatalf("ExactName: %v", err)
	}
	if !found {
		t.Fatalf("ExactName(%q) found=false, want true", "ZZFIXTURE Sushi Near")
	}
	if c.Name != "ZZFIXTURE Sushi Near" {
		t.Errorf("ExactName name = %q, want %q", c.Name, "ZZFIXTURE Sushi Near")
	}
	if c.Archetype != domain.ArchetypeLowStakesFastNearby {
		t.Errorf("ExactName archetype = %q, want %q", c.Archetype, domain.ArchetypeLowStakesFastNearby)
	}
}

func TestExactNameMissReturnsNotFound(t *testing.T) {
	pool, ctx := setup(t)

	repo, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, found, err := repo.ExactName(ctx, "ZZFIXTURE No Such Business Exists")
	if err != nil {
		t.Fatalf("ExactName miss returned error: %v", err)
	}
	if found {
		t.Errorf("ExactName miss found=true, want false")
	}
}

func TestSearchDistanceNearVsFar(t *testing.T) {
	pool, ctx := setup(t)

	got := mustSearch(ctx, t, pool, "ZZFIXTURE sushi")
	byName := candidateByName(got)
	near, okN := byName["ZZFIXTURE Sushi Near"]
	far, okF := byName["ZZFIXTURE Sushi Far"]
	if !okN || !okF {
		t.Fatalf("missing near/far fixtures: near=%v far=%v", okN, okF)
	}
	if near.DistanceKM > 1.0 {
		t.Errorf("near fixture distance = %.2f km, want < 1", near.DistanceKM)
	}
	if far.DistanceKM < 60.0 || far.DistanceKM > 100.0 {
		t.Errorf("far fixture distance = %.2f km, want ~80 (50 mi)", far.DistanceKM)
	}
}

func TestSearchOpenStatus(t *testing.T) {
	pool, ctx := setup(t)

	got := mustSearch(ctx, t, pool, "ZZFIXTURE")
	byName := candidateByName(got)

	// Open now: Monday 2pm within 9am–9pm.
	assertOpen(t, byName, "ZZFIXTURE Sushi Near", boolPtr(true), false)
	// Opens later: closed at 2pm, opens 5pm Monday.
	assertOpen(t, byName, "ZZFIXTURE Sushi Far", boolPtr(false), true)
	// Closed all day Monday.
	assertOpen(t, byName, "ZZFIXTURE Closed Barber", boolPtr(false), false)
	// Unknown hours → is_open_now NULL.
	assertOpen(t, byName, "ZZFIXTURE Unknown Hours Spa", nil, false)
}

func TestSearchHoursPassthrough(t *testing.T) {
	pool, ctx := setup(t)

	got := mustSearch(ctx, t, pool, "ZZFIXTURE")
	byName := candidateByName(got)

	near, ok := byName["ZZFIXTURE Sushi Near"]
	if !ok {
		t.Fatalf("near fixture missing")
	}
	var parsed map[string]any
	if err := json.Unmarshal(near.Hours, &parsed); err != nil {
		t.Fatalf("hours passthrough not valid JSON: %v (%s)", err, near.Hours)
	}
	if _, ok := parsed["monday"]; !ok {
		t.Errorf("hours passthrough missing monday key: %s", near.Hours)
	}

	spa, ok := byName["ZZFIXTURE Unknown Hours Spa"]
	if !ok {
		t.Fatalf("spa fixture missing")
	}
	if spa.Hours != nil {
		t.Errorf("null-hours fixture Hours = %s, want nil", spa.Hours)
	}
}

// --- helpers ---

func assertOpen(t *testing.T, byName map[string]domain.Candidate, name string, wantOpen *bool, wantLater bool) {
	t.Helper()
	c, ok := byName[name]
	if !ok {
		t.Fatalf("fixture %q missing from results", name)
	}
	switch {
	case wantOpen == nil && c.IsOpenNow != nil:
		t.Errorf("%s is_open_now = %v, want nil", name, *c.IsOpenNow)
	case wantOpen != nil && c.IsOpenNow == nil:
		t.Errorf("%s is_open_now = nil, want %v", name, *wantOpen)
	case wantOpen != nil && c.IsOpenNow != nil && *c.IsOpenNow != *wantOpen:
		t.Errorf("%s is_open_now = %v, want %v", name, *c.IsOpenNow, *wantOpen)
	}
	if c.OpensLater != wantLater {
		t.Errorf("%s opens_later = %v, want %v", name, c.OpensLater, wantLater)
	}
}

func setup(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	seed(ctx, t, pool)
	// Cleanups run LIFO: delete fixtures first, then close the pool last.
	t.Cleanup(pool.Close)
	t.Cleanup(func() { cleanup(context.Background(), t, pool) })
	return pool, ctx
}

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

func mustSearch(ctx context.Context, t *testing.T, pool *pgxpool.Pool, q string) []domain.Candidate {
	t.Helper()
	repo, err := New(pool)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := repo.Search(ctx, q, domain.SearchOpts{Lat: anchorLat, Lng: anchorLng, Limit: 150, Now: fixedNow})
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return got
}

// seed inserts fixtures the same way ingest does: computing loc via ll_to_earth
// and search_vector via the weighted to_tsvector, so they behave like real rows.
func seed(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	cleanup(ctx, t, pool) // start clean even if a prior run left rows
	const ins = `
		insert into businesses (
			id, name, category, subcategory, archetype, latitude, longitude,
			lemon_score, google_rating, google_review_count, price_range,
			photos, hours, universal_tags, is_claimed, friend_count, loc, search_vector
		) values (
			$1, $2, $3, $4, $5, $6, $7,
			9.0, 4.5, 100, '$$',
			array['https://example.com/a.jpg','https://example.com/b.jpg','https://example.com/c.jpg'],
			$8::jsonb, $9, false, 0,
			ll_to_earth($6, $7),
			setweight(to_tsvector('english', coalesce($2,'')), 'A')
			  || setweight(to_tsvector('english', coalesce($4,'')), 'B')
			  || setweight(to_tsvector('english', coalesce($3,'')), 'C')
		)`
	for _, fx := range fixtures() {
		var hours any
		if fx.hours != "" {
			hours = fx.hours
		}
		var tags any
		if fx.tags != nil {
			tags = fx.tags
		}
		if _, err := pool.Exec(
			ctx, ins,
			fx.id, fx.name, fx.cat, fx.sub, string(fx.arch), fx.lat, fx.lng, hours, tags,
		); err != nil {
			t.Fatalf("seed %q: %v", fx.name, err)
		}
	}
}

func cleanup(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(fixtures()))
	for _, fx := range fixtures() {
		ids = append(ids, fx.id)
	}
	if _, err := pool.Exec(ctx, `delete from businesses where id = any($1)`, ids); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }

func isFixture(name string) bool {
	return len(name) >= 9 && name[:9] == "ZZFIXTURE"
}

func candidateNames(cs []domain.Candidate) map[string]bool {
	m := make(map[string]bool, len(cs))
	for _, c := range cs {
		m[c.Name] = true
	}
	return m
}

func candidateByName(cs []domain.Candidate) map[string]domain.Candidate {
	m := make(map[string]domain.Candidate, len(cs))
	for _, c := range cs {
		m[c.Name] = c
	}
	return m
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
