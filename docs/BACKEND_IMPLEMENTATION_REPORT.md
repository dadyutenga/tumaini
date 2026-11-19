# Backend implementation report

Status after the acceptance run on 16 Nov 2026.

## Verified

`go test ./internal/modules/payroll/ ./internal/app/ -count=1` passed.

The acceptance test, against a real PostgreSQL 16 database and the `bms_app` role, covered:

- Business registration, owner, manager, and cashier
- Cashier forbidden from creating products
- A manager cannot read a sale that belongs to a branch they are not assigned
- Stock in at two costs, weighted average 1,500, quantity 20, value 30,000, two stock-card lines
- Cash sale of 3, stock 17, cash up 7,500, profit 3,000
- The same idempotency key does not create a second sale
- Return of 1, then a return larger than the remainder is rejected
- A later tax rate and receipt footer do not change the earlier sale
- Credit limit blocks a sale, a higher limit allows it, and a payment reduces the balance
- Purchase order, two receipts, invoice, and supplier payment
- Transfer of 10 with 9 received, recorded as a difference
- An expense above the cashier limit waits for approval, then is paid from petty cash
- A money transfer leaves the trial balance equal
- Clock-in 20 minutes after start with a 10 minute grace is late by 20 minutes, and the day stays flagged until clock-out
- Payroll for base 600,000, allowance 50,000, 2 unpaid days of 26, and a 50,000 loan pays net 553,846.16 and locks the run
- A second business sees none of the first business's products or sales

## Modules with working handlers

| Module | What works |
| --- | --- |
| Identity | Register, login, refresh, logout, password, PIN, users, roles, sessions, login history |
| Organisation | Business profile, branches, settings groups, tax rates, payment methods, file upload |
| Catalog | Products, categories, units, barcode lookup, branch prices, price history, CSV/Excel import |
| Inventory | Stock in and out, levels, valuation, stock card, adjustments, transfers |
| Sales | Till open and close, preview, complete sale, receipt snapshot, returns |
| Customers | Customers, credit limit, allocation of payments oldest first, statement, aged debt |
| Purchasing | Suppliers, purchase orders, goods receipts, invoices, supplier payments |
| Expenses | Submit, approve through the inbox, pay from a money account, categories |
| Finance | Money accounts, transfers, transaction list, trial balance |
| HR | Employees, loans, disbursement |
| Attendance | Clock-in, schedules, manual day status |
| Payroll | Components, calculate, approve, pay |
| Reports | Profit and loss |
| Inbox | Approval decide, notifications, audit log |

## Still open

These are specified and not built yet:

- Quotations, held carts, sale void, till pay-out
- Purchase returns, recurring expenses, petty-cash reconcile, period lock
- Leave, commissions, payslip PDF, payment-file export
- Dashboard cards and the rest of the report catalogue
- OpenAPI file and the contract test that checks it
- Forgot-password delivery. The sender interface is not wired to SMS or email
- The website screen spec's services business was added on 19 November 2026 (migration 0020, section 9 of the API reference)

## Security that is in place

- Argon2id passwords, 15 minute access tokens, rotating refresh tokens stored as SHA-256
- Five failed logins in 15 minutes lock the account and notify someone who can manage users
- Default-deny permissions, branch and own-record filters, PostgreSQL row-level security
- Idempotency keys on sale, stock, payment, transfer, and expense posts
- Journal lines must balance, and posted money is not edited in place
- Local development passwords live only in `deploy/postgres/init.sql` and `.env.example`

## How to continue

1. Add the missing sales and finance endpoints listed above, reusing `ledger`, `stock`, and `approval`.
2. Write `docs/api/openapi.yaml` from the routes in each `Register` function, then add a test that fails when a route is missing from the spec.
3. Add dashboard and report queries as read-only SQL. Do not introduce a second store.
