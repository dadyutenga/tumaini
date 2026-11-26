# 02 — Supplier statement button

## Goal

On the Suppliers tab, the operator can download the supplier statement PDF that the API already builds.

## Current state

`purchasing.Handler.Statement` (`backend/internal/modules/purchasing/handler.go`) serves `GET /suppliers/:id/statement`. Query `from` and `to` go through `httpx.Range`. Without `format=pdf` it returns JSON from `purchasing.Service.Statement`. With `format=pdf` it builds "Supplier statement" via `pdf.New` and returns `supplier-statement.pdf` (`export.File`). Permission on the route is `canViewSup`: `shop.supplier.manage`, `shop.purchase.manage`, `finance.debt.view`, or `finance.payment.make`.

`frontend/src/pages/Purchasing.tsx` `SuppliersTab` lists suppliers from `GET /suppliers` and shows Pay (`POST /suppliers/:id/payments`) when `finance.payment.make` and the balance is positive. There is no statement control. `frontend/src/features/services/shared.tsx` `PdfButton` already downloads `format=pdf` and is used for client statements (`ClientDetail.tsx`) and contractor statements (`Contractors.tsx`).

The route is covered by `backend/internal/app/purchasing_flow_test.go` (`GET /api/v1/suppliers/{id}/statement`).

## Out of scope

- A new statement query, PDF layout, or email/WhatsApp send. Sending documents is task 01, and that task does not message suppliers.
- Purchase invoices and purchase returns (removed).
- Editing supplier fields.

## Backend tasks

The API is done. No handler change. `from` and `to` may be omitted; `httpx.Range` allows empty dates. If the PDF is opened with a range where `to` is before `from`, the API already returns 400 `invalid_query`.

## Frontend tasks

1. In `SuppliersTab` (`frontend/src/pages/Purchasing.tsx`), add a Statement button on each supplier row, next to Pay. Use `PdfButton` with `path={`/suppliers/${s.id}/statement`}` and `filename={`supplier-statement-${s.name}.pdf`}`. Show it for anyone who can see the tab (the page permission is `shop.purchase.manage`, `shop.supplier.manage`, `finance.payment.make`, or `finance.debt.view`, which matches `canViewSup`).

2. Optional dates: two date inputs above the grid, passed as `query={{ from, to }}` on `PdfButton`, cleared by default so the download matches today's unfiltered API. If `to` is before `from`, do not call the API; show the same "to must not be before from" message the API returns.

3. Do not add a second statement implementation. Contractor statements in `Contractors.tsx` already have their own PDF button; leave them.

## Acceptance checks

- As a user with `shop.supplier.manage`, open Purchasing → Suppliers, click Statement, and get a PDF whose heading is "Supplier statement" and whose supplier name matches the row.
- As a user with only `finance.debt.view`, the same button works.
- A user without those permissions does not see Purchasing, and `GET /suppliers/:id/statement` stays 403.
- Pay still records a supplier payment. The statement button does not open the pay dialog.

## Permissions

`shop.supplier.manage`, `shop.purchase.manage`, `finance.debt.view`, or `finance.payment.make`. The button must not require a stricter permission than the route.
