# Deployment runbook

Three deploy targets. Each owns one piece of the system (ADR-0007).

| Target | Hosts | Region |
|---|---|---|
| Supabase | Postgres 15 (the DB) | `us-east-1` |
| AWS EC2 | Go API binary (`cmd/api`) + in-process ORT embedder | `us-east-1` |
| Vercel | Next.js FE (`web/`) | edge (global) |

EC2 (`c7i.xlarge`) + Supabase both in `us-east-1` keep the Go ↔ DB hop ≤ 10ms.
The API host moved Fly.io → EC2 for SSH/profiling of the CGo embedder and to use
the box's vCPUs via the embed pool — see [ADR-0007](../adr/0007-api-host-ec2.md)
for the rationale + capacity model.

The runnable scripts live in [`../../deploy/`](../../deploy/); this is the
narrative. Order: **Supabase → EC2 → Vercel.**

## Supabase

1. Create a **Pro** project at <https://supabase.com/dashboard/projects>, region
   `us-east-1`. Pro (not Free): Free auto-pauses after ~7 days — a paused DB when
   a grader opens the link is the worst failure mode. Save the DB password.
   Sizing: **Small** compute is plenty for the demo; co-scale to 2XL–4XL only for
   the ceiling load test, then back down ([bench plan](../bench/plan.md)).
2. From **Settings → Database**, copy the **direct** connection string (port
   5432, not the pooler). This is `LEMON_DATABASE_URL`.
3. Apply migrations (idempotent):
   ```bash
   LEMON_DATABASE_URL='<direct url>' deploy/supabase/apply-migrations.sh
   # expect extensions: cube earthdistance pg_trgm vector
   ```
4. Seed the ~23k businesses + embeddings. Fast path — copy from the already-
   seeded local dev DB (no re-embedding; vectors transfer as-is):
   ```bash
   SOURCE_DATABASE_URL='postgresql://postgres:postgres@127.0.0.1:54322/postgres' \
   LEMON_DATABASE_URL='<supabase direct url>' \
     deploy/supabase/seed.sh
   ```
   No local DB? Seed fresh from JSON instead (needs Ollama for the embed pass,
   which `cmd/ingest` uses — its vectors match the ONNX query path per the
   parity test):
   ```bash
   LEMON_DATABASE_URL='<supabase>' go run ./cmd/ingest -input businesses-<date>.json
   LEMON_DATABASE_URL='<supabase>' go run ./cmd/ingest -embed
   ```
5. Set the grader role password (the migration creates the role without one) and
   share `(lemon_grader, password)` over a secure channel — this is the spec's
   "read access shared" deliverable:
   ```sql
   alter role lemon_grader with password '<a-strong-password>';
   ```

## EC2

1. Launch a **`c7i.xlarge`** (4 vCPU / 8 GB), **Ubuntu 24.04 LTS x86-64**,
   `us-east-1`. Security group: inbound 22 (SSH, your IP) + 8080 (or 443 if you
   add TLS — see below). Attach a key pair.
2. SSH in and provision — installs Go, the two native libs (`libonnxruntime`
   dlopen'd + `libtokenizers.a` static-linked), the embedding model, builds the
   API with `-tags ORT`, and installs the systemd service:
   ```bash
   git clone https://github.com/danielreales00/lemon-search.git
   sudo REPO_REF=main bash lemon-search/deploy/ec2/setup.sh
   ```
   It ends with an ORT embed smoke test that validates the native-lib pairing
   (an onnxruntime/`onnxruntime_go` mismatch fails loudly here, not at runtime).
3. Fill the runtime env, then start:
   ```bash
   sudoedit /etc/lemon/lemon-api.env   # LEMON_DATABASE_URL + LEMON_CORS_ALLOW_ORIGIN
   sudo systemctl start lemon-api
   curl localhost:8080/readyz && curl 'localhost:8080/search?q=sushi'
   ```
   Key env (full template: `deploy/ec2/lemon-api.env.example`):
   `LEMON_DATABASE_URL`, `LEMON_CORS_ALLOW_ORIGIN=https://<vercel-url>`,
   `LEMON_FF_SEMANTIC=true`, `LEMON_EMBED_BACKEND=onnx`, `LEMON_EMBED_POOL_SIZE=4`,
   `LEMON_ONNX_MODEL_PATH`, `LEMON_ONNX_RUNTIME_DIR=/usr/lib`.

**Native-lib recipe** (what `setup.sh` automates, pinned to `api/go.mod`): the
in-process ORT embedder is CGo + glibc — `libtokenizers.a` (daulet/tokenizers
`v1.27.0`) static-linked with `-tags ORT CGO_ENABLED=1`, `libonnxruntime.so`
(`v1.26.0` — must expose an ORT API version ≥ what `onnxruntime_go` requests;
parameterizable) dlopen'd from `LEMON_ONNX_RUNTIME_DIR`, and the
bundled `all-MiniLM-L6-v2` model (`model.onnx` + `tokenizer.json`, ~86MB) at
`LEMON_ONNX_MODEL_PATH`.

**TLS / public HTTPS.** The service serves plain HTTP on :8080. For a public URL,
either front it with Caddy (`caddy reverse-proxy --to :8080`, auto-TLS) or an
ALB, or point the web app straight at `http://<ec2-host>:8080` for a quick demo.

## Vercel

1. From <https://vercel.com/new>, import the repo:
   - **Root directory**: `web`
   - **Framework**: Next.js (auto-detected)
   - **Env**: `NEXT_PUBLIC_API_BASE_URL = https://<api-host>` (or `http://<ec2-ip>:8080`)
2. Production branch `main`; first deploy is automatic on push.
3. After it deploys, set `LEMON_CORS_ALLOW_ORIGIN` on the box to the Vercel
   origin and `sudo systemctl restart lemon-api`.

## GitHub Actions secrets

| Secret | Used by | Value |
|---|---|---|
| `EC2_SSH_HOST` | `deploy-api.yml` | the box's public DNS/IP |
| `EC2_SSH_USER` | `deploy-api.yml` | `ubuntu` (default) |
| `EC2_SSH_KEY` | `deploy-api.yml` | a deploy private key authorized on the box |
| `LEMON_DATABASE_URL` | `ci.yml` (optional) | the Supabase direct URL |

## Deploy flow

Push to `main`:

1. CI (`ci.yml`) runs lint/test/build/migrations/secrets.
2. `deploy-api.yml` SSHes to the box and runs `deploy/ec2/deploy.sh` (git pull →
   build `-tags ORT` → `systemctl restart`). Path-filtered so docs-only changes
   don't redeploy. **Skips safely** until the `EC2_SSH_*` secrets exist.
3. Vercel auto-deploys `web/` on the same push (its own pipeline).

Post-deploy checks:

- `curl http://<host>:8080/healthz` · `/readyz` · `/version` (SHA matches)
- `curl 'http://<host>:8080/search?q=sushi'` — sanity
- Open the Vercel URL, type a query, verify results render

## Rollback

**API**: redeploy a known-good SHA — `ssh <box> 'sudo REPO_REF=<sha> bash
/opt/lemon/lemon-search/deploy/ec2/deploy.sh'`. The build is fast; systemd
restarts in place. (`Restart=always` also recovers a crash automatically.)

**FE (Vercel)**: dashboard → Deployments → pick a green one → **Promote to
Production**.

**Database**: migrations are forward-only — to "undo" a column, write a new
migration that drops it; never edit a merged one. Data restore: Supabase keeps
daily backups (dashboard → Database → Backups).

## Emergency stop

- **API**: `sudo systemctl stop lemon-api` (new requests get connection-refused;
  put it behind the proxy if you want a clean 503). Re-enable: `systemctl start`.
- **Vercel**: pause the project in the dashboard, or remove the production domain.
- **Supabase**: pause the project (Settings → General) — the DB stops accepting
  connections.

## Common operational questions

**API is slow — what do I check?**

1. `journalctl -u lemon-api -f` — look for slow `sql_ms` in the request logs.
2. `systemctl status lemon-api` — confirm it's `active (running)`, not flapping.
3. EC2 per-core CPU (`htop`) — is the embed pool saturating all 4 vCPUs?
4. Supabase dashboard → Database → Query Performance — top queries by time.
5. The [load-bench](../bench/plan.md) harness attributes a degrading p95 to the
   API box vs the DB; [observability.md](observability.md) explains the timings.

**How do I share schema access with a grader?**

1. Confirm `lemon_grader` exists with a password set (Supabase step 5).
2. Share `(lemon_grader, password)` + the project ref/connection string via a
   password manager link or secure note.

## Cross-references

- Deploy scripts: [`../../deploy/`](../../deploy/)
- Capacity model + host rationale: [../adr/0007-api-host-ec2.md](../adr/0007-api-host-ec2.md)
- Load-bench plan: [../bench/plan.md](../bench/plan.md)
- Schema reference: [../data/schema.md](../data/schema.md)
- Observability: [observability.md](observability.md)
