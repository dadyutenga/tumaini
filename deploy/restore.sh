#!/usr/bin/env bash
# Runs ON THE SERVER, from /opt/tumaini: puts a backup into service. Use restore-verify.sh for drills.
#   ./restore.sh <db backup> [files backup] --confirm
# The current database is kept, renamed to bms_before_restore_<time>; nothing is dropped. Uploaded
# files are extracted over the volume, replacing files of the same name and removing none.
# Leaves api and web stopped: start them with ./deploy.sh <tag>.
set -euo pipefail
usage() { echo "usage: restore.sh <backup.dump[.enc]> [backup-files.tar[.enc]] --confirm" >&2; exit 2; }
CONFIRM=0; ARGS=()
for a in "$@"; do
  if [ "$a" = --confirm ]; then CONFIRM=1; else ARGS+=("$(readlink -f "$a")"); fi
done
[ "$CONFIRM" = 1 ] && [ "${#ARGS[@]}" -ge 1 ] && [ "${#ARGS[@]}" -le 2 ] || usage
DB_BACKUP="${ARGS[0]}"; FILES_BACKUP="${ARGS[1]:-}"

cd "$(dirname "$(readlink -f "$0")")"
[ -f .env ] || { echo ".env not found: restore the saved production .env first" >&2; exit 1; }
set -a; . ./.env; set +a
export TAG="${TAG:-restore}"
. ./backup-lib.sh
STARTED=$SECONDS
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-docker-compose.prod.yml}"
FILES_VOLUME="${BACKUP_FILES_VOLUME:-tumaini_files}"
STAMP="$(date -u +%Y%m%d%H%M%S)"
NEW_DB="bms_restore_$STAMP"
KEPT_DB="bms_before_restore_$STAMP"
DC=(docker compose -f "$COMPOSE_FILE")

psql_in() { "${DC[@]}" exec -T postgres psql -U postgres -v ON_ERROR_STOP=1 -q "$@"; }

bk_check "$DB_BACKUP"
if [ -n "$FILES_BACKUP" ]; then bk_check "$FILES_BACKUP"; bk_plain "$FILES_BACKUP" | tar -tf - >/dev/null; fi

echo "==> start postgres"
"${DC[@]}" up -d postgres
for _ in $(seq 1 30); do
  "${DC[@]}" exec -T postgres pg_isready -U postgres -q && break
  sleep 2
done

echo "==> verify the backup before touching anything"
./restore-verify.sh "$DB_BACKUP"

echo "==> stop api and web"
"${DC[@]}" stop api web

echo "==> restore into $NEW_DB"
psql_in -d postgres < roles.sql
psql_in -d postgres -c "CREATE DATABASE $NEW_DB"
# Ownership is restored as dumped: row-level security and the SECURITY DEFINER functions depend on it.
bk_plain "$DB_BACKUP" | "${DC[@]}" exec -T postgres pg_restore -U postgres -d "$NEW_DB" --exit-on-error

echo "==> swap databases (previous one kept as $KEPT_DB)"
if [ "$("${DC[@]}" exec -T postgres psql -U postgres -d postgres -At -c "SELECT 1 FROM pg_database WHERE datname = 'bms'")" = 1 ]; then
  psql_in -d postgres -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'bms'" >/dev/null
  psql_in -d postgres --single-transaction -c "ALTER DATABASE bms RENAME TO $KEPT_DB" -c "ALTER DATABASE $NEW_DB RENAME TO bms"
else
  psql_in -d postgres -c "ALTER DATABASE $NEW_DB RENAME TO bms"
fi

echo "==> set login role passwords"
"${DC[@]}" exec -T -e API_PW="$BMS_API_DB_PASSWORD" -e JOBS_PW="$BMS_JOBS_DB_PASSWORD" postgres \
  psql -U postgres -d bms -v ON_ERROR_STOP=1 -q <<'SQL'
\getenv api_pw API_PW
\getenv jobs_pw JOBS_PW
ALTER ROLE bms_api LOGIN PASSWORD :'api_pw';
ALTER ROLE bms_jobs LOGIN PASSWORD :'jobs_pw';
SQL

if [ -n "$FILES_BACKUP" ]; then
  echo "==> restore uploaded files into $FILES_VOLUME"
  bk_plain "$FILES_BACKUP" | docker run --rm -i --network none -v "$FILES_VOLUME:/app/data" postgres:16-alpine tar -C /app -xf -
else
  echo "WARNING: no files backup given; uploaded documents were not restored" >&2
fi
echo "==> restore complete in $((SECONDS - STARTED))s. Start the application with: ./deploy.sh <tag>"
