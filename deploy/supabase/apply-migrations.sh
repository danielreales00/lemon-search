#!/usr/bin/env bash
# Apply all forward-only migrations to a target Postgres (Supabase cloud or
# local). Idempotent: every migration is `create ... if not exists`, so re-runs
# are safe (CI applies them twice to prove it).
#
#   LEMON_DATABASE_URL='postgresql://postgres:...@db.<ref>.supabase.co:5432/postgres' \
#     deploy/supabase/apply-migrations.sh
set -euo pipefail

URL="${LEMON_DATABASE_URL:?set LEMON_DATABASE_URL (Supabase direct connection, port 5432)}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MIG_DIR="$ROOT/supabase/migrations"

command -v psql >/dev/null || { echo "psql is required (apt-get install postgresql-client)" >&2; exit 1; }

for f in "$MIG_DIR"/*.sql; do
  echo "==> $(basename "$f")"
  psql "$URL" -v ON_ERROR_STOP=1 -q -f "$f"
done

echo "==> extensions"
psql "$URL" -tAc "select extname from pg_extension order by 1;" | tr '\n' ' '; echo
echo "==> done. Expect: cube earthdistance pg_trgm vector (+ plpgsql)"
