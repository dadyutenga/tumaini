# BMS database (PostgreSQL 16)

Multi-tenant schema for the Business Management System: shop/POS, inventory,
services/projects, design jobs, finance, HR/payroll, approvals, audit.
The full specification (architecture, workflows, security, compliance) lives in
the "BMS — Enterprise Database & Workflow Engineering Specification" doc.

## Layout
- `migrations/000_bootstrap.sql` – roles, extensions, schemas (run once as superuser)
- `migrations/010…150` – schema, business rules, RLS, grants, reference data, views (run as `bms_owner`)
- `migrate.sh` – ordered, checksummed, idempotent runner
- `tests/01_suite.sql` – 107 integration assertions, executed as the API role `bms_app`
- `tests/02_concurrency.sh` – two sessions racing for the last unit in stock
- `DATA_DICTIONARY.md` – every table/column/constraint/index, generated from the live catalog

## Run
```bash
export PGHOST=localhost PGPORT=5432 PGUSER=postgres
createdb bms_dev
./migrate.sh bms_dev
psql -d bms_dev -f tests/01_suite.sql      # expect: 107 passed, 0 failed
./tests/02_concurrency.sh bms_dev           # expect: CONCURRENCY TEST: PASS
```
The test suite creates a `test` schema and two demo tenants; run it on a throwaway database.

## API connection contract
Connect as `bms_api` (member of `bms_app`). In every transaction, before any query:
```sql
SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true),
       set_config('app.membership_id', $3, true), set_config('app.request_id', $4, true),
       set_config('app.client_ip', $5, true);
```
Business-rule errors use SQLSTATE BM001–BM008 (see spec section 8).
