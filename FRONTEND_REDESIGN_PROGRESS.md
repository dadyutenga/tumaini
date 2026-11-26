# Frontend Redesign Progress (OpenDashboards Design System)

## Overview
- **Design System**: OpenDashboards (`od`, `od-app`, `od-mkt`)
- **Total Pages Discovered**: 52 routed pages (53 page source files including `MoneyExtras.tsx` sub-tab)
- **Completed Pages**: 48 / 52
- **Current Batch**: Batch 17 — `Auth`, `ForgotPassword`, `ClientApproval`

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
| 3 | `Stock`, `Purchasing`, `PurchaseOrderDetail` | Completed | `734bdd4` | ESLint 0 errors, 37/37 tests, build passed |
| 4 | `StockOps`, `Customers`, `CustomerDetail` | Completed | `b893efd` | ESLint 0 errors, 37/37 tests, build passed |
| 5 | `ServicesHome`, `Clients`, `ClientDetail` | Completed | `c2e3b00` | ESLint 0 errors, 37/37 tests, build passed |
| 6 | `Projects`, `ProjectDetail`, `ServiceCatalog` | Completed | `655c671` | ESLint 0 errors, 37/37 tests, build passed |
| 7 | `ServiceQuotations`, `ServiceInvoices`, `DesignJobs` | Completed | `1e45d5c` | ESLint 0 errors, 37/37 tests, build passed |
| 8 | `DesignJobDetail`, `ClientDocuments`, `ServiceTasks` | Completed | `2977191` | ESLint 0 errors, 37/37 tests, build passed |
| 9 | `Renewals`, `Contractors`, `Money` (+ `MoneyExtras`) | Completed | `e4d86f6` | ESLint 0 errors, 37/37 tests, build passed |
| 10 | `Debts`, `DayClosing`, `Expenses` | Completed | `b4813ba` | ESLint 0 errors, 37/37 tests, build passed |
| 11 | `ExpenseSetup`, `People`, `EmployeeDetail` | Completed | `d254b52` | ESLint 0 errors, 37/37 tests, build passed |
| 12 | `AttendanceAdmin`, `HrSetup`, `Payroll` | Completed | `ab81ea2` | ESLint 0 errors, 37/37 tests, build passed |
| 13 | `PayrollSetup`, `PayrollRun`, `Reports` | Completed | `3065177` | ESLint 0 errors, 37/37 tests, build passed |
| 14 | `Approvals`, `Notifications`, `Team` | Completed | `c17b95a` | ESLint 0 errors, 37/37 tests, build passed |
| 15 | `Audit`, `LoginHistory`, `Backups` | Completed | `725aeca` | ESLint 0 errors, 37/37 tests, build passed |
| 16 | `Settings`, `MyWork`, `Profile` (+ `SettingsGroups`) | Completed | Pending hash | ESLint 0 errors, 37/37 tests, build passed |
| 17 | `Auth`, `ForgotPassword`, `ClientApproval` | In Progress | — | Pending |
| 18 | `Status` (+ Final QA) | Remaining | — | Pending |

## Completed Pages
- `Dashboard`, `Pos`, `Sales`
- `SaleDetail`, `Products`, `ProductDetail`
- `Stock`, `Purchasing`, `PurchaseOrderDetail`
- `StockOps`, `Customers`, `CustomerDetail`
- `ServicesHome`, `Clients`, `ClientDetail`
- `Projects`, `ProjectDetail`, `ServiceCatalog`
- `ServiceQuotations`, `ServiceInvoices`, `DesignJobs`
- `DesignJobDetail`, `ClientDocuments`, `ServiceTasks`
- `Renewals`, `Contractors`, `Money` (+ `MoneyExtras`)
- `Debts`, `DayClosing`, `Expenses`
- `ExpenseSetup`, `People`, `EmployeeDetail`
- `AttendanceAdmin`, `HrSetup`, `Payroll`
- `PayrollSetup`, `PayrollRun`, `Reports`
- `Approvals`, `Notifications`, `Team`
- `Audit`, `LoginHistory`, `Backups`
- `Settings`, `MyWork`, `Profile`

## Remaining Pages
`Auth`, `ForgotPassword`, `ClientApproval`, `Status`

## Blockers & Resolutions
- Pre-existing prettier formatting errors in `Backups.tsx`, `LoginHistory.tsx`, `Notifications.tsx` were resolved as Batches 14–15 were redesigned and formatted.
- Pre-existing `react-hooks/exhaustive-deps` warnings in `Debts.tsx` were resolved during Batch 10 using memoized arrays and a pure filter helper.
