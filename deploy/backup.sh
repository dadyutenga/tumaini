#!/usr/bin/env bash
# Nightly full database backup via direct PostgreSQL connection (FR-SEC-07).
# Must connect with a BYPASSRLS / superuser role (e.g. postgres) because FORCE ROW LEVEL SECURITY
# is enabled on tenant tables.
#   BACKUP_DATABASE_URL=postgres://postgres:...@127.0.0.1:5433/bms BACKUP_ENCRYPTION_KEY=... ./backup.sh
# Keeps BACKUP_KEEP_DAYS (default 30) days locally. Set BACKUP_UPLOAD_CMD to copy each
# dump off the server; it receives the file path as $1.
set -euo pipefail
: "${BACKUP_DATABASE_URL:?set BACKUP_DATABASE_URL to a postgres/BYPASSRLS connection string}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
REQUIRE_ENC="${BACKUP_REQUIRE_ENCRYPTION:-0}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BASE="$DIR/bms-$STAMP"

notify_failure() {
  local rc=$?
  rm -f "$BASE.dump.partial" "$BASE.dump.enc.partial"
  echo "ERROR: backup failed with exit code $rc at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  if [ -n "${BACKUP_ALERT_WEBHOOK_URL:-}" ]; then
    curl -fsS -m 10 -X POST -H "Content-Type: application/json" \
      -d "{\"event\":\"backup_failed\",\"host\":\"$(hostname -f 2>/dev/null || hostname)\",\"exit_code\":$rc,\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
      "$BACKUP_ALERT_WEBHOOK_URL" >/dev/null || true
  fi
  exit "$rc"
}
trap notify_failure ERR

mkdir -p "$DIR"
chmod 700 "$DIR"

if [ "$REQUIRE_ENC" = "1" ] && [ -z "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  echo "BACKUP_ENCRYPTION_KEY is required when BACKUP_REQUIRE_ENCRYPTION=1" >&2
  exit 2
fi

raw_file="$BASE.dump"
pg_dump --format=custom --no-owner --file="$raw_file.partial" "$BACKUP_DATABASE_URL"
mv "$raw_file.partial" "$raw_file"
chmod 600 "$raw_file"
pg_restore --list "$raw_file" >/dev/null

final_file="$raw_file"
if [ -n "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  enc_file="$raw_file.enc"
  openssl enc -aes-256-cbc -pbkdf2 -iter 600000 -salt \
    -in "$raw_file" -out "$enc_file.partial" -pass env:BACKUP_ENCRYPTION_KEY
  mv "$enc_file.partial" "$enc_file"
  chmod 600 "$enc_file"
  rm -f "$raw_file"
  final_file="$enc_file"
fi

sha256sum "$final_file" > "$final_file.sha256"
chmod 600 "$final_file.sha256"

if [ -n "${BACKUP_UPLOAD_CMD:-}" ]; then
  bash -c "$BACKUP_UPLOAD_CMD" upload "$final_file"
  bash -c "$BACKUP_UPLOAD_CMD" upload "$final_file.sha256"
fi

find "$DIR" \( -name 'bms-*.dump' -o -name 'bms-*.dump.enc' -o -name 'bms-*.sha256' \) -mtime +"$KEEP" -delete
echo "backup written: $final_file"
