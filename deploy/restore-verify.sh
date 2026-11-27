#!/usr/bin/env bash
# Automated backup restore & financial/inventory/audit reconciliation verification.
# Restores a .dump or .dump.enc backup into an ephemeral database, runs post-recovery
# integrity checks (ledger balances, stock balances, audit hash-chain seals), and cleans up.
#
# Usage:
#   # Production Docker stack (from /opt/tumaini):
#   ./restore-verify.sh /var/backups/tumaini/bms-nightly-20261127T011500Z.dump.enc
#
#   # Direct PostgreSQL URL (CI or staging):
#   ADMIN_DATABASE_URL=postgres://postgres:...@127.0.0.1:5433/postgres \
#     BACKUP_ENCRYPTION_KEY=... ./restore-verify.sh /path/to/backup.dump.enc
set -euo pipefail

BACKUP_FILE="${1:?usage: restore-verify.sh <backup.dump|backup.dump.enc>}"
[ -f "$BACKUP_FILE" ] || { echo "backup file not found: $BACKUP_FILE" >&2; exit 1; }

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
if [ -z "${ADMIN_DATABASE_URL:-}" ] && [ -f "$SCRIPT_DIR/.env" ]; then
  set -a; . "$SCRIPT_DIR/.env"; set +a
fi

TMP_DUMP=""
VERIFY_DB="bms_restore_verify_$$"
COMPOSE_FILE="${DOCKER_COMPOSE_FILE:-$SCRIPT_DIR/docker-compose.prod.yml}"
USE_DOCKER=0
if [ -z "${ADMIN_DATABASE_URL:-}" ]; then
  USE_DOCKER=1
fi

run_psql_admin() {
  if [ "$USE_DOCKER" = 1 ]; then
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U postgres -d postgres -v ON_ERROR_STOP=1 "$@"
  else
    psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 "$@"
  fi
}

run_psql_verify() {
  if [ "$USE_DOCKER" = 1 ]; then
    docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U postgres -d "$VERIFY_DB" -v ON_ERROR_STOP=1 "$@"
  else
    local verify_url="${ADMIN_DATABASE_URL%/*}/$VERIFY_DB"
    psql "$verify_url" -v ON_ERROR_STOP=1 "$@"
  fi
}

cleanup() {
  if [ -n "$TMP_DUMP" ] && [ -f "$TMP_DUMP" ]; then
    rm -f "$TMP_DUMP"
  fi
  run_psql_admin -q -c "DROP DATABASE IF EXISTS $VERIFY_DB WITH (FORCE);" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [ -f "$BACKUP_FILE.sha256" ]; then
  echo "==> verifying sha256 checksum"
  sha256sum -c "$BACKUP_FILE.sha256"
fi

DUMP_SRC="$BACKUP_FILE"
if [[ "$BACKUP_FILE" == *.enc ]]; then
  : "${BACKUP_ENCRYPTION_KEY:?set BACKUP_ENCRYPTION_KEY to decrypt $BACKUP_FILE}"
  TMP_DUMP="$(mktemp /tmp/bms-restore-verify-XXXXXX.dump)"
  chmod 600 "$TMP_DUMP"
  echo "==> decrypting AES-256 backup"
  openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 \
    -in "$BACKUP_FILE" -out "$TMP_DUMP" -pass env:BACKUP_ENCRYPTION_KEY
  DUMP_SRC="$TMP_DUMP"
fi

echo "==> creating ephemeral verification database $VERIFY_DB"
run_psql_admin -q -c "CREATE DATABASE $VERIFY_DB;"

echo "==> restoring dump into $VERIFY_DB"
if [ "$USE_DOCKER" = 1 ]; then
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    pg_restore -U postgres -d "$VERIFY_DB" --no-owner --exit-on-error < "$DUMP_SRC"
else
  verify_url="${ADMIN_DATABASE_URL%/*}/$VERIFY_DB"
  pg_restore --dbname="$verify_url" --no-owner --exit-on-error "$DUMP_SRC"
fi

echo "==> running post-recovery financial, inventory, and audit reconciliation"
run_psql_verify -At <<'SQL'
DO $$
DECLARE
  fin_mismatch bigint;
  stk_mismatch bigint;
  bad_seals    bigint;
  sales_cnt    bigint;
  pay_cnt      bigint;
BEGIN
  SELECT count(*) INTO fin_mismatch
    FROM app.financial_accounts a
    LEFT JOIN (
      SELECT tenant_id, account_id,
             SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END) AS net
      FROM app.financial_transactions
      GROUP BY tenant_id, account_id
    ) t ON t.tenant_id = a.tenant_id AND t.account_id = a.id
   WHERE a.current_balance <> COALESCE(t.net, 0);

  IF fin_mismatch <> 0 THEN
    RAISE EXCEPTION 'post-recovery financial reconciliation failed: % mismatched accounts', fin_mismatch;
  END IF;

  SELECT count(*) INTO stk_mismatch
    FROM app.stock_balances sb
    LEFT JOIN (
      SELECT tenant_id, location_id, product_id, SUM(quantity_delta) AS qty
      FROM app.stock_movements
      GROUP BY tenant_id, location_id, product_id
    ) m ON m.tenant_id = sb.tenant_id AND m.location_id = sb.location_id AND m.product_id = sb.product_id
   WHERE sb.quantity_on_hand <> COALESCE(m.qty, 0);

  IF stk_mismatch <> 0 THEN
    RAISE EXCEPTION 'post-recovery stock reconciliation failed: % mismatched stock balances', stk_mismatch;
  END IF;

  SELECT count(*) INTO bad_seals
    FROM app.tenants t, LATERAL audit.verify_tenant(t.id) v;

  IF bad_seals <> 0 THEN
    RAISE EXCEPTION 'post-recovery audit seal verification failed: % broken seals', bad_seals;
  END IF;

  SELECT count(*) INTO sales_cnt FROM app.sales;
  SELECT count(*) INTO pay_cnt FROM app.payments_received;
  RAISE NOTICE 'RESTORE VERIFIED OK: financial_mismatches=0 stock_mismatches=0 broken_audit_seals=0 sales=% payments_received=%', sales_cnt, pay_cnt;
END $$;
SQL
echo "==> restore verification PASSED"
