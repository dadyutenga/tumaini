# Agent Handoff — Backend BMS Transition Continuation

**Last updated**: 2026-11-26  
**Branch**: `main` (synced with `origin/main`)  
**Latest code commit before this handoff**: `b69543f`  
**User preference**: Continue finishing the backend ASAP, verify thoroughly, and **`git commit` + `git push origin main` after every completed step/batch**.

---

## 1. What Is Already Complete & Pushed on `origin/main`

All Go backend modules have been ported to the approved BMS schema, legacy kernels removed, and the first 3 integration test batches are passing:

- `ecefcb3`, `85d914a` — Frontend aligned with approved BMS schema, 3-part permission codes, authority limits, and DTOs for the first 12 ported modules (Vitest 37/37 + Vite build passing).
- `154a030` — Fixed legacy permission middleware codes in `backend/internal/modules/identity/handler.go`.
- `8c57620` — Ported **Services & Design** module (`backend/internal/modules/services`) to approved BMS schema (Step 29).
- `9dadb8e` — Ported **Reports & Dashboard** module (`backend/internal/modules/reports`) to approved BMS schema (Step 30).
- `1e2af07` — Ported **Inbox & System** modules (`backend/internal/modules/inbox`, `backend/internal/modules/system`) to approved BMS schema.
- `3e70f4f` — Deleted obsolete legacy `kernel/ledger`, `kernel/stock`, `kernel/chart`, old payment helpers, and removed the `Principal.BusinessID` alias (everything now uses `Principal.TenantID`).
- `8383f42` — Added migration `190_notifications_link_path_fix.sql`, fixed notifications/identity/catalog/org bugs, and passed **Batch 1** integration tests (`acceptance_test.go`, `identity_flow_test.go`, `org_flow_test.go`, `users_test.go`).
- `46b3c07` — Added migration `200_stock_avg_cost_on_positive_adjustment.sql`, fixed stock avg cost / `StockCard` / `SaveCategory`, and passed **Batch 2a** integration tests (`batch_expiry_test.go`, `catalog_flow_test.go`, `customers_flow_test.go`, `inventory_flow_test.go`, `invoice_numbers_test.go`).
- `b69543f` — Added migration `210_day_closings_admin_reopen.sql`, fixed finance/customers/purchasing/sales/system/jobs bugs, and passed **Batch 2b** integration tests (`finance_flow_test.go`, `expenses_flow_test.go`, `purchasing_flow_test.go`, `sales_flow_test.go`).

---

## 2. What Remains (Exact Priority Order)

### Step A — Finish Remaining Backend Integration Tests (`backend/internal/app/`)
Run against the Docker PostgreSQL instance (`deploy/docker-compose.yml` service `postgres` on `127.0.0.1:5433`, env in `backend/.env`). Rewrite tests to match the approved BMS schema and fix any real backend bugs uncovered along the way:

1. **Batch 3 — HR, Attendance & Payroll** (commit & push once green):
   - `hr_flow_test.go`
   - `hr_master_test.go`
   - `employee_documents_test.go`
   - `attendance_network_test.go`
   - `loan_void_flow_test.go`
   - `payroll_flow_test.go`
   - `payroll_async_test.go`
2. **Batch 4 — Services, Reports, Inbox, System, Audit, Recovery & Files** (commit & push once green):
   - `services_flow_test.go`
   - `reports_flow_test.go`
   - `inbox_flow_test.go`
   - `system_jobs_test.go`
   - `audit_fixes_test.go`
   - `recovery_flow_test.go`
   - `files_flow_test.go`
3. **Batch 5 — OpenAPI Contract & Spec Parity** (commit & push once green):
   - Update `docs/api/fragments/*.yaml` to match current registered routes on the approved schema.
   - Regenerate `docs/api/openapi.yaml` via `cd backend && go run ./cmd/openapi`.
   - Pass `openapi_contract_test.go` (`TestOpenAPIContract`) and `go run ./cmd/openapi -check`.

### Step B — Full Backend Suite Verification
Run the complete backend verification commands:
```bash
cd backend
set -a && . ./.env && set +a
go run ./cmd/migrate up
go run ./cmd/openapi -check
go vet ./...
go test ./internal/app
# Run unit tests without .env LISTEN_ADDR override so TestLoadDefaults passes:
env -u LISTEN_ADDR go test ./internal/config ./internal/modules/... ./internal/platform/...
```

### Step C — Frontend Re-audit for Newly Ported Modules
Frontend was aligned before `services`, `reports`, `inbox`, and `system` were ported (`8c57620`, `9dadb8e`, `1e2af07`). After backend tests pass:
- Audit frontend pages for Services/Design, Dashboard/Reports, Approvals/Notifications/Audit/Backups against the newly ported Go DTOs and permission codes.
- Verify `npm --prefix frontend run test -- --run`, `npm --prefix frontend run build`, and ESLint on changed files.
- Commit & push.

### Step D — Update Status & Ops Docs
- Update `docs/database/database-transition.md` checklist (and remove this `HANDOFF.md` once all backend/frontend transition items are complete).
- Check whether deployment/cutover/rollback/backup docs in `docs/database/database-transition.md` line 62 are needed.

---

## 3. Environment & Gotchas
- **Database**: Docker Postgres (`deploy/docker-compose.yml` service `postgres` on `127.0.0.1:5433`), credentials in `backend/.env`. Run `go run ./cmd/migrate up` after adding any additive SQL file in `backend/database/migrations/` (forward-only, atomic, checksummed — next number is `220_*.sql`).
- **`TestLoadDefaults` in `internal/config`**: Fails if `LISTEN_ADDR=:8081` is exported from `backend/.env`; run `env -u LISTEN_ADDR go test ./internal/config`.
- **OpenAPI YAML fragments**: Always append cleanly rather than editing generic YAML blocks to avoid duplicate matches.
- **Keep `main` pushed**: User explicitly asked to push to `origin/main` after every implementation step.
