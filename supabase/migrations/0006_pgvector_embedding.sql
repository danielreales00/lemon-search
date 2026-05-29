-- 0006_pgvector_embedding.sql
-- Storage foundation for local sentence-embedding semantic recall (ADR-0006, E1).
-- Adds the pgvector extension, a per-business `embedding vector(384)` column, and
-- an HNSW cosine index. Values land later: the column stays NULL until the ingest
-- embedding pass (#91) backfills it; the vector recall blend (#92) reads it.
--
-- Model: all-MiniLM-L6-v2 → 384 dims. Distance: cosine — the recall query blends
-- `embedding <=> $query_vec`, so the index MUST use vector_cosine_ops to be used.
--
-- NULLABLE on purpose: a row with no embedding is a harmless no-op for the recall
-- query (#92 guards for a NULL/absent query vector), never an error. HNSW over an
-- all-NULL column is valid and simply fills in as #91 writes embeddings.
--
-- HNSW build params: m = 16 (graph degree), ef_construction = 64 (build-time
-- candidate list). pgvector defaults — a sound recall/latency/build-time balance
-- for ~23k rows; revisit with the semantic bench (#93) if recall needs tuning.
--
-- Forward-only + idempotent (CI applies the whole set twice): `create extension
-- if not exists`, `add column if not exists`, and `create index if not exists`
-- all re-run without error.

create extension if not exists vector;

alter table businesses
  add column if not exists embedding vector(384);

create index if not exists idx_biz_embedding
  on businesses using hnsw (embedding vector_cosine_ops)
  with (m = 16, ef_construction = 64);
