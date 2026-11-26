# Local implementation tasks

These tasks are for the local app on `main`. They cover operator gaps that the approved schema still supports. They do not cover production deploy, and they do not bring back features the schema removed (tills, shop quotations, purchase invoices, purchase returns, departments, positions, work schedules, holidays, salary-component masters, other income, owner drawings, journal, trial balance, period lock, debt write-off, batch/expiry, per-branch prices, PIN approval, staff email/SMS). Customer outbound messaging is in scope: `app.outbound_messages` exists and nothing sends it.

| # | Task | Status | Owner | File |
|---|------|--------|-------|------|
| 1 | Customer messaging (receipts, statements, debt reminders, renewal reminders, project updates) via `app.outbound_messages` | not started | Backend, then Frontend | [01-customer-messaging.md](01-customer-messaging.md) |
| 2 | Supplier statement button | not started | Frontend | [02-supplier-statement.md](02-supplier-statement.md) |
| 3 | Customer statement print button | not started | Frontend | [03-customer-statement-print.md](03-customer-statement-print.md) |
| 4 | Dashboard stock value and salaries due | not started | Frontend | [04-dashboard-stock-and-salaries.md](04-dashboard-stock-and-salaries.md) |
| 5 | Expenses filtered by location | not started | Backend, then Frontend | [05-expenses-by-location.md](05-expenses-by-location.md) |
| 6 | Report charts | not started | Frontend | [06-report-charts.md](06-report-charts.md) |
| 7 | Reprinted receipt marked Copy | not started | Backend, then Frontend | [07-reprinted-receipt-copy.md](07-reprinted-receipt-copy.md) |
| 8 | In-app backup restore decision | not started | Backend or docs | [08-backup-restore.md](08-backup-restore.md) |
