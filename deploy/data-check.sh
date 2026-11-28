#!/usr/bin/env bash
# Runs ON THE SERVER: looks for damage or exposure that code fixes do not repair by themselves.
# Read-only. Exits 1 when something needs attention.
#   ./data-check.sh
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
if [ -f .env ]; then
  set -a; . ./.env; set +a
fi
export TAG="${TAG:-check}"
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-docker-compose.prod.yml}"
FILES_VOLUME="${BACKUP_FILES_VOLUME:-tumaini_files}"
FOUND=0
q() { docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U postgres -d bms -X -At -v ON_ERROR_STOP=1 -c "$1" </dev/null; }
report() { # report <title> <lines>
  if [ -z "$2" ]; then
    echo "ok       $1"
  else
    FOUND=1
    echo "ATTENTION $1"
    sed 's/^/           /' <<<"$2" | head -50
  fi
}

# Uploaded files that are missing or no longer match what was recorded.
list="$(q "SELECT encode(sha256, 'hex') || '  ' || storage_key FROM app.files WHERE deleted_at IS NULL AND sha256 IS NOT NULL ORDER BY storage_key")"
bad=""
if [ -n "$list" ]; then
  # Each file is tested on its own: sha256sum -c says nothing on standard output about a missing one.
  bad="$(printf '%s\n' "$list" | docker run --rm -i --network none -v "$FILES_VOLUME:/app/data:ro" postgres:16-alpine sh -c '
    while read -r sum key; do
      f="/app/data/files/$key"
      if [ ! -f "$f" ]; then
        echo "missing: $key"
      elif [ "$(sha256sum < "$f" | cut -d" " -f1)" != "$sum" ]; then
        echo "altered: $key"
      fi
    done')"
fi
report "uploaded files present and unchanged" "$bad"

report "archived or inactive customers who still owe money" "$(q "
  SELECT 'customer ' || c.id
    FROM app.customers c
   WHERE (c.archived_at IS NOT NULL OR c.client_status = 'inactive')
     AND (EXISTS (SELECT 1 FROM app.sales s WHERE s.customer_id = c.id
                    AND s.status = 'completed' AND s.balance_due > 0)
       OR EXISTS (SELECT 1 FROM app.invoices i WHERE i.customer_id = c.id
                    AND i.status IN ('issued', 'partially_paid') AND i.balance_due > 0))")"

report "national ID numbers still stored in clear text" "$(q "
  SELECT count(*) || ' customer rows (the daily job converts them)' FROM app.customers
   WHERE national_id_key_id = 'local-v1' HAVING count(*) > 0")"

report "audit entries holding a national ID number in clear text (append-only: restrict who may read them)" "$(q "
  SELECT count(*) || ' entries, ' || min(occurred_at)::date || ' to ' || max(occurred_at)::date FROM audit.audit_log
   WHERE length(coalesce(new_values #>> '{after,id_number}', '')) > 4
      OR length(coalesce(new_values #>> '{before,id_number}', '')) > 4
  HAVING count(*) > 0")"

report "money account balances match the ledger" "$(q "
  SELECT 'account ' || a.id FROM app.financial_accounts a
    LEFT JOIN (SELECT account_id, sum(CASE WHEN direction = 'in' THEN amount ELSE -amount END) AS net
                 FROM app.financial_transactions GROUP BY 1) t ON t.account_id = a.id
   WHERE a.current_balance <> coalesce(t.net, 0)")"

report "stock balances match movements" "$(q "
  SELECT 'product ' || b.product_id || ' at ' || b.location_id FROM app.stock_balances b
    LEFT JOIN (SELECT location_id, product_id, sum(quantity_delta) AS q FROM app.stock_movements GROUP BY 1, 2) m
      USING (location_id, product_id)
   WHERE b.quantity_on_hand <> coalesce(m.q, 0)")"

report "audit seals intact" "$(q "
  SELECT 'chain ' || k.tenant_key || ' seal ' || v.seal_no || ': ' || v.problem
    FROM (SELECT DISTINCT tenant_key FROM audit.audit_seals) k,
         LATERAL audit.verify_tenant(nullif(k.tenant_key, '00000000-0000-0000-0000-000000000000')) v")"

missing=""
for v in BACKUP_ENCRYPTION_KEY BACKUP_UPLOAD_CMD BACKUP_ALERT_WEBHOOK_URL BACKUP_HEARTBEAT_URL; do
  [ -n "${!v:-}" ] || missing+="$v is not set"$'\n'
done
[ "${BACKUP_REQUIRE_ENCRYPTION:-0}" = 1 ] || missing+="BACKUP_REQUIRE_ENCRYPTION is not 1"$'\n'
report "backup settings in .env" "${missing%$'\n'}"
exit "$FOUND"
