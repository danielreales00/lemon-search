// Command bench-runner scores a query cluster against the real retrieval +
// ranking pipeline and prints a pass@3 / latency report. It runs in-process
// (no HTTP server) so the same binary can later drive alternative BusinessRepo
// adapters (Postgres matcher variants, a search engine) for an A/B comparison.
//
//	cd api && LEMON_DATABASE_URL=... go run ./cmd/bench-runner \
//	    -bench ../bench/match-cluster.json -config ../config/ranking.yaml
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
	pgrepo "github.com/danielreales00/lemon-search/api/internal/retrieve/postgres"
)

const (
	defaultDB       = "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"
	candidateLimit  = 150
	resultLimit     = 15
	topK            = 3
	percentile95Num = 95
)

type benchFile struct {
	UserLocation struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	} `json:"user_location"`
	NowOverride string `json:"now_override"`
	Tests       []test `json:"tests"`
}

type test struct {
	Kind     string   `json:"kind"`
	Q        string   `json:"q"`
	Expected []string `json:"expected_top_3"`
	Note     string   `json:"note"`
}

type result struct {
	kind     string
	q        string
	top1     string
	pass     bool
	pinFired bool
	ms       int64
	err      error
}

func main() {
	benchPath := flag.String("bench", "../bench/match-cluster.json", "path to the bench cluster JSON")
	cfgPath := flag.String("config", "../config/ranking.yaml", "path to ranking.yaml")
	label := flag.String("label", "baseline", "label for this run (shown in the report)")
	flag.Parse()

	if err := run(*benchPath, *cfgPath, *label); err != nil {
		fmt.Fprintln(os.Stderr, "bench-runner:", err)
		os.Exit(1)
	}
}

func run(benchPath, cfgPath, label string) error {
	bf, err := loadBench(benchPath)
	if err != nil {
		return err
	}
	cfg, err := config.LoadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	now, err := time.Parse(time.RFC3339, bf.NowOverride)
	if err != nil {
		return fmt.Errorf("parsing now_override %q: %w", bf.NowOverride, err)
	}

	ctx := context.Background()
	url := os.Getenv("LEMON_DATABASE_URL")
	if url == "" {
		url = defaultDB
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", url, err)
	}
	defer pool.Close()
	repo, err := pgrepo.New(pool)
	if err != nil {
		return fmt.Errorf("building repo: %w", err)
	}

	results := make([]result, 0, len(bf.Tests))
	for i := range bf.Tests {
		results = append(results, evalTest(ctx, repo, cfg, bf, now, bf.Tests[i]))
	}
	report(label, results)
	return nil
}

func loadBench(path string) (benchFile, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied bench path, not user input
	if err != nil {
		return benchFile{}, fmt.Errorf("reading bench file %q: %w", path, err)
	}
	var bf benchFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		return benchFile{}, fmt.Errorf("parsing bench file %q: %w", path, err)
	}
	return bf, nil
}

func evalTest(ctx context.Context, repo domain.BusinessRepo, cfg *config.Ranking, bf benchFile, now time.Time, t test) result {
	start := time.Now()
	opts := domain.SearchOpts{Lat: bf.UserLocation.Lat, Lng: bf.UserLocation.Lng, Now: now, Limit: candidateLimit}

	cands, err := repo.Search(ctx, t.Q, opts)
	if err != nil {
		return result{kind: t.Kind, q: t.Q, err: fmt.Errorf("search: %w", err)}
	}
	pin, found, err := repo.ExactName(ctx, t.Q)
	if err != nil {
		return result{kind: t.Kind, q: t.Q, err: fmt.Errorf("exactname: %w", err)}
	}
	var pinPtr *domain.Candidate
	if found {
		pinPtr = &pin
	}
	rankOpts := opts
	rankOpts.Limit = resultLimit
	ranked, err := rank.Run(ctx, cands, pinPtr, cfg, rankOpts)
	if err != nil {
		return result{kind: t.Kind, q: t.Q, err: fmt.Errorf("rank: %w", err)}
	}
	ms := time.Since(start).Milliseconds()

	top1 := ""
	if len(ranked) > 0 {
		top1 = ranked[0].Candidate.Name
	}
	return result{kind: t.Kind, q: t.Q, top1: top1, pass: score(t, ranked, found), pinFired: found, ms: ms}
}

// score applies the per-kind rule: over_fire passes iff the pin did NOT fire on
// the bare category word; everything else is pass@3 on expected names.
func score(t test, ranked []rank.Result, pinFired bool) bool {
	if t.Kind == "over_fire" {
		return !pinFired
	}
	top := lowerTopNames(ranked, topK)
	for _, want := range t.Expected {
		if contains(top, strings.ToLower(strings.TrimSpace(want))) {
			return true
		}
	}
	return false
}

func lowerTopNames(ranked []rank.Result, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(ranked); i++ {
		out = append(out, strings.ToLower(ranked[i].Candidate.Name))
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func report(label string, results []result) {
	fmt.Printf("\n=== bench: match-cluster [%s] ===\n", label)

	byKind := map[string][2]int{} // kind -> {pass, total}
	var passN, total int
	lat := make([]int64, 0, len(results))
	for _, r := range results {
		pt := byKind[r.kind]
		pt[1]++
		if r.pass && r.err == nil {
			pt[0]++
			passN++
		}
		byKind[r.kind] = pt
		total++
		if r.err == nil {
			lat = append(lat, r.ms)
		}
	}

	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Printf("%-12s %s\n", "kind", "pass/total")
	for _, k := range kinds {
		fmt.Printf("%-12s %d/%d\n", k, byKind[k][0], byKind[k][1])
	}
	fmt.Printf("%-12s %d/%d (%.0f%%)\n", "overall", passN, total, pct(passN, total))
	fmt.Printf("latency: p50=%dms p95=%dms (n=%d, local DB, cold-start included)\n", p50(lat), p95(lat), len(lat))

	fmt.Println("--- per-test ---")
	for _, r := range results {
		status := "PASS"
		switch {
		case r.err != nil:
			status = "ERR "
		case !r.pass:
			status = "FAIL"
		}
		detail := fmt.Sprintf("top1=%q", r.top1)
		if r.kind == "over_fire" {
			detail = fmt.Sprintf("pin_fired=%v top1=%q", r.pinFired, r.top1)
		}
		if r.err != nil {
			detail = r.err.Error()
		}
		fmt.Printf("  %s [%-10s] %-22q %s\n", status, r.kind, r.q, detail)
	}
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100.0 / float64(d) //nolint:mnd // percent
}

func p50(xs []int64) int64 { return percentile(xs, 50) }
func p95(xs []int64) int64 { return percentile(xs, percentile95Num) }

func percentile(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (p * (len(sorted) - 1)) / 100
	return sorted[idx]
}
