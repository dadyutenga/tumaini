#!/usr/bin/env bash
# Runs ON THE SERVER, from /opt/tumaini: backup of the production database and uploaded files.
#   ./backup-prod.sh [label]     label defaults to "nightly"; "hourly" saves the database only
# Writes, each with a .sha256 file and, when encrypted, a .mac file:
#   bms-<label>-<time>.dump[.enc]           database
#   bms-files-<label>-<time>.tar[.enc]      uploaded files (not for "hourly")
#   bms-anchors-<label>-<time>.tsv[.enc]    audit seal hashes for verify-anchors.sh (not for "hourly")
# Environment (loaded from .env when present):
#   BACKUP_DIR                    Local backup directory (default /var/backups/tumaini, mode 0700)
#   BACKUP_KEEP_DAYS              Local retention in days (default 30; anchors are kept)
#   BACKUP_HOURLY_KEEP_DAYS       Local retention of hourly dumps (default 3)
#   BACKUP_ENCRYPTION_KEY         Passphrase for AES-256-CBC + PBKDF2 encryption (required in production)
#   BACKUP_REQUIRE_ENCRYPTION     Set to 1 to refuse unencrypted backups
#   BACKUP_UPLOAD_CMD             Offsite copy command receiving a file path as $1
#   BACKUP_ALERT_WEBHOOK_URL      Optional HTTPS webhook POSTed on backup failure
#   BACKUP_HEARTBEAT_URL          Optional URL requested after a successful nightly backup, for a
#   BACKUP_HOURLY_HEARTBEAT_URL   monitor that alerts when the request stops arriving
#   BACKUP_FILES_VOLUME           Docker volume holding uploads (default tumaini_files)
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
if [ -f .env ]; then
  set -a; . ./.env; set +a
fi
export TAG="${TAG:-backup}" # compose refuses to load without it; only the postgres service is used
. ./backup-lib.sh

LABEL="${1:-nightly}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
REQUIRE_ENC="${BACKUP_REQUIRE_ENCRYPTION:-0}"
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-docker-compose.prod.yml}"
FILES_VOLUME="${BACKUP_FILES_VOLUME:-tumaini_files}"
TOOL_IMAGE="postgres:16-alpine" # already on the host; provides tar
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DB_FILE="$DIR/bms-$LABEL-$STAMP.dump"
FILES_FILE="$DIR/bms-files-$LABEL-$STAMP.tar"
ANCHORS_FILE="$DIR/bms-anchors-$LABEL-$STAMP.tsv"
umask 077

# Runs on every exit, so refusals and signals alert as well as failed commands.
on_exit() {
  local rc=$?
  rm -f "$DB_FILE"*.partial "$FILES_FILE"*.partial "$ANCHORS_FILE"*.partial
  [ "$rc" = 0 ] && return
  echo "ERROR: backup ($LABEL) failed with exit code $rc at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  bk_alert backup_failed "$LABEL" "$rc"
}
trap on_exit EXIT

mkdir -p "$DIR"; chmod 700 "$DIR"

if [ -z "${BACKUP_ENCRYPTION_KEY:-}" ]; then
  if [ "$REQUIRE_ENC" = "1" ]; then
    echo "BACKUP_ENCRYPTION_KEY is required when BACKUP_REQUIRE_ENCRYPTION=1" >&2
    exit 2
  fi
  echo "WARNING: BACKUP_ENCRYPTION_KEY is not set; this backup is NOT encrypted" >&2
fi

# store <target> <check command> -- <producing command...>
# Streams the producer into the target (encrypting on the way, so no plaintext copy touches the disk),
# reads it back through the check command, then records the checksum. Sets $stored.
store() {
  local target="$1" check="$2"; shift 3
  if [ -n "${BACKUP_ENCRYPTION_KEY:-}" ]; then
    stored="$target.enc"
    "$@" | bk_encrypt > "$stored.partial"
    # The trailing cat drains the stream so a check that stops early does not break the pipe.
    bk_decrypt "$stored.partial" | { $check >/dev/null && cat >/dev/null; }
  else
    stored="$target"
    "$@" > "$stored.partial"
    $check < "$stored.partial" >/dev/null
  fi
  mv "$stored.partial" "$stored"
  local made=("$stored" "$stored.sha256") f
  (cd "$DIR" && sha256sum "$(basename "$stored")" > "$(basename "$stored").sha256")
  if [ -n "${BACKUP_ENCRYPTION_KEY:-}" ]; then
    bk_mac "$stored" > "$stored.mac"
    made+=("$stored.mac")
  fi
  if [ -n "${BACKUP_UPLOAD_CMD:-}" ]; then
    for f in "${made[@]}"; do bash -c "$BACKUP_UPLOAD_CMD" upload "$f"; done
  fi
  echo "backup written: $stored"
}

pg() { docker compose -f "$COMPOSE_FILE" exec -T postgres "$@"; }
store "$DB_FILE" "pg pg_restore --list" -- pg pg_dump -U postgres --format=custom bms

if [ "$LABEL" != hourly ]; then
  # Audit seal hashes as they are now. A copy held off the server shows later whether history was
  # rewritten, even by someone who could recompute the seals inside the database.
  store "$ANCHORS_FILE" "cat" -- pg psql -U postgres -d bms -X -At -v ON_ERROR_STOP=1 -F "$(printf '\t')" \
    -c "SELECT tenant_key, seal_no, encode(chain_hash, 'hex') FROM audit.audit_seals ORDER BY 1, 2"

  if docker volume inspect "$FILES_VOLUME" >/dev/null 2>&1; then
    store "$FILES_FILE" "tar -tf -" -- \
      docker run --rm --network none -v "$FILES_VOLUME:/app/data:ro" "$TOOL_IMAGE" tar -C /app -cf - data
  else
    echo "ERROR: uploads volume $FILES_VOLUME not found; set BACKUP_FILES_VOLUME" >&2
    exit 3
  fi
fi

if [ -z "${BACKUP_UPLOAD_CMD:-}" ]; then
  echo "WARNING: BACKUP_UPLOAD_CMD is not set; backups exist only on this server" >&2
fi

find "$DIR" -name 'bms-hourly-*' -mtime +"${BACKUP_HOURLY_KEEP_DAYS:-3}" -delete
find "$DIR" \( -name 'bms-*.dump*' -o -name 'bms-files-*' \) -mtime +"$KEEP" -delete

case "$LABEL" in
  nightly) HEARTBEAT="${BACKUP_HEARTBEAT_URL:-}" ;;
  hourly)  HEARTBEAT="${BACKUP_HOURLY_HEARTBEAT_URL:-}" ;;
  *)       HEARTBEAT="" ;;
esac
if [ -n "$HEARTBEAT" ]; then
  curl -fsS -m 10 "$HEARTBEAT" >/dev/null || echo "WARNING: could not reach the backup heartbeat URL" >&2
fi
