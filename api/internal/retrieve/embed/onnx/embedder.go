// Package onnx is the in-process embedding adapter: it runs all-MiniLM-L6-v2
// inside the API binary via hugot's ONNX Runtime (ORT) backend — native
// onnxruntime kernels, no network hop. It is the production runtime for ADR-0006
// semantic recall: same domain.Embedder port and 384-dim output as the Ollama
// adapter, but ~2ms/query (measured ~9x faster than the Ollama hop, ~40x faster
// than the pure-Go GoMLX interpreter) and no sidecar.
//
// BUILD: the ORT backend is CGo and tag-gated. Build the server (and this
// package's parity test) with `-tags ORT`, CGO_ENABLED=1, and the two native
// libs present: libonnxruntime (runtime, dlopen'd from LEMON_ONNX_RUNTIME_DIR or
// the platform default) and libtokenizers.a (build-time static link). Without
// `-tags ORT` the package still compiles (hugot ships a stub) but New returns an
// "enable ORT" error — so default CI builds stay green and only the deploy image
// (#22) needs the libs. See docs/operations/deployment.md.
//
// Parity: WithNormalization L2-normalizes the mean-pooled vector to match
// sentence-transformers / the Ollama all-minilm output (verified cosine ≈ 1.0 —
// embedder_integration_test.go).
package onnx

import (
	"context"
	"fmt"
	"runtime"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// onnxFilename is the model file hugot loads from the model directory (which
// also holds tokenizer.json + the BERT config the tokenizer reads).
const onnxFilename = "model.onnx"

// Embedder is the in-process domain.Embedder. A single RunPipeline is not
// concurrency-safe (the Rust tokenizer wrapping isn't documented as such), so
// instead of serializing every embed behind one shared pipeline, we hold a pool
// of independent pipelines and check one out per call. The pool channel doubles
// as the semaphore: at most len(pool) embeds run at once, each on its own ORT
// inference session, so concurrent queries scale across cores instead of
// queueing behind a global mutex.
//
// Each pooled pipeline owns a full ORT InferenceSession — i.e. its own copy of
// the model weights (~86MB for MiniLM). Pool size therefore trades RAM for embed
// parallelism: size it to the box's vCPUs (the default), not higher.
type Embedder struct {
	session *hugot.Session
	pool    chan *pipelines.FeatureExtractionPipeline
}

// New loads the model from modelPath (a directory with model.onnx +
// tokenizer.json + config) and returns an in-process Embedder backed by ONNX
// Runtime. libDir is the directory holding libonnxruntime (empty = the hugot
// platform default, e.g. /usr/lib on linux). poolSize is the number of
// concurrent embed pipelines; <= 0 defaults to GOMAXPROCS. It is built once at
// the composition root; Close releases the session and every pooled pipeline.
//
// Session options pin intra-op parallelism to 1 thread and disable spinning:
// the pool already provides cross-core concurrency (one busy core per in-flight
// embed), so per-Run intra-op threads would only oversubscribe, and spinning
// threads would busy-wait and burn the cores the pool needs for other embeds.
func New(ctx context.Context, modelPath, libDir string, poolSize int) (*Embedder, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("onnx embedder: empty model path")
	}
	if poolSize <= 0 {
		poolSize = runtime.GOMAXPROCS(0)
	}
	opts := []options.WithOption{
		options.WithIntraOpNumThreads(1),
		options.WithIntraOpSpinning(false),
	}
	if libDir != "" {
		opts = append(opts, options.WithOnnxLibraryPath(libDir))
	}
	session, err := hugot.NewORTSession(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("onnx embedder: new ORT session (build with -tags ORT?): %w", err)
	}
	pool := make(chan *pipelines.FeatureExtractionPipeline, poolSize)
	for i := 0; i < poolSize; i++ {
		cfg := hugot.FeatureExtractionConfig{
			ModelPath:    modelPath,
			Name:         fmt.Sprintf("lemon-embed-%d", i),
			OnnxFilename: onnxFilename,
			Options:      []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
		}
		pipeline, err := hugot.NewPipeline(session, cfg)
		if err != nil {
			_ = session.Destroy()
			return nil, fmt.Errorf("onnx embedder: load pipeline %d from %s: %w", i, modelPath, err)
		}
		pool <- pipeline
	}
	return &Embedder{session: session, pool: pool}, nil
}

// Embed returns the embedding of a single text.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.run(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch returns one embedding per input text, index-aligned with texts.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	return e.run(ctx, texts)
}

func (e *Embedder) run(ctx context.Context, texts []string) ([][]float32, error) {
	var pipeline *pipelines.FeatureExtractionPipeline
	select {
	case pipeline = <-e.pool:
	case <-ctx.Done():
		return nil, fmt.Errorf("onnx embed: waiting for a pipeline: %w", ctx.Err())
	}
	defer func() { e.pool <- pipeline }()

	out, err := pipeline.RunPipeline(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("onnx embed %d texts: %w", len(texts), err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("onnx embedder: %d embeddings for %d texts", len(out.Embeddings), len(texts))
	}
	for i, v := range out.Embeddings {
		if len(v) != domain.EmbeddingDim {
			return nil, fmt.Errorf("onnx embedder: text %d got %d dims, want %d", i, len(v), domain.EmbeddingDim)
		}
	}
	return out.Embeddings, nil
}

// Close releases the ORT session and its resources.
func (e *Embedder) Close() error {
	return e.session.Destroy()
}
