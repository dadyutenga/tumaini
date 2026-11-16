# Frontend design: Vite and React

How the web app is structured, and every screen it must contain.

## 1. Stack and libraries

A single-page app in TypeScript. Server data lives in a query cache, not in a global store.

| Concern | Choice | Why |
| --- | --- | --- |
| Build | Vite | Decided |
| UI | React with TypeScript, strict mode | Decided. Types catch API shape mistakes |
| Routing | React Router with lazy-loaded route modules | One code chunk per module keeps the POS fast |
| Server state | TanStack Query | Caching, refetch, pagination, optimistic updates |
| Client state | Zustand, small stores only: auth, active branch, POS cart | Simple, no boilerplate |
| Forms | React Hook Form with Zod schemas | Fast forms, one schema for validation and types |
| HTTP | Axios instance with interceptors, or a thin `fetch` wrapper | Token refresh and headers in one place |
| Styling | Tailwind CSS with shadcn/ui components | Consistent, accessible building blocks you own |
| Tables | TanStack Table | Server-side pagination, sorting, column visibility |
| Charts | Recharts | Dashboard and reports |
| Money maths | `decimal.js` for cart preview only. The server total is the truth | No float errors in the UI |
| Dates | `date-fns` with timezone helper | Business timezone display |
| Language | `i18next` with `en` and `sw` resource files | English and Kiswahili |
| Icons | `lucide-react` | Matches shadcn/ui |
| Tests | Vitest and React Testing Library for units, Playwright for end-to-end flows | POS and approval flows tested in a real browser |
| Quality | ESLint, Prettier, `tsc --noEmit` in CI |  |

## 2. Project structure

Feature folders mirror the backend modules, so a developer finds the same name on both sides.

```
frontend/
  index.html
  vite.config.ts
  src/
    main.tsx
    app/
      App.tsx
      router.tsx               all routes, lazy imports, guards
      providers.tsx            QueryClient, i18n, theme, toaster
    layouts/
      AdminLayout.tsx          sidebar, header, branch switcher, bell
      PosLayout.tsx            full screen, no sidebar
      SelfServiceLayout.tsx    simple top bar for staff
      AuthLayout.tsx
    lib/
      api.ts                   HTTP client, interceptors, error type
      queryClient.ts
      money.ts                 format and parse amounts
      dates.ts
      permissions.ts           can(), scopeOf(), limitOf()
      print.ts                 receipt and A4 print helpers
    stores/
      auth.ts                  access token, user, permissions
      branch.ts                active branch
      cart.ts                  POS cart
    components/
      ui/                      shadcn components
      DataTable.tsx            paginated server table
      PageHeader.tsx
      MoneyInput.tsx, QtyInput.tsx, DateRangePicker.tsx
      Can.tsx                  render children only with permission
      ConfirmDialog.tsx, ApprovalPendingDialog.tsx, PinDialog.tsx
      StatusBadge.tsx, EmptyState.tsx, ErrorState.tsx
      PrintReceipt.tsx, PrintInvoice.tsx, PrintPayslip.tsx
    features/
      auth/                    login, register, change password
      dashboard/
      pos/                     sale screen, till open and close, held carts
      sales/                   history, detail, returns, quotations
      inventory/               products, categories, stock in/out, adjustments, transfers, valuation
      customers/
      suppliers/
      purchases/               orders, receipts, invoices, returns
      expenses/
      finance/                 accounts, payments, transfers, income, transactions
      employees/               profiles, departments, positions, leave, loans, adjustments
      attendance/
      payroll/
      branches/
      users/                   users, roles, permissions, login history
      approvals/
      notifications/
      reports/
      audit/
      settings/
      me/                      self-service pages
    locales/
      en/*.json
      sw/*.json
    types/                     API types, one file per module
```

Each feature folder contains:

```
features/sales/
  api.ts          typed request functions and query keys
  hooks.ts        useSales(), useSale(id), useVoidSale() built on TanStack Query
  schemas.ts      Zod schemas for forms
  pages/          route components: SalesListPage, SaleDetailPage
  components/     pieces used only by this feature
  routes.tsx      route objects with required permission
```

## 3. Routing, layouts and permission guards

The menu and the routes are both built from the permission map returned by `GET /me`. Hiding in the UI is for convenience only; the server still checks everything.

```tsx
// src/lib/permissions.ts
import { useAuth } from '@/stores/auth';

export type Scope = 'all' | 'branch' | 'own';
export type Grant = { scope: Scope; limit: string | null };

export function useCan() {
  const perms = useAuth((s) => s.permissions); // Record<string, Grant>
  return {
    can: (key: string) => key in perms,
    scopeOf: (key: string) => perms[key]?.scope,
    limitOf: (key: string) => perms[key]?.limit ?? null,
  };
}
```

```tsx
// src/components/Can.tsx
export function Can({ perm, children, fallback = null }: {
  perm: string; children: React.ReactNode; fallback?: React.ReactNode;
}) {
  const { can } = useCan();
  return can(perm) ? <>{children}</> : <>{fallback}</>;
}
```

```tsx
// src/app/router.tsx (shape)
function RequireAuth() {
  const status = useAuth((s) => s.status); // 'loading' | 'in' | 'out'
  if (status === 'loading') return <FullPageSpinner />;
  if (status === 'out') return <Navigate to="/login" replace />;
  return <Outlet />;
}

function RequirePerm({ perm }: { perm: string }) {
  const { can } = useCan();
  return can(perm) ? <Outlet /> : <ForbiddenPage />;
}

export const router = createBrowserRouter([
  { element: <AuthLayout />, children: [
    { path: '/login', element: <LoginPage /> },
    { path: '/register', element: <RegisterPage /> },
  ]},
  { element: <RequireAuth />, children: [
    { element: <PosLayout />, children: [
      { element: <RequirePerm perm="sales.create" />, children: [
        { path: '/pos', lazy: () => import('@/features/pos/pages/PosPage') },
      ]},
    ]},
    { element: <SelfServiceLayout />, children: [
      { path: '/me/*', lazy: () => import('@/features/me/routes') },
    ]},
    { element: <AdminLayout />, children: [
      { path: '/', element: <HomeRedirect /> },   // dashboard, or /pos for cashiers
      ...dashboardRoutes, ...salesRoutes, ...inventoryRoutes, ...customerRoutes,
      ...supplierRoutes, ...purchaseRoutes, ...expenseRoutes, ...financeRoutes,
      ...employeeRoutes, ...attendanceRoutes, ...payrollRoutes, ...branchRoutes,
      ...userRoutes, ...approvalRoutes, ...reportRoutes, ...auditRoutes, ...settingsRoutes,
    ]},
  ]},
  { path: '*', element: <NotFoundPage /> },
]);
```

**Layout rules**

- **AdminLayout.** Collapsible sidebar generated from one menu config where every item names its permission. Header holds branch switcher, approvals badge, notification bell, language switch and user menu.
- **PosLayout.** No sidebar. Top bar shows branch, cashier, till status, clock, and buttons for held carts, lock screen and exit.
- **SelfServiceLayout.** For staff without admin permissions: tabs for clock in and out, attendance, leave, loans, payslips.
- **Landing page by role.** Owner and Manager land on the dashboard. A user whose only sales permission is `sales.create` lands on `/pos`.
- **Branch switch.** Changing branch clears the query cache and reloads the current page data.

## 4. API client, auth and data fetching

The access token stays in memory only. On page load the app calls `/auth/refresh` (the cookie is sent automatically), then `/me`.

```ts
// src/lib/api.ts
import axios, { AxiosError } from 'axios';
import { useAuth } from '@/stores/auth';
import { useBranch } from '@/stores/branch';

export const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL + '/api/v1',
  withCredentials: true, // refresh cookie
});

api.interceptors.request.use((config) => {
  const token = useAuth.getState().accessToken;
  const branchId = useBranch.getState().activeId;
  if (token) config.headers.Authorization = `Bearer ${token}`;
  if (branchId) config.headers['X-Branch-Id'] = branchId;
  return config;
});

let refreshing: Promise<string> | null = null;

api.interceptors.response.use(undefined, async (error: AxiosError) => {
  const original = error.config as any;
  if (error.response?.status === 401 && !original._retry && !original.url.includes('/auth/')) {
    original._retry = true;
    refreshing ??= api.post('/auth/refresh').then((r) => r.data.data.access_token).finally(() => (refreshing = null));
    try {
      const token = await refreshing;
      useAuth.getState().setToken(token);
      return api(original);
    } catch {
      useAuth.getState().signOut();
    }
  }
  return Promise.reject(toApiError(error)); // { status, code, message, fields }
});
```

```ts
// src/features/sales/hooks.ts
export const salesKeys = {
  list: (f: SalesFilter) => ['sales', 'list', f] as const,
  detail: (id: string) => ['sales', 'detail', id] as const,
};

export function useSales(filter: SalesFilter) {
  return useQuery({ queryKey: salesKeys.list(filter), queryFn: () => salesApi.list(filter), placeholderData: keepPreviousData });
}

export function useCreateSale() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateSaleBody) => salesApi.create(body, { idempotencyKey: body.clientKey }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sales'] });
      qc.invalidateQueries({ queryKey: ['products'] }); // stock changed
      qc.invalidateQueries({ queryKey: ['dashboard'] });
    },
  });
}
```

**Rules**

- Every mutation that creates money or stock generates a UUID idempotency key when the form opens, and reuses it on retry.
- A 202 response opens the approval pending dialog and shows the request in the user's own list.
- Field errors from a 422 are mapped onto the form with `setError`. Other errors show a toast with the server message.
- Lists keep filters, page and sort in the URL query string so a page can be shared and the back button works.
- Query cache stale time: 30 seconds for lists, 5 minutes for master data (categories, units, tax rates), 0 for the POS product lookup.
- No money calculation result from the client is sent to the server. The client sends quantities and choices, the server returns totals.

## 5. Screen inventory

Every menu item in the module tree, its route, the permission that shows it, and who sees it by default. O = Owner, M = Manager, S = Staff / Cashier, M\* = Manager only if the Owner allows.

| Menu item | Route | Permission | Roles |
| --- | --- | --- | --- |
| **Dashboard** | `/dashboard` | `dashboard.view` | O, M |
| Dashboard: profit, cash balance cards | same page | `dashboard.view_profit`, `accounts.view_balance` | O, M\* |
| My day (cashier home) | `/me` | any | S |
| **Sales / POS** |  |  |  |
| New Sale | `/pos` | `sales.create` | O, M, S |
| Till open and close | `/pos/till` | `tills.open_close` | O, M, S |
| Invoices | `/sales/invoices` | `sales.view` | O, M, S (own) |
| Receipts | `/sales/:id/receipt` | `sales.view` | O, M, S (own) |
| Returns / Refunds | `/sales/returns`, `/sales/:id/return` | `sales.refund_request` | O, M, S (request) |
| Discounts (limits and report) | `/sales/discounts` | `reports.sales` | O, M |
| Sales History | `/sales`, `/sales/:id` | `sales.view` | O, M, S (own) |
| Quotations | `/sales/quotations` | `quotations.manage` | O, M |
| Till sessions | `/sales/tills` | `tills.view` | O, M |
| **Products and Inventory** |  |  |  |
| Products | `/inventory/products`, `/new`, `/:id` | `products.view` | O, M, S (view) |
| Categories | `/inventory/categories` | `categories.manage` | O, M |
| Stock In | `/inventory/stock-in` | `stock.in` | O, M |
| Stock Out | `/inventory/stock-out` | `stock.out` | O, M |
| Stock Transfer | `/inventory/transfers`, `/new`, `/:id` | `transfers.view` | O, M |
| Stock Adjustment | `/inventory/adjustments`, `/new`, `/:id` | `stock.adjust` | O, M |
| Low Stock Alerts | `/inventory/low-stock` | `stock.view` | O, M |
| Inventory Valuation | `/inventory/valuation` | `valuation.view` | O, M\* |
| Stock card | `/inventory/products/:id/stock-card` | `stock.view` | O, M |
| **Customers** |  |  |  |
| Customer List | `/customers`, `/new`, `/:id` | `customers.view` | O, M, S |
| Customer Accounts | `/customers/:id` (Account tab) | `customers.view` | O, M |
| Credit / Debt | `/customers/debts` | `customers.view` | O, M |
| Customer Payments | `/customers/payments`, `/customers/:id/pay` | `payments.receive` | O, M, S |
| Customer Statements | `/customers/:id/statement` | `customers.statement` | O, M |
| **Suppliers** |  |  |  |
| Supplier List | `/suppliers`, `/new`, `/:id` | `suppliers.view` | O, M |
| Purchases (per supplier) | `/suppliers/:id` (Purchases tab) | `po.view` | O, M |
| Supplier Credit | `/suppliers/debts` | `suppliers.view` | O, M |
| Supplier Payments | `/suppliers/payments`, `/suppliers/:id/pay` | `payments.pay` | O, M\* |
| Supplier Statements | `/suppliers/:id/statement` | `suppliers.view` | O, M |
| **Purchases** |  |  |  |
| Purchase Orders | `/purchases/orders`, `/new`, `/:id` | `po.view` | O, M |
| Goods Received | `/purchases/receipts`, `/new`, `/:id` | `grn.create` | O, M |
| Purchase Invoices | `/purchases/invoices`, `/new`, `/:id` | `purchase_invoices.manage` | O, M |
| Returns | `/purchases/returns`, `/new` | `purchase_returns.create` | O, M |
| Purchase History | `/purchases/history` | `po.view` | O, M |
| **Expenses** |  |  |  |
| Business Expenses | `/expenses`, `/new`, `/:id` | `expenses.view` | O, M, S (own) |
| Expense Categories | `/expenses/categories` | `expense_categories.manage` | O, M |
| Petty Cash | `/expenses/petty-cash` | `petty_cash.manage` | O, M |
| Recurring Expenses | `/expenses/recurring` | `recurring_expenses.manage` | O, M |
| Expense Approval | `/approvals?kind=expense` | `expenses.approve` | O, M |
| **Payments and Finance** |  |  |  |
| Cash | `/finance/accounts?type=cash` | `accounts.view` | O, M |
| Bank Accounts | `/finance/accounts?type=bank` | `accounts.view` | O, M\* |
| Mobile Money | `/finance/accounts?type=mobile_money` | `accounts.view` | O, M\* |
| Account statement | `/finance/accounts/:id` | `accounts.view_balance` | O, M\* |
| Customer Payments | `/finance/payments?party=customer` | `payments.view` | O, M |
| Supplier Payments | `/finance/payments?party=supplier` | `payments.view` | O, M\* |
| Staff Payments | `/finance/payments?party=employee` | `payments.view` | O, M\* |
| Transfers | `/finance/transfers`, `/new` | `transfers_money.create` | O, M |
| Income | `/finance/income`, `/new` | `income.create` | O, M\* |
| Expenses | `/finance/expenses` | `expenses.view` | O, M |
| Transactions History | `/finance/transactions` | `transactions.view` | O, M\* |
| Period lock | `/finance/periods` | `periods.lock` | O |
| **Employees / Staff** |  |  |  |
| Employee Profiles | `/employees`, `/new`, `/:id` | `employees.view` | O, M |
| Departments | `/employees/departments` | `departments.manage` | O, M |
| Positions | `/employees/positions` | `departments.manage` | O, M |
| Attendance | `/employees/:id` (Attendance tab) | `attendance.view` | O, M |
| Leave | `/employees/leave` | `leave.approve` | O, M |
| Loans / Advances | `/employees/loans`, `/new`, `/:id` | `loans.view` | O, M\* |
| Commissions | `/employees/commissions` | `components.manage` | O |
| Bonuses | `/employees/adjustments?kind=bonus` | `adjustments.manage` | O, M\* |
| Deductions | `/employees/adjustments?kind=deduction` | `adjustments.manage` | O, M\* |
| Payroll | `/employees/:id` (Payroll tab) | `payroll.view` | O, M\* |
| Salary Payments | `/employees/:id` (Payments tab) | `payroll.view` | O, M\* |
| Employee Statements | `/employees/:id/statement` | `employees.view_salary` | O, M\* |
| **Attendance and Time** |  |  |  |
| Clock In / Out | `/attendance/clock` and `/me` | `attendance.clock` | O, M, S |
| Attendance History | `/attendance` | `attendance.view` | O, M, S (own at `/me/attendance`) |
| Late Records | `/attendance/late` | `attendance.view` | O, M |
| Overtime | `/attendance/overtime` | `overtime.approve` | O, M |
| Attendance Reports | `/attendance/reports` | `attendance.view` | O, M |
| Schedules and holidays | `/attendance/schedules` | `schedules.manage` | O, M |
| **Payroll** |  |  |  |
| Salary Structure | `/payroll/structure` | `components.manage` | O |
| Monthly Payroll | `/payroll/runs`, `/new`, `/:id` | `payroll.view` | O, M\* |
| Allowances | `/payroll/components?kind=allowance` | `components.manage` | O |
| Deductions | `/payroll/components?kind=deduction` | `components.manage` | O |
| Overtime | `/payroll/runs/:id` (Overtime column) | `payroll.view` | O, M\* |
| Commission | `/payroll/runs/:id` (Commission column) | `payroll.view` | O, M\* |
| Loans | `/payroll/runs/:id` (Loan column) | `payroll.view` | O, M\* |
| Payslips | `/payroll/payslips/:id`, `/me/payslips` | `payslips.view` | O, M\*, S (own) |
| Salary Payment History | `/payroll/payments` | `payroll.view` | O, M\* |
| **Branches** |  |  |  |
| Branch List | `/branches`, `/new`, `/:id` | `branches.view` | O, M (own) |
| Branch Staff | `/branches/:id` (Staff tab) | `branches.view` | O, M |
| Branch Inventory | `/branches/:id` (Inventory tab) | `stock.view` | O, M |
| Branch Sales | `/branches/:id` (Sales tab) | `sales.view` | O, M |
| Branch Expenses | `/branches/:id` (Expenses tab) | `expenses.view` | O, M |
| Stock Transfers | `/inventory/transfers` | `transfers.view` | O, M |
| Branch Performance | `/branches/performance` | `reports.branches` | O, M (own) |
| **Users and Roles** |  |  |  |
| Users | `/users`, `/new`, `/:id` | `users.view` | O, M\* |
| Roles | `/users/roles`, `/:id` | `roles.view` | O |
| Permissions | `/users/roles/:id` (matrix editor) | `roles.manage` | O |
| Login History | `/users/login-history` | `login_history.view` | O |
| Activity / Audit Logs | `/audit` | `audit.view` | O, M\* |
| **Reports** | `/reports` (index), `/reports/:key` | one `reports.*` key per report | O, M (branch), financial M\* |
| **Notifications** | `/notifications` and header bell | any | O, M, S |
| **Security and Audit** |  |  |  |
| Activity Logs | `/audit` | `audit.view` | O, M\* |
| Login Logs | `/users/login-history` | `login_history.view` | O |
| Permission Control | `/users/roles` | `roles.manage` | O |
| Transaction Approval | `/approvals` | `approvals.view` | O, M |
| Data Backup | `/settings/backup` | `backup.manage` | O |
| **Settings** |  |  |  |
| Business Profile | `/settings/business` | `business.manage` | O |
| Tax Settings | `/settings/tax` | `settings.manage` | O |
| Currency | `/settings/currency` | `settings.manage` | O |
| Payment Methods | `/settings/payment-methods` | `settings.manage` | O |
| Invoice Settings | `/settings/invoice` | `settings.manage` | O |
| Receipt Settings | `/settings/receipt` | `settings.manage` | O |
| Payroll Settings | `/settings/payroll` | `settings.manage` | O |
| Notification Settings | `/settings/notifications` | `settings.manage` | O |
| System Settings | `/settings/system` | `settings.manage` | O |
| **Self-service** | `/me`, `/me/attendance`, `/me/leave`, `/me/loans`, `/me/payslips`, `/me/profile` | any linked user | O, M, S |

**Standard page types.** Every list page has search, filters, date range, paginated table, export and a create button guarded by permission. Every detail page has a header with number and status badge, action buttons guarded by permission and status, tabs for related data, and a history panel from the audit log. Every form page validates with Zod, disables submit while saving, and warns before leaving with unsaved changes.

## 6. POS screen design

The POS is the screen used most, by the least trained users, under time pressure. It gets its own layout and its own rules.

**Layout (1024 px and wider)**

| Area | Content |
| --- | --- |
| Top bar | Branch, cashier name, till status, held carts button, lock, exit |
| Left, 60% | Search box with focus by default, category chips, product grid with name, price and stock |
| Right, 40% | Cart lines with quantity stepper and remove, customer selector, discount, subtotal, tax, total |
| Bottom right | Large Pay button showing the total, Hold button, Clear button |

On a phone the cart becomes a bottom sheet and the product grid fills the screen.

**Behaviour**

- A barcode scanner types fast and ends with Enter. If the search box receives Enter with an exact barcode match, add the item and clear the box.
- Scanning the same product again increases quantity.
- The cart lives in the Zustand store and is saved to `localStorage`, so a page refresh does not lose a sale in progress.
- Totals shown while building the cart are a local estimate. Before payment the app calls `POST /sales/preview` and shows the server's numbers.
- The payment dialog has quick cash buttons (exact, next round amounts), method tabs, split payment, reference field for mobile money and bank, and change due in large type.
- For a credit sale the customer must be chosen. The dialog shows current balance and remaining credit.
- If a discount is over the limit, the dialog offers Manager PIN approval on the spot, or send for approval and hold the cart.
- On success the receipt prints automatically if that setting is on, the cart clears and focus returns to the search box.
- On network failure the Pay button retries with the same idempotency key. It never creates the sale twice.
- If no till session is open, the POS shows the open-till dialog first.

**Keyboard shortcuts**

| Key | Action |
| --- | --- |
| F2 | Focus search |
| F4 | Select customer |
| F6 | Discount |
| F8 | Hold cart |
| F9 | Pay |
| Enter in payment dialog | Confirm payment |
| Esc | Close dialog |
| + and - on a selected line | Change quantity |
| Del | Remove selected line |

**Till close screen.** Shows expected cash by denomination entry, counted total, difference, and sales by payment method. A difference above the configured threshold requires a note and notifies the Manager.

## 7. UI standards

| Topic | Standard |
| --- | --- |
| Money display | Thousands separators, currency code from settings, right-aligned in tables: `TZS 1,250,000`. Negative in red with minus sign |
| Money input | `MoneyInput` component: digits only, formats on blur, sends a decimal string |
| Quantity input | Whole numbers unless the product's unit allows decimals |
| Dates | Display `16 Nov 2026`, time `14:35`, in the business timezone. Date range picker has presets: today, yesterday, this week, this month, last month, custom |
| Status badges | One colour per meaning across the app: grey draft, amber pending, green completed or paid, blue approved, red voided or rejected |
| Tables | Server-side pagination, 25 rows default, sticky header, row click opens detail, column chooser, totals row where it makes sense |
| Forms | Labels above fields, required marked, inline errors under the field, one primary button, Cancel returns without saving |
| Confirmation | Every void, delete, reverse, approve and post action asks for confirmation and states the effect, for example what stock and money will change |
| Empty and error states | Each list has an empty state with the create action, and an error state with retry |
| Loading | Skeleton rows for tables, spinner only inside buttons |
| Permissions | Buttons the user cannot use are hidden, not disabled, except where knowing the action exists helps (then disabled with a tooltip) |
| Language | All text from `i18next` keys. No hard-coded strings. Language switch in the header, saved to the user profile |
| Printing | Receipt: a hidden component styled for 58 or 80 mm with `@media print` and `@page { margin: 0 }`. Invoice, statement, payslip: A4 print views. Use `window.print()` |
| Responsive | Sidebar collapses under 1024 px. Tables scroll horizontally under 768 px or switch to cards on list pages used on phones |
| Accessibility | Keyboard reachable controls, visible focus, labels on inputs, colour never the only signal |
| Notifications | Toast for the result of the user's own action. Bell for events from others |
| Session | On 401 after a failed refresh, go to login and return to the same page after signing in |

**Environment variables**

```
VITE_API_URL=https://api.example.com
VITE_APP_NAME=BMS
VITE_DEFAULT_LANG=en
```
