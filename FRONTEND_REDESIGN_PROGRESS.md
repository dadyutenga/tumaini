# Frontend Redesign Progress (OpenDashboards Design System)

## Overview
- **Design System**: OpenDashboards (`od`, `od-app`, `od-mkt`)
- **Total Pages Discovered**: 52 routed pages (53 page source files including `MoneyExtras.tsx` sub-tab)
- **Completed Pages**: 9 / 52
- **Current Batch**: Batch 4 — `StockOps`, `Customers`, `CustomerDetail`

## Design System Improvements
- [x] Copied OpenDashboards bundled assets (`od-tokens.css`, `od-components.css`, `od-tokens.json`) into `frontend/src/styles/`
- [x] Loaded Inter, Geist, and Geist Mono typography in `frontend/index.html`
- [x] Mapped Tailwind v4 theme variables and light/dark modes in `globals.css` & `ThemeProvider.tsx` (`data-mode="dark"`)
- [x] Upgraded core UI primitives (`button`, `card`, `input`, `textarea`, `badge`, `table`, `tabs`, `dialog`)
- [x] Upgraded app chrome (`DashboardLayout`, `AuthLayout`, `Toolbar`, `BranchSwitcher`, `RouteErrors`)
- [x] Upgraded shared patterns (`kit.tsx`, `Display.tsx`, `Charts.tsx` with `SectionCard`, `SegmentedChips`, `FilterBar`, `MetaGrid`, OD chart series tokens)

## Batch Schedule & Status

| Batch | Pages | Status | Commit | Validation |
| --- | --- | --- | --- | --- |
| 1 | `Dashboard`, `Pos`, `Sales` (+ Design System Foundation) | Completed | `12fb5c4` | ESLint 0 errors, 37/37 tests, build passed |
| 2 | `SaleDetail`, `Products`, `ProductDetail` | Completed | `3ce6cd0` | ESLint 0 errors, 37/37 tests, build passed |
| 3 | `Stock`, `Purchasing`, `PurchaseOrderDetail` | Completed | Pending hash | ESLint 0 errors, 37/37 tests, build passed |
| 4 | `StockOps`, `Customers`, `CustomerDetail` | In Progress | — | Pending |
| 5 | `ServicesHome`, `Clients`, `ClientDetail` | Remaining | — | Pending |
| 6 | `Projects`, `ProjectDetail`, `ServiceCatalog` | Remaining | — | Pending |
| 7 | `ServiceQuotations`, `ServiceInvoices`, `DesignJobs` | Remaining | — | Pending |
| 8 | `DesignJobDetail`, `ClientDocuments`, `ServiceTasks` | Remaining | — | Pending |
| 9 | `Renewals`, `Contractors`, `Money` (+ `MoneyExtras`) | Remaining | — | Pending |
| 10 | `Debts`, `DayClosing`, `Expenses` | Remaining | — | Pending |
| 11 | `ExpenseSetup`, `People`, `EmployeeDetail` | Remaining | — | Pending |
| 12 | `AttendanceAdmin`, `HrSetup`, `Payroll` | Remaining | — | Pending |
| 13 | `PayrollSetup`, `PayrollRun`, `Reports` | Remaining | — | Pending |
| 14 | `Approvals`, `Notifications`, `Team` | Remaining | — | Pending |
| 15 | `Audit`, `LoginHistory`, `Backups` | Remaining | — | Pending |
| 16 | `Settings`, `MyWork`, `Profile` | Remaining | — | Pending |
| 17 | `Auth`, `ForgotPassword`, `ClientApproval` | Remaining | — | Pending |
| 18 | `Status` (+ Final QA) | Remaining | — | Pending |

## Completed Pages
- `Dashboard`, `Pos`, `Sales`
- `SaleDetail`, `Products`, `ProductDetail`
- `Stock`, `Purchasing`, `PurchaseOrderDetail`

## Remaining Pages
`StockOps`, `Customers`, `CustomerDetail`, `ServicesHome`, `Clients`, `ClientDetail`, `Projects`, `ProjectDetail`, `ServiceCatalog`, `ServiceQuotations`, `ServiceInvoices`, `DesignJobs`, `DesignJobDetail`, `ClientDocuments`, `ServiceTasks`, `Renewals`, `Contractors`, `Money`, `Debts`, `DayClosing`, `Expenses`, `ExpenseSetup`, `People`, `EmployeeDetail`, `AttendanceAdmin`, `HrSetup`, `Payroll`, `PayrollSetup`, `PayrollRun`, `Reports`, `Approvals`, `Notifications`, `Team`, `Audit`, `LoginHistory`, `Backups`, `Settings`, `MyWork`, `Profile`, `Auth`, `ForgotPassword`, `ClientApproval`, `Status`

## Blockers & Resolutions
- Pre-existing prettier formatting errors detected in `Backups.tsx`, `LoginHistory.tsx`, `Notifications.tsx` during baseline lint; will be resolved as those batches are redesigned and formatted with Prettier.
