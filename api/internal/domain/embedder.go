package domain

import "context"

// EmbeddingDim is the dimensionality of the sentence-embedding vectors the
// engine stores and queries. It is fixed by the model (all-MiniLM-L6-v2, 384)
// and matches the businesses.embedding vector(384) column (ADR-0006). Any
// Embedder adapter must produce vectors of exactly this length.
const EmbeddingDim = 384

// Embedder is the semantic-embedding port: it turns query/business text into a
// fixed-length vector for semantic recall. The core owns this interface; the
// runtime (Ollama now, in-process ONNX later — ADR-0006) is a swappable adapter
// behind it, so nothing outside the adapter knows which model serves the call.
//
// Implementations must return vectors of length EmbeddingDim.
type Embedder interface {
	// Embed returns the embedding of a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns one embedding per input text, index-aligned with texts.
	// It exists for ingest throughput (embedding ~23k businesses in one pass).
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
