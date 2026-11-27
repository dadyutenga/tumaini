#!/usr/bin/env bash
# Runs ON THE SERVER, from /opt/tumaini: encrypted full database dump of the production stack.
#   ./backup-prod.sh [label]     label defaults to "nightly"
# Environment (loaded from .env when present):
#   BACKUP_DIR                Local backup directory (default /var/backups/tumaini, mode 0700)
#   BACKUP_KEEP_DAYS          Local retention in days (default 30)
#   BACKUP_ENCRYPTION_KEY     Passphrase for AES-256-CBC + PBKDF2 encryption (recommended/required in prod)
#   BACKUP_REQUIRE_ENCRYPTION Set to 1 to refuse unencrypted dumps (default 0 for local dev compatibility)
#   BACKUP_UPLOAD_CMD         Offsite copy command receiving file path as $1
#   BACKUP_ALERT_WEBHOOK_URL  Optional HTTPS webhook POSTed on backup failure
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
if [ -f .env ]; then
  set -a; . ./.env; set +a
fi

LABEL="${1:-nightly}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
REQUIRE_ENC="${BACKUP_REQUIRE_ENCRYPTION:-0}"
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-docker-compose.prod.yml}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BASE="$DIR/bms-$LABEL-$STAMP"

notify_failure() {
  local rc=$?
  rm -f "$BASE.dump.partial" "$BASE.dump.enc.partial"
  echo "ERROR: backup ($LABEL) failed with exit code $rc at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  if [ -n "${BACKUP_ALERT_WEBHOOK_URL:-}" ]; then
    curl -fsS -m 10 -X POST -H "Content-Type: application/json" \
      -d "{\"event\":\"backup_failed\",\"label\":\"$LABEL\",\"host\":\"$(hostname -f 2>/dev/null || hostname)\",\"exit_code\":$rc,\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
      "$BACKUP_ALERT_WEBHOOK_URL" >/dev/null || true
  fi
  exit "$rc"
}
trap notify_failure ERR

mkdir -p "$DIR"; chmod 700 "$DIR"

if [ "$REQUIRE_ENC" = "1" ] && [ -z "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  echo "BACKUP_ENCRYPTION_KEY is required when BACKUP_REQUIRE_ENCRYPTION=1" >&2
  exit 2
fi

raw_file="$BASE.dump"
docker compose -f "$COMPOSE_FILE" exec -T postgres \
  pg_dump -U postgres --format=custom --no-owner bms > "$raw_file.partial"
mv "$raw_file.partial" "$raw_file"
chmod 600 "$raw_file"
docker compose -f "$COMPOSE_FILE" exec -T postgres pg_restore --list < "$raw_file" >/dev/null

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
