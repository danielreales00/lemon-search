#!/usr/bin/env bash
# Seed a freshly-migrated Supabase DB with the ~23k businesses + embeddings.
#
# Default (recommended) path: copy the businesses table — embeddings included —
# straight from the already-seeded LOCAL dev DB. No re-ingest, no re-embedding;
# the vectors are already computed and (per the ONNX↔Ollama parity test) live in
# the same space the query embedder uses. ~23k rows transfer in seconds.
#
# Requires migrations applied on the target first (deploy/supabase/apply-migrations.sh)
# so the schema, indexes, and functions exist.
#
#   SOURCE_DATABASE_URL='postgresql://postgres:postgres@127.0.0.1:54322/postgres' \
#   LEMON_DATABASE_URL='postgresql://postgres:...@db.<ref>.supabase.co:5432/postgres' \
#     deploy/supabase/seed.sh
#
# Fresh-from-JSON alternative (no local DB): run cmd/ingest instead —
#   go run ./cmd/ingest -input businesses-<date>.json     # load rows
#   go run ./cmd/ingest -embed                            # backfill vectors (Ollama)
# both with LEMON_DATABASE_URL pointed at the target. See docs/operations/deployment.md.
set -euo pipefail

SRC="${SOURCE_DATABASE_URL:?set SOURCE_DATABASE_URL (the already-seeded local DB)}"
DST="${LEMON_DATABASE_URL:?set LEMON_DATABASE_URL (the Supabase target)}"

for bin in psql pg_dump; do
  command -v "$bin" >/dev/null || { echo "$bin is required (postgresql-client)" >&2; exit 1; }
done

SRC_ROWS=$(psql "$SRC" -tAc "select count(*) from businesses;")
echo "==> source has ${SRC_ROWS} rows; copying businesses (data only) → target"

# --data-only: schema/indexes/functions already exist from the migrations.
# pg_dump omits generated columns (photo_count, is_new) from COPY automatically.
# Truncate first so re-runs are clean (idempotent).
psql "$DST" -v ON_ERROR_STOP=1 -q -c "truncate businesses;"
pg_dump --data-only --no-owner --table=businesses "$SRC" \
  | psql "$DST" -v ON_ERROR_STOP=1 -q

echo "==> ANALYZE (refresh planner stats for the new rows)"
psql "$DST" -v ON_ERROR_STOP=1 -q -c "analyze businesses;"

DST_ROWS=$(psql "$DST" -tAc "select count(*) from businesses;")
EMBEDDED=$(psql "$DST" -tAc "select count(embedding) from businesses;")
echo "==> target now has ${DST_ROWS} rows, ${EMBEDDED} embedded"
[ "$DST_ROWS" = "$SRC_ROWS" ] || { echo "!! row count mismatch (src ${SRC_ROWS} != dst ${DST_ROWS})" >&2; exit 1; }

echo "==> set the grader role password (read-only access deliverable)"
echo "    run once, then share (user=lemon_grader, password) over a secure channel:"
echo "    psql \"\$LEMON_DATABASE_URL\" -c \"alter role lemon_grader with password '<strong>';\""
echo "==> seed complete"
