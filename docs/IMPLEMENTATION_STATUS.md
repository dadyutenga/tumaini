# BMS implementation status

Updated 19 November 2026. Backend and frontend are both in `main`.

## Backend

Every route in `docs/spec/API reference.md` is implemented, and `TestOpenAPIContract` verifies bidirectional exact parity between registered Fiber routes and `docs/api/openapi.yaml`. All backend tests run against PostgreSQL 16 with row-level security (`go test ./...`, with `DATABASE_URL` and `MIGRATE_DATABASE_URL` set).

Key platform capabilities across both Business A (Shop) and Business B (Services):
- **Shop & Inventory**: Products (photos, warranty, serial/IMEI tracking, barcode label PDF, Excel/CSV export & import), POS with split payments & offline queue, thermal narrow-format receipt PDFs (58mm/80mm), quotation PDFs, stock transfers, stock counts, purchasing.
- **Services & Design Jobs**: Clients/leads, service catalog with packages, projects with stage timeline history & checklists, quotations, invoices with installments, service payment receipt PDFs, contractor statement PDFs, design versions with inline preview & private client approval link, extra revision charges, print orders (including voiding linked costs on cancel), renewals, tasks & calendar.
- **Finance**: Money accounts (with safe balance masking for POS/Till staff), transfers, payments with vouchers & voids, unified Debts & Aging (Shop customers + Service clients + Suppliers + Contractors), End-of-Day Closing with Owner-only Reopen, expenses with receipt photos & inline approval, period lock.
- **HR & Payroll**: Employee detail, statement, documents, salary components, project-based & sales commissions, monthly attendance summary & corrections, payslip manual line edits, payroll reversal, leave, loans/advances.
- **Security, Admin & Settings**: Global search (`/search`), forced first-login password change, 30-min inactivity timeout, per-user permission overrides (`/users/:id/permissions`), active sessions & login history, polymorphic record attachments (`/attachments`), backups/exports, business logo upload, payment-method account mapping, 25-event notification rules + templates + test notification endpoint.

Password recovery needs an SMTP or SMS webhook configured. Without one, `POST /auth/forgot-password` answers 503 `recovery_unavailable`, and the Owner resets passwords from Team.

## Frontend screens

| Area | Screen | Notes |
| --- | --- | --- |
| Overview | Dashboard / Staff Home | Admin/Manager dashboard (period & Business A/B filters, charts, unified debts, tasks, renewals, approvals) and Staff Home screen (clock in/out, New Sale / New Job / Receive Payment) |
| Shop | Point of sale, Sales, Sale detail, Quotations | Quick-tap product grid, serial/IMEI, split payment, discount, credit due date, offline POS queue, thermal receipt PDF & WhatsApp, returns, void, quotation PDF |
| Stock | Products, Product detail, Stock, Transfers and counts | Catalog & table views, categories CRUD, Excel/CSV import & export, barcode label PDF, price & stock history, transfers, counts |
| Purchasing | Purchasing, Purchase order | Suppliers, orders, receiving, invoices, supplier payments, attachments |
| Customers | Customers, Customer | Statement, payments, credit limit, write-off, aged debts, attachments |
| Finance | Money, Debts, Day closing, Expenses, Expense setup | Clickable account cards, filtered/paginated transactions + CSV, unified Debts & Aging with WhatsApp reminders, End-of-Day cash count & closing (Owner reopen), expenses with receipt photo preview & inline approve/reject |
| People | People, Employee detail, Attendance, HR setup, My work | Full employee detail (edit, statement, components, documents, login, terminate), monthly attendance summary & corrections, leave, overtime, working week, loans |
| Payroll | Payroll, Payroll run, Payroll setup | Calculate, edit payslip lines, approve, pay, reverse payroll, payslip PDF & WhatsApp share, sales & project commissions |
| Services | Services home, Clients, Client, Projects, Project, Service catalog, Service quotations, Service invoices, Design jobs, Design job, Client documents, Tasks & calendar, Renewals, Contractors | Inline file preview, stage history, extra revision charge, print orders tab, WhatsApp updates/reminders, service receipt PDF, contractor statement PDF |
| Public | Client approval page at `/approve/:token` | No sign-in. Shows the design, one decision per link, 14-day expiry |
| Insights | Reports, Approvals, Notifications | Reports Home with Business A/B P&L breakdown, charts, Print, CSV/XLSX/PDF export, Projects & Design Jobs reports, full Notifications center |
| Admin | Team and roles, Activity log, Login history, Backups, Settings | Per-user permission overrides, reset password, force sign out, audit diff + CSV, login history, data export backups, business logo, payment method mapping, live receipt preview, notification rules & test message |
| Auth & UX | Sign in, Register, Forgot password, My account | Forced first-login password change, inactivity warning modal, global search (`Ctrl+K`), `+ New` menu, English/Swahili language switching, sortable `DataGrid` |

## Remaining operational items

- Configure production SMTP / SMS provider credentials for external password recovery and SMS delivery.
- Review `npm audit` findings in frontend dev dependencies.
