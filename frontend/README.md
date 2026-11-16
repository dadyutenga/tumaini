# Tumaini BMS frontend

Vite, React 19, TypeScript, Tailwind 4, TanStack Query, React Router, and shadcn/Radix components. It talks to the Go API in `../backend`. The layout and UI kit come from the template this project started from; the pages, auth, and API client are written for the BMS API.

## Run

```bash
# 1. API on http://127.0.0.1:8080 (see the root README)
# 2. Frontend
cd frontend
npm install
npm run dev        # http://localhost:5173
```

Open `http://localhost:5173` (not `127.0.0.1`) and choose **Register** to create a business and its owner.

In development Vite proxies `/api` to the API, so the browser sees a single origin. The refresh cookie is `SameSite=Strict` and needs that. Set `API_PROXY_TARGET` to proxy somewhere else. Set `VITE_API_BASE_URL` only if you serve the API from a different origin; then the API's `CORS_ORIGINS` must include the frontend and the cookie rules need a shared site.

Scripts: `npm run lint`, `npm test`, `npm run build`.

## How it works

| Piece | File | Notes |
| --- | --- | --- |
| API client | `src/lib/api.ts` | Bearer token in memory, rotating refresh cookie, single-flight refresh on 401, `X-Branch-Id`, `Idempotency-Key`, `{data, meta}` and `{error}` envelopes |
| Session | `src/features/auth/auth.tsx` | Restores the session from the refresh cookie, loads `/me`, exposes `can(permission)` and the active branch |
| Query hooks | `src/hooks/useApi.ts` | `useGet`, `useGetPage`, `useWrite` with toasts, approval (202) handling and idempotency keys |
| Navigation | `src/app/routes.tsx` | Each route lists the permissions that show it. The sidebar and route guard both use them |
| UI kit | `src/components/shared/kit.tsx` | `PageHeader`, `DataGrid`, `FormDialog`, `Field`, `Pager`, `StatusBadge` |

Money and quantities stay strings end to end. `toNumber` and `formatMoney` are for display only.

## Pages

| Page | API |
| --- | --- |
| Overview | sales, profit and loss, low stock, approvals, customers |
| Point of sale | tills, products, `/sales/preview`, `/sales` |
| Sales and sale detail | sales, returns |
| Products | products, categories, units |
| Stock | levels, low stock, stock in and out |
| Customers and detail | customers, statement, payments, credit limit |
| Money | money accounts, transfers, transactions |
| Expenses | categories, submit, pay |
| Approvals | list, approve, reject |
| Team and roles | users, roles |
| Activity log | audit log |
| Settings | business, branches, tax rates |
| My account | profile, password |

## Not built yet

The backend has no endpoint for these, so there is no screen yet: suppliers and purchase orders lists, employees, attendance, payroll runs, quotations, held carts, and reports beyond profit and loss. Add the screens as the endpoints land. Expenses has no list endpoint, so that page only shows expenses submitted in the current session.

The language switcher and its dictionary come from the template and are not wired into the new pages, which are in English.

`docs/` holds the original template notes and is kept for reference.
