# Database transition: legacy schema to the approved BMS schema

Status document. It is updated with every step; the commit log on branch `feature/new-database` is the detailed history.

## What changed

| | Legacy | Approved (now) |
|---|---|---|
| Tenancy | `businesses`, GUC `app.business_id` | `app.tenants`, GUCs `app.tenant_id`, `app.user_id`, `app.membership_id`, `app.request_id`, `app.client_ip` |
| Identity | `users` per business, `sessions` with refresh hash | global `iam.users` + `iam.user_credentials`; seat = `app.memberships`; `iam.sessions` (opaque token hash, idle and absolute limits) |
| Authorization | role permissions with scope and limit per key | flat permission codes (`module.resource.action`), one role per seat, per-person allow/deny overrides, separate `app.authority_limits`, role `rank` |
| Business rules | Go (kernel: ledger, stock, sequence, approval) | PostgreSQL triggers and functions; SQLSTATE `BM001..BM008` |
| Audit | Go inserts into `audit_logs` | row triggers (`audit.tg_capture`) plus `audit.log_event`; hash-chained, sealed |
| Migrations | golang-migrate, up/down | `backend/database/migrations`, forward-only, atomic per file, checksummed (`cmd/migrate up|status|verify`) |
| Roles | `bms_owner` (bypass RLS), `bms_app` | `bms_owner` (no bypass), `bms_api` (API), `bms_jobs` (workers), `bms_readonly`, `bms_migrator` |

The legacy migrations are frozen in `backend/database/legacy/` for auditability. The legacy development database was dropped on the owner's instruction; it held demo data only and no production data existed.

## Decisions and deviations (each documented, none silent)

1. **Migration 160** (additive): `bms_worker` may insert into `iam.users`, `iam.user_credentials`, `iam.password_history`, so registering a business (user + `app.provision_tenant`) is one transaction. 150 grants `provision_tenant` to the worker role only.
2. **No PIN.** The schema has no PIN credential; the `/auth/pin` endpoints were removed (the web client did not use them).
3. **No refresh-token reuse detection.** `iam.sessions` has no previous-hash column; a replayed old refresh token simply no longer matches and the caller must sign in again.
4. **Scopes.** Grants no longer carry `branch`/`own` scope. "All versus own" is a permission code; locations are not assigned per person (only a default). `Principal.AllowsBranch` is therefore always true inside the tenant.
5. **Roles are retired, not deleted** (`status = 'inactive'`): people and history reference them and the API role has no DELETE on `app.roles`.
7. **Migration 170** (additive): `app.tenant_setting_values`, a validated key/value table for operational settings the specification has no column for (payroll parameters, receipt layout, reminder lead times, clock-in networks, payment-method mapping, currency display). Settings that do have a column (`tenant_settings`, `tenants`, `document_sequences`) are read and written there; `platform/setting` hides the difference from modules.
8. **Notification types** are the 17 values of `app.notification_type`; the old 25 event names are mapped when the notification module is ported. `days_before` per type is stored as `notifications.days_before_<type>`.
9. **Branches** lose `code`, `manager_id` and per-branch invoice prefix: numbering is per tenant (`app.document_sequences`) and locations have no such columns.
10. **Catalog.** A unit of measure is a lowercase code on the product, with no master table; `/units` lists common codes plus those in use, writes only validate. Per-branch selling prices do not exist in the approved model (`PUT /products/:id/branch-prices` answers 410). Products are stock items only; the `service` kind belongs to the services catalogue. Selling-price changes are logged and permission/approval-gated by the database (`tg_product_price_guard`); the optional `reason` field feeds `app.product_price_changes.reason`.
11. **Migration 180** (additive): `app.job_queue` and `app.scheduled_job_marks`. The specification lists background workers but no queue. Jobs are enqueued inside the request transaction by the API role; the worker role claims them across tenants through explicit worker policies (the same pattern 140 uses for notifications). Job handlers run on the API pool under the job's tenant context, so triggers and RLS still apply.
12. **Idempotency** uses `app.idempotency_keys`: keys are tenant-wide, the request hash covers method, path and body, and the stored response is the same JSON the client first received.
13. **Stock documents.** Stock in/out entries are stock adjustments that are created and posted in one step (reasons outside the enum become `other` with the original reason as the note); batch numbers and expiry dates are not part of the approved model (400 `batch_not_supported`). A counted adjustment is a `stock_counts` record plus a draft adjustment; posting recomputes each difference against stock at that moment. Only one count may be open per location (unique index). There is no approval step for adjustments in the approved approval types; the permission `shop.stock.manage` gates them. Stock-in no longer revalues the moving average (only purchase receipts do).
14. **Approvals.** Seven typed approval requests (`app.approval_type`). Who may approve, authority limits and no-self-approval are enforced by the database; `kernel/approval` files requests, records decisions, runs the optional hook that performs the approved action in the deciding transaction, and notifies. Other former approval kinds (expense, stock adjustment, purchase order, loan, leave, overtime, payroll run) are status changes on their own records. Money: the `payments` kernel gained `Receive` and `Pay`; the database posts `financial_transactions`. Payment method `bank` is stored as `bank_transfer` and shown as `bank`.
6. **API vocabulary kept where possible.** `business_id`, `branch_id`, `X-Branch-Id` and `slug` still appear in JSON/headers and now carry tenant id, location id and role code. Permission codes are new; the web client is updated in a later step.

## Backend port checklist

Legend: [x] done (compiles; not yet integration-tested), [~] partly done, [ ] not started. Work happens on branch `feature/new-database`, one commit per step; `git log --oneline` is the history. Nothing here has been run against a live API yet: the integration tests in `internal/app` still describe the old schema and are re-enabled module by module once a module is ported.

- [x] Migration runner, schema installed, dev roles, additive migrations 160 (worker grants), 170 (setting values), 180 (job queue)
- [x] Platform: request context, worker pool, BM error mapping, tokens, idempotency, document numbering, jobs
- [x] Identity: register, login, refresh, logout, password, principal, users, roles, sessions, login history, overrides, recovery
- [x] Kernel: audit (`audit.log_event`), notify (in-app), approval (typed requests), payments writers (`Receive`, `Pay`)
- [x] Org and settings (profile, locations, tax, files, attachments, settings, notification rules)
- [x] Catalog (products, categories, price history; units are codes, per-branch prices and the product `kind` are gone)
- [x] Inventory (reads, entries, counts, adjustments, transfers)
- [x] Customers (profiles, receive payment, unpaid, statement, debts; write-off is not in the approved model)
- [x] Finance (accounts, statements, transfers, transactions, payments, void, debts, day closing; ledger, income, drawings, statutory are not in the approved model)
- [x] Sales and POS (sales, lines, units, held carts, returns, void; tills and shop quotations are not in the approved model)
- [x] Purchasing (purchases, goods receipts, serial units, suppliers, supplier payments; separate purchase invoices and purchase returns are not in the approved model)
- [x] Expenses (categories, expenses, recurring templates, petty cash top-up and reconciliations; reversing a paid expense uses transaction corrections)
- [x] HR, attendance, payroll (employees, compensation, allowances, attendance records/corrections, leave, advances, commissions, pay adjustments, payroll runs/lines/statutory items/payslips; salary components master, work schedules, and holidays are not in the approved model)
- [x] Services and design (clients, projects, quotations, invoices, design jobs, contractors, renewals, tasks)
- [x] Reports and dashboard
- [x] Inbox (approvals list, notifications), system (exports, backups)
- [x] Delete the old `kernel/ledger`, `kernel/stock`, `kernel/chart` and the old `payments` helpers once nothing uses them
- [x] Rename `Principal.BusinessID` to `TenantID` everywhere, remove the alias
- [~] Rewrite the integration tests against the new schema (Batches 1, 2a, 2b passing: identity/org/users/acceptance, catalog/inventory/customers/batch/invoice-numbers, finance/expenses/purchasing/sales; remaining: HR/attendance/payroll, services/reports/inbox/system/audit/recovery/files, OpenAPI contract — see `docs/database/HANDOFF.md`)
- [~] Update the web client (permission codes, role editor, user form, removed features — aligned for first 12 ported modules in `ecefcb3`/`85d914a`; re-audit services/reports/inbox/system after backend tests finish)
- [ ] OpenAPI
- [ ] Legacy data migration tool, mapping and reconciliation
- [ ] Deployment, cutover, rollback and backup documents
