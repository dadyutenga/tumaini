# 07 — Reprinted receipt marked Copy

## Goal

The first shop receipt and the first service receipt print as the original. Every later print or PDF of that same receipt shows the word Copy. Opening the sale on screen does not consume the original.

## Current state

Spec section 11: a reprinted copy is marked "Copy". There are two receipts: the narrow shop receipt and the service payment receipt.

Shop: `sales.Service.Receipt` (`backend/internal/modules/sales/documents.go`) writes audit action `sale.receipt_printed` on every call, counts those audit rows, and sets `copy` true when the count is greater than 1. `ReceiptPDF` prints the heading `*** COPY ***` when `copy` is true. `sales.Handler.Receipt` (`backend/internal/modules/sales/handler.go`) serves `GET /sales/:id/receipt` as JSON or `format=pdf`, permission `shop.sale.view_all` or `shop.sale.create`. `backend/internal/app/sales_flow_test.go` expects the first GET `copy=false` and the second GET `copy=true`. That means any second GET, including a preview followed by a download, marks the PDF as a copy.

The screens do not use `copy`. `SaleDetail.tsx` downloads the PDF and also calls `window.print()` on the sale page, which is the HTML items table, not the thermal receipt, and has no Copy line. `Pos.tsx` `ReceiptDialog` prints the dialog with `window.print()` and downloads the PDF. Neither reads `copy`.

Service: `GET /service-payments/:id/receipt` (`services/handler.go`) calls `Service.PaymentReceipt` and `serviceReceiptPDF` (`backend/internal/modules/services/billing.go`). The PDF has receipt number, client, project, purpose, amount, and balance. It never sets `copy` and never writes a print audit. Permission is `payRead`: `finance.payment.view_all`, `finance.payment.receive`, `services.invoice.manage`, or `services.invoice.draft`.

## Out of scope

- Invoices, quotations, and payslips. Spec section 11 names them too; this task is the two receipts only.
- Changing receipt numbering, logo, or the thermal paper width (`receipt.paper_width`).
- Counting a WhatsApp click as a print. Sending the receipt is task 01.

## Backend tasks

1. Shop receipt: record `sale.receipt_printed` only when the operator actually prints or downloads. `GET /sales/:id/receipt` with `format` empty or `json` returns the same payload including `copy` and `receipt_prints`, and does not insert an audit row. `format=pdf` inserts the audit row and then renders `ReceiptPDF`, so the first PDF has `copy=false` and the second PDF has `copy=true` and the `*** COPY ***` heading. Update `sales_flow_test.go` so the two calls that prove `copy` are PDF calls (or one JSON preview that stays `copy=false`, then a PDF that is still the original). A JSON preview after the first PDF returns `copy=true` and does not add another audit row.

2. If the on-screen Print button cannot use the PDF, add `POST /sales/:id/receipt/printed` with the same permission, which inserts one `sale.receipt_printed` and returns `{copy, receipt_prints}`. The frontend calls it when the operator prints, not when the sale page loads. Prefer this only if you keep browser print instead of opening the PDF. Do not increment on both the PDF GET and the POST for a single click.

3. Service receipt: apply the same rule in `PaymentReceipt` / the receipt handler. Audit action `service_receipt.printed` on `payment_received` (the payment id). `serviceReceiptPDF` prints `*** COPY ***` when `copy` is true, under the business heading and above "Service Payment Receipt". The first PDF is the original. JSON does not increment.

4. `copy` must be a JSON boolean. The PDF mark is the word Copy, readable without zooming past the heading. Do not watermark a first print.

## Frontend tasks

1. `SaleDetail.tsx`: the Receipt PDF button keeps `download(/sales/${id}/receipt, { format: "pdf" })`. After a reprint, the downloaded file shows Copy. Change the Print button so it prints that receipt, not the bare items table: either download/open the PDF, or render a thermal preview from `GET /sales/${id}/receipt` (no `format`, so it does not count) and show a "Copy" banner when `copy` is true. If the preview is what gets printed, call `POST /sales/:id/receipt/printed` once on that click when you added it, and then show Copy on the next print.

2. `Pos.tsx` `ReceiptDialog`: the first Print or Receipt PDF on a new sale is the original. A second Print or PDF on the same sale shows Copy. The dialog's `window.print()` of the summary, with no Copy line, does not meet the spec. Use the same receipt payload as the sale page.

3. Service payment receipt downloads already call `GET /service-payments/${id}/receipt?format=pdf` from `ClientDetail.tsx`, `ProjectDetail.tsx`, and `DesignJobDetail.tsx`. The second download of the same payment shows Copy. Do not add a print counter to the page header.

## Acceptance checks

- Complete a shop sale. The first Receipt PDF has no Copy line. The second PDF of the same sale has `*** COPY ***`. Reloading Sale detail does not flip the first PDF into a copy.
- Print from the POS dialog twice. The second piece of paper or the second PDF says Copy.
- Take a service payment. The first service receipt PDF is original. The second says Copy.
- A user with `shop.sale.create` can reprint their sale. A user without `shop.sale.view_all` and without `shop.sale.create` gets 403.
- `sales_flow_test.go` still passes and no longer treats a JSON preview as a print.

## Permissions

Shop receipt: `shop.sale.view_all` or `shop.sale.create`. Service receipt: `finance.payment.view_all`, `finance.payment.receive`, `services.invoice.manage`, or `services.invoice.draft`.
