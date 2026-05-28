# HTTP API

Base URL: `https://lemon-search-api.fly.dev` (production) ·
`http://localhost:8080` (dev).

All responses are JSON. Authentication: none in V1. CORS: open for the
Vercel FE origin.

## Endpoints

### `GET /search`

The only ranked-search endpoint. Returns up to 15 ranked businesses.

**Query parameters**:

| Name | Required | Type | Default | Notes |
|---|---|---|---|---|
| `q` | yes | string | — | The user query. Empty → 200 with `results: []`. |
| `lat` | no | float | `LEMON_DEFAULT_LAT` (Downtown Miami) | User latitude |
| `lng` | no | float | `LEMON_DEFAULT_LNG` (Downtown Miami) | User longitude |
| `now` | no | RFC3339 timestamp | wall-clock | Override "current time" for reproducible bench runs |
| `limit` | no | int | 15 | Max results; capped at 50 |

**Validation**:

- `q`: trimmed; truncated at 200 chars.
- `lat`, `lng`: must parse as float; otherwise 400.
- `now`: must parse as RFC3339; otherwise 400.
- `limit`: must be int 1..50; otherwise 400.

**Success response** (200):

```json
{
  "query": "joes barbr near me",
  "results": [
    {
      "id":            "39a5ece2-c293-4664-98a7-90156b3d3999",
      "name":          "Joe's Barber Shop",
      "category":      "Beauty",
      "subcategory":   "Barbershop",
      "archetype":     "medium_stakes_occasion",
      "neighborhood":  "Brickell",
      "distance_km":   1.24,
      "rating":        4.7,
      "review_count":  812,
      "price_range":   "$$",
      "photo_url":     "https://classpass-res.cloudinary.com/.../foo.jpg",
      "is_claimed":    true,
      "is_new":        false,
      "is_open_now":   true,
      "score":         0.8643
    }
  ],
  "timings": {
    "intent_ms":  0,
    "sql_ms":    18,
    "rerank_ms":  3,
    "total_ms":  27
  }
}
```

Notes:

- All keys are `snake_case` (enforced by `tagliatelle`).
- `results` is always an array, even when empty. Never `null`.
- `timings.total_ms` includes `intent_ms + sql_ms + rerank_ms` + small
  bookkeeping; bench assertion is `total_ms ≥ sum of stages`.
- `score = +Inf` is serialized as a large finite number (`1e308`) when an
  exact-name hard-pin fires. Clients should treat any score ≥ 1e6 as
  "pinned" and not compare with non-pinned scores.

**Error responses**:

| Code | Body shape | When |
|---|---|---|
| 400 | `{"error": "invalid_param", "field": "lat"}` | Param validation fails |
| 408 | `{"error": "timeout"}` | API request budget exceeded (5s) |
| 503 | `{"error": "database_unavailable"}` | Postgres unreachable |
| 500 | `{"error": "internal"}` | Unexpected — logged with trace id |

We do **not** include stack traces in responses. The internal trace id
is logged server-side and can be requested via a future debug endpoint.

**Examples**:

```bash
# Plain query
curl 'http://localhost:8080/search?q=sushi'

# With explicit location + fixed time (for bench)
curl 'http://localhost:8080/search?q=cheap+restaurants&lat=25.7741&lng=-80.1937&now=2026-05-27T13:00:00-04:00'

# Empty query → empty results, not 4xx
curl 'http://localhost:8080/search?q='
# {"query":"","results":[],"timings":{...}}
```

### `GET /healthz`

Cheap liveness probe; used by Fly health checks.

```json
{ "status": "ok" }
```

Returns 200 always when the process is responsive. Does not check DB
connectivity (use `/readyz` for that — see below).

### `GET /readyz`

Readiness probe — confirms DB connectivity.

- 200 + `{"status": "ok"}` when a `SELECT 1` succeeds within 100ms.
- 503 + `{"status": "db_unavailable"}` otherwise.

Fly is configured to use `/readyz` for the "ready to accept traffic" gate.

### `GET /version`

```json
{
  "version":   "git-sha-or-tag",
  "commit":    "fb4c8a3",
  "built_at":  "2026-05-30T18:42:11Z",
  "go":        "go1.23.4"
}
```

Set at build time via `-ldflags '-X main.version=$(git rev-parse --short HEAD)'`.

## Stability + versioning

- The response shape under §`GET /search` (C4 contract) is locked for V1.
- Adding fields is non-breaking.
- Renaming or removing a field is a breaking change; we'd reissue from
  `/v2/search` and keep `/search` working until clients migrate. (Not
  expected during the trial.)

## Rate limits + timeouts

- No client-side rate limit in V1.
- Server-side request timeout: 5 seconds (returns 408).
- Postgres statement timeout: 1 second per query (configured at pool init).
- Fly's edge caps individual response size at 16 MB — we never approach that.

## Observability

Every `/search` request emits a structured log line:

```
level=info msg=search q="sushi" lat=25.774 lng=-80.194 results=15
  intent_ms=0 sql_ms=18 rerank_ms=3 total_ms=27
  trace_id=...
```

See [operations/observability.md](operations/observability.md).

## CORS

The API responds to `OPTIONS` preflight with:

```
Access-Control-Allow-Origin:   <Vercel FE origin>
Access-Control-Allow-Methods:  GET, OPTIONS
Access-Control-Allow-Headers:  Content-Type
Access-Control-Max-Age:        86400
```

Origin is read from `LEMON_CORS_ORIGIN` env var. Set to `*` only in dev.

## Cross-references

- Response struct in TypeScript: `web/lib/api.ts` (`SearchResponse`)
- Response struct in Go: `api/internal/api/dto.go` (lands Stage 2)
- Contract test: `api/internal/api/contract_test.go` (lands Stage 2)
- Ranking semantics: [ranking/semantics.md](ranking/semantics.md)
- Intent extractor: [ranking/intent.md](ranking/intent.md)
- C4 contract: [roadmap/05-architectural-contracts.md](roadmap/05-architectural-contracts.md)
