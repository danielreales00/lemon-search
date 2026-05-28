# Benchmark

`queries.json` is the curated test set we grade ourselves on, run daily.

Each test specifies a query plus an `expected_top_3` list — the test passes if **at least one** of the expected names lands in the API's top 3. Multiple acceptable answers per query because ranking is fuzzy.

## Running

```bash
# Local (against http://localhost:8080)
go run ./scripts/bench-runner

# Against deployed API
LEMON_API_BASE_URL=https://lemon-search-api.fly.dev go run ./scripts/bench-runner
```

Outputs `bench/results-YYYY-MM-DD.json` with:

- Pass/fail per query
- Per-query timings (intent_ms, sql_ms, rerank_ms, total_ms)
- Aggregate: pass rate, p50/p95/p99 total latency

## Expected hits

`expected_top_3` is filled with `__FILL__` placeholders until ingestion (Stage 1) produces stable names. The bench script treats `__FILL__` as "skip pass/fail, still record timings."
