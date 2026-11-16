# API reference

Every endpoint, the permission it needs and what it does. Base path `/api/v1`.

## 1. Conventions

| Topic | Rule |
| --- | --- |
| Base | `/api/v1`, JSON in and out, UTF-8 |
| Auth | `Authorization: Bearer <access token>` on every route except `/auth/login`, `/auth/register`, `/auth/refresh` |
| Branch | `X-Branch-Id: <uuid>` on branch-scoped routes. Users with scope `all` may send `all` on GET routes |
| Idempotency | `Idempotency-Key: <uuid>` on POST routes marked (I). Same key and same body returns the first response. Same key and different body returns 409 |
| Pagination | `?page=1&per_page=25`, maximum 100. Response has `meta: {page, per_page, total}` |
| Sorting | `?sort=-created_at` (minus for descending), whitelisted fields only |
| Filtering | Query parameters: `from`, `to` (dates), `status`, `branch_id`, `q` (search) and resource-specific ones |
| Money and quantity | Strings with decimals, for example `"2500.00"`, never floats |
| Dates | `YYYY-MM-DD` for business dates, RFC 3339 UTC for timestamps |
| Success | `{ "data": ... }` or `{ "data": [...], "meta": {...} }` |
| Error | `{ "error": { "code", "message", "fields" } }` |
| Pending approval | Status 202 with `{ "data": { "approval_request_id", "status": "pending_approval" } }` |
| Exports | Add `?format=xlsx` or `?format=pdf` to report routes. Large exports return 202 with an export job ID |

In the tables below the Permission column is the key checked by the route. Scope (all, branch, own) then limits which rows are visible.

## 2. Auth, users, roles, business, branches, settings

| Method and path | Permission | Does |
| --- | --- | --- |
| POST `/auth/register` | public | Create business, main branch, defaults and Owner |
| POST `/auth/login` | public | Return access token, set refresh cookie |
| POST `/auth/refresh` | cookie | Rotate refresh token, return new access token |
| POST `/auth/logout` | any | End current session |
| POST `/auth/pin` | any | Exchange PIN for a fresh access token on a known device |
| PUT `/auth/password` | any | Change own password |
| PUT `/auth/pin` | any | Set or change own PIN |
| GET `/me` | any | Profile, role, permission map with scopes and limits, branches, business settings needed by the UI |
| PUT `/me` | any | Update own name, language |
| GET `/me/sessions` | any | List own sessions |
| DELETE `/me/sessions/:id` | any | End one of own sessions |
| GET `/users` | `users.view` | List users |
| POST `/users` | `users.manage` | Create user with role and branches |
| GET `/users/:id` | `users.view` | User detail |
| PUT `/users/:id` | `users.manage` | Edit user, role, branches |
| POST `/users/:id/deactivate`, `/activate` | `users.manage` | Change active state, end sessions |
| POST `/users/:id/reset-password` | `users.manage` | Set a temporary password |
| DELETE `/users/:id/sessions` | `sessions.manage` | End all sessions of a user |
| GET `/login-history` | `login_history.view` | Login attempts |
| GET `/permissions` | `roles.view` | Permission catalogue grouped by module |
| GET `/roles` | `roles.view` | List roles |
| POST `/roles` | `roles.manage` | Create custom role |
| GET `/roles/:id` | `roles.view` | Role with permissions, scopes, limits |
| PUT `/roles/:id` | `roles.manage` | Rename, replace permissions, scopes and limits |
| DELETE `/roles/:id` | `roles.manage` | Delete a custom role with no users |
| GET `/business` | `settings.view` | Business profile |
| PUT `/business` | `business.manage` | Update profile |
| POST `/business/logo` | `business.manage` | Upload logo |
| GET `/branches` | `branches.view` | List branches |
| POST `/branches` | `branches.manage` | Create branch with sequences and default cash account |
| GET `/branches/:id` | `branches.view` | Branch detail |
| PUT `/branches/:id` | `branches.manage` | Edit branch |
| POST `/branches/:id/deactivate` | `branches.manage` | Deactivate if empty |
| GET `/branches/:id/staff` | `branches.view` | Employees and users of the branch |
| GET `/settings` | `settings.view` | All settings grouped |
| PUT `/settings/:group` | `settings.manage` | Update a group: `tax`, `currency`, `invoice`, `receipt`, `payroll`, `notifications`, `system` |
| GET, POST `/tax-rates`; PUT `/tax-rates/:id` | `settings.manage` | Manage tax rates |
| GET `/payment-methods`; PUT `/payment-methods` | `settings.manage` | Enable methods and map to money accounts |
| POST `/files` | any | Upload a file, returns file ID |
| GET `/files/:id` | owner of record | Download a file |

## 3. Products and inventory

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/products` | `products.view` | List and search. `q`, `category_id`, `is_active`, `low_stock=true`. Returns stock for the active branch. Cost fields only with `products.view_cost` |
| GET `/products/lookup?barcode=` | `products.view` | Exact barcode or SKU match for the POS |
| POST `/products` | `products.manage` | Create product |
| GET `/products/:id` | `products.view` | Detail with stock per branch |
| PUT `/products/:id` | `products.manage` | Edit. Price change writes price history |
| POST `/products/:id/deactivate`, `/activate` | `products.manage` | Toggle active |
| PUT `/products/:id/branch-prices` | `products.manage` | Set per-branch prices |
| GET `/products/:id/price-history` | `products.view` | Price changes |
| GET `/products/:id/stock-card` | `stock.view` | Movements with running balance for a branch and period |
| POST `/products/import` | `products.import` | Upload CSV or Excel, returns validation report. `?commit=true` saves |
| GET, POST `/categories`; PUT, DELETE `/categories/:id` | `categories.manage` (GET: `products.view`) | Manage categories |
| GET, POST `/units`; PUT `/units/:id` | `categories.manage` | Manage units |
| GET `/stock/levels` | `stock.view` | Quantity and value per product for a branch |
| GET `/stock/low` | `stock.view` | Products at or below reorder level |
| GET `/stock/movements` | `stock.view` | All movements, filter by product, type, date |
| GET `/stock/valuation?as_of=` | `valuation.view` | Valuation by product, category, branch |
| GET `/stock-entries` | `stock.view` | List Stock In and Stock Out entries |
| POST `/stock-entries` (I) | `stock.in` or `stock.out` by direction | Create and post a stock entry |
| GET `/stock-entries/:id` | `stock.view` | Entry detail |
| GET `/stock-adjustments` | `stock.view` | List adjustments |
| POST `/stock-adjustments` | `stock.adjust` | Create draft with counted quantities |
| POST `/stock-adjustments/count-sheet` | `stock.adjust` | Create a stock take draft for a branch or category, prefilled with system quantities |
| PUT `/stock-adjustments/:id` | `stock.adjust` | Edit draft |
| POST `/stock-adjustments/:id/post` (I) | `stock.adjust` | Post, or 202 if above limit |
| POST `/stock-adjustments/:id/cancel` | `stock.adjust` | Cancel draft |
| GET `/stock-transfers` | `transfers.view` | List, `direction=in` or `out` relative to active branch |
| POST `/stock-transfers` | `transfers.create` | Create draft |
| GET `/stock-transfers/:id` | `transfers.view` | Detail |
| PUT `/stock-transfers/:id` | `transfers.create` | Edit draft |
| POST `/stock-transfers/:id/dispatch` (I) | `transfers.dispatch` | Remove stock from sender |
| POST `/stock-transfers/:id/receive` (I) | `transfers.receive` | Body: received quantity per item. Adds stock to receiver, records differences |
| POST `/stock-transfers/:id/cancel` | `transfers.create` | Cancel draft |

## 4. Sales, POS, customers

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/tills/current` | `tills.open_close` | The caller's open till session or null |
| POST `/tills/open` | `tills.open_close` | Open a session on a cash account with opening float |
| POST `/tills/:id/close` | `tills.open_close` | Close with counted cash. Returns expected, counted, difference |
| POST `/tills/:id/pay-out` (I) | `tills.open_close` | Take cash out of the till to the safe or bank as a money transfer |
| GET `/tills` | `tills.view` | List sessions |
| GET `/tills/:id` | `tills.view` | Session report: sales by method, refunds, pay-outs |
| POST `/sales/preview` | `sales.create` | Price a cart: returns lines, discount, tax, total. No side effects |
| POST `/sales` (I) | `sales.create` | Create and complete a sale. 202 if discount or credit needs approval |
| POST `/sales/hold` | `sales.create` | Save a held cart |
| GET `/sales/held` | `sales.create` | List own held carts |
| DELETE `/sales/held/:id` | `sales.create` | Discard a held cart |
| GET `/sales` | `sales.view` | Sales history. Filters: `from`, `to`, `status`, `customer_id`, `cashier_id`, `method`, `q` |
| GET `/sales/:id` | `sales.view` | Detail with items, payments, returns, audit trail |
| GET `/sales/:id/receipt` | `sales.view` | Receipt data for printing |
| GET `/sales/:id/invoice?format=pdf` | `sales.view` | Invoice PDF |
| POST `/sales/:id/void` (I) | `sales.void` | Void same-day sale with reason, or 202 |
| POST `/sales/:id/payments` (I) | `payments.receive` | Receive a payment against this sale |
| GET `/sales/:id/returns` | `sales.view` | Returns of a sale |
| POST `/sales/:id/returns` (I) | `sales.refund_request` | Request a return. Completes at once if caller has `sales.refund_approve` within limit, else 202 |
| GET `/sale-returns` | `sales.view` | List all returns |
| GET `/sale-returns/:id` | `sales.view` | Return detail |
| GET, POST `/quotations`; PUT `/quotations/:id` | `quotations.manage` | Manage quotations |
| POST `/quotations/:id/convert` (I) | `sales.create` | Turn a quotation into a sale |
| GET `/customers` | `customers.view` | List and search, with balance |
| POST `/customers` | `customers.manage` | Create |
| GET `/customers/:id` | `customers.view` | Profile with account summary |
| PUT `/customers/:id` | `customers.manage` | Edit |
| PUT `/customers/:id/credit-limit` | `customers.credit_limit` | Set limit and terms |
| POST `/customers/:id/deactivate` | `customers.manage` | Deactivate |
| GET `/customers/:id/unpaid` | `customers.view` | Unpaid sales with balance and age |
| GET `/customers/:id/statement?from=&to=` | `customers.statement` | Statement with running balance. `format=pdf` |
| POST `/customers/:id/payments` (I) | `payments.receive` | Receive payment. Body: amount, method, account, reference, optional allocations |
| POST `/customers/:id/write-off` (I) | `customers.write_off` | Write off a bad debt |
| GET `/customers/debts` | `customers.view` | Aged debt list for all customers |

## 5. Suppliers, purchases, expenses

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/suppliers` | `suppliers.view` | List with balance owed |
| POST `/suppliers` | `suppliers.manage` | Create |
| GET `/suppliers/:id` | `suppliers.view` | Profile with account summary |
| PUT `/suppliers/:id` | `suppliers.manage` | Edit |
| POST `/suppliers/:id/deactivate` | `suppliers.manage` | Deactivate |
| GET `/suppliers/:id/purchases` | `po.view` | Orders, receipts and invoices of the supplier |
| GET `/suppliers/:id/unpaid` | `suppliers.view` | Unpaid invoices with age |
| GET `/suppliers/:id/statement?from=&to=` | `suppliers.view` | Statement with running balance |
| POST `/suppliers/:id/payments` (I) | `payments.pay` | Pay supplier. Body: amount, account, method, reference, optional allocations |
| GET `/suppliers/debts` | `suppliers.view` | Aged payables |
| GET `/purchase-orders` | `po.view` | List. Filters: status, supplier, dates |
| POST `/purchase-orders` | `po.create` | Create draft |
| GET `/purchase-orders/:id` | `po.view` | Detail with ordered, received, outstanding per line |
| PUT `/purchase-orders/:id` | `po.create` | Edit draft |
| POST `/purchase-orders/:id/submit` | `po.create` | Approve at once if within limit, else 202 |
| POST `/purchase-orders/:id/cancel` | `po.create` | Cancel if nothing received |
| POST `/purchase-orders/:id/close` | `po.approve` | Close a partially received order |
| GET `/purchase-orders/:id/pdf` | `po.view` | Printable order |
| GET `/purchase-orders/suggest?supplier_id=` | `po.create` | Lines suggested from low stock |
| GET `/goods-receipts` | `po.view` | List receipts |
| POST `/goods-receipts` (I) | `grn.create` | Receive against a PO, or direct with supplier and lines. Posts stock |
| GET `/goods-receipts/:id` | `po.view` | Receipt detail |
| GET `/purchase-invoices` | `po.view` | List invoices |
| POST `/purchase-invoices` (I) | `purchase_invoices.manage` | Record invoice for receipts. Optional `pay_now` block pays it in the same call |
| GET `/purchase-invoices/:id` | `po.view` | Invoice detail with payments |
| POST `/purchase-invoices/:id/void` (I) | `purchase_invoices.manage` | Void an unpaid invoice |
| GET `/purchase-returns` | `po.view` | List returns |
| POST `/purchase-returns` (I) | `purchase_returns.create` | Return goods to supplier |
| GET `/purchase-returns/:id` | `po.view` | Return detail |
| GET `/expenses` | `expenses.view` | List. Filters: status, category, dates, submitter |
| POST `/expenses` (I) | `expenses.create` | Submit. Auto-approved within limit, else pending |
| GET `/expenses/:id` | `expenses.view` | Detail |
| PUT `/expenses/:id` | `expenses.create` | Edit while submitted |
| POST `/expenses/:id/approve`, `/reject` | `expenses.approve` | Decide (same as deciding its approval request) |
| POST `/expenses/:id/pay` (I) | `expenses.pay` | Pay from a money account |
| POST `/expenses/:id/reverse` (I) | `expenses.reverse` | Reverse a paid expense |
| GET, POST `/expense-categories`; PUT `/expense-categories/:id` | `expense_categories.manage` | Manage categories |
| GET, POST `/recurring-expenses`; PUT `/recurring-expenses/:id` | `recurring_expenses.manage` | Manage templates |
| POST `/recurring-expenses/:id/pause`, `/resume` | `recurring_expenses.manage` | Toggle |
| GET `/petty-cash` | `petty_cash.manage` | Petty cash accounts with balance and custodian |
| POST `/petty-cash/:id/top-up` (I) | `petty_cash.manage` | Transfer money into petty cash |
| POST `/petty-cash/:id/reconcile` (I) | `petty_cash.manage` | Record a count and post any difference |

## 6. Payments and finance

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/money-accounts` | `accounts.view` | List accounts. Balance included only with `accounts.view_balance` |
| POST `/money-accounts` | `accounts.manage` | Create cash, bank, mobile money or petty cash account with opening balance |
| GET `/money-accounts/:id` | `accounts.view` | Detail |
| PUT `/money-accounts/:id` | `accounts.manage` | Edit name, provider, custodian, overdraft flag |
| POST `/money-accounts/:id/deactivate` | `accounts.manage` | Deactivate a zero-balance account |
| GET `/money-accounts/:id/statement?from=&to=` | `accounts.view_balance` | Movements with running balance |
| POST `/money-accounts/:id/reconcile` (I) | `reconcile.manage` | Record actual balance and post adjustment |
| GET `/payments` | `payments.view` | All payments. Filters: `direction`, `kind`, `party_type`, `party_id`, `account_id`, dates |
| GET `/payments/:id` | `payments.view` | Detail with allocations |
| GET `/payments/:id/voucher` | `payments.view` | Printable receipt or voucher |
| POST `/payments/:id/void` (I) | `payments.void` | Void with reason, or 202 |
| GET `/payments/customers` | `payments.view` | Customer payments list |
| GET `/payments/suppliers` | `payments.view` | Supplier payments list |
| GET `/payments/staff` | `payments.view` | Staff payments list |
| GET `/money-transfers` | `accounts.view` | List transfers |
| POST `/money-transfers` (I) | `transfers_money.create` | Move money between two accounts with optional fee |
| GET `/income` | `payments.view` | Other income list |
| POST `/income` (I) | `income.create` | Record other income or owner capital |
| GET, POST `/income-categories` | `income.create` | Manage income categories |
| POST `/drawings` (I) | `drawings.create` | Record owner drawing |
| POST `/statutory-payments` (I) | `payments.pay` | Remit statutory deductions |
| GET `/transactions` | `transactions.view` | Unified money movement history across accounts |
| GET `/ledger/accounts` | `reports.financial` | Chart of accounts with balances |
| GET `/ledger/entries` | `reports.financial` | Journal entries with lines |
| GET `/periods/lock` | `periods.lock` | Current lock date |
| PUT `/periods/lock` | `periods.lock` | Set locked-until date |

## 7. Employees, attendance, payroll

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/employees` | `employees.view` | List. Salary fields only with `employees.view_salary` |
| POST `/employees` | `employees.manage` | Create profile |
| GET `/employees/:id` | `employees.view` | Profile |
| PUT `/employees/:id` | `employees.manage` | Edit |
| POST `/employees/:id/terminate` | `employees.manage` | Terminate with date and reason |
| POST `/employees/:id/user` | `users.manage` | Create a login for the employee |
| GET `/employees/:id/statement?from=&to=` | `employees.view_salary` | Earnings, deductions, payments, loans |
| GET, PUT `/employees/:id/components` | `components.manage` | Salary structure of the employee |
| GET, POST `/departments`; PUT, DELETE `/departments/:id` | `departments.manage` | Manage departments |
| GET, POST `/positions`; PUT, DELETE `/positions/:id` | `departments.manage` | Manage positions |
| GET, POST `/leave-types`; PUT `/leave-types/:id` | `departments.manage` | Manage leave types |
| GET `/leave-requests` | `leave.approve` | List requests for the scope |
| POST `/leave-requests` | `leave.request` | Request leave for self, or for an employee with `leave.approve` |
| POST `/leave-requests/:id/approve`, `/reject`, `/cancel` | `leave.approve` (cancel: requester) | Decide |
| GET `/employees/:id/leave-balance` | `employees.view` | Days allowed, taken, remaining per type |
| GET `/loans` | `loans.view` | List loans and advances |
| POST `/loans` (I) | `loans.create` | Create a loan or advance. Approved within limit, else 202 |
| GET `/loans/:id` | `loans.view` | Detail with schedule and repayments |
| POST `/loans/:id/disburse` (I) | `loans.approve` | Pay out from a money account |
| POST `/loans/:id/repay` (I) | `payments.receive` | Record a direct cash repayment |
| GET, POST `/commission-rules`; PUT `/commission-rules/:id` | `components.manage` | Manage commission rules |
| GET, POST `/pay-adjustments`; DELETE `/pay-adjustments/:id` | `adjustments.manage` | One-off bonuses and deductions for a period |
| POST `/attendance/clock-in` | `attendance.clock` | Clock in self. With `attendance.edit` may pass `employee_id` |
| POST `/attendance/clock-out` | `attendance.clock` | Clock out |
| GET `/attendance/today` | `attendance.view` | Who is in, late, absent today in the branch |
| GET `/attendance/days` | `attendance.view` | Attendance history. Filters: employee, dates, status |
| PUT `/attendance/days/:id` | `attendance.edit` | Correct a day with reason |
| POST `/attendance/events` | `attendance.edit` | Add a missed clock event with reason |
| GET `/attendance/late` | `attendance.view` | Late records |
| GET `/attendance/overtime` | `attendance.view` | Overtime records with status |
| POST `/attendance/overtime/:day_id/approve`, `/reject` | `overtime.approve` | Decide overtime |
| GET `/attendance/summary?month=` | `attendance.view` | Monthly summary per employee |
| GET, PUT `/schedules` | `schedules.manage` | Work schedules per branch or employee |
| GET, POST `/holidays`; DELETE `/holidays/:id` | `schedules.manage` | Holiday calendar |
| GET, POST `/salary-components`; PUT `/salary-components/:id` | `components.manage` | Allowances, deductions, employer contributions |
| GET `/payroll-runs` | `payroll.view` | List runs |
| POST `/payroll-runs` | `payroll.prepare` | Create a run for a period and scope |
| GET `/payroll-runs/:id` | `payroll.view` | Run with payslip summary |
| POST `/payroll-runs/:id/calculate` | `payroll.prepare` | Calculate or recalculate. Returns 202 and runs as a job for large runs |
| PUT `/payroll-runs/:id/payslips/:pid/lines` | `payroll.prepare` | Manually adjust lines before approval, with reason |
| POST `/payroll-runs/:id/submit` | `payroll.prepare` | Send for approval |
| POST `/payroll-runs/:id/approve` (I) | `payroll.approve` | Freeze and post to journal |
| POST `/payroll-runs/:id/pay` (I) | `payroll.pay` | Body: money account and list of payslip IDs, or `all`. Records salary payments |
| POST `/payroll-runs/:id/reverse` (I) | `payroll.reverse` | Reverse an approved, unpaid run |
| GET `/payroll-runs/:id/payment-list?format=csv` | `payroll.pay` | Bank or mobile money payment file |
| GET `/payslips/:id` | `payslips.view` | One payslip. `format=pdf` |
| GET `/salary-payments` | `payroll.view` | Salary payment history |
| GET `/me/employee` | any linked user | Own employee profile |
| GET `/me/attendance`, `/me/leave`, `/me/loans`, `/me/payslips`, `/me/payments` | any linked user | Own records only |

## 8. Approvals, notifications, audit, reports, dashboard

| Method and path | Permission | Does |
| --- | --- | --- |
| GET `/approvals` | `approvals.view` | Requests the caller may decide, plus own requests. Filters: `status`, `kind` |
| GET `/approvals/count` | `approvals.view` | Pending count for the badge |
| GET `/approvals/:id` | `approvals.view` | Detail with link to the subject |
| POST `/approvals/:id/approve` (I) | `approvals.decide` | Approve with optional note. Completes the underlying action |
| POST `/approvals/:id/reject` | `approvals.decide` | Reject with note |
| POST `/approvals/:id/cancel` | requester | Cancel own pending request |
| POST `/approvals/:id/approve-with-pin` (I) | `approvals.decide` | Body: approver identifier and PIN. For on-the-spot POS approval |
| GET `/notifications` | any | Own notifications, paginated |
| GET `/notifications/unread-count` | any | Count for the bell |
| POST `/notifications/:id/read` | any | Mark one read |
| POST `/notifications/read-all` | any | Mark all read |
| GET `/audit-logs` | `audit.view` | Activity log. Filters: user, action, entity type, entity ID, dates |
| GET `/audit-logs/entity/:type/:id` | `audit.view` | History of one record |
| POST `/backups/export` | `backup.manage` | Start a business data export job |
| GET `/backups` | `backup.manage` | Export jobs and last automatic backup time |
| GET `/exports/:id` | requester | Status and download link of an export job |
| GET `/dashboard?period=&branch_id=` | `dashboard.view` | All cards the caller may see in one response |
| GET `/dashboard/me` | any | Cashier view: own sales today, till status, clock status |
| GET `/reports/sales/summary` | `reports.sales` | `group_by=day,week,month` |
| GET `/reports/sales/by-product`, `/by-category`, `/by-cashier`, `/by-customer`, `/by-payment-method` | `reports.sales` | Breakdown reports |
| GET `/reports/tills` | `reports.sales` | Till session report |
| GET `/reports/profit-loss` | `reports.financial` | With `compare=previous` |
| GET `/reports/expenses` | `reports.expenses` | `group_by=category,branch,payee,month` |
| GET `/reports/inventory/on-hand`, `/valuation`, `/movements`, `/low-stock`, `/dead-stock`, `/losses` | `reports.inventory` | Inventory reports |
| GET `/reports/purchases` | `reports.purchases` | `group_by=supplier,product,month` |
| GET `/reports/customer-debt` | `reports.debts` | Aged receivables |
| GET `/reports/supplier-debt` | `reports.debts` | Aged payables |
| GET `/reports/payroll/summary`, `/by-department`, `/deductions`, `/loans` | `reports.payroll` | Payroll reports |
| GET `/reports/staff-performance` | `reports.staff` | Sales, attendance, commission per employee |
| GET `/reports/branches` | `reports.branches` | Branch comparison |
| GET `/reports/cash-flow` | `reports.financial` | Money in and out by account and category |
| GET `/reports/trial-balance`, `/balance-sheet` | `reports.financial` | Ledger reports |
| GET `/reports/tax` | `reports.financial` | Output and input tax |

**Example: complete a sale**

```
POST /api/v1/sales
Authorization: Bearer eyJ...
X-Branch-Id: 0192f0a1-...
Idempotency-Key: 6f1c2b9e-...

{
  "customer_id": null,
  "items": [
    { "product_id": "0192f0b2-...", "qty": "3" },
    { "product_id": "0192f0b3-...", "qty": "1", "discount": { "type": "amount", "value": "500" } }
  ],
  "payments": [
    { "method": "cash", "money_account_id": "0192f0c4-...", "amount": "10000" }
  ]
}
```

```
201 Created
{
  "data": {
    "id": "0192f0d5-...",
    "number": "DSM01-SAL-000124",
    "status": "completed",
    "subtotal": "9500.00",
    "discount_total": "500.00",
    "tax_total": "1372.88",
    "total": "9000.00",
    "paid_total": "9000.00",
    "balance": "0.00",
    "change_due": "1000.00",
    "items": [ { "product_name": "...", "qty": "3", "unit_price": "2500.00", "line_total": "7500.00" } ],
    "payments": [ { "method": "cash", "amount": "9000.00" } ],
    "receipt_url": "/api/v1/sales/0192f0d5-.../receipt"
  }
}
```

The example assumes prices include 18% tax, so tax is the part of 9,000 that is tax: 9,000 x 18 / 118 = 1,372.88.
