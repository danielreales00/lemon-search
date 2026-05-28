# Observability

What the system tells you, and where to look. Keep it lean — V1 has no
metrics stack, only structured logs and per-response timings.

## Per-request timings (the perf signal)

Every `/search` response includes a `timings` block:

```json
{
  "timings": {
    "intent_ms":  0,
    "sql_ms":    18,
    "rerank_ms":  3,
    "total_ms":  27
  }
}
```

| Field | What it measures |
|---|---|
| `intent_ms` | Time spent inside `intent.Extract(query)` |
| `sql_ms` | Wall-clock from request-to-pool through `Search()` returning to caller |
| `rerank_ms` | Pure CPU spent in the Go ranker (hard-filter + signals + sum + dedup + pin + sort + de-pin) |
| `total_ms` | End-to-end inside the handler (request decode → response encode), measured by the outermost `observ.Stopwatch` |

Invariant the bench script asserts:
`total_ms ≥ intent_ms + sql_ms + rerank_ms` (the remainder is bookkeeping
+ JSON encode + small overhead).

## Performance budget

End-to-end p95 target: **< 100 ms**.

| Slice | Budget | Owner |
|---|---|---|
| Browser → Vercel | ≤ 30 ms | Network / TLS |
| Vercel → Fly (`iad`) | ≤ 25 ms | Network (same region) |
| `intent_ms` | ≤ 1 ms | `internal/intent` |
| `sql_ms` | ≤ 25 ms | `internal/retrieve/postgres` + Postgres |
| `rerank_ms` | ≤ 5 ms | `internal/rank` |
| JSON encode + headers | ≤ 4 ms | `internal/api` |
| Fly → Browser | ≤ 10 ms | Network |
| **End-to-end p95** | **≤ 100 ms** | — |

Slack ~5 ms for variance. Anything above budget shows up in the loadtest
report (`bench/loadtest-*.md`).

## Structured logs

Format: JSON via `slog` (Go's stdlib structured logger). Logs go to
stdout/stderr; Fly captures them; `flyctl logs` streams them; the Fly
dashboard archives them.

Every `/search` request emits one log line:

```
{
  "time":      "2026-05-30T14:23:18.213Z",
  "level":     "INFO",
  "msg":       "search",
  "trace_id":  "01HZ…",
  "q":         "sushi",
  "lat":       25.7741,
  "lng":       -80.1937,
  "results":   15,
  "intent_ms": 0,
  "sql_ms":    18,
  "rerank_ms": 3,
  "total_ms":  27
}
```

Errors:

```
{
  "time":  "…",
  "level": "ERROR",
  "msg":   "search: retrieval failed",
  "trace_id": "01HZ…",
  "err":   "context deadline exceeded",
  "q":     "…"
}
```

We do **not** log the full query string at WARN/ERROR if it might contain
PII; current product has no auth so this isn't a concern.

## Trace IDs

Every request gets a ULID-like `trace_id` at handler entry (via
`observ.NewTraceID()`). It's threaded through `slog` via a `context`
value. The trace_id is included in:

- Every log line for that request
- The `X-Trace-Id` response header
- Error responses (TBD — not in V1's error body)

When debugging a slow or wrong response, grab the trace_id from the
response header and grep Fly logs.

## What we do NOT have (V1 scope cuts)

- **Metrics** (Prometheus, Datadog, Honeycomb). Out of scope for the
  trial. Mentioned in writeup as V2.
- **Distributed tracing** (OpenTelemetry). Same.
- **Alerting**. Same.
- **APM** (Sentry, Honeycomb). Same.
- **DB query logging at the application level**. We rely on Supabase's
  built-in slow-query dashboard for V1.

A future Lemon production system would add at minimum: Honeycomb +
OpenTelemetry + a Grafana board for p95 vs. RPS. Not for a 4-day trial.

## Bench output

`scripts/bench-runner` writes `bench/results-<date>.json` with:

```json
{
  "ran_at": "2026-05-30T14:00:00Z",
  "config": { "signal_formulas": {"rating": "literal", "distance": "literal"} },
  "summary": {
    "tests": 30,
    "passed": 24,
    "skipped (placeholder)": 0,
    "pass_rate": 0.80,
    "p50_total_ms": 22,
    "p95_total_ms": 64,
    "p99_total_ms": 91
  },
  "by_test": [ … ]
}
```

A markdown summary table for the writeup goes to
`bench/results-<date>.md`.

## Loadtest output

`scripts/loadtest.sh` runs `hey -z 60s -c 50 -q 10` against `/search` with
a query pool and writes `bench/loadtest-<date>.md`:

```
# Loadtest 2026-05-30

50 RPS for 60 s · 50 concurrent · query pool of 30

Total requests: 3000   non-2xx: 0
Latency (ms)
  p50:  24
  p95:  68
  p99: 102

Per-stage at p95
  intent_ms: 1
  sql_ms:   42
  rerank_ms: 4
```

## What to look at when something goes wrong

| Symptom | First place to look |
|---|---|
| 5xx responses | `flyctl logs` — grep ERROR; pull the trace_id |
| Slow responses (high `sql_ms`) | Supabase dashboard → Query Performance |
| Slow responses (high `rerank_ms`) | Unlikely; profile with `go test -bench` on `internal/rank` |
| Empty results for known-good queries | API logs for the trace_id; check intent overlay produced |
| Bench pass-rate drop | `scripts/bench-runner --diff <prev>` |

## Cross-references

- Deployment: [deployment.md](deployment.md)
- Bench runner: `scripts/bench-runner/`
- Loadtest script: `scripts/loadtest.sh`
- The ranking timings are sized in [../ranking/semantics.md](../ranking/semantics.md)
- The `observ` package: `api/internal/observ/`
