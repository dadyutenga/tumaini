#!/usr/bin/env bash
# Runs ON THE SERVER, monthly from cron: restores the newest nightly backup into a temporary database
# and checks it, so a backup that cannot be restored is found before it is needed. The time it takes
# is the database part of the recovery time.
#   ./restore-drill.sh [backup file]
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
if [ -f .env ]; then
  set -a; . ./.env; set +a
fi
. ./backup-lib.sh
DIR="${BACKUP_DIR:-/var/backups/tumaini}"

on_exit() {
  local rc=$?
  [ "$rc" = 0 ] && return
  echo "ERROR: restore drill failed with exit code $rc at $(date -u +%Y-%m-%dT%H:%M:%SZ)" >&2
  bk_alert restore_drill_failed drill "$rc"
}
trap on_exit EXIT

FILE="${1:-$(find "$DIR" -maxdepth 1 \( -name 'bms-nightly-*.dump' -o -name 'bms-nightly-*.dump.enc' \) | sort | tail -1)}"
[ -n "$FILE" ] || { echo "no nightly backup found in $DIR" >&2; exit 1; }
if [ -z "${1:-}" ] && [ -z "$(find "$FILE" -mtime -2)" ]; then
  echo "newest nightly backup is more than two days old: $FILE" >&2
  exit 1
fi
echo "restore drill $(date -u +%Y-%m-%dT%H:%M:%SZ) on $FILE"
./restore-verify.sh "$FILE"
