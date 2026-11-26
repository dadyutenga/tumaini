# 03 — Customer statement print button

## Goal

On a customer page, someone who can see the statement can download it as a PDF for the period on screen. The client page already has this button; the shop customer page does not.

## Current state

`customers.Handler.Statement` (`backend/internal/modules/customers/handler.go`) serves `GET /customers/:id/statement` with permission `finance.debt.view`. `format=pdf` calls `statementPDF`, which writes the heading "Customer statement", the customer name, phone, period, opening balance, lines (date, number, description, debit, credit, balance), and closing balance. JSON is the default. `from` and `to` use `httpx.Range`. `backend/internal/app/customers_flow_test.go` asserts the PDF prefix `%PDF` and a 400 when `to` is before `from`.

`frontend/src/pages/CustomerDetail.tsx` loads `GET /customers/${id}/statement` only when `can("finance.debt.view")` and renders a `DataGrid`. The header actions are Credit limit and Receive payment. There is no PDF or print control, and the request sends no `from` or `to`.

`frontend/src/pages/ClientDetail.tsx` already renders `PdfButton` for `GET /clients/${id}/statement` when `services.invoice.manage`, `services.invoice.draft`, or `finance.debt.view`. `PdfButton` is in `frontend/src/features/services/shared.tsx`.

The customer route in `frontend/src/app/routes.tsx` (`/customers/:id`) allows `shop.customer.view_all` or `shop.customer.manage`. The statement block is additionally gated by `finance.debt.view`.

## Out of scope

- Changing `statementPDF` or the client statement PDF.
- Sending the statement (task 01).
- Employee statements (`EmployeeDetail.tsx` already has "Print statement") and money-account statements (`Money.tsx`).

## Backend tasks

The PDF route is done. No API change. Pass `from` and `to` as query parameters when the screen has a period. Empty dates are valid.

## Frontend tasks

1. In `CustomerDetail.tsx`, inside the block already wrapped in `can("finance.debt.view")`, add a period: From and To date inputs, defaulting to empty (the API's full statement). Refetch `GET /customers/${id}/statement` with those query params. If To is before From, do not fetch; show the error under the fields.

2. Add `PdfButton` labelled "Print statement" (or "Statement PDF") with `path={`/customers/${id}/statement`}`, the same `from` and `to` in `query`, and `filename={`statement-${c.name}.pdf`}`. Place it beside the "Statement" heading so it is the print action for the table underneath.

3. Hide the button when `finance.debt.view` is missing, same as the table. `shop.customer.view_all` alone must not show it, because the API returns 403.

## Acceptance checks

- Open a customer who has sales and payments, as a user with `finance.debt.view`. The table and the PDF show the same opening balance, lines, and closing balance.
- Set a period that excludes a known sale. The table and the PDF both drop that line.
- Set To before From. The screen explains the range and does not download a file.
- A user with `shop.customer.view_all` and without `finance.debt.view` still opens the customer page and does not see the statement or the button.
- Client detail's existing Statement button still downloads `client-statement` data from `/clients/:id/statement`.

## Permissions

Statement data and the PDF: `finance.debt.view`. Page access: `shop.customer.view_all` or `shop.customer.manage`.
