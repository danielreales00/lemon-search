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

2. **EC2** — launch the box (needs the `aws` CLI authenticated), then provision it:
   ```bash
   LAUNCH_CONFIRM=yes deploy/ec2/launch.sh        # key pair + SG + c7i.xlarge; prints the ssh cmd
   ssh -i ~/.ssh/lemon-api.pem ubuntu@<host> \
     'git clone https://github.com/danielreales00/lemon-search.git && sudo REPO_REF=main bash lemon-search/deploy/ec2/setup.sh'
   ssh ... 'sudoedit /etc/lemon/lemon-api.env'    # LEMON_DATABASE_URL + CORS origin
   ssh ... 'sudo systemctl start lemon-api'
   ```
   `launch.sh` is a dry-run without `LAUNCH_CONFIRM=yes`. `setup.sh` ends with an
   ORT embed smoke test that validates the native-lib pairing. Redeploys later:
   `deploy/ec2/deploy.sh` (pull + build + restart). Done with the box?
   `INSTANCE_ID=<id> deploy/ec2/teardown.sh` stops the meter.

3. **HTTPS** (needed before Vercel — an HTTPS page can't call `http://`): point a
   DNS A record at the box (an Elastic IP keeps it stable), open 80+443, then:
   ```bash
   ssh ... 'sudo DOMAIN=api.example.com bash /opt/lemon/lemon-search/deploy/ec2/tls-setup.sh'
   ```
   Caddy fetches + auto-renews a Let's Encrypt cert; the API serves at
   `https://api.example.com`.

4. **Vercel** — import the repo, root dir `web`, set
   `NEXT_PUBLIC_API_BASE_URL=https://api.example.com`.

## Files

| Path | What |
|---|---|
| `ec2/launch.sh` | provision the EC2 instance (key pair, security group, `c7i.xlarge`); dry-run unless `LAUNCH_CONFIRM=yes` |
| `ec2/teardown.sh` | terminate the instance (+ optional SG/key cleanup) |
| `ec2/setup.sh` | one-time box provisioning (Go, libonnxruntime, libtokenizers, model, build, systemd) |
| `ec2/tls-setup.sh` | front the API with Caddy + auto-TLS (Let's Encrypt) on a domain pointing at the box |
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
