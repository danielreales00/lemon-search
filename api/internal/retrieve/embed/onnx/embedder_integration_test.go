//go:build integration && ORT

// Parity check: the in-process ONNX (ORT) adapter must produce the same vectors
// as the Ollama adapter (same model). Tag-gated on ORT because it needs the CGo
// onnxruntime build + native libs; the default `-tags integration` CI tier does
// not set ORT, so this file isn't compiled there (no libs/model needed in CI).
// Needs the model dir (LEMON_ONNX_MODEL_PATH, ~86MB, gitignored), libonnxruntime
// (LEMON_ONNX_RUNTIME_DIR), and a running Ollama (the parity assertion skips if
// Ollama is absent).
//
//	cd api && CGO_ENABLED=1 CGO_LDFLAGS=-L/path/to/libtokenizers \
//	  LEMON_ONNX_MODEL_PATH="$PWD/models/all-MiniLM-L6-v2" \
//	  LEMON_ONNX_RUNTIME_DIR=/opt/homebrew/lib \
//	  go test -tags "integration ORT" ./internal/retrieve/embed/onnx/...
package onnx

import (
	"context"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/domain"
	ollama "github.com/danielreales00/lemon-search/api/internal/retrieve/embed/ollama"
)

const (
	defaultModelPath = "../../../../models/all-MiniLM-L6-v2"
	ollamaURL        = "http://localhost:11434"
	ollamaModel      = "all-minilm"
)

func TestONNXEmbedderParityWithOllama(t *testing.T) {
	ctx := context.Background()
	modelPath := modelPathOrSkip(t)

	emb, err := New(ctx, modelPath, os.Getenv("LEMON_ONNX_RUNTIME_DIR"))
	if err != nil {
		t.Fatalf("New(%s): %v", modelPath, err)
	}
	t.Cleanup(func() { _ = emb.Close() })

	oll, err := ollama.New(ollamaURL, &http.Client{Timeout: 10 * time.Second}, ollamaModel)
	if err != nil {
		t.Fatalf("ollama.New: %v", err)
	}

	texts := []string{
		"cuban coffee in brickell",
		"a relaxed coffee shop to work with wifi",
		"thinking about getting some ink",
		"somewhere to get pampered",
	}
	for _, text := range texts {
		hv, err := emb.Embed(ctx, text)
		if err != nil {
			t.Fatalf("onnx Embed(%q): %v", text, err)
		}
		if len(hv) != domain.EmbeddingDim {
			t.Fatalf("onnx dims = %d, want %d", len(hv), domain.EmbeddingDim)
		}
		ov, err := oll.Embed(ctx, text)
		if err != nil {
			t.Skipf("ollama not reachable (%v); parity check needs a running Ollama", err)
		}
		if cos := cosine(hv, ov); cos < 0.99 {
			t.Errorf("cosine(onnx, ollama) = %.4f for %q, want ≥ 0.99 (same model ⇒ near-identical)", cos, text)
		}
	}
}

func TestONNXEmbedBatchMatchesEmbed(t *testing.T) {
	ctx := context.Background()
	emb, err := New(ctx, modelPathOrSkip(t), os.Getenv("LEMON_ONNX_RUNTIME_DIR"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = emb.Close() })

	texts := []string{"sushi downtown", "a quiet place to read"}
	batch, err := emb.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != len(texts) {
		t.Fatalf("EmbedBatch returned %d vectors for %d texts", len(batch), len(texts))
	}
	for i, text := range texts {
		single, err := emb.Embed(ctx, text)
		if err != nil {
			t.Fatalf("Embed(%q): %v", text, err)
		}
		if cos := cosine(batch[i], single); cos < 0.999 {
			t.Errorf("batch[%d] vs single cosine = %.5f, want ≥ 0.999", i, cos)
		}
	}
}

func modelPathOrSkip(t *testing.T) string {
	t.Helper()
	p := os.Getenv("LEMON_ONNX_MODEL_PATH")
	if p == "" {
		p = defaultModelPath
	}
	if _, err := os.Stat(filepath.Join(p, onnxFilename)); err != nil {
		t.Skipf("model not found at %s (set LEMON_ONNX_MODEL_PATH); skipping ONNX parity test", p)
	}
	return p
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
