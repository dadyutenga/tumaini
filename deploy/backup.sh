#!/usr/bin/env bash
# Nightly full database backup via direct PostgreSQL connection (FR-SEC-07).
# Must connect with a BYPASSRLS / superuser role (e.g. postgres) because FORCE ROW LEVEL SECURITY
# is enabled on tenant tables.
#   BACKUP_DATABASE_URL=postgres://postgres:...@127.0.0.1:5433/bms BACKUP_ENCRYPTION_KEY=... ./backup.sh
# Database only; production uses backup-prod.sh, which also saves uploaded files.
# Keeps BACKUP_KEEP_DAYS (default 30) days locally. Set BACKUP_UPLOAD_CMD to copy each
# dump off the server; it receives the file path as $1.
set -euo pipefail
: "${BACKUP_DATABASE_URL:?set BACKUP_DATABASE_URL to a postgres/BYPASSRLS connection string}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
REQUIRE_ENC="${BACKUP_REQUIRE_ENCRYPTION:-0}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BASE="$DIR/bms-$STAMP"

umask 077

# Runs on every exit, so refusals and signals alert as well as failed commands.
on_exit() {
  local rc=$?
  rm -f "$BASE".dump*.partial
  [ "$rc" = 0 ] && return
  echo "ERROR: backup failed with exit code $rc at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  if [ -n "${BACKUP_ALERT_WEBHOOK_URL:-}" ]; then
    curl -fsS -m 10 -X POST -H "Content-Type: application/json" \
      -d "{\"event\":\"backup_failed\",\"host\":\"$(hostname -f 2>/dev/null || hostname)\",\"exit_code\":$rc,\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
      "$BACKUP_ALERT_WEBHOOK_URL" >/dev/null || true
  fi
}
trap on_exit EXIT

mkdir -p "$DIR"
chmod 700 "$DIR"

if [ "$REQUIRE_ENC" = "1" ] && [ -z "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  echo "BACKUP_ENCRYPTION_KEY is required when BACKUP_REQUIRE_ENCRYPTION=1" >&2
  exit 2
fi

# Encrypt while streaming, so no plaintext copy touches the disk.
if [ -n "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  final_file="$BASE.dump.enc"
  pg_dump --format=custom "$BACKUP_DATABASE_URL" \
    | openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt -out "$final_file.partial" -pass env:BACKUP_ENCRYPTION_KEY
  openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -in "$final_file.partial" -pass env:BACKUP_ENCRYPTION_KEY \
    | { pg_restore --list >/dev/null && cat >/dev/null; }
else
  echo "WARNING: BACKUP_ENCRYPTION_KEY is not set; this backup is NOT encrypted" >&2
  final_file="$BASE.dump"
  pg_dump --format=custom --file="$final_file.partial" "$BACKUP_DATABASE_URL"
  pg_restore --list "$final_file.partial" >/dev/null
fi
mv "$final_file.partial" "$final_file"

(cd "$DIR" && sha256sum "$(basename "$final_file")" > "$(basename "$final_file").sha256")

if [ -n "${BACKUP_UPLOAD_CMD:-}" ]; then
  bash -c "$BACKUP_UPLOAD_CMD" upload "$final_file"
  bash -c "$BACKUP_UPLOAD_CMD" upload "$final_file.sha256"
fi

# restore-verify.sh requires this for encrypted backups.
if [ -n "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  . "$(dirname "$(readlink -f "$0")")/backup-lib.sh"
  bk_mac "$final_file" > "$final_file.mac"
  if [ -n "${BACKUP_UPLOAD_CMD:-}" ]; then bash -c "$BACKUP_UPLOAD_CMD" upload "$final_file.mac"; fi
fi

find "$DIR" \( -name 'bms-*.mac' -o -name 'bms-*.dump' -o -name 'bms-*.dump.enc' -o -name 'bms-*.sha256' \) -mtime +"$KEEP" -delete
echo "backup written: $final_file"
