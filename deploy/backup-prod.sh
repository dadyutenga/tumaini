#!/usr/bin/env bash
# Runs ON THE SERVER, from /opt/tumaini: full database dump of the production stack.
#   ./backup-prod.sh [label]     label defaults to "nightly"
# Keeps BACKUP_KEEP_DAYS (default 30) days in BACKUP_DIR (default /var/backups/tumaini). Set
# BACKUP_UPLOAD_CMD to copy each dump off the server; it receives the file path as $1, e.g.
#   BACKUP_UPLOAD_CMD='rclone copy "$1" offsite:tumaini-backups/'
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
LABEL="${1:-nightly}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
mkdir -p "$DIR"; chmod 700 "$DIR"
file="$DIR/bms-$LABEL-$(date -u +%Y%m%dT%H%M%SZ).dump"
docker compose -f docker-compose.prod.yml exec -T postgres pg_dump -U postgres --format=custom --no-owner bms > "$file.partial"
mv "$file.partial" "$file"; chmod 600 "$file"
docker compose -f docker-compose.prod.yml exec -T postgres pg_restore --list < "$file" >/dev/null
[ -z "${BACKUP_UPLOAD_CMD:-}" ] || bash -c "$BACKUP_UPLOAD_CMD" upload "$file"
find "$DIR" -name 'bms-*.dump' -mtime +"$KEEP" -delete
echo "backup written: $file"
