# Backend implementation gap analysis

Baseline: commit `3e82692` (16 Nov 2026), before this round of work. Sources of truth: `docs/spec/SRS.md`, `docs/spec/API reference.md`, `docs/spec/Backend design.md`, `docs/spec/Database schema.md`. The website screen spec's services scope (projects, design jobs, renewals) was out of scope at the time of this analysis. It was added afterwards as section 9 of the API reference and section 9 of the database schema.

## Method

1. Every route in the API reference was extracted (313 method and path pairs) and compared with the routes registered in each module's `Register` function (124 registered).
2. Each module was then read from route to SQL to check behaviour, not only the route's presence.
3. The acceptance test was run against PostgreSQL 16 as the `bms_app` role.

## Headline findings

| Finding | Severity |
| --- | --- |
| The acceptance test failed: access tokens were signed with the injected clock and verified against wall time, so with any non-system clock a fresh token looked expired (`401 invalid_token` on the second request) | Critical (BROKEN) |
| 190 of 313 documented routes did not exist | Critical (MISSING) |
| HR, attendance and payroll had only create endpoints; payroll ignored components, overtime, commission, bonuses, unpaid leave and statutory deductions | Critical (PARTIAL) |
| No list endpoints for expenses, purchase orders, suppliers, employees, payments, loans, payroll runs | High (MISSING) |
| Only 1 of 34 report routes existed; no dashboard | High (MISSING) |
| Daily jobs (recurring expenses, debt reminders, salary due, attendance close, summaries, integrity checks, cleanup) were declared as constants but never scheduled or implemented | High (MISSING) |
| `GET /files/:id` let any user of the business download any file | High (security) |
| Approval decide did not lock the request row and did not check the decider's branch scope | High (security, concurrency) |
| Expense rejection stored the request summary as the reject reason | Medium (BROKEN) |
| Loans were created as approved with no limit check; payroll and loans used `CURRENT_DATE` instead of the application clock | Medium (BROKEN) |
| Configuration silently fell back to defaults on malformed values | Medium |
| No OpenAPI file and no contract test | High (MISSING) |
| No backend CI workflow (only the frontend has one) | Medium (MISSING) |

## Traceability matrix

State at baseline. COMPLETE means implemented and covered by the acceptance test.

### Authentication, users, settings, branches (FR-AUTH, FR-USR, FR-SET, FR-BRN)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-AUTH-01..08 login, register, tokens, lockout, password, PIN, sessions, deactivation | BROKEN | Token expiry checked against wall clock | Critical | Acceptance test step 2 failed with 401 |
| FR-AUTH-09 forgot password | MISSING | Needs a sender; not in API reference | Low (C) | n/a |
| FR-USR-01..10 users, roles, scopes, limits, login history, last Owner | COMPLETE | None found | — | Acceptance test (user creation, 403) |
| FR-SET-01..10 profile, tax, currency, payment methods, invoice, receipt, payroll, notification, system settings, audit | COMPLETE | Notification channels had no delivery | Medium | Acceptance test (tax rate, footer) |
| FR-BRN-01..05, 07 branches | COMPLETE | — | — | Acceptance test |
| FR-BRN-06 branch performance | MISSING | `/reports/branches` | High | — |

### Inventory and catalog (FR-INV)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-INV-01..11 products, categories, units, import, price history | COMPLETE | Product list filters (`low_stock`, `category_id`) | Medium | Acceptance test |
| FR-INV-12..15 stock in and out | PARTIAL | `GET /stock-entries`, `/stock-entries/:id`; stock-out reason to expense account | High | Acceptance test (stock in) |
| FR-INV-16..19 adjustments, stock take | PARTIAL | List, edit, cancel, count sheet | High | — |
| FR-INV-20..25 transfers | PARTIAL | List, detail, edit, cancel | High | Acceptance test (shortfall) |
| FR-INV-26..29 low stock, valuation, stock card | COMPLETE | `GET /stock/movements` | Medium | Acceptance test |
| FR-INV-30..31 movement-only stock, negative stock | COMPLETE | — | — | Acceptance test |

### Sales and customers (FR-SAL, FR-CUS)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-SAL-01..07, 10, 21, 22 POS sale, tills, payments, credit, idempotency, cost snapshot | COMPLETE | Till close ignored pay-outs | — | Acceptance test |
| FR-SAL-08 discount approval incl. PIN | PARTIAL | `approve-with-pin` route | High | — |
| FR-SAL-09 held carts | MISSING | `/sales/hold`, `/sales/held` | Medium (S) | — |
| FR-SAL-11, 12 receipt COPY, invoice PDF | PARTIAL | COPY mark, `/sales/:id/invoice` | High | — |
| FR-SAL-13 quotations | MISSING | `/quotations*` | Low (C) | — |
| FR-SAL-14..17 returns | PARTIAL | Return lists and detail | Medium | Acceptance test |
| FR-SAL-18 void | MISSING | `/sales/:id/void` with approval | High | — |
| FR-SAL-19, 20 history filters, detail with payments, returns, audit | PARTIAL | Filters and detail sections | High | — |
| FR-SAL-23 commission base | MISSING | `sales.employee_id` not set | Medium (S) | — |
| FR-SAL-04 / FR-REP-04 till list, report, pay-out | MISSING | `/tills`, `/tills/:id`, `/tills/:id/pay-out` | High | — |
| FR-CUS-01..09 customers, credit, payments, statement | COMPLETE | `/customers/:id/unpaid`, deactivate | Medium | Acceptance test |
| FR-CUS-11 debt reminders | MISSING | Daily job | Medium (S) | — |
| FR-CUS-12 write-off | MISSING | `/customers/:id/write-off` | Medium (S) | — |

### Suppliers and purchases (FR-SUP, FR-PUR)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-SUP-01..07 suppliers, payments, statements | PARTIAL | List, detail, edit, deactivate, purchases, unpaid, statement, debts | High | Acceptance test (payment) |
| FR-PUR-01..07, 09, 10, 13 PO, receipts, invoices | PARTIAL | Lists, detail views, edit, cancel, close, PDF | High | Acceptance test |
| FR-PUR-08 extra costs | UNVERIFIED | Test | Medium | — |
| FR-PUR-11 purchase returns | MISSING | `/purchase-returns*` | High | — |
| FR-PUR-12 purchase history | MISSING | Filtered lists | High | — |
| FR-PUR-14 suggested PO | MISSING | `/purchase-orders/suggest` | Low (C) | — |

### Expenses and finance (FR-EXP, FR-FIN)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-EXP-01..04 submit, approve, pay | PARTIAL | List, edit, approve/reject routes, reject note bug | High | Acceptance test |
| FR-EXP-05 reverse | MISSING | `/expenses/:id/reverse` | High | — |
| FR-EXP-06, 07 petty cash | MISSING | `/petty-cash*` | High | — |
| FR-EXP-08, 09 recurring expenses | MISSING | Templates and daily job | High | — |
| FR-EXP-10, 11 list filters, own scope | MISSING | `GET /expenses` | High | — |
| FR-FIN-01..05, 09, 17, 21 money accounts, transfers | COMPLETE | Detail, edit, deactivate | Medium | Acceptance test |
| FR-FIN-06..08, 13, 20 payment lists, voucher | MISSING | `/payments*` | High | — |
| FR-FIN-11, 12 income, drawings | MISSING | `/income*`, `/drawings` | High | — |
| FR-FIN-14 transactions history | PARTIAL | Filters, running balance, pagination | High | — |
| FR-FIN-15 payment void | MISSING | `/payments/:id/void` | High | — |
| FR-FIN-16 reconciliation | MISSING | `/money-accounts/:id/reconcile` | Medium (S) | — |
| FR-FIN-18 hide bank balances | COMPLETE | — | — | Code review |
| FR-FIN-19 period lock | PARTIAL | Enforced in ledger; no `/periods/lock` routes | High | — |
| Statutory remittance, ledger entries | MISSING | `/statutory-payments`, `/ledger/entries` | Medium | — |

### Employees, attendance, payroll (FR-EMP, FR-ATT, FR-PAY)

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-EMP-01..05 profiles, terminate, departments, positions | PARTIAL | Only create; no list, detail, edit, terminate, departments, positions | Critical | — |
| FR-EMP-07..09 leave | MISSING | Types, requests, decisions, balance, payroll effect | High | — |
| FR-EMP-10..12 loans | PARTIAL | List, detail, repay, approval limit | High | Acceptance test (disburse) |
| FR-EMP-13..15 commission, bonuses, deductions | MISSING | Rules, adjustments, payroll use | High | — |
| FR-EMP-17, 18 statements, self-service | MISSING | `/employees/:id/statement`, `/me/*` | High | — |
| FR-ATT-01, 02 clock in and out, manual entry | PARTIAL | Clock-out, manager clocking, events | Critical | Acceptance test (clock-in) |
| FR-ATT-03..10 schedules, statuses, history, late, overtime, holidays, summary | PARTIAL | Most routes missing | High | — |
| FR-ATT-09, 11 missing clock-out flag, lock on payroll | MISSING | Daily close job, lock | High | — |
| FR-PAY-01..04 components | PARTIAL | Only a fixed allowance for all | Critical | — |
| FR-PAY-05..09 runs, calculation | PARTIAL | Prorating, overtime, commission, deductions, brackets | Critical | Acceptance test (simple case) |
| FR-PAY-10 recalc and manual lines | MISSING | `/payroll-runs/:id/payslips/:pid/lines` | High | — |
| FR-PAY-11..13 approve, partial pay, lock | PARTIAL | Submit step, partial pay | High | Acceptance test |
| FR-PAY-14..16 payslips, payment history, CSV | MISSING | Routes | High | — |
| FR-PAY-17 reversal | MISSING | `/payroll-runs/:id/reverse` | High | — |
| FR-PAY-18 salary due | MISSING | Daily job | Medium | — |

### Approvals, notifications, security, reports, dashboard

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| FR-APR-01..04 requests and decisions | PARTIAL | Row lock, branch scope, kinds for void, loan, leave, overtime, payroll, payment void | High | Acceptance test (expense) |
| FR-APR-05, 06 inbox, count, cancel | MISSING | `/approvals/:id`, `/count`, `/cancel` | High | — |
| FR-APR-07 PIN approval | MISSING | `/approvals/:id/approve-with-pin` | Medium (S) | — |
| FR-NTF-01 bell, unread count, read | PARTIAL | `/unread-count`, `/:id/read`, `/read-all` route | High | — |
| FR-NTF-02..08, 11 event notifications | PARTIAL | Low stock and approvals exist; reminders, salary due, summary missing | High | — |
| FR-NTF-10 email and SMS | MISSING | Sender integration | Low (C) | — |
| FR-SEC-01..03, 08, 09 audit and login logs | PARTIAL | Filters, entity history route | High | — |
| FR-SEC-06 data export | MISSING | `/backups*`, `/exports/:id` | High | — |
| FR-SEC-07 nightly backup | MISSING | Host script | High | — |
| FR-REP-01..17 reports | PARTIAL | 1 of 34 routes (profit and loss); no exports | High | — |
| FR-DSH-01..10 dashboard | MISSING | `/dashboard`, `/dashboard/me` | High | — |

### Non-functional and engineering

| Requirement | Current state | Missing work | Priority | Verification |
| --- | --- | --- | --- | --- |
| NFR-02 pagination on all lists | PARTIAL | Several lists unbounded or `LIMIT 100` without meta | High | Code review |
| NFR-06 one transaction per multi-step write | COMPLETE for existing flows | Keep for new flows | — | Code review |
| NFR-09 RLS | COMPLETE | New tables need policies | — | Acceptance test (tenancy) |
| NFR-16 tests | PARTIAL | One acceptance test, one unit test | High | — |
| NFR-17 observability | COMPLETE | — | — | Code review |
| OpenAPI and contract test | MISSING | `docs/api/openapi.yaml` and a test | High | — |
| Backend CI | MISSING | Workflow with Postgres service | Medium | — |

## Decisions recorded

- The design names sqlc and River. The code uses hand-written pgx queries and a small PostgreSQL job table. Both work and are tested, so they are kept; replacing them is churn without a requirement behind it.
- PDFs are written by a small built-in writer (`internal/platform/pdf`) instead of headless Chrome, so the single binary has no browser dependency.
- Large report exports stay synchronous; the business-wide data export runs as a background job.

## Final status

This file is the baseline. The state after the work, including the second audit, the fixes made and the gaps still open, is in `BACKEND_IMPLEMENTATION_REPORT.md` (sections "Audit fixes" and "Known open items") and `BACKEND_COMPLETION_PROGRESS.md`.
