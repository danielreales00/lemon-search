package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/config"
	"github.com/danielreales00/lemon-search/api/internal/domain"
	"github.com/danielreales00/lemon-search/api/internal/rank"
	ollama "github.com/danielreales00/lemon-search/api/internal/retrieve/embed/ollama"
	onnx "github.com/danielreales00/lemon-search/api/internal/retrieve/embed/onnx"
	"github.com/danielreales00/lemon-search/api/internal/search"
)

const (
	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "all-minilm"
	defaultONNXModel   = "models/all-MiniLM-L6-v2"
	// materialLiftPP is the pass@3 gain (percentage points) below which the
	// semantic channel does not justify its latency/ops cost (E5 gate).
	materialLiftPP = 10.0
	// engineP95Budget is the local engine-time p95 ceiling (ms). The 100ms p95
	// gate is end-to-end; on the deployed path browser→Vercel→Fly→Supabase adds
	// ~30-40ms, so the in-process engine must stay well under 100 to clear it.
	engineP95Budget = 60
)

// semanticBenchFile is the hand-labeled NL query set (bench/semantic-cluster.json).
// Ground truth is category/subcategory level: `Expect` holds lowercase substring
// tokens matched against each top-3 result's category+subcategory.
type semanticBenchFile struct {
	UserLocation struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	} `json:"user_location"`
	NowOverride string         `json:"now_override"`
	Tests       []semanticTest `json:"tests"`
}

type semanticTest struct {
	Q      string   `json:"q"`
	Expect []string `json:"expect"`
	Note   string   `json:"note"`
}

// semResult is one query run through both arms (semantic OFF vs ON).
type semResult struct {
	q       string
	offPass bool
	onPass  bool
	onTop1  string
	offMS   int64
	onMS    int64
	embedMS int64
	err     error
}

// runSemantic is the E5 go/no-go bench: it runs the NL set through the search
// service with semantic recall OFF then ON (the only difference is the wired
// embedder), scores pass@3 by category/subcategory, and writes the comparison +
// recommendation. Intent is ON in both arms, so the table isolates the marginal
// lift of the vector channel over the production lexical+intent baseline.
func runSemantic(ctx context.Context, cfg *config.Ranking, repo domain.BusinessRepo, o opts) error {
	sf, err := loadSemanticBench(o.semanticBench)
	if err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339, sf.NowOverride)
	if err != nil {
		return fmt.Errorf("parsing now_override %q: %w", sf.NowOverride, err)
	}
	// LEMON_EMBED_BACKEND selects the runtime under test (ollama | onnx), so the
	// same harness re-measures the in-process ONNX path vs the Ollama hop.
	emb, closeEmb, err := benchEmbedder(ctx)
	if err != nil {
		return err
	}
	defer closeEmb()

	svcOff := search.New(benchLogger(), repo, cfg, true, nil)
	svcOn := search.New(benchLogger(), repo, cfg, true, emb)

	results := make([]semResult, 0, len(sf.Tests))
	for _, t := range sf.Tests {
		results = append(results, runSemanticOne(ctx, svcOff, svcOn, sf, now, t))
	}

	if err := writeSemanticReport(o.semanticOut, sf, results); err != nil {
		return err
	}
	printSemanticSummary(o.semanticOut, results)
	return nil
}

// benchEmbedder builds the embedder under test, selected by LEMON_EMBED_BACKEND
// (ollama | onnx; default ollama) — so the same harness re-measures the
// in-process ONNX path's latency against the Ollama hop. Typed as the domain
// port; the returned func releases ONNX session resources.
func benchEmbedder(ctx context.Context) (domain.Embedder, func(), error) {
	noop := func() {}
	if envDefault("LEMON_EMBED_BACKEND", "ollama") == "onnx" {
		path := envDefault("LEMON_ONNX_MODEL_PATH", defaultONNXModel)
		o, err := onnx.New(ctx, path, os.Getenv("LEMON_ONNX_RUNTIME_DIR"))
		if err != nil {
			return nil, noop, fmt.Errorf("building onnx embedder (model at %s): %w", path, err)
		}
		var emb domain.Embedder = o
		return emb, func() { _ = o.Close() }, nil
	}
	o, err := ollama.New(defaultOllamaURL, nil, defaultOllamaModel)
	if err != nil {
		return nil, noop, fmt.Errorf("building ollama embedder (is `ollama serve` running with %s?): %w", defaultOllamaModel, err)
	}
	var emb domain.Embedder = o
	return emb, noop, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// runSemanticOne runs a single query through both arms and times each. The OFF
// arm is purely lexical; the ON arm embeds the query (timed as embedMS) and adds
// the vector recall channel.
func runSemanticOne(ctx context.Context, svcOff, svcOn *search.Service, sf semanticBenchFile, now time.Time, t semanticTest) semResult {
	opts := domain.SearchOpts{Lat: sf.UserLocation.Lat, Lng: sf.UserLocation.Lng, Now: now}

	offStart := time.Now()
	offRanked, _, offErr := svcOff.Search(ctx, t.Q, opts)
	offMS := time.Since(offStart).Milliseconds()

	onStart := time.Now()
	onRanked, onTimings, onErr := svcOn.Search(ctx, t.Q, opts)
	onMS := time.Since(onStart).Milliseconds()

	if offErr != nil {
		return semResult{q: t.Q, err: fmt.Errorf("off arm: %w", offErr)}
	}
	if onErr != nil {
		return semResult{q: t.Q, err: fmt.Errorf("on arm: %w", onErr)}
	}
	return semResult{
		q:       t.Q,
		offPass: scoreSemantic(t, offRanked),
		onPass:  scoreSemantic(t, onRanked),
		onTop1:  top1Name(onRanked),
		offMS:   offMS,
		onMS:    onMS,
		embedMS: onTimings.EmbedMS,
	}
}

// scoreSemantic passes a query when any of its top-3 results has a category or
// subcategory containing any expected token (case-insensitive substring).
func scoreSemantic(t semanticTest, ranked []rank.Result) bool {
	n := topK
	if len(ranked) < n {
		n = len(ranked)
	}
	for i := 0; i < n; i++ {
		hay := strings.ToLower(ranked[i].Candidate.Category)
		if sub := ranked[i].Candidate.Subcategory; sub != nil {
			hay += " " + strings.ToLower(*sub)
		}
		for _, tok := range t.Expect {
			if strings.Contains(hay, strings.ToLower(tok)) {
				return true
			}
		}
	}
	return false
}

func top1Name(ranked []rank.Result) string {
	if len(ranked) == 0 {
		return "(none)"
	}
	return ranked[0].Candidate.Name
}

func loadSemanticBench(path string) (semanticBenchFile, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied bench path
	if err != nil {
		return semanticBenchFile{}, fmt.Errorf("reading semantic bench %s: %w", path, err)
	}
	var sf semanticBenchFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return semanticBenchFile{}, fmt.Errorf("parsing semantic bench %s: %w", path, err)
	}
	if len(sf.Tests) == 0 {
		return semanticBenchFile{}, fmt.Errorf("semantic bench %s has no tests", path)
	}
	return sf, nil
}

// tallySemantic counts pass@3 for each arm plus gained (off-fail → on-pass) and
// regressed (off-pass → on-fail), over the error-free results.
func tallySemantic(results []semResult) (offN, onN, gained, regressed, total int) {
	for _, r := range results {
		if r.err != nil {
			continue
		}
		total++
		if r.offPass {
			offN++
		}
		if r.onPass {
			onN++
		}
		switch {
		case !r.offPass && r.onPass:
			gained++
		case r.offPass && !r.onPass:
			regressed++
		}
	}
	return offN, onN, gained, regressed, total
}

// latencies returns the per-arm wall times and the ON-arm embed times, error-free.
func latencies(results []semResult) (off, on, embed []int64) {
	for _, r := range results {
		if r.err != nil {
			continue
		}
		off = append(off, r.offMS)
		on = append(on, r.onMS)
		embed = append(embed, r.embedMS)
	}
	return off, on, embed
}

// printSemanticSummary echoes the headline numbers to stdout; this is a dev CLI,
// so direct stdout printing (not slog) is the intended output channel.
func printSemanticSummary(out string, results []semResult) {
	offN, onN, gained, regressed, total := tallySemantic(results)
	offLat, onLat, embedLat := latencies(results)
	fmt.Printf("semantic bench: OFF %d/%d  ON %d/%d  (+%d gained, -%d regressed)\n", //nolint:forbidigo // bench CLI stdout
		offN, total, onN, total, gained, regressed)
	fmt.Printf("latency: off p50=%dms p95=%dms | on p50=%dms p95=%dms | embed p50=%dms p95=%dms\n", //nolint:forbidigo // bench CLI stdout
		percentile(offLat, pctl50), percentile(offLat, pctl95),
		percentile(onLat, pctl50), percentile(onLat, pctl95),
		percentile(embedLat, pctl50), percentile(embedLat, pctl95))
	fmt.Printf("wrote semantic report: %s\n", out) //nolint:forbidigo // bench CLI stdout
}
