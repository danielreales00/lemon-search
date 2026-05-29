package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// httpTimeout bounds a single embedding call. Query-embed sits on the hot path
// (every keystroke), so a stalled Ollama must fail fast rather than hang the
// request; callers can override by passing their own *http.Client.
const httpTimeout = 10 * time.Second

// Embedder calls an Ollama server over HTTP to embed text. It implements
// domain.Embedder. baseURL is the server root (e.g. http://localhost:11434);
// model is the Ollama model tag (e.g. all-minilm).
type Embedder struct {
	baseURL string
	model   string
	hc      *http.Client
}

// New constructs an Ollama-backed Embedder. baseURL and model must be non-empty
// (validated at the boundary). A nil httpClient gets a default client with a
// fail-fast timeout.
func New(baseURL string, httpClient *http.Client, model string) (*Embedder, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("ollama embedder: empty baseURL")
	}
	if model == "" {
		return nil, fmt.Errorf("ollama embedder: empty model")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: httpTimeout}
	}
	return &Embedder{baseURL: baseURL, model: model, hc: httpClient}, nil
}

// embedRequest is the /api/embeddings wire request (single prompt).
type embedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// embedResponse is the /api/embeddings wire response.
type embedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// batchRequest is the /api/embed wire request (input array).
type batchRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// batchResponse is the /api/embed wire response.
type batchResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// Embed embeds a single text via Ollama's /api/embeddings endpoint.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	var resp embedResponse
	if err := e.post(ctx, "/api/embeddings", embedRequest{Model: e.model, Prompt: text}, &resp); err != nil {
		return nil, err
	}
	vec, err := toVector(resp.Embedding)
	if err != nil {
		return nil, fmt.Errorf("embed %q: %w", text, err)
	}
	return vec, nil
}

// EmbedBatch embeds many texts in one call via Ollama's /api/embed endpoint,
// returning vectors index-aligned with texts. An empty input is a no-op.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp batchResponse
	if err := e.post(ctx, "/api/embed", batchRequest{Model: e.model, Input: texts}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embed batch: got %d vectors for %d texts", len(resp.Embeddings), len(texts))
	}
	out := make([][]float32, len(resp.Embeddings))
	for i, raw := range resp.Embeddings {
		vec, err := toVector(raw)
		if err != nil {
			return nil, fmt.Errorf("embed batch index %d: %w", i, err)
		}
		out[i] = vec
	}
	return out, nil
}

// post marshals body, POSTs it to baseURL+path, and decodes a JSON response
// into out. It threads ctx and treats any non-2xx as an error.
func (e *Embedder) post(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.hc.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on read path
	if resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("post %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// toVector converts a raw float64 embedding to []float32 and asserts the model
// dimensionality (domain.EmbeddingDim). A wrong length means a model/schema
// mismatch and is a hard error.
func toVector(raw []float64) ([]float32, error) {
	if len(raw) != domain.EmbeddingDim {
		return nil, fmt.Errorf("expected %d dims, got %d", domain.EmbeddingDim, len(raw))
	}
	vec := make([]float32, len(raw))
	for i, v := range raw {
		vec[i] = float32(v)
	}
	return vec, nil
}
