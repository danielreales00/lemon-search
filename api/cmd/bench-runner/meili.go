package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// This is a BENCH-ONLY Meilisearch adapter: it implements domain.BusinessRepo
// over Meili's REST API (net/http, no SDK dep) so the runner can A/B Meili
// recall against Postgres through the SAME ranker + SAME pin logic
// (coverageMatch mirrors the SQL lemon_name_match). Two deliberate
// simplifications vs the Postgres adapter, neither material to name-match
// pass@3: distance is haversine in Go, and open-status is left unknown (0.7
// soft) rather than evaluated from hours.

const (
	meiliIndex        = "businesses"
	meiliCoverage     = 0.8
	coverageTokenFrac = 0.8
	exactNameProbe    = 50
	meiliOneTypo      = 3
	meiliTwoTypos     = 7
	distSentinel      = 1e9
	earthRadiusKM     = 6371.0
	degToRad          = math.Pi / 180
	editDivisor       = 4
	maxEditTol        = 4
	indexBatch        = 5000
	taskPoll          = 500 * time.Millisecond
	taskTimeout       = 90 * time.Second
	httpTimeout       = 30 * time.Second
)

// ---- REST client ----

type meiliClient struct {
	base string
	key  string
	hc   *http.Client
}

func newMeiliClient(base, key string) *meiliClient {
	return &meiliClient{base: base, key: key, hc: &http.Client{Timeout: httpTimeout}}
}

type meiliTaskRef struct {
	TaskUID int `json:"taskUid"` //nolint:tagliatelle // Meili API wire field
}

func (c *meiliClient) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		payload = b
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on read path
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

func (c *meiliClient) waitTask(ctx context.Context, uid int) error {
	deadline := time.Now().Add(taskTimeout)
	for {
		var st struct {
			Status string `json:"status"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/tasks/%d", uid), nil, &st); err != nil {
			return err
		}
		switch st.Status {
		case "succeeded":
			return nil
		case "failed", "canceled":
			return fmt.Errorf("meili task %d %s", uid, st.Status)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("meili task %d timed out", uid)
		}
		time.Sleep(taskPoll)
	}
}

// ---- index document ----

type meiliDoc struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Category          string   `json:"category"`
	Subcategory       string   `json:"subcategory"`
	Archetype         string   `json:"archetype"`
	Neighborhood      string   `json:"neighborhood"`
	Lat               float64  `json:"lat"`
	Lng               float64  `json:"lng"`
	LemonScore        *float64 `json:"lemon_score"`
	GoogleRating      *float64 `json:"google_rating"`
	GoogleReviewCount int      `json:"google_review_count"`
	PriceRange        string   `json:"price_range"`
	PhotoCount        int      `json:"photo_count"`
	PhotoURL          string   `json:"photo_url"`
	IsClaimed         bool     `json:"is_claimed"`
	FriendCount       int      `json:"friend_count"`
	IsNew             bool     `json:"is_new"`
}

func ptrStr(s string) *string { return &s }

func (d meiliDoc) toCandidate(userLat, userLng float64, located bool) domain.Candidate {
	c := domain.Candidate{
		Name:              d.Name,
		Category:          d.Category,
		Archetype:         domain.Archetype(d.Archetype),
		DistanceKM:        distSentinel,
		LemonScore:        d.LemonScore,
		GoogleRating:      d.GoogleRating,
		GoogleReviewCount: d.GoogleReviewCount,
		PhotoCount:        d.PhotoCount,
		IsClaimed:         d.IsClaimed,
		FriendCount:       d.FriendCount,
		IsNew:             d.IsNew,
	}
	if id, err := uuid.Parse(d.ID); err == nil {
		c.ID = id
	}
	if d.Subcategory != "" {
		c.Subcategory = ptrStr(d.Subcategory)
	}
	if d.Neighborhood != "" {
		c.Neighborhood = ptrStr(d.Neighborhood)
	}
	if d.PriceRange != "" {
		c.PriceRange = ptrStr(d.PriceRange)
	}
	if d.PhotoURL != "" {
		c.PhotoURL = ptrStr(d.PhotoURL)
	}
	if located {
		c.DistanceKM = haversine(userLat, userLng, d.Lat, d.Lng)
	}
	return c
}

// ---- adapter (domain.BusinessRepo) ----

type meiliRepo struct {
	c *meiliClient
}

type meiliSearchResp struct {
	Hits []meiliDoc `json:"hits"`
}

func (c *meiliClient) search(ctx context.Context, q string, limit int) ([]meiliDoc, error) {
	var r meiliSearchResp
	body := map[string]any{"q": q, "limit": limit, "matchingStrategy": "frequency"}
	if err := c.do(ctx, http.MethodPost, "/indexes/"+meiliIndex+"/search", body, &r); err != nil {
		return nil, err
	}
	return r.Hits, nil
}

func (m meiliRepo) Search(ctx context.Context, q string, opts domain.SearchOpts) ([]domain.Candidate, error) {
	hits, err := m.c.search(ctx, q, opts.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Candidate, 0, len(hits))
	for i := range hits {
		out = append(out, hits[i].toCandidate(opts.Lat, opts.Lng, true))
	}
	return out, nil
}

func (m meiliRepo) ExactName(ctx context.Context, q string) (domain.Candidate, bool, error) {
	hits, err := m.c.search(ctx, q, exactNameProbe)
	if err != nil {
		return domain.Candidate{}, false, err
	}
	bestI, best := -1, 0.0
	for i := range hits {
		if s := coverageMatch(q, hits[i].Name); s >= meiliCoverage && s > best {
			best, bestI = s, i
		}
	}
	if bestI < 0 {
		return domain.Candidate{}, false, nil
	}
	return hits[bestI].toCandidate(0, 0, false), true, nil
}

// ---- indexing ----

func indexBusinesses(ctx context.Context, pool *pgxpool.Pool, c *meiliClient) (int, error) {
	docs, err := readDocs(ctx, pool)
	if err != nil {
		return 0, err
	}
	var t meiliTaskRef
	// Posting documents auto-creates the index (primary key inferred as "id").
	for start := 0; start < len(docs); start += indexBatch {
		end := start + indexBatch
		if end > len(docs) {
			end = len(docs)
		}
		if err := c.do(ctx, http.MethodPost, "/indexes/"+meiliIndex+"/documents", docs[start:end], &t); err != nil {
			return 0, fmt.Errorf("add documents: %w", err)
		}
		if err := c.waitTask(ctx, t.TaskUID); err != nil {
			return 0, err
		}
	}
	settings := map[string]any{
		"searchableAttributes": []string{"name", "subcategory", "category"},
		"typoTolerance": map[string]any{
			"minWordSizeForTypos": map[string]any{"oneTypo": meiliOneTypo, "twoTypos": meiliTwoTypos},
		},
	}
	if err := c.do(ctx, http.MethodPatch, "/indexes/"+meiliIndex+"/settings", settings, &t); err != nil {
		return 0, fmt.Errorf("configure settings: %w", err)
	}
	if err := c.waitTask(ctx, t.TaskUID); err != nil {
		return 0, err
	}
	return len(docs), nil
}

func readDocs(ctx context.Context, pool *pgxpool.Pool) ([]meiliDoc, error) {
	const q = `
		select id::text, name, coalesce(category,''), coalesce(subcategory,''), archetype,
		       coalesce(neighborhood,''), coalesce(latitude,0), coalesce(longitude,0),
		       lemon_score, google_rating, coalesce(google_review_count,0), coalesce(price_range,''),
		       photo_count,
		       case when photos is not null and cardinality(photos) >= 1 then photos[1] else '' end,
		       is_claimed, friend_count, is_new
		from businesses
		where latitude is not null and longitude is not null`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read businesses: %w", err)
	}
	defer rows.Close()
	docs := make([]meiliDoc, 0, 23000) //nolint:mnd // capacity hint
	for rows.Next() {
		var d meiliDoc
		if err := rows.Scan(&d.ID, &d.Name, &d.Category, &d.Subcategory, &d.Archetype,
			&d.Neighborhood, &d.Lat, &d.Lng, &d.LemonScore, &d.GoogleRating, &d.GoogleReviewCount,
			&d.PriceRange, &d.PhotoCount, &d.PhotoURL, &d.IsClaimed, &d.FriendCount, &d.IsNew); err != nil {
			return nil, fmt.Errorf("scan business: %w", err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("business rows: %w", err)
	}
	return docs, nil
}

// ---- helpers: coverage match (mirror of SQL lemon_name_match), levenshtein, geo ----

func coverageMatch(q, name string) float64 {
	qt := strings.Fields(strings.ToLower(q))
	nt := strings.Fields(strings.ToLower(name))
	if len(qt) == 0 || len(nt) == 0 {
		return 0
	}
	if len(qt) < int(math.Ceil(coverageTokenFrac*float64(len(nt)))) {
		return 0
	}
	for _, qq := range qt {
		if minLev(qq, nt) > tol(qq) {
			return 0
		}
	}
	matched := 0
	for _, n := range nt {
		if minLev(n, qt) <= tol(n) {
			matched++
		}
	}
	return float64(matched) / float64(len(nt))
}

func tol(w string) int {
	t := (len([]rune(w)) + editDivisor - 1) / editDivisor // ceil(len/4)
	if t < 1 {
		return 1
	}
	if t > maxEditTol {
		return maxEditTol
	}
	return t
}

func minLev(w string, against []string) int {
	best := len(w) + 64 //nolint:mnd // larger than any plausible distance
	for _, a := range against {
		if d := levenshtein(w, a); d < best {
			best = d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	p1, p2 := lat1*degToRad, lat2*degToRad
	dLat := (lat2 - lat1) * degToRad
	dLng := (lng2 - lng1) * degToRad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return earthRadiusKM * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}
