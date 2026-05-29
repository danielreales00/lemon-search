// Package ollama is a localhost-HTTP adapter implementing domain.Embedder
// against an Ollama server (ADR-0006, "Ollama adapter first, to measure").
//
// It POSTs text to Ollama's embeddings endpoints and returns 384-dim vectors,
// validating the dimensionality at the boundary. Nothing outside this package
// knows the runtime is Ollama; the in-process ONNX adapter (E6) will satisfy
// the same port without touching the core.
package ollama
