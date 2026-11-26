# 05 — Expenses filtered by location

## Goal

With "All locations" selected in the header, the operator can narrow the expenses table to one location and can see each row's location name. With one location selected in the header, the table stays limited to that location.

## Current state

`expenses.Service.List` (`backend/internal/modules/expenses/service.go`) always adds `e.location_id = p.BranchID` when `p.BranchID` is set. `ListFilter` has status, category, submitter, project, from, to, and q. It has no location field. `expenses.Handler.List` (`backend/internal/modules/expenses/handler.go`) reads those query params and nothing else.

`middleware.Branch` (`backend/internal/platform/middleware/middleware.go`) sets `p.BranchID` from `X-Branch-Id`. The header value `all` leaves `BranchID` nil, so the list is every location. An empty header uses the membership's `default_location_id`. `frontend/src/lib/api.ts` sends `X-Branch-Id` from the auth branch id. `frontend/src/components/layout/BranchSwitcher.tsx` loads `GET /branches` and offers "All locations" plus each active branch. The switcher is hidden when the tenant has only one active location.

`scanExpense` returns `branch_id` (the location uuid) and does not return the location name. The list SQL joins `app.expense_categories` and the requester, not `app.locations`.

`frontend/src/pages/Expenses.tsx` filters with a status `<NativeSelect>` only. `useGetPage("/expenses", { status, page })` sends no location. The `Expense` type has no `branch_id`. The grid columns are number, date, category, description, and the rest of the money columns. `GET /branches` is already used by the switcher and needs no new permission (`org.Handler.Register` mounts `GET /branches` without `settings.manage`; create and edit still require `settings.manage`).

Spec section 8 also lists a business column (shop or services). This task is the location filter the operator asked for. Do not add a business-unit column unless `business_unit_id` is already on the list payload. It is not: `expenseCols` selects `e.location_id` and the insert writes `business_unit_id`, but `scanExpense` does not expose it.

## Out of scope

- Petty cash, recurring expenses, and expense approval rules. Recurring list already applies `p.BranchID` in `expenses/recurring.go`.
- A per-user location lock. Any active location of the tenant is allowed by `middleware.Branch`.
- Rebuilding removed finance screens (journal, period lock).

## Backend tasks

1. Add optional `location_id` to `ListFilter` and to `Handler.List` as query `location_id`. When `p.BranchID` is set, ignore the query and keep `e.location_id = p.BranchID`. If the query is present and differs from `p.BranchID`, return 400 `invalid_query` ("That location is not the one selected in the header") so a hand-built request cannot widen the header scope. When `p.BranchID` is nil, apply `e.location_id = location_id` when the query is a uuid. Unknown or inactive location: 404 `not_found`, same wording as `middleware.Branch` ("Branch not found").

2. Join `app.locations` in `expenseFrom` and add `location_name` to `scanExpense` (`l.name`). Keep `branch_id`. Every list and get response includes both.

3. Users who only see their own expenses (`canViewAllExpenses` is false, filter `e.requested_by = p.MembershipID`) still get that filter ANDed with the location filter.

## Frontend tasks

1. In `Expenses.tsx`, when the header branch is "all" (`useAuth().branchId` is null or `"all"`), show a location `<NativeSelect>` fed by `useGet` of `/branches`, active locations only, default "All locations". Pass `location_id` into `useGetPage("/expenses", …)` and reset `page` to 1 when it changes. When the header is a specific location, hide this select; the API is already scoped and a second control would disagree with the header.

2. Add a Location column bound to `location_name`. Show it when the header is all locations, including when the extra filter is a single location, so the operator can see what they filtered. When the header is one location, the column may stay; every row is that location.

3. Status filter, new expense, approve, reject, pay, and receipt preview stay as they are.

## Acceptance checks

- Header "All locations": the table lists expenses from two locations and the Location column shows both names. Choosing one location drops the other. Clearing it shows both again.
- Header set to location A: the table shows only A's expenses. A request with `?location_id=` of location B returns 400. The page does not offer B.
- A staff user who can only submit expenses still sees only their own rows, further limited by the location.
- A tenant with one location never sees the header switcher and does not need the extra select.

## Permissions

List and the page: `finance.expense.submit`, `finance.expense.enter`, `finance.expense.approve`, `finance.expense.pay`, or `finance.transaction.view` (the `/expenses` route in `frontend/src/app/routes.tsx` and the existing list handler). `GET /branches` stays open to any signed-in user, as it is today. Creating a location stays `settings.manage`.
