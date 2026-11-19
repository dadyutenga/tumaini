# Backend completion progress

Last updated at commit `a8f286a` on branch `claude/loving-noether-4idp7v`. The final account is `BACKEND_IMPLEMENTATION_REPORT.md`; the baseline analysis is `BACKEND_IMPLEMENTATION_GAP_ANALYSIS.md`.

## Counts

| Measure | Baseline | Now |
| --- | --- | --- |
| Routes in the API reference | 313 | 313 implemented |
| Operations in `docs/api/openapi.yaml` | none | 323 on 260 paths |
| Functional requirements (SRS) | 261: 227 Must, 26 Should, 8 Could | see below |
| Migrations | 12 | versions 1 to 26 (no 18), all with working down files |
| Test functions | 2 | 99 |
| Statement coverage | not measured | 78% of about 15,000 statements |

Requirement status: Must and Should requirements are implemented except the gaps under "Known open items" in the report; 7 of 8 Could requirements are implemented. FR-EXP-12 (an expense owed to a supplier) is deliberately not built.

## Done and verified

Each item has integration tests that drive the HTTP handlers against PostgreSQL 16 as `bms_app`.

- Infrastructure: shared wiring (`deps`, one `Mount` per module), validated configuration, structured logs with `LOG_LEVEL`, request IDs, health and readiness, graceful shutdown, scheduler and job queue with panic recovery, trusted-proxy handling, bounded rate limiter.
- Identity: login, lockout, rotating refresh with replay detection, logout, token tampering, password and PIN with guess limits, forgot password, sessions, idle timeout, new-device notice, users, roles, delegation guards, last-Owner rule.
- Organisation, catalog, inventory, purchasing, sales, customers, expenses, finance, HR, attendance, payroll, reports, approvals, notifications, audit, exports: every route of the API reference, with the business rules the SRS acceptance table gives.
- Background jobs: recurring expenses, attendance close, salary due, debt reminders, owner summary, ledger and stock checks, cleanup, notification delivery, payroll calculation, business export.
- Optional requirements: clock-in network restriction, employee documents, batch and expiry per receipt, password recovery, email and SMS senders.
- Documentation: OpenAPI generated from `base.yaml` and per-module fragments, with a contract test; backend README, `.env.example`, Makefile, backup script, backend CI workflow.

## Tests executed (final commit before this file)

```
gofmt -l .                      nothing
go vet ./...                    clean
go run ./cmd/openapi -check     up to date
go test -race -count=1 ./...    12 packages ok
```

Also run: migrations up, down to version 0 and up again on an empty database; the compiled binary against a clean database (health, readiness, register, authenticated request, 401 without a token, SIGTERM stop, zero error logs); `deploy/backup.sh` end to end.

No test currently fails. Failures met on the way and fixed are listed under "Bugs found by testing".

## In progress

The audit's confirmed security and correctness findings are fixed (see the report, "Audit fixes"). Remaining audit items are listed under "Known open items" in the report and are being worked through in the order: audit-log coverage, report exports and filters, smaller requirement gaps.

## Real blockers (cannot be resolved from this environment)

| Blocker | Effect |
| --- | --- |
| No Docker daemon | the image and Compose were corrected but not built |
| No SMTP or SMS provider credentials | senders are tested against local fakes only |
| No off-server backup target, no host cron | `deploy/backup.sh` works locally; scheduling and restore tests are the operator's |
| No load-test environment | NFR-01, 03 and 04 are not measured |
| GitHub Actions not run | the workflow mirrors the local commands |

## Significant engineering decisions

- **Keep the existing stack** (hand-written pgx queries, an in-process PostgreSQL job queue) rather than the sqlc and River the design names; both are tested and replacing them would not satisfy any requirement.
- **Modules mount themselves** from one `deps.Deps`, so parallel work touched disjoint files and `app.go` stays stable.
- **Migrations are run by `cmd/migrate` as `bms_owner`, never by the API** (the app role has no schema rights by design). `AUTO_MIGRATE` was removed.
- **Decimals are rendered at their scale** in every response (`"1000.00"`, `"3.000"`, `"1200.0000"`) by one normaliser in `httpx`, matching the API reference. The frontend parses with `Number()` and is unaffected.
- **Updates are partial.** Several PUT handlers reset omitted fields to Go zero values; they now use optional fields, and `parent_id` is changed only when sent.
- **Authority rules live in the service layer**, not only on routes: scope returns 404, approval limits return 202, and delegation guards stop a delegated administrator from promoting themselves.
- **Reports and uploads**: PDFs from a small built-in writer; uploads decided by content against an allow-list.
- **Optional requirements chosen**: expiry is recorded per receipt, not tracked per batch of stock; clock-in restriction is by network address only; FR-EXP-12 is left out because supplier debt is built on purchase invoices.

## Bugs found by testing and fixed

Access tokens checked against wall time; category, unit and tax-rate updates resetting fields; settings accepting any value; `ledger.Reverse` re-reversing; expense rejection note; loans after a payment void; approver using the wrong till; stock out and in posting to the wrong account; PO rejection doing nothing; delegated admins creating Owners; PIN guessing unlimited; password change leaving sessions alive; dead configuration; session timeout, notification rules and invoice prefix stored but never used; recovery SQL type error; HR numeric scale; goods receipt dropping batch fields.
