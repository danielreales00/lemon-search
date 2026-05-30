// Command loadbench is an open-loop load generator for the Lemon Search
// /search endpoint. It sweeps a ramp of request rates, holds each for a fixed
// window, and reports end-to-end latency percentiles alongside the server's own
// stage timings (embed/sql/rerank). The point is attribution: when p95 crosses
// 100ms you can see whether the API box (embed) or the database (sql) is the
// wall — see docs/bench/plan.md.
//
// Open-loop: requests are scheduled at fixed wall-clock instants (start + i/rate)
// regardless of how many are already in flight, and each request's latency is
// measured from its *intended* send time. That makes coordinated omission
// visible — a server that stalls shows blown-up tail latency instead of silently
// throttling the offered load. A bounded in-flight cap prevents OOM; requests
// that can't be admitted are counted as `dropped` (a saturation signal).
//
//	cd api && go run ./cmd/loadbench \
//	  -base-url http://<api-host>:8080 \
//	  -rates 25,50,100,200,400,800 -duration 30s -warmup 5s \
//	  -out ../bench/load-results-YYYY-MM-DD.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxInflight caps concurrent in-flight requests so a stalled server can't make
// the generator spawn unbounded goroutines. Hitting it is itself a result: the
// offered rate exceeds what the system drains, recorded as `dropped`.
const maxInflight = 8000

type point struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label"`
}

type weightedQuery struct {
	Q      string `json:"q"`
	Weight int    `json:"weight"`
	Kind   string `json:"kind"`
}

type corpus struct {
	Points  []point         `json:"points"`
	Nows    []string        `json:"nows"`
	Queries []weightedQuery `json:"queries"`
}

type request struct {
	q   string
	lat float64
	lng float64
	now string
}

type sample struct {
	wallMs   float64
	serverMs int64
	embedMs  int64
	sqlMs    int64
	rerankMs int64
	ok       bool
}

type rateResult struct {
	TargetRate int     `json:"target_rate"`
	AchievedRP float64 `json:"achieved_rps"`
	Count      int     `json:"count"`
	Errors     int     `json:"errors"`
	Dropped    int     `json:"dropped"`
	WallP50    float64 `json:"wall_p50_ms"`
	WallP95    float64 `json:"wall_p95_ms"`
	WallP99    float64 `json:"wall_p99_ms"`
	ServerP50  float64 `json:"server_p50_ms"`
	ServerP95  float64 `json:"server_p95_ms"`
	EmbedP95   float64 `json:"embed_p95_ms"`
	SQLP95     float64 `json:"sql_p95_ms"`
	RerankP95  float64 `json:"rerank_p95_ms"`
}

type config struct {
	baseURL  string
	corpus   string
	rates    string
	duration time.Duration
	warmup   time.Duration
	timeout  time.Duration
	seed     int64
	out      string
}

type searchResp struct {
	Timings struct {
		EmbedMS  int64 `json:"embed_ms"`
		SQLMS    int64 `json:"sql_ms"`
		RerankMS int64 `json:"rerank_ms"`
		TotalMS  int64 `json:"total_ms"`
	} `json:"timings"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "loadbench:", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var c config
	flag.StringVar(&c.baseURL, "base-url", "http://localhost:8080", "API base URL")
	flag.StringVar(&c.corpus, "corpus", "../bench/load-corpus.json", "weighted query corpus JSON")
	flag.StringVar(&c.rates, "rates", "25,50,100,200,400", "comma-separated target req/s to sweep")
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "measured window per rate")
	flag.DurationVar(&c.warmup, "warmup", 5*time.Second, "warmup window per rate (discarded)")
	flag.DurationVar(&c.timeout, "timeout", 3*time.Second, "per-request timeout")
	flag.Int64Var(&c.seed, "seed", 42, "RNG seed (fixed => reproducible request stream)")
	flag.StringVar(&c.out, "out", "../bench/load-results.json", "artifact path")
	flag.Parse()
	return c
}

func run(cfg config) error {
	cp, err := loadCorpus(cfg.corpus)
	if err != nil {
		return err
	}
	rates, err := parseRates(cfg.rates)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewSource(cfg.seed)) //nolint:gosec // load shaping, not crypto
	client := newClient(cfg.timeout)

	fmt.Printf("loadbench → %s  (corpus %d queries, rates %v, %s/rate)\n",
		cfg.baseURL, len(cp.Queries), rates, cfg.duration)
	results := make([]rateResult, 0, len(rates))
	for _, rate := range rates {
		rr := runRate(client, cfg, cp, rate, rng)
		results = append(results, rr)
		printRow(rr)
	}
	if err := writeArtifact(cfg, rates, results); err != nil {
		return err
	}
	printMarkdown(results)
	return nil
}

func loadCorpus(path string) (*corpus, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // corpus path is an operator-supplied flag, not user input
	if err != nil {
		return nil, fmt.Errorf("reading corpus %s: %w", path, err)
	}
	var c corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parsing corpus: %w", err)
	}
	if len(c.Queries) == 0 || len(c.Points) == 0 {
		return nil, fmt.Errorf("corpus needs at least one query and one point")
	}
	return &c, nil
}

func parseRates(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid rate %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func newClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        maxInflight,
		MaxIdleConnsPerHost: maxInflight,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// pick returns a weighted-random query paired with a random point and now, so
// the offered stream matches the corpus distribution without precomputing a
// giant slice.
func (c *corpus) pick(rng *rand.Rand, total int) request {
	r := rng.Intn(total)
	q := c.Queries[len(c.Queries)-1].Q
	for _, wq := range c.Queries {
		if r -= wq.Weight; r < 0 {
			q = wq.Q
			break
		}
	}
	p := c.Points[rng.Intn(len(c.Points))]
	return request{q: q, lat: p.Lat, lng: p.Lng, now: c.Nows[rng.Intn(len(c.Nows))]}
}

func weightTotal(c *corpus) int {
	t := 0
	for _, q := range c.Queries {
		t += q.Weight
	}
	return t
}

func runRate(client *http.Client, cfg config, cp *corpus, rate int, rng *rand.Rand) rateResult {
	total := weightTotal(cp)
	dispatch(client, cfg, cp, rate, cfg.warmup, rng, total) // warmup, discarded
	samples, dropped, elapsed := dispatch(client, cfg, cp, rate, cfg.duration, rng, total)
	return summarize(rate, samples, dropped, elapsed)
}

// dispatch fires requests at `rate` for `dur`, scheduling each at start+i/rate
// and measuring latency from that intended instant. It returns the completed
// samples, the count it couldn't admit (saturation), and the wall elapsed.
func dispatch(client *http.Client, cfg config, cp *corpus, rate int, dur time.Duration,
	rng *rand.Rand, total int,
) ([]sample, int, time.Duration) {
	interval := time.Second / time.Duration(rate)
	n := int(dur / interval)
	out := make(chan sample, n)
	sem := make(chan struct{}, maxInflight)
	ctx := context.Background()
	dropped := 0
	start := time.Now()
	for i := 0; i < n; i++ {
		target := start.Add(time.Duration(i) * interval)
		if d := time.Until(target); d > 0 {
			time.Sleep(d)
		}
		req := cp.pick(rng, total)
		select {
		case sem <- struct{}{}:
		default:
			dropped++
			continue
		}
		go func(req request, target time.Time) {
			defer func() { <-sem }()
			out <- doRequest(ctx, client, cfg.baseURL, req, target)
		}(req, target)
	}
	elapsed := time.Since(start)
	for len(sem) > 0 {
		time.Sleep(2 * time.Millisecond)
	}
	close(out)
	samples := make([]sample, 0, n)
	for s := range out {
		samples = append(samples, s)
	}
	return samples, dropped, elapsed
}

func doRequest(ctx context.Context, client *http.Client, base string, req request, target time.Time) sample {
	u := urlFor(base, req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return sample{wallMs: msSince(target), ok: false}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return sample{wallMs: msSince(target), ok: false}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return sample{wallMs: msSince(target), ok: false}
	}
	var body searchResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return sample{wallMs: msSince(target), ok: false}
	}
	t := body.Timings
	return sample{
		wallMs: msSince(target), serverMs: t.TotalMS, embedMs: t.EmbedMS,
		sqlMs: t.SQLMS, rerankMs: t.RerankMS, ok: true,
	}
}

func urlFor(base string, req request) string {
	v := url.Values{}
	v.Set("q", req.q)
	v.Set("lat", strconv.FormatFloat(req.lat, 'f', 6, 64))
	v.Set("lng", strconv.FormatFloat(req.lng, 'f', 6, 64))
	v.Set("now", req.now)
	return strings.TrimRight(base, "/") + "/search?" + v.Encode()
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}

func summarize(rate int, samples []sample, dropped int, elapsed time.Duration) rateResult {
	wall := make([]float64, 0, len(samples))
	srv := make([]float64, 0, len(samples))
	embed := make([]float64, 0, len(samples))
	sqlms := make([]float64, 0, len(samples))
	rerank := make([]float64, 0, len(samples))
	errs := 0
	for _, s := range samples {
		wall = append(wall, s.wallMs)
		if !s.ok {
			errs++
			continue
		}
		srv = append(srv, float64(s.serverMs))
		embed = append(embed, float64(s.embedMs))
		sqlms = append(sqlms, float64(s.sqlMs))
		rerank = append(rerank, float64(s.rerankMs))
	}
	sortf(wall, srv, embed, sqlms, rerank)
	return rateResult{
		TargetRate: rate, AchievedRP: float64(len(samples)) / elapsed.Seconds(),
		Count: len(samples), Errors: errs, Dropped: dropped,
		WallP50: pctl(wall, 50), WallP95: pctl(wall, 95), WallP99: pctl(wall, 99),
		ServerP50: pctl(srv, 50), ServerP95: pctl(srv, 95),
		EmbedP95: pctl(embed, 95), SQLP95: pctl(sqlms, 95), RerankP95: pctl(rerank, 95),
	}
}

func sortf(xs ...[]float64) {
	for _, x := range xs {
		sort.Float64s(x)
	}
}

// pctl returns the p-th percentile of an already-sorted slice (nearest-rank).
func pctl(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := p * len(sorted) / 100
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func printRow(r rateResult) {
	fmt.Printf("  rate %5d → %6.0f rps | wall p50/p95/p99 %5.0f/%5.0f/%5.0f ms"+
		" | srv p95 %4.0f (sql %3.0f embed %3.0f) | err %d drop %d\n",
		r.TargetRate, r.AchievedRP, r.WallP50, r.WallP95, r.WallP99,
		r.ServerP95, r.SQLP95, r.EmbedP95, r.Errors, r.Dropped)
}

func writeArtifact(cfg config, rates []int, results []rateResult) error {
	art := struct {
		GeneratedBy   string       `json:"generated_by"`
		Timestamp     string       `json:"timestamp"`
		BaseURL       string       `json:"base_url"`
		Corpus        string       `json:"corpus"`
		DurationPerRT string       `json:"duration_per_rate"`
		Rates         []int        `json:"rates"`
		Results       []rateResult `json:"results"`
	}{
		GeneratedBy: "cmd/loadbench", Timestamp: time.Now().UTC().Format(time.RFC3339),
		BaseURL: cfg.baseURL, Corpus: cfg.corpus, DurationPerRT: cfg.duration.String(),
		Rates: rates, Results: results,
	}
	raw, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding artifact: %w", err)
	}
	if err := os.WriteFile(cfg.out, raw, 0o644); err != nil { //nolint:gosec // bench artifact, not a secret
		return fmt.Errorf("writing %s: %w", cfg.out, err)
	}
	fmt.Printf("\nwrote %s\n", cfg.out)
	return nil
}

func printMarkdown(results []rateResult) {
	fmt.Println("\n| rate | rps | wall p50 | wall p95 | wall p99 | srv p95 | sql p95 | embed p95 | err | drop |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range results {
		fmt.Printf("| %d | %.0f | %.0f | %.0f | %.0f | %.0f | %.0f | %.0f | %d | %d |\n",
			r.TargetRate, r.AchievedRP, r.WallP50, r.WallP95, r.WallP99,
			r.ServerP95, r.SQLP95, r.EmbedP95, r.Errors, r.Dropped)
	}
}
