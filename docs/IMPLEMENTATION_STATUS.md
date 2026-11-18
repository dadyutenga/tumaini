# BMS implementation status

Updated 18 November 2026. Backend and frontend are both in `main`.

## Backend

Every route in `docs/spec/API reference.md` is implemented, and `TestOpenAPIContract` fails if a route is missing from `docs/api/openapi.yaml` or if the reference lists a route that is not registered. The backend tests run against PostgreSQL with row-level security (`go test ./...`, with `DATABASE_URL` and `MIGRATE_DATABASE_URL` set).

Password recovery needs an SMTP or SMS webhook configured. Without one, `POST /auth/forgot-password` answers 503 `recovery_unavailable`, and the Owner resets passwords from Team.

## Frontend screens

| Area | Screen | Notes |
| --- | --- | --- |
| Sales | Point of sale, Sales, Sale detail, Quotations | Till open/close/pay-out, held carts, returns, void, invoice PDF, quotation to sale |
| Stock | Products, Stock, Transfers and counts | Transfers with receive differences, count sheets, adjustments, movements |
| Purchasing | Purchasing, Purchase order | Suppliers, orders, receiving, invoices, supplier payments |
| Customers | Customers, Customer | Statement, payments, credit limit, write-off, aged debts |
| Money | Money | Accounts, transfers, payments with void, income, period lock |
| Expenses | Expenses, Expense setup | List and filters, categories, recurring, petty cash top-up and count |
| People | People, Attendance, HR setup, My work | Employees, leave, clock in/out, overtime, working week, departments, positions, holidays, loans, staff self-service |
| Payroll | Payroll, Payroll run, Payroll setup | Calculate, approve, pay, payment list, payslip PDF, components, commission, adjustments |
| Reports | Reports | The whole catalogue with date ranges and CSV, XLSX and PDF export |
| Admin | Team and roles, Settings, Activity log, Approvals | Role and permission editor, business, branches, tax, preferences |
| Auth | Sign in, Register, Forgot password, My account | |

Each screen shows only what the signed-in role can use.

## Still open

- Employee detail page, employee statement, per-employee salary components and termination UI. The APIs exist.
- Manual payslip line edits, payroll reversal and payroll reports beyond the catalogue.
- Product import (CSV/Excel), price history, branch prices, stock card and valuation pages. The APIs exist.
- Branch detail page with staff, stock and performance tabs.
- Notifications page (the bell shows recent ones), login history and session management screens.
- Settings: payment-method to account mapping, logo upload.
- English and Swahili switching on the newer pages.
- The Staff role has no `accounts.view`, so staff cannot pick a mobile-money or bank account at the till.
- The website spec's Services scope (projects, design jobs, renewals) is not in the SRS or API reference and is not built.
- `npm audit` reports vulnerabilities in dev dependencies that have not been reviewed.
