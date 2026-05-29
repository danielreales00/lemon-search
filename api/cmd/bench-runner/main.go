// Command bench-runner scores a query set against the real retrieval + ranking
// pipeline and prints a pass@3 / latency report. It runs in-process (no HTTP
// server) so the same binary can drive alternative BusinessRepo adapters
// (Postgres matcher variants, a search engine) for an A/B comparison.
//
// Two modes:
//
//   - curated:   -bench bench/match-cluster.json
//
//   - generated: -generate 300 -seed 42   (samples real businesses and derives
//     exact/typo/accent/partial queries with automatic ground truth — a large,
//     unbiased sample. Fixed seed => identical cases across adapters.)
//
//     cd api && LEMON_DATABASE_URL=... go run ./cmd/bench-runner -generate 300
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
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
	defaultDB      = "postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable"
	candidateLimit = 150
	resultLimit    = 15
	topK           = 3
	pctl95         = 95
	maxFailLines   = 30

	genLat = 25.7741728
	genLng = -80.1937
	genNow = "2026-05-27T13:00:00-04:00"
	minRev = 20 // sample businesses with at least this many reviews (findable)
)

// overFireWords are bare category terms that must NOT trigger an exact-name pin.
var overFireWords = []string{
	"coffee", "sushi", "pizza", "taco", "tacos", "barber", "gym", "spa",
	"nails", "yoga", "ramen", "burger", "burgers", "bakery", "tattoo",
	"massage", "steak", "seafood", "pilates", "cafe", "brunch", "donuts",
	"smoothie", "salon", "boba",
}

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

type opts struct {
	benchPath string
	cfgPath   string
	label     string
	generate  int
	seed      int64
	engine    string
	index     bool
	meiliURL  string
	meiliKey  string
}

func main() {
	var o opts
	flag.StringVar(&o.benchPath, "bench", "../bench/match-cluster.json", "curated bench JSON (when -generate=0)")
	flag.StringVar(&o.cfgPath, "config", "../config/ranking.yaml", "path to ranking.yaml")
	flag.StringVar(&o.label, "label", "run", "label shown in the report")
	flag.IntVar(&o.generate, "generate", 0, "if >0, generate this many sampled businesses' query variants")
	flag.Int64Var(&o.seed, "seed", 42, "RNG seed for generated typos (fixed => reproducible)")
	flag.StringVar(&o.engine, "engine", "postgres", "retrieval engine: postgres | meili")
	flag.BoolVar(&o.index, "index", false, "index businesses into Meili, then exit")
	flag.StringVar(&o.meiliURL, "meili-url", "http://localhost:7700", "Meilisearch base URL")
	flag.StringVar(&o.meiliKey, "meili-key", "lemonbenchkey", "Meilisearch master key")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "bench-runner:", err)
		os.Exit(1)
	}
}

func run(o opts) error {
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

	if o.index {
		n, err := indexBusinesses(ctx, pool, newMeiliClient(o.meiliURL, o.meiliKey))
		if err != nil {
			return err
		}
		fmt.Printf("indexed %d businesses into Meili (%s)\n", n, o.meiliURL)
		return nil
	}

	cfg, err := config.LoadFile(o.cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	repo, err := buildRepo(o, pool)
	if err != nil {
		return err
	}
	bf, err := buildBench(ctx, pool, o)
	if err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339, bf.NowOverride)
	if err != nil {
		return fmt.Errorf("parsing now_override %q: %w", bf.NowOverride, err)
	}

	results := make([]result, 0, len(bf.Tests))
	for i := range bf.Tests {
		results = append(results, evalTest(ctx, repo, cfg, bf, now, bf.Tests[i]))
	}
	report(o.label, len(bf.Tests), results)
	return nil
}

func buildRepo(o opts, pool *pgxpool.Pool) (domain.BusinessRepo, error) {
	if o.engine == "meili" {
		return meiliRepo{c: newMeiliClient(o.meiliURL, o.meiliKey)}, nil
	}
	r, err := pgrepo.New(pool)
	if err != nil {
		return nil, fmt.Errorf("building repo: %w", err)
	}
	return r, nil
}

func buildBench(ctx context.Context, pool *pgxpool.Pool, o opts) (benchFile, error) {
	if o.generate > 0 {
		return generateBench(ctx, pool, o.generate, o.seed)
	}
	return loadBench(o.benchPath)
}

func loadBench(path string) (benchFile, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied bench path
	if err != nil {
		return benchFile{}, fmt.Errorf("reading bench file %q: %w", path, err)
	}
	var bf benchFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		return benchFile{}, fmt.Errorf("parsing bench file %q: %w", path, err)
	}
	return bf, nil
}

// generateBench samples real businesses and derives query variants with
// automatic ground truth (the sampled business name). Fixed seed => stable set.
func generateBench(ctx context.Context, pool *pgxpool.Pool, n int, seed int64) (benchFile, error) {
	names, err := sampleNames(ctx, pool, n)
	if err != nil {
		return benchFile{}, err
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // reproducible bench RNG, not security
	tests := make([]test, 0, n*3)
	for _, nm := range names {
		tests = append(tests, test{Kind: "exact_name", Q: nm, Expected: []string{nm}})
		if ty := injectTypos(nm, rng); ty != "" && ty != strings.ToLower(nm) {
			tests = append(tests, test{Kind: "typo", Q: ty, Expected: []string{nm}})
		}
		if s := stripAccents(nm); s != nm {
			tests = append(tests, test{Kind: "accent", Q: strings.ToLower(s), Expected: []string{nm}})
		}
		if toks := strings.Fields(nm); len(toks) >= 4 {
			tests = append(tests, test{Kind: "partial", Q: strings.Join(toks[:3], " "), Expected: []string{nm}})
		}
	}
	for _, w := range overFireWords {
		tests = append(tests, test{Kind: "over_fire", Q: w})
	}
	bf := benchFile{NowOverride: genNow, Tests: tests}
	bf.UserLocation.Lat = genLat
	bf.UserLocation.Lng = genLng
	return bf, nil
}

func sampleNames(ctx context.Context, pool *pgxpool.Pool, n int) ([]string, error) {
	const q = `
		select name from businesses
		where coalesce(google_review_count, 0) >= $1 and name ~ '^[A-Za-z0-9]'
		order by md5(id::text)
		limit $2`
	rows, err := pool.Query(ctx, q, minRev, n)
	if err != nil {
		return nil, fmt.Errorf("sampling businesses: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, n)
	for rows.Next() {
		var nm string
		if err := rows.Scan(&nm); err != nil {
			return nil, fmt.Errorf("scanning sample: %w", err)
		}
		out = append(out, nm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sample rows: %w", err)
	}
	return out, nil
}

// injectTypos applies 1 edit to ~60% of tokens longer than 3 chars (the spec's
// 1-4 char/word model), returning a lowercased typo'd query.
func injectTypos(name string, rng *rand.Rand) string {
	toks := strings.Fields(strings.ToLower(name))
	for i, t := range toks {
		if len([]rune(t)) <= 3 {
			continue
		}
		if rng.Float64() < 0.6 { //nolint:mnd // bench heuristic
			toks[i] = mutateToken(t, rng)
		}
	}
	return strings.Join(toks, " ")
}

func mutateToken(t string, rng *rand.Rand) string {
	r := []rune(t)
	pos := rng.Intn(len(r))
	switch rng.Intn(4) { //nolint:mnd // 4 edit kinds
	case 0: // delete
		return string(r[:pos]) + string(r[pos+1:])
	case 1: // substitute
		r[pos] = rune('a' + rng.Intn(26)) //nolint:mnd // 26 letters
		return string(r)
	case 2: // transpose with next
		if pos < len(r)-1 {
			r[pos], r[pos+1] = r[pos+1], r[pos]
		}
		return string(r)
	default: // insert
		return string(r[:pos]) + string(rune('a'+rng.Intn(26))) + string(r[pos:]) //nolint:mnd // 26 letters
	}
}

var accentRepl = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c",
	"Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ñ", "N", "Ü", "U",
)

func stripAccents(s string) string { return accentRepl.Replace(s) }

func evalTest(ctx context.Context, repo domain.BusinessRepo, cfg *config.Ranking, bf benchFile, now time.Time, t test) result {
	start := time.Now()
	o := domain.SearchOpts{Lat: bf.UserLocation.Lat, Lng: bf.UserLocation.Lng, Now: now, Limit: candidateLimit}

	cands, err := repo.Search(ctx, t.Q, o)
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
	ro := o
	ro.Limit = resultLimit
	ranked, err := rank.Run(ctx, cands, pinPtr, cfg, ro)
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

// score: over_fire passes iff the pin did NOT fire; else pass@3 on expected.
func score(t test, ranked []rank.Result, pinFired bool) bool {
	if t.Kind == "over_fire" {
		return !pinFired
	}
	top := make([]string, 0, topK)
	for i := 0; i < topK && i < len(ranked); i++ {
		top = append(top, strings.ToLower(ranked[i].Candidate.Name))
	}
	for _, want := range t.Expected {
		w := strings.ToLower(strings.TrimSpace(want))
		for _, h := range top {
			if h == w {
				return true
			}
		}
	}
	return false
}

func report(label string, total int, results []result) {
	fmt.Printf("\n=== bench [%s]  n=%d ===\n", label, total)
	byKind, passN, lat, fails := aggregate(results)
	printKindSummary(byKind)
	fmt.Printf("%-12s %d/%d (%.0f%%)\n", "overall", passN, total, pct(passN, total))
	fmt.Printf("latency: p50=%dms p95=%dms (n=%d, local DB)\n", percentile(lat, 50), percentile(lat, pctl95), len(lat))
	printFailures(fails)
}

func aggregate(results []result) (byKind map[string][2]int, passN int, lat []int64, fails []result) {
	byKind = map[string][2]int{}
	lat = make([]int64, 0, len(results))
	fails = make([]result, 0)
	for _, r := range results {
		pt := byKind[r.kind]
		pt[1]++
		if r.pass && r.err == nil {
			pt[0]++
			passN++
		} else {
			fails = append(fails, r)
		}
		byKind[r.kind] = pt
		if r.err == nil {
			lat = append(lat, r.ms)
		}
	}
	return byKind, passN, lat, fails
}

func printKindSummary(byKind map[string][2]int) {
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Printf("%-12s %s\n", "kind", "pass/total")
	for _, k := range kinds {
		pt := byKind[k]
		fmt.Printf("%-12s %d/%d (%.0f%%)\n", k, pt[0], pt[1], pct(pt[0], pt[1]))
	}
}

func printFailures(fails []result) {
	if len(fails) == 0 {
		return
	}
	fmt.Printf("--- failures (%d; showing up to %d) ---\n", len(fails), maxFailLines)
	for i, r := range fails {
		if i >= maxFailLines {
			break
		}
		detail := fmt.Sprintf("top1=%q", r.top1)
		switch {
		case r.err != nil:
			detail = "ERR: " + r.err.Error()
		case r.kind == "over_fire":
			detail = fmt.Sprintf("pin_fired=%v top1=%q", r.pinFired, r.top1)
		}
		fmt.Printf("  [%-10s] %-28q %s\n", r.kind, r.q, detail)
	}
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100.0 / float64(d) //nolint:mnd // percent
}

func percentile(xs []int64, p int) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(p*(len(sorted)-1))/100]
}
