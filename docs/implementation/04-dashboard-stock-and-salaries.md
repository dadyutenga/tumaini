# 04 — Dashboard stock value and salaries due

## Goal

When the dashboard API includes stock value or salaries due, the dashboard shows those amounts. When the API omits them, the page does not invent a zero or a locked figure.

## Current state

`reports.Service` dashboard builder in `backend/internal/modules/reports/dashboard.go`:

- Stock is filled when the caller has `shop.stock.view`, `reports.operations.view`, `dashboard.operations.view`, or `dashboard.business.view`. The card always has `low_stock_count` and `items`. `value` (sum of `quantity_on_hand * average_cost`) is set only when `canCost` is true. `canCost` in `backend/internal/modules/reports/service.go` is `shop.product.view_cost` or `dashboard.business.view` or `reports.business.view`.
- `outstanding.salaries_due` is the sum of `app.payroll_lines.net_pay` for payroll runs in status `approved`, and only when the caller has `hr.payroll.view` or `dashboard.business.view`. The query respects `p.BranchID` (`r.location_id`).

`frontend/src/pages/Dashboard.tsx` types already include `stock.value` and `outstanding.salaries_due`. The low-stock card (around the "Low stock" heading) prints `low_stock_count` and item quantities. It never reads `stock.value`. The debts card prints customers, clients, suppliers, and contractors. It never reads `salaries_due`. The four KPI cards and `IncomeExpenseChart` are unrelated and already render.

Spec section 5 says the Secretary's dashboard hides profit, salary, and bank figures. The API already does that by omitting fields. Spec section 6 says the stock screen shows total stock value; this task only draws the figure the dashboard already returns.

## Out of scope

- Changing the stock or payroll SQL, cost stripping, or who may see profit.
- A new salaries report or a payroll run screen.
- Showing salaries due as zero for a manager who did not receive the field.

## Backend tasks

The fields are already returned. No API change. Do not add `value` for callers who fail `canCost`. Do not add `salaries_due` for callers who fail the payroll check.

## Frontend tasks

1. In the low-stock section of `Dashboard.tsx`, when `d.stock.value` is present, show "Stock value" with `formatMoney(d.stock.value)` above the item list. Link it to `/reports?path=/reports/inventory/valuation` when the user has `reports.business.view` (that report's catalogue entry uses `reports.business.view`). Otherwise link to `/products`, which is where the low-stock list already goes. When `value` is absent, render the list exactly as today.

2. In the debts section, when `d.outstanding.salaries_due` is present, add a row "Salaries due" with `formatMoney`. Link that row to `/payroll` (route permission includes `hr.payroll.view`). Do not add the amount into the "We owe" total unless you also add it on purpose and label the total "We owe, including approved salaries". Prefer a separate line under the two debt cards so supplier debt stays comparable to today.

3. Format with the existing `formatMoney`. A missing field stays hidden. An explicit `"0"` or `"0.00"` from the API is shown as zero.

## Acceptance checks

- Sign in as the owner (`dashboard.business.view` and cost). The dashboard shows a stock value matching `stock.value` and a salaries-due figure matching `outstanding.salaries_due` after an approved payroll run. A business with no approved run shows 0 if the API sends 0.
- Sign in as a user with `dashboard.operations.view` and without `shop.product.view_cost`, `dashboard.business.view`, `reports.business.view`, and `hr.payroll.view`. Low stock still lists products. Stock value and salaries due are absent.
- Changing the header location (`X-Branch-Id` via `BranchSwitcher`) reloads the dashboard and the two figures follow the branch the API applied.

## Permissions

Shown only when the API includes them, which is:

- Stock value: stock card permission (`shop.stock.view`, `reports.operations.view`, `dashboard.operations.view`, or `dashboard.business.view`) and `canCost` (`shop.product.view_cost`, `dashboard.business.view`, or `reports.business.view`).
- Salaries due: `hr.payroll.view` or `dashboard.business.view`.
