# BMS implementation status

Audited 18 November 2026 against the checked-in routes, pages, API handlers, SRS, frontend design, and website screen specification.

The implementation is incomplete. It has 15 authenticated routes, including two detail routes and My account; 12 routes appear in the sidebar before permission filtering. Login and registration are separate. Permissions can hide menu entries, but missing modules are also absent for the Owner.

This is a source audit, not acceptance certification of every workflow. The older backend implementation report records an earlier test run and should not be interpreted as full product completion.

## Current coverage and gaps

| Area | Current implementation | Required work |
| --- | --- | --- |
| Authentication | Login, registration, refresh, logout, profile and password UI | Forgot-password delivery and recovery screens; session/security screens |
| Dashboard | Overview assembled from sales, low stock, customers, approvals and profit/loss | Dedicated staff My day, remaining dashboard metrics and role-specific behavior |
| Sales | POS, till controls within POS, history, detail and returns | Quotations, held carts, void, till payout; dedicated invoice, receipt and till-history workflows |
| Catalog and stock | Product and stock pages, categories/units APIs, stock in/out | Transfer and adjustment workflows, valuation, stock cards, import UI and richer product management |
| Customers | List, detail, statement, credit limit and payment UI | Debt overview, payment history and remaining specification details |
| Suppliers and purchasing | Create supplier, create/get/submit order, receive goods, invoice and pay APIs | Supplier and purchase lists, missing detail/read APIs, all purchasing screens, supplier statements and purchase returns |
| Expenses | Submit/pay UI, category API, approval inbox | Persisted expense list API/UI, category management, recurring expenses and petty cash reconciliation |
| Finance | Money accounts, transfers, transactions UI; trial balance API | Account statements, payment lists, income, period locking and remaining reports |
| Employees | Create employee and loan/disbursement APIs | Employee list/detail/update APIs and screens, departments, positions, leave and employee statements |
| Attendance | Clock action, schedule write, manual day status and per-employee day list APIs | Clock/self-service screen, attendance lists/filters, schedule UI and complete overtime/leave workflows |
| Payroll | Component creation and run create/calculate/approve/pay APIs | Component/run lists and detail APIs, payroll UI, payslip PDF, payment export and commission workflows |
| Branches | Branch APIs and list/create UI in Settings | Dedicated branch detail, staff/inventory/sales/expense tabs and performance reports |
| Users and roles | User list/create/activate/deactivate, role list | User editing, role/permission matrix editor, session management and login history UI |
| Reports | Profit/loss API used by Overview | Reports index and remaining sales, stock, customer, purchasing, finance, HR and payroll reports |
| Notifications and audit | Header notification popover and audit page | Full notifications page and remaining security/backup flows |
| Settings | Business, branch and tax UI; grouped settings/payment method APIs | Receipt, invoice, payment, payroll, notification and system settings UI |
| Cross-cutting UI | Permission-filtered navigation, API hooks, pagination and shared forms | Complete English/Swahili coverage, consistent filters/export, form validation, unsaved-change handling and accessibility checks |
| API contract | Handwritten routes and API reference | OpenAPI document and automated route/contract coverage |

## Additional website scope

`spec/Website Design and Screen Specification.md` includes Services: clients/leads, projects, service catalog, quotations/invoices, documents, tasks/calendar, renewals, design jobs, client approvals and contractors. Those modules are not implemented. They also lack matching domain/API definitions in the current core SRS and API reference. The specification needs to be reconciled before claiming that the full website design is delivered.

## Completion order

1. Complete supplier and purchasing read APIs and screens so receiving stock is usable end to end.
2. Complete employees, attendance and payroll read APIs and screens, including staff self-service.
3. Add stock transfers, adjustments, valuation and stock-card screens.
4. Complete expense history, finance, reports, permission editing and settings.
5. Add the remaining sales workflows, exports, recovery and security operations.
6. Reconcile and implement the additional Services/design-job scope.
7. Validate each workflow with real PostgreSQL data and permission/branch checks; complete the OpenAPI contract and screen acceptance checklist.

Adding sidebar entries alone does not complete these modules. Each needs real read/write behavior, permission enforcement and verified user flows.

## Verification limits

The Team regression was verified against PostgreSQL and in the browser. The frontend build and tests passed at that point. The existing broad backend acceptance test currently fails with `invalid_token` because its fixed clock is 16 November 2026 while token validation uses the current clock; that needs correction before it can certify the rest of the backend today.
