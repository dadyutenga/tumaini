# 06 — A chart on each report

## Goal

Every report the operator opens shows a chart of that report's amounts, then the summary and the table. A report with no amounts still shows the table and a single line that there is nothing to chart.

## Current state

Spec section 10, "Any report": filters, a summary row of totals, a chart, and a detailed table. Export PDF, Excel, and print are already on the page.

`frontend/src/pages/Reports.tsx` loads one catalogue entry at a time. `catalogue` lists the paths the screen can open (sales summary, by product, by category, by cashier, by customer, by payment method, profit and loss, cash flow, balance sheet, tax, project profit `/reports/projects`, design jobs, expenses, stock on hand, valuation, movements, low stock, dead stock, losses, purchases, customer debt, supplier debt, payroll summary, payroll by department, payroll deductions, payroll loans, staff performance, branches). Permissions on each entry are `reports.operations.view` plus `reports.business.view`, or `reports.business.view` alone (`OPS` / `BIZ` in that file). The page route allows either permission.

`chartSeries` (in `Reports.tsx`) takes column 0 as the label and the first later column whose values look numeric, then `rows.slice(0, 10)`, and renders `BarChartView` inside `ChartCard` (`frontend/src/components/shared/Charts.tsx`, `frontend/src/components/shared/Display.tsx`). The chart is omitted when `chartSeries` is empty. That heuristic drops a report whose first numeric column is an id-like string, charts a count when a money column sits further right, and truncates labels to 18 characters. Profit and loss puts the shop / services / total figures in `report.data.businesses`, not only in `rows`, and the chart ignores `businesses`.

The dashboard line chart is separate and already drawn: `IncomeExpenseChart` in `Dashboard.tsx`, fed by `income_vs_expenses`. Do not rebuild it.

Report handlers are `reports.Handler` in `backend/internal/modules/reports/handler.go` (`/reports/sales/summary`, `/reports/profit-loss`, and the rest). They already return `columns`, `rows`, and sometimes `totals`. This task uses that payload.

`/reports/tills` and `/reports/inventory/expiring` exist on the handler and are not in the frontend catalogue. Leave them out of the catalogue. Do not add a chart for a removed screen.

## Out of scope

- New report SQL, new export formats, or a second chart library. `BarChartView` and `LineChartView` in `Charts.tsx` are enough.
- Dashboard widgets (task 04).
- Putting person, category, and business filters on every report. Date range is already on dated reports. Profit and loss already has business and "compare previous period". Add a filter only when the open report's API already accepts it. Do not invent query params.

## Backend tasks

No new endpoint. If a report's money column is returned as a formatted string that `toNumber` cannot parse, fix that column in the existing report function so the value is a decimal string (`"1234.50"`). Do not add a `chart` object unless a report has no numeric column and the screen cannot see `businesses` or `totals`. Prefer fixing the frontend choice of column.

## Frontend tasks

1. In `Reports.tsx`, replace the "first numeric column" choice with an explicit amount key per catalogue path. Use the money column the operator came to see, for example net sales or total on sales breakdowns, net profit on profit and loss, amount on expenses, value on stock valuation, balance on the debt reports, net on payroll summary. Skip columns named like `id`, `count` when a money column exists. Keep the top 10 by that amount, largest first, so a long product report still charts the rows that matter.

2. Profit and loss: chart `report.data.businesses` (shop income, services income, operating expenses, total net profit) with `BarChartView`, in addition to the four summary cards already on the page. When `compare` is on and `report.data.previous` is present, the chart or the card subtitle must show the previous net profit (the card already does). A row chart of the P&L lines is fine when `businesses` is missing.

3. Cash flow and any report whose rows are dates: use `LineChartView` with the date label untruncated (or truncated only past a full `YYYY-MM-DD`). Sales-by-something stays a bar chart.

4. When the chosen column is missing or every value is zero and there are no rows, do not render an empty `ChartCard`. Render one line under the filters: "No amounts to chart for this period." The table and the export buttons stay.

5. Print (`window.print`) must include the chart. The chart is already in the page, so do not open a second print layout that drops it. PDF and Excel export stay server-side via `download(current.path, { format })` and do not need a chart image in this task.

6. Respect the catalogue permission: a user with only `reports.operations.view` never requests a `BIZ` path. The chart must not display a cost or profit figure the table has already stripped (`stripCost` in `reports/service.go` removes `cost`, `gross_profit`, `profit`, `margin`, `value`, `unit_cost` without `canCost`). Chart a column that is still in `columns`.

## Acceptance checks

- Open Sales by product, Expenses, Customer debt, and Stock valuation. Each shows a bar chart of the money column, limited to the largest 10 rows, and the table still has every row and the totals row.
- Open Profit and loss with compare on. The chart shows shop, services, and net profit, and the previous net profit is visible.
- Open a dated report with no rows. The page says there is nothing to chart and the table says "No data for this report and period".
- A user with `reports.operations.view` and without `reports.business.view` can chart an operations report and cannot open Profit and loss.
- The dashboard income-versus-expenses line chart is unchanged.

## Permissions

`reports.operations.view` for operational catalogue entries, `reports.business.view` for business entries, matching `catalogue` in `Reports.tsx` and the route guards in `reports/handler.go` (`ops` vs `biz`). Export stays `reports.export` on the existing buttons. The chart is visible to anyone who can open the report.
