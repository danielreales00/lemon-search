# ADR-0006: Semantic retrieval via local embeddings + pgvector

- **Status**: Proposed
- **Date**: 2026-05-29
- **Deciders**: Daniel

## Context

The findability bench and the relevance deep-dive surfaced the engine's real
ceiling: it handles typos / prefixes / exact names / categories well, but
**semantic** intent beyond the hand-curated lexicon falls through. Queries like
"a place to study quietly with wifi" or "somewhere romantic for an anniversary"
have no keyword hook, so `intent.Extract` produces a zero overlay and recall
relies on trigram + tsvector — which won't find semantically-near businesses.

The spec asks for "smart semantic search … lightweight intent understanding
beyond strict keyword matching" and explicitly says it "does not need full
LLM-based retrieval." A **local sentence-embedding model is not an LLM** and
satisfies this directly. It also improves the layer the spec puts relevance in:
**retrieval** (recall), not the 7-signal quality ranking (ADR-0004).

This is V2 / post-trial work. The aim is to spec it, build it behind a flag, and
**measure** whether the quality gain justifies the latency cost — not to ship it
on by default.

## Decision

Add a semantic recall channel:

1. **`domain.Embedder` port** — `Embed(ctx, text) ([]float32, error)`. The
   `search.Service` (the use-case seam we just extracted) embeds the query via
   this port and passes the vector into retrieval. The core depends on the
   interface; no embedding infra leaks into `domain`/`rank`/`intent`.
2. **Storage**: the `pgvector` extension + an `embedding vector(384)` column on
   `businesses`, with an **HNSW** index. Business embeddings are computed at
   **ingest** from `name + category + subcategory + tags + about`.
3. **Recall blend**: `search_candidates` gains a vector channel —
   `embedding <=> $query_vec` UNION the existing trigram/tsvector/prefix recall —
   so semantic candidates join the set the 7-signal ranker scores. Gated by
   `LEMON_FF_SEMANTIC` (default **off**); a zero/absent query vector is a no-op,
   so today's behavior is unchanged when the flag is off.
4. **Stays spec-faithful**: semantic improves **recall**. A `semantic_relevance`
   *ranking* signal (vector score in the linear sum) would be a further opt-in
   `signal_formulas` switch (ADR-0004 discipline), not part of V1.

### Model + runtime

- **Model**: `all-MiniLM-L6-v2` (384-dim). Small, CPU-fast, general-purpose, and
  available **both** in Ollama (`ollama pull all-minilm`) and as an ONNX export,
  so the same `vector(384)` schema works for either adapter — the runtime is a
  swappable detail.
- **Runtime — two adapters behind the one port** (mirrors the Postgres/Meili
  escape-hatch pattern):
  - **Ollama adapter first, to measure.** A localhost HTTP call (same shape as
    the bench's Meili adapter). No CGo, no native build fight — gets us to a
    working embedder and real numbers fastest.
  - **In-process ONNX adapter as the production target.** `onnxruntime`/`hugot`
    in the Go binary: single binary, no sidecar, lowest query-embed latency —
    the one-system coherence the rest of the architecture holds. Adopt it **iff**
    the measurement justifies productionizing semantic search.
  - **Cheapest first measurement**: precompute business + bench-query embeddings
    offline (one Python script) into pgvector and run the semantic bench with
    **no runtime embedder** — isolates the quality question from the runtime.

## Consequences

**Good**
- Catches the semantic queries the lexicon can't; raises the relevance ceiling
  on the dimension the bench shows is weakest.
- Hexagonal: the `Embedder` port keeps the core pure and the runtime swappable
  (offline → Ollama → ONNX) without touching `rank`/`intent`/`domain`.
- Spec-faithful: enhances retrieval (where the spec wants relevance); not an LLM;
  ranking contract untouched.

**Bad / cost**
- **Latency is the risk** (and the thing to measure): query-embed (~5–20ms CPU)
  + HNSW ANN (~1–5ms) on top of today's p95 ≈ 25ms → likely ~40–50ms locally —
  still sub-100ms, but it **must be measured before enabling**, on every
  keystroke. The Ollama hop and ONNX CGo dependency have different latency/ops
  profiles; the port lets us measure both.
- A second model dependency (a vendored ONNX model, or a running Ollama) — more
  to provision/deploy. The offline-precompute path defers this for the quality
  measurement.
- Embedding ~23k businesses at ingest (a one-off minutes-long pass).

**Revisit when**
- The semantic bench shows the quality lift doesn't justify the latency/ops cost
  → keep the lexicon-only path, drop the flag.
- p95 with semantic on exceeds the budget on the deployed path → keep it off by
  default / behind an explicit opt-in.

## Rollout (board chunks)

- **E1** `pgvector` extension + `embedding vector(384)` column + HNSW index (migration) + schema doc.
- **E2** `domain.Embedder` port + Ollama adapter (`retrieve/embed/ollama`), flag-gated.
- **E3** ingest embedding pass — compute + store business embeddings.
- **E4** query-embed in `search.Service` + vector recall blend in `search_candidates`, behind `LEMON_FF_SEMANTIC`.
- **E5** semantic bench (NL query set → expected businesses) + **latency measurement** (p50/p95 with vs without).
- **E6** (opt) in-process ONNX adapter for production, behind the same port.
- **E7** (opt) `semantic_relevance` ranking signal behind a `signal_formulas` switch.

## Cross-references

- Why relevance lives in retrieval, not ranking: [0004-spec-contract-discipline.md](0004-spec-contract-discipline.md)
- The use-case seam it plugs into: `internal/search` (architecture audit, search.Service extraction)
- Engine choice + escape-hatch pattern: [0002-search-engine.md](0002-search-engine.md)
