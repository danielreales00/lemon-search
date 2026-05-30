#!/usr/bin/env bash
# Convenience wrapper around api/cmd/loadbench: dated artifact + env-overridable
# knobs. See docs/bench/plan.md.
#
#   BASE_URL=http://10.0.0.5:8080 RATES=25,50,100,200,400,800 ./run-sweep.sh
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
RATES="${RATES:-25,50,100,200,400,800}"
DURATION="${DURATION:-30s}"
WARMUP="${WARMUP:-5s}"
TIMEOUT="${TIMEOUT:-3s}"
DATE="$(date +%F)"
OUT="${OUT:-bench/load-results-${DATE}.json}"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT/api"

echo "loadbench → ${BASE_URL}  rates=${RATES}  dur=${DURATION}  → ${OUT}"
exec go run ./cmd/loadbench \
  -base-url "$BASE_URL" \
  -rates "$RATES" \
  -duration "$DURATION" \
  -warmup "$WARMUP" \
  -timeout "$TIMEOUT" \
  -out "../${OUT}"
