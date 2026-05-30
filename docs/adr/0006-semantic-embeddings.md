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
4. **Stays spec-faithful — relevance stays in recall.** A `semantic_relevance`
   *ranking* signal is **explicitly rejected**: it would add an 8th signal to the
   spec's fixed 7-signal × archetype sum — a structural change to the ranking
   contract, not a formula swap for an existing signal (unlike bayesian/decay).
   The spec's only sanctioned ranking-level relevance override is the exact-name
   pin. So semantic improves *retrieval*; the 7-signal ranker is untouched.

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

### Model reconsideration (2026-05-29) — one shared model, and why not a bigger one

Tempting idea: ingest is offline, so embed businesses with a large long-context
model and keep a small fast one for queries. **It doesn't work** — the recall
vectors are a matched pair.

- Cosine recall (`embedding <=> $query_vec`) is only meaningful if the business
  vector and the query vector live in the **same learned space**. Two different
  models = two spaces = the nearest-neighbour results are noise. So **one model
  serves both ingest and query** — there is no ingest-only model choice. The one
  legitimate asymmetry is a *task prefix* on the same model (`search_document:` /
  `search_query:`), not a different model. A genuinely different model only
  appears as a cross-encoder **reranker** that scores `(query, business)` pairs
  and emits no vectors — a *query-time* cost, not an ingest one, and out of scope
  for the trial.
- The offline budget therefore buys **context length** (feed the model more
  text), **not a bigger model** — the query path inherits whatever we pick, and
  the query path is what the sub-100ms p95 gate grades.
- And context length is not the bottleneck for the queries embeddings exist to
  catch. Open-vocabulary *vibe* intent ("chill place to work") is carried by the
  **tags + the first sentences of `about`**, which `EmbedText` front-loads —
  inside MiniLM's 256-token window. The truncated tail is low-signal prose. A
  heavy model would spend the graded latency budget to capture text that does not
  move recall.

**Decision**: keep **`all-MiniLM-L6-v2` (384d) as the single shared model.** It
is the only option that embeds a query in-process per keystroke (~5–10ms) and
holds the p95 gate; nomic / bge-base (~40–80ms) would not. Any upgrade is
**E5-gated** — measured recall lift on a vibe-query bench weighed against the
latency cost — and is a one-adapter + one-migration swap behind the `Embedder`
port.

**Where each "semantic" query shape is handled** (embeddings are load-bearing
only for the third row; the first two are the spec's own examples and cost
nothing):

| Query shape | Example | Mechanism |
|---|---|---|
| Structured intent | "cheap restaurants", "open now" | intent lexicon → `Overlay` (µs, pure Go) |
| Phrase → category | "i'm hungry" | intent lexicon → category filter |
| Open-vocabulary vibe | "chill place to work" | dense embedding recall (MiniLM) |

**E4 blend (refined)**: the vector channel is an **additive** recall arm — it
UNIONs into the ≤150-candidate pool under the same `Overlay` filters and never
displaces the lexical guarantees (exact-name pin, typo tolerance, prefix). Fuse
the lexical and vector candidate lists with **RRF** (tuning-free). Lexical runs
every keystroke; the query-embed fires on the settled / multi-word query (an
optimization under MiniLM, not a correctness requirement — each request still
clears the gate either way).

### E6 runtime resolution (2026-05-29) — measured three runtimes; chose in-process ORT

E5 cleared the gate (pass@3 50→88%, +38pp), so E6 productionized the runtime.
We measured all three in-process/sidecar options on the same model + harness
(per-query embed, local):

| runtime | embed p50 | ON-pipeline p95 | native deps | verdict |
|---|---|---|---|---|
| pure-Go GoMLX (hugot `GO`) | ~67ms | ~95-103ms | none (`CGO_ENABLED=0`) | **fails the 100ms gate** — GoMLX is an interpreter (no SIMD) |
| Ollama sidecar (E2) | ~15ms | ~30-40ms | sidecar process | clears, but a sidecar |
| **in-process ORT (hugot `ORT`)** | **~1-2ms** | **~17ms** | libonnxruntime + libtokenizers (CGo) | **chosen** — fastest, no sidecar |

The hop is ~1-2ms; **compute dominates**, so the only fast path is native
onnxruntime kernels — which means CGo + two native libs (`libonnxruntime`
runtime-dlopen, `libtokenizers.a` build-time static). We took that build cost for
the ~9× speedup over Ollama and the no-sidecar single-binary coherence. The
GoMLX pure-Go path was attractive (`CGO_ENABLED=0`, zero libs) but is ~40× slower
and fails the gate, so it was rejected as the runtime.

Behind the `domain.Embedder` port, so it is a drop-in swap: `LEMON_EMBED_BACKEND`
selects `ollama` (default; no native deps) or `onnx` (ORT). The ORT path is
tag-gated (`-tags ORT`) and CGo, so default/CI builds compile via hugot's stub
(no libs needed) and only the deploy image bundles the libs + model (#22; recipe
in `docs/operations/deployment.md`). Toolchain bumped Go 1.23 → 1.26 (hugot
v0.7.4 floor).

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

- **E1** (#89) `pgvector` extension + `embedding vector(384)` column + HNSW index (migration) + schema doc.
- **E2** (#90) `domain.Embedder` port + Ollama adapter (`retrieve/embed/ollama`), flag-gated.
- **E3** (#91) ingest embedding pass — compute + store business embeddings.
  **Correction (2026-05-29)**: the embed-text cap shipped as `1000 chars`, but the
  model limit is **256 tokens** — char count is a bad proxy, and Ollama's
  `/api/embed` 400s the *whole batch* if any one input overflows, so one dense
  row poisons its page and only ~1,472/22,568 rows embedded on the first pass.
  Fix (#100): cap at **512 runes** — a corpus-verified-safe proxy for the
  256-token / batch-400 ceiling (zero 400s across all 22,568 rows) — and
  re-embed. The front-loaded composition means only low-signal `about` tail is
  dropped.
- **E4** (#92) query-embed in `search.Service` + vector recall blend in `search_candidates`, behind `LEMON_FF_SEMANTIC`.
- **E5** (#93) semantic bench (NL query set → expected businesses) + **latency measurement** (p50/p95 with vs without) — the go/no-go gate.
- **E6** (#95) in-process ONNX adapter for production, behind the same port — **done**: measured 3 runtimes (see "E6 runtime resolution") and shipped the **ORT (CGo) backend** at ~2ms/embed, `LEMON_EMBED_BACKEND=onnx`. Default/CI builds compile via the stub (no native libs); the deploy image (#22) bundles libonnxruntime + libtokenizers + model.
- ~~**E7** `semantic_relevance` ranking signal~~ — **rejected as counter-spec** (Decision §4): it would add an 8th ranking signal. Relevance stays in retrieval.

## Cross-references

- Why relevance lives in retrieval, not ranking: [0004-spec-contract-discipline.md](0004-spec-contract-discipline.md)
- The use-case seam it plugs into: `internal/search` (architecture audit, search.Service extraction)
- Engine choice + escape-hatch pattern: [0002-search-engine.md](0002-search-engine.md)
