# Deployment runbook

Three deploy targets. Each owns one piece of the system.

| Target | Hosts | Region |
|---|---|---|
| Supabase | Postgres 15 (the DB) | `us-east-1` |
| Fly.io | Go API binary (`cmd/api`) | `iad` |
| Vercel | Next.js FE (`web/`) | edge (global) |

Same-region pairing of Fly `iad` + Supabase `us-east-1` keeps the Go ↔ DB
hop ≤ 10ms.

## First-time setup

### Supabase

1. Create a project at <https://supabase.com/dashboard/projects>. Region
   `us-east-1` (close-to-`iad`). Save the DB password to a vault.
2. From **Settings → Database**, copy the **direct** connection string
   (port 5432, not the pooler). Add to local `.env.local` as
   `LEMON_DATABASE_URL` and to GitHub Actions Secrets.
3. Apply migrations:
   ```bash
   for f in supabase/migrations/*.sql; do
     psql "$LEMON_DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"
   done
   ```
4. Create the `lemon_grader` password (the migration creates the role
   without a password):
   ```sql
   ALTER ROLE lemon_grader WITH PASSWORD '<a-strong-password>';
   ```
   Share the (user, password) pair with the grader via secure channel.
5. Confirm extensions present:
   ```sql
   \dx
   -- expect: pg_trgm, cube, earthdistance
   ```

### Fly.io

1. `brew install flyctl` and `flyctl auth login`.
2. From repo root: `flyctl launch --no-deploy --region iad --copy-config`
   (interactively pick the app name `lemon-search-api`). This writes
   `fly.toml`.
3. Set secrets:
   ```bash
   flyctl secrets set \
     LEMON_DATABASE_URL='<direct supabase url>' \
     LEMON_CORS_ORIGIN='https://<your-vercel-url>' \
     LEMON_DEFAULT_LAT='25.7741728' \
     LEMON_DEFAULT_LNG='-80.19362' \
     LEMON_RANKING_CONFIG='/app/config/ranking.yaml'
   ```
4. Generate a deploy token and add as `FLY_API_TOKEN` in GitHub Actions
   Secrets:
   ```bash
   flyctl tokens create deploy
   ```
5. First deploy:
   ```bash
   flyctl deploy
   ```
   Verify: `curl https://lemon-search-api.fly.dev/healthz` → `{"status":"ok"}`.

### Vercel

1. From <https://vercel.com/new>, import the GitHub repo. Set:
   - **Root directory**: `web`
   - **Framework**: Next.js (auto-detected)
   - **Environment variables**:
     - `NEXT_PUBLIC_API_BASE_URL = https://lemon-search-api.fly.dev`
2. Add a deploy branch (typically `main`). Branch protection on GitHub
   handles which commits land there.
3. First deploy is automatic on the first push.

### GitHub Actions secrets

| Secret | Used by | Value |
|---|---|---|
| `FLY_API_TOKEN` | `deploy-api.yml` | from `flyctl tokens create deploy` |
| `LEMON_DATABASE_URL` | `ci.yml` migrations job (optional) | the Supabase direct URL |

## fly.toml (reference)

```toml
app = "lemon-search-api"
primary_region = "iad"

[build]
  dockerfile = "Dockerfile"

[env]
  LEMON_API_PORT = "8080"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 1
  processes = ["app"]

  [[http_service.checks]]
    grace_period = "10s"
    interval = "30s"
    method = "GET"
    timeout = "5s"
    path = "/readyz"

[[vm]]
  cpu_kind = "shared"
  cpus = 2
  memory_mb = 512
```

## Dockerfile (reference)

```dockerfile
# api/Dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/api /app/api
COPY config/ /app/config/
USER nobody
EXPOSE 8080
ENTRYPOINT ["/app/api"]
```

## Deploy flow

Push to `main` triggers:

1. CI (`.github/workflows/ci.yml`) runs everything (lint, test, build,
   migrations check, secrets scan, etc.).
2. After CI green: `.github/workflows/deploy-api.yml` runs `flyctl deploy`.
   Path-filtered so docs-only changes don't redeploy the API.
3. Vercel auto-deploys the `web/` build on the same push (its own pipeline,
   not via GitHub Actions).

End-of-deploy checks (run manually after the first deploy of any change):

- `curl https://lemon-search-api.fly.dev/healthz`
- `curl https://lemon-search-api.fly.dev/readyz`
- `curl https://lemon-search-api.fly.dev/version` — confirm SHA matches
- `curl 'https://lemon-search-api.fly.dev/search?q=sushi'` — sanity check
- Open the Vercel URL, type a query, verify results render

## Rollback

### API (Fly.io)

```bash
flyctl releases             # list recent releases
flyctl deploy --image registry.fly.io/lemon-search-api:<old-tag>
```

Or, equivalent via UI: <https://fly.io/apps/lemon-search-api/monitoring>
→ rollback button on the release row.

### FE (Vercel)

Promote a previous deployment via the Vercel dashboard
(`https://vercel.com/<org>/lemon-search-web/deployments`): pick a
green deployment, click **Promote to Production**.

### Database

Migrations are forward-only. To "rollback" a column, write a new
migration that drops it. **Do not** edit a merged migration.

To restore data: Supabase keeps daily backups (7-day retention on the
free tier). Restore via the Supabase dashboard → Database → Backups.

## Emergency stop

Fly: `flyctl scale count 0 --yes` (sets running count to 0; new requests
get 502 from Fly's edge). Re-enable with `flyctl scale count 1`.

Vercel: pause the project via the dashboard, or remove the production
domain.

Supabase: pause the project from the dashboard (Settings → General).
The DB stops accepting connections.

## Common operational questions

**Q: API is slow, what do I check?**

1. `flyctl logs` — look for slow `sql_ms`. If high: check `EXPLAIN ANALYZE`
   on the slow query.
2. `flyctl status` — confirm 1 machine running, not in `pending`.
3. Supabase dashboard → Database → Query Performance — top queries by
   time.
4. [observability.md](observability.md) for what the timings mean.

**Q: Bench pass rate dropped — how do I diagnose?**

1. `scripts/bench-runner --diff <yesterday's results>` — which queries
   regressed?
2. Hit the API directly for a regressed query; inspect the JSON timings
   and result IDs.
3. If the regression came from a config change: revert
   `config/ranking.yaml` and confirm.
4. If from a ranker code change: re-run the unit tests that cover the
   relevant ranker step.

**Q: How do I share schema access with a grader?**

1. Confirm the `lemon_grader` role exists and has a password set.
2. Share via password manager link (1Password / Bitwarden) or in-thread
   secure note. Include the project ref + connection string.

## Cross-references

- CI/CD configs: `.github/workflows/`
- Schema reference: [../data/schema.md](../data/schema.md)
- Observability: [observability.md](observability.md)
- Stack ADR: [../adr/0001-stack-choice.md](../adr/0001-stack-choice.md)
