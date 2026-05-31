# Deploy kit

Scripts to stand up Lemon Search in production. Three targets, one piece each
(ADR-0007): **Supabase** (`us-east-1`) for Postgres, **AWS EC2** (`c7i.xlarge`,
`us-east-1`) for the Go API + in-process ORT embedder, **Vercel** for the web
app. EC2 + Supabase share `us-east-1` for a ≤10ms API↔DB hop.

Full narrative runbook: [`../docs/operations/deployment.md`](../docs/operations/deployment.md).

## Order

1. **Supabase** — create a Pro project in `us-east-1`, then:
   ```bash
   LEMON_DATABASE_URL='<direct url, :5432>' deploy/supabase/apply-migrations.sh
   SOURCE_DATABASE_URL='<local dev db>' LEMON_DATABASE_URL='<supabase>' deploy/supabase/seed.sh
   ```
   `apply-migrations.sh` is idempotent; `seed.sh` copies the ~23k businesses +
   embeddings from the already-seeded local DB (no re-embedding).

2. **EC2** — launch a `c7i.xlarge` (Ubuntu 24.04, x86-64), then on the box:
   ```bash
   sudo REPO_REF=main bash deploy/ec2/setup.sh   # Go + native libs + model + build + service
   sudoedit /etc/lemon/lemon-api.env             # LEMON_DATABASE_URL + CORS origin
   sudo systemctl start lemon-api
   ```
   `setup.sh` ends with an ORT embed smoke test that validates the native-lib
   pairing. Redeploys later: `deploy/ec2/deploy.sh` (pull + build + restart).

3. **Vercel** — import the repo, root dir `web`, set
   `NEXT_PUBLIC_API_BASE_URL=http://<ec2-host>:8080` (or the API's HTTPS URL).

## Files

| Path | What |
|---|---|
| `ec2/setup.sh` | one-time box provisioning (Go, libonnxruntime, libtokenizers, model, build, systemd) |
| `ec2/deploy.sh` | redeploy on a provisioned box (pull, build `-tags ORT`, restart) |
| `ec2/lemon-api.service` | systemd unit |
| `ec2/lemon-api.env.example` | runtime env template |
| `supabase/apply-migrations.sh` | apply `supabase/migrations/*` (idempotent) |
| `supabase/seed.sh` | copy businesses + embeddings from local → cloud |

## Notes

- **Versions** in `setup.sh` (Go, tokenizers, onnxruntime) are pinned to match
  `api/go.mod`; the smoke test catches an onnxruntime mismatch on first run.
- **TLS**: the unit serves plain HTTP on :8080. For a public HTTPS URL, front it
  with a reverse proxy (Caddy/nginx) or an ALB — see the runbook.
- **GitHub Actions** (`.github/workflows/deploy-api.yml`) can auto-run
  `deploy.sh` over SSH on push to `main` once the `EC2_SSH_*` secrets are set;
  until then it safely skips so `main` stays green.
