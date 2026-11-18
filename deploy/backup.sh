#!/usr/bin/env bash
# Nightly full database backup (FR-SEC-07). Run from the host's cron, for example:
#   15 1 * * * BACKUP_DATABASE_URL=postgres://bms_owner:...@127.0.0.1:5433/bms /opt/tumaini/deploy/backup.sh
# Keeps BACKUP_KEEP_DAYS (default 30) days locally. Set BACKUP_UPLOAD_CMD to copy each
# dump off the server; it receives the file path as $1, for example:
#   BACKUP_UPLOAD_CMD='rclone copy "$1" offsite:tumaini-backups/'
# After a successful dump it records the time in every business's settings, which
# GET /api/v1/backups shows as last_automatic_backup_at.
set -euo pipefail
: "${BACKUP_DATABASE_URL:?set BACKUP_DATABASE_URL to the bms_owner connection string}"
DIR="${BACKUP_DIR:-/var/backups/tumaini}"
KEEP="${BACKUP_KEEP_DAYS:-30}"
mkdir -p "$DIR"
chmod 700 "$DIR"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
file="$DIR/bms-$stamp.dump"
pg_dump --format=custom --no-owner --file="$file.partial" "$BACKUP_DATABASE_URL"
mv "$file.partial" "$file"
chmod 600 "$file"
pg_restore --list "$file" >/dev/null
if [ -n "${BACKUP_UPLOAD_CMD:-}" ]; then
  bash -c "$BACKUP_UPLOAD_CMD" upload "$file"
fi
find "$DIR" -name 'bms-*.dump' -mtime +"$KEEP" -delete
psql "$BACKUP_DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "
  INSERT INTO settings (business_id, key, value)
  SELECT id, 'backup.last_automatic', to_jsonb(now()) FROM businesses
  ON CONFLICT (business_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();"
echo "backup written: $file"
