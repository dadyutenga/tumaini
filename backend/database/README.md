# BMS database (PostgreSQL 16)

Schema for the Business Management System (one business, with branches and business units): shop/POS, inventory,
services/projects, design jobs, finance, HR/payroll, approvals, audit.
The full specification (architecture, workflows, security, compliance) lives in
the "BMS — Enterprise Database & Workflow Engineering Specification" doc.

## Layout
- `migrations/000_bootstrap.sql` – roles, extensions, schemas (run once as superuser)
- `migrations/010…` – schema, business rules, row-level rules, grants, reference data, views (run as `bms_owner`).
  `340` turned the earlier multi-business schema into a single-business one; `341` removed two-factor authentication and API keys.
- `migrate.sh` – ordered, checksummed, idempotent runner
- `tests/01_suite.sql` – 107 integration assertions, executed as the API role `bms_app`
- `tests/02_concurrency.sh` – two sessions racing for the last unit in stock
- `DATA_DICTIONARY.md` – every table/column/constraint/index, generated from the live catalog

## Run
```bash
export PGHOST=localhost PGPORT=5432 PGUSER=postgres
createdb bms_dev
./migrate.sh bms_dev          # or, from backend/: go run ./cmd/migrate up
```
The schema is tested by the Go integration tests (`go test ./...`). The SQL suites under `tests/`
were written for the multi-business schema and do not run against it any more.

## API connection contract
Connect as `bms_api` (member of `bms_app`). In every transaction, before any query:
```sql
SELECT set_config('app.user_id', $1, true), set_config('app.membership_id', $2, true),
       set_config('app.request_id', $3, true), set_config('app.client_ip', $4, true);
```
Business-rule errors use SQLSTATE BM001–BM008 (see spec section 8).
