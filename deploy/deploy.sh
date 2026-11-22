#!/usr/bin/env bash
# Runs ON THE SERVER, from /opt/tumaini: deploys one image tag.
#   ./deploy.sh <tag>                 pull images, back up, migrate, start, health-check
#   PULL=0 ./deploy.sh <tag>          use images already loaded on the host (first manual deploy)
# If the new version does not become healthy it restarts the previous tag. Migrations are
# forward-only, so a rollback restores the previous images, not the previous schema.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
TAG_NEW="${1:?usage: deploy.sh <tag>}"
PULL="${PULL:-1}"
exec 9>.deploy.lock
flock -n 9 || { echo "another deploy is running" >&2; exit 1; }

set -a; . ./.env; set +a
DC=(docker compose -f docker-compose.prod.yml)
PREV="$(cat .deployed_tag 2>/dev/null || true)"
export TAG="$TAG_NEW"

log() { printf '\n==> %s\n' "$*"; }

if [ "$PULL" = 1 ]; then
  log "pull $IMAGE_PREFIX/{api,web}:$TAG"
  "${DC[@]}" pull api web
fi

log "start postgres"
"${DC[@]}" up -d postgres
for _ in $(seq 1 30); do
  [ "$("${DC[@]}" ps --format '{{.Health}}' postgres)" = healthy ] && break
  sleep 2
done

if [ -n "$PREV" ]; then
  log "pre-deploy backup"
  ./backup-prod.sh predeploy
fi

log "migrate"
"${DC[@]}" --profile migrate run --rm migrate

log "set login role passwords"
"${DC[@]}" exec -T -e API_PW="$BMS_API_DB_PASSWORD" -e JOBS_PW="$BMS_JOBS_DB_PASSWORD" postgres \
  psql -U postgres -d bms -v ON_ERROR_STOP=1 -q <<'SQL'
\getenv api_pw API_PW
\getenv jobs_pw JOBS_PW
ALTER ROLE bms_api LOGIN PASSWORD :'api_pw';
ALTER ROLE bms_jobs LOGIN PASSWORD :'jobs_pw';
SQL

log "start api and web"
"${DC[@]}" up -d --remove-orphans api web

healthy() {
  local cid
  cid="$("${DC[@]}" ps -q api)"
  [ -n "$cid" ] && [ "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy ] \
    && curl -fsS "http://127.0.0.1:${WEB_PORT:-18501}/" >/dev/null
}
ok=0
for _ in $(seq 1 40); do
  if healthy; then ok=1; break; fi
  sleep 3
done

if [ "$ok" != 1 ]; then
  echo "!! $TAG did not become healthy" >&2
  "${DC[@]}" logs --tail 60 api >&2 || true
  if [ -n "$PREV" ] && [ "$PREV" != "$TAG_NEW" ]; then
    echo "!! rolling back to $PREV" >&2
    TAG="$PREV" "${DC[@]}" up -d api web
  fi
  exit 1
fi

echo "$TAG_NEW" > .deployed_tag
docker image prune -f >/dev/null
log "deployed $TAG_NEW"
