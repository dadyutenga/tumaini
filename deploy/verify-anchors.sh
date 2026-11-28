#!/usr/bin/env bash
# Runs ON THE SERVER: checks the live audit trail against seal hashes saved earlier.
#   ./verify-anchors.sh <bms-anchors-...tsv[.enc]>
# Use a copy fetched from offsite storage. Fails if a saved seal is missing or different, or if the
# rows under any seal no longer match it. Read-only.
set -euo pipefail
FILE="$(readlink -f "${1:?usage: verify-anchors.sh <anchors file>}")"
cd "$(dirname "$(readlink -f "$0")")"
if [ -z "${ADMIN_DATABASE_URL:-}" ] && [ -f .env ]; then
  set -a; . ./.env; set +a
fi
. ./backup-lib.sh
export TAG="${TAG:-check}"
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-docker-compose.prod.yml}"
run_psql() {
  if [ -n "${ADMIN_DATABASE_URL:-}" ]; then
    psql "$ADMIN_DATABASE_URL" -X -q -v ON_ERROR_STOP=1
  else
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U postgres -d bms -X -q -v ON_ERROR_STOP=1
  fi
}
bk_check "$FILE"
{
  echo "CREATE TEMP TABLE anchors (tenant_key uuid, seal_no bigint, chain_hash text);"
  echo "COPY anchors FROM stdin;"
  bk_plain "$FILE"
  echo '\.'
  cat <<'SQL'
DO $$
DECLARE changed bigint; broken bigint; total bigint;
BEGIN
  SELECT count(*), count(*) FILTER (WHERE s.chain_hash IS NULL OR encode(s.chain_hash, 'hex') <> a.chain_hash)
    INTO total, changed
    FROM anchors a LEFT JOIN audit.audit_seals s USING (tenant_key, seal_no);
  SELECT count(*) INTO broken
    FROM (SELECT DISTINCT tenant_key FROM audit.audit_seals) k,
         LATERAL audit.verify_tenant(nullif(k.tenant_key, '00000000-0000-0000-0000-000000000000')) v;
  IF changed <> 0 OR broken <> 0 THEN
    RAISE EXCEPTION 'AUDIT TRAIL ALTERED: % of % saved seals missing or different, % broken chains', changed, total, broken;
  END IF;
  RAISE NOTICE 'audit trail matches % saved seals', total;
END $$;
SQL
} | run_psql
