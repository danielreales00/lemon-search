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
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// onnxFilename is the model file hugot loads from the model directory (which
// also holds tokenizer.json + the BERT config the tokenizer reads).
const onnxFilename = "model.onnx"

// Embedder is the in-process domain.Embedder. onnxruntime's Run is thread-safe,
// but the hugot pipeline + tokenizer wrapping is not documented as such, so
// RunPipeline is serialized by mu; query embeds are ~2ms, so a single shared
// pipeline is ample for V1. A pipeline pool is the lever if embed throughput
// ever bounds the hot path.
type Embedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	mu       sync.Mutex
}

// New loads the model from modelPath (a directory with model.onnx +
// tokenizer.json + config) and returns an in-process Embedder backed by ONNX
// Runtime. libDir is the directory holding libonnxruntime (empty = the hugot
// platform default, e.g. /usr/lib on linux). It is built once at the composition
// root; Close releases the session.
func New(ctx context.Context, modelPath, libDir string) (*Embedder, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("onnx embedder: empty model path")
	}
	var opts []options.WithOption
	if libDir != "" {
		opts = append(opts, options.WithOnnxLibraryPath(libDir))
	}
	session, err := hugot.NewORTSession(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("onnx embedder: new ORT session (build with -tags ORT?): %w", err)
	}
	cfg := hugot.FeatureExtractionConfig{
		ModelPath:    modelPath,
		Name:         "lemon-embed",
		OnnxFilename: onnxFilename,
		Options:      []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
	}
	pipeline, err := hugot.NewPipeline(session, cfg)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("onnx embedder: load pipeline from %s: %w", modelPath, err)
	}
	return &Embedder{session: session, pipeline: pipeline}, nil
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
	e.mu.Lock()
	out, err := e.pipeline.RunPipeline(ctx, texts)
	e.mu.Unlock()
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
