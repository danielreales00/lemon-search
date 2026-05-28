// Package intent extracts a small filter/boost overlay from a raw query string
// using a hand-curated lexicon (no LLM, no embeddings).
//
// Output is consumed by the retrieve layer; intent does not touch ranking.
package intent
