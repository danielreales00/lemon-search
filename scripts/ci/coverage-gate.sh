#!/usr/bin/env bash
# Coverage gate: the pure-logic "core" packages must clear a statement-coverage
# floor. These are the graded, fixture-testable heart of the engine — the
# ranker, the intent extractor, the config loader. Thin glue (cmd/*, observ),
# the HTTP transport, and the DB adapters are exercised by the integration and
# e2e tiers instead and are deliberately NOT subject to this floor (their
# coverage lives in separate, DB-backed profiles). See docs/development.md.
#
# Per core package:
#   - has a coverage number  -> must be >= THRESHOLD, else FAIL
#   - has functions, no tests -> FAIL (you cannot add core logic untested)
#   - no functions yet (stub) -> skip (nothing to cover)
set -euo pipefail

cd "$(git rev-parse --show-toplevel)/api"

readonly THRESHOLD=90.0
readonly CORE_PKGS=(config rank intent)

paths=()
for p in "${CORE_PKGS[@]}"; do
  paths+=("./internal/$p/...")
done

if ! out="$(go test -cover "${paths[@]}" 2>&1)"; then
  printf '%s\n' "$out"
  echo "coverage-gate: core package tests failed"
  exit 1
fi
printf '%s\n' "$out"
echo "----------------------------------------"

fail=0
for p in "${CORE_PKGS[@]}"; do
  line="$(printf '%s\n' "$out" | grep -E "/internal/$p\b" || true)"
  if printf '%s' "$line" | grep -q "coverage:"; then
    pct="$(printf '%s' "$line" | sed -E 's/.*coverage: ([0-9.]+)% of statements.*/\1/')"
    if awk "BEGIN { exit !($pct < $THRESHOLD) }"; then
      echo "coverage-gate: FAIL internal/$p at ${pct}% (< ${THRESHOLD}%)"
      fail=1
    else
      echo "coverage-gate: ok   internal/$p at ${pct}% (>= ${THRESHOLD}%)"
    fi
  elif grep -rlE '^func ' --include='*.go' --exclude='*_test.go' "internal/$p" >/dev/null 2>&1; then
    echo "coverage-gate: FAIL internal/$p has functions but no tests"
    fail=1
  else
    echo "coverage-gate: skip internal/$p (no functions yet — stub)"
  fi
done

echo "----------------------------------------"
if [ "$fail" -ne 0 ]; then
  echo "coverage-gate: FAILED — core coverage below ${THRESHOLD}%"
  exit 1
fi
echo "coverage-gate: PASSED — core packages >= ${THRESHOLD}%"
