#!/usr/bin/env bash
# Render bench/load-corpus.json as a vegeta target file, for an independent
# cross-check of the pure latency numbers (vegeta doesn't parse our response
# timings, so the Go harness stays the source of truth for stage attribution).
# Each query is repeated `weight` times across every point so the distribution
# matches the corpus; the now field is fixed to the first entry. Requires jq.
#
#   ./emit-vegeta-targets.sh http://10.0.0.5:8080 > targets.txt
#   vegeta attack -targets targets.txt -rate 200 -duration 30s | vegeta report
set -euo pipefail

BASE="${1:-http://localhost:8080}"
CORPUS="${2:-$(cd "$(dirname "$0")/../.." && pwd)/bench/load-corpus.json}"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

# Portable shuffle: shuf (GNU), gshuf (coreutils on macOS), else an awk fallback.
shuffle() {
  if command -v shuf >/dev/null; then shuf
  elif command -v gshuf >/dev/null; then gshuf
  else awk 'BEGIN{srand()}{printf "%f\t%s\n", rand(), $0}' | sort -k1,1n | cut -f2-
  fi
}

jq -r --arg base "${BASE%/}" '
  .nows[0] as $now
  | [ .queries[] as $q
      | range($q.weight) as $_
      | .points[] as $p
      | "GET \($base)/search?q=\($q.q|@uri)&lat=\($p.lat)&lng=\($p.lng)&now=\($now|@uri)"
    ]
  | .[]
' "$CORPUS" | shuffle
