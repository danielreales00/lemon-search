package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielreales00/lemon-search/api/internal/domain"
)

// floats returns a slice of n identical float64s, for canned embedding bodies.
func floats(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestNewValidatesArgs(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		model   string
		wantErr bool
	}{
		{"ok", "http://localhost:11434", "all-minilm", false},
		{"empty baseURL", "", "all-minilm", true},
		{"empty model", "http://localhost:11434", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(tc.baseURL, nil, tc.model)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("New(%q, %q) = nil error, want error", tc.baseURL, tc.model)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q, %q) unexpected error: %v", tc.baseURL, tc.model, err)
			}
			if e.hc == nil {
				t.Fatalf("nil httpClient must be defaulted")
			}
		})
	}
}

func TestEmbedRequestShapeAndParse(t *testing.T) {
	var gotMethod, gotPath string
	var gotReq embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		_ = json.NewEncoder(w).Encode(embedResponse{Embedding: floats(domain.EmbeddingDim, 0.5)})
	}))
	defer srv.Close()

	e, err := New(srv.URL, srv.Client(), "all-minilm")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vec, err := e.Embed(context.Background(), "quiet place to study")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/embeddings" {
		t.Errorf("path = %q, want /api/embeddings", gotPath)
	}
	if gotReq.Model != "all-minilm" {
		t.Errorf("request model = %q, want all-minilm", gotReq.Model)
	}
	if gotReq.Prompt != "quiet place to study" {
		t.Errorf("request prompt = %q, want the query text", gotReq.Prompt)
	}
	if len(vec) != domain.EmbeddingDim {
		t.Fatalf("vector len = %d, want %d", len(vec), domain.EmbeddingDim)
	}
	if vec[0] != 0.5 {
		t.Errorf("vec[0] = %v, want 0.5 (float64→float32 conversion)", vec[0])
	}
}

func TestEmbedErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			"non-200",
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		},
		{
			"malformed JSON",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{not json") },
		},
		{
			"wrong-length vector",
			func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(embedResponse{Embedding: floats(128, 0.1)})
			},
		},
		{
			"empty vector",
			func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(embedResponse{Embedding: nil})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			e, err := New(srv.URL, srv.Client(), "all-minilm")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := e.Embed(context.Background(), "x"); err == nil {
				t.Fatalf("Embed = nil error, want error for %s", tc.name)
			}
		})
	}
}

func TestEmbedRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(embedResponse{Embedding: floats(domain.EmbeddingDim, 0.1)})
	}))
	defer srv.Close()
	e, err := New(srv.URL, srv.Client(), "all-minilm")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Embed(ctx, "x"); err == nil {
		t.Fatalf("Embed with cancelled ctx = nil error, want error")
	}
}

func TestEmbedBatchRequestShapeAndParse(t *testing.T) {
	var gotPath string
	var gotReq batchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		_ = json.NewEncoder(w).Encode(batchResponse{Embeddings: [][]float64{
			floats(domain.EmbeddingDim, 0.1),
			floats(domain.EmbeddingDim, 0.2),
		}})
	}))
	defer srv.Close()

	e, err := New(srv.URL, srv.Client(), "all-minilm")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	if gotPath != "/api/embed" {
		t.Errorf("path = %q, want /api/embed", gotPath)
	}
	if strings.Join(gotReq.Input, ",") != "a,b" {
		t.Errorf("request input = %v, want [a b]", gotReq.Input)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if vecs[1][0] != 0.2 {
		t.Errorf("vecs[1][0] = %v, want 0.2", vecs[1][0])
	}
}

func TestEmbedBatchEmptyInputIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("empty batch must not hit the server")
	}))
	defer srv.Close()
	e, err := New(srv.URL, srv.Client(), "all-minilm")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vecs, err := e.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch(nil): %v", err)
	}
	if vecs != nil {
		t.Fatalf("EmbedBatch(nil) = %v, want nil", vecs)
	}
}

func TestEmbedBatchCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(batchResponse{Embeddings: [][]float64{floats(domain.EmbeddingDim, 0.1)}})
	}))
	defer srv.Close()
	e, err := New(srv.URL, srv.Client(), "all-minilm")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := e.EmbedBatch(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatalf("EmbedBatch with mismatched count = nil error, want error")
	}
}

// compile-time assertion that the adapter satisfies the port.
var _ domain.Embedder = (*Embedder)(nil)
