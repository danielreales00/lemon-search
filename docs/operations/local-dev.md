# Local development

The whole stack runs locally with **no cloud provisioning**. The database is
Supabase's own Postgres 15 image, run in Docker by the Supabase CLI — identical
extensions and version to the hosted project. A cloud Supabase project is only
needed at the very end, to give graders read access (see
[deployment.md](deployment.md)).

## Prerequisites

- **Docker** running (Docker Desktop or equivalent).
- **Supabase CLI**: `make db-install` (Homebrew) — one-time.

## The loop

```bash
make dev-env        # one-time: create .env.local from .env.example
make db-up          # start local Postgres on :54322 (first run pulls images)
make db-reset       # apply supabase/migrations/*.sql (extensions, tables, indexes, role)
make db-ingest      # load businesses-*.json into the local DB  (needs cmd/ingest — issue #20)
make db-psql        # psql shell for poking around
make db-down        # stop the stack when done
```

`make db-status` prints connection details. The default local connection string —
used by `.env.local` (`LEMON_DATABASE_URL`) and the Go services — is:

```
postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable
```

## Why local-first

- **Fast inner loop**: a schema change is one `make db-reset` away; no network hop.
- **Parity**: same Postgres image + extensions (`pg_trgm`, `cube`, `earthdistance`)
  as the hosted project, so "works locally" means "works on Supabase."
- **CI mirrors it**: the `go-test` and `migrations-check` jobs run `postgres:15`
  and apply the same migrations.

## Going to the cloud (end-step, for the graded share)

When it's time to share with graders:

```bash
supabase link --project-ref <ref>   # link to the hosted project
supabase db push                    # apply supabase/migrations/* to the cloud DB
make db-ingest                      # with LEMON_DATABASE_URL pointed at the cloud DB
```

Then grant the read-only `lemon_grader` role / share read access. See
[deployment.md](deployment.md) for the full runbook.
