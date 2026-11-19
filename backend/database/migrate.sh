#!/usr/bin/env bash
# Apply BMS migrations in order. 000 runs as the connecting superuser;
# every later file runs as bms_owner inside a single transaction.
# Usage: PGHOST=... PGPORT=... PGUSER=postgres ./migrate.sh <dbname>
set -euo pipefail
DB="${1:?database name}"
DIR="$(cd "$(dirname "$0")/migrations" && pwd)"

psql -X -q -v ON_ERROR_STOP=1 -d "$DB" -c "CREATE TABLE IF NOT EXISTS public.schema_migrations (version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"

for f in "$DIR"/*.sql; do
  v="$(basename "$f" .sql)"
  sum="$(sha256sum "$f" | cut -d' ' -f1)"
  applied="$(psql -X -At -d "$DB" -c "SELECT checksum FROM public.schema_migrations WHERE version='$v'")"
  if [[ -n "$applied" ]]; then
    [[ "$applied" == "$sum" ]] || { echo "checksum mismatch for applied migration $v" >&2; exit 1; }
    continue
  fi
  echo "applying $v"
  if [[ "$v" == 000_* ]]; then
    psql -X -q -v ON_ERROR_STOP=1 -d "$DB" -f "$f"
    psql -X -q -v ON_ERROR_STOP=1 -d "$DB" -c "INSERT INTO public.schema_migrations VALUES ('$v','$sum')"
  else
    { echo "BEGIN; SET LOCAL ROLE bms_owner;"; cat "$f"; echo "RESET ROLE; INSERT INTO public.schema_migrations VALUES ('$v','$sum'); COMMIT;"; } \
      | psql -X -q -v ON_ERROR_STOP=1 -d "$DB"
  fi
done
echo "migrations complete"
