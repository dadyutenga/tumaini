# 01 — Customer messaging through the outbox

## Goal

An operator can send a customer a receipt, a statement, a debt reminder, a renewal reminder, a project update, or a design-approval link. The app writes a row in `app.outbound_messages` in the same transaction as the business change, a worker delivers it, and the screen shows whether it was queued. When no channel is configured, the send returns 503 and the existing WhatsApp click-to-chat link stays available so the counter is not stuck.

## Current state

`app.outbound_messages` is created in `backend/database/migrations/090_approvals_notifications_governance.sql`. Columns that matter: `channel` (`app.message_channel`: `sms`, `whatsapp`, `email` from `060_services_design.sql`), `recipient`, `template_id`, `subject`, `body`, `attachment_file_id`, `status` (`app.outbound_status`: `queued`, `sending`, `sent`, `delivered`, `failed`, `cancelled`), `attempts` (0–20), `next_attempt_at`, `locked_until`, `last_error`, `provider`, `provider_message_id`, `idempotency_key` (unique per tenant), `sent_at`, and exactly one-or-more of `customer_id`, `sale_id`, `invoice_id`, `quotation_id`, `payment_received_id`, `project_id`, `design_job_id`, `renewal_id`, `payslip_id`. The table comment says a worker claims rows with `FOR UPDATE SKIP LOCKED`. `app.renewal_reminders` points at an outbound row and is unique on `(tenant_id, renewal_id, days_before)`. `app.message_templates` exists and is unused.

`backend/internal/kernel/notify/notify.go` writes staff rows in `app.notifications` only. Its package comment says `app.outbound_messages` is for customers and must point at a customer, sale, invoice, and so on. Staff email and SMS stay out of this task. `jobs.SendNotification` (`backend/internal/modules/system/service.go`, `Service.SendNotification`) delivers a staff notification through `sender.Sender` by looking up `iam.users`. Do not put customer bodies on that job.

`backend/internal/platform/sender/sender.go` defines `sender.Sender` and `sender.Message`. `FromConfig` returns an SMTP sender when `SMTP_HOST` is set and a JSON webhook sender (`Webhook.Send`, body `{to, from, message}`) when `SMS_WEBHOOK_URL` is set. A missing channel is a nil sender.

Password recovery already refuses an unconfigured channel. `identity.Recovery.Forgot` in `backend/internal/modules/identity/recovery.go` returns HTTP 503 `recovery_unavailable` ("Password recovery by this channel is not set up…") when `channel == nil`, before it writes a reset row. `backend/internal/app/app.go` wires `recovery.Email, recovery.SMS = sender.FromConfig(cfg)`. `backend/internal/app/recovery_flow_test.go` expects status 503.

Customer sends today skip the outbox and call `s.SMS.Send` after the transaction, or they only open `wa.me`:

- `services.Service.SendUpdate` (`backend/internal/modules/services/projects.go`) inserts `app.client_interactions` (`kind = 'message'`) and then sends SMS if `s.SMS != nil`. Route: `POST /projects/:id/updates`, permission `services.project.manage` (`services/handler.go`). The dialog in `frontend/src/pages/ProjectDetail.tsx` still offers `whatsappUrl` ("Open in WhatsApp").
- `services.Service.RemindClient` (`backend/internal/modules/services/misc.go`) sends SMS for a renewal whose status is `pending` (the DB status is `upcoming`; the handler maps it). Route: `POST /renewals/:id/remind`, permission `services.renewal.manage`. `Renewals.tsx` also renders a `wa.me` link with no phone. `Service.RunReminders` (job `service_reminders`, registered in `services/mount.go`, daily at 08:15) notifies staff with `Notify.ToPermission` and then SMS-es the client directly. It does not insert `app.renewal_reminders`.
- `services.Service.SendForApproval` (`backend/internal/modules/services/design.go`) creates the 14-day link and `iam.public_tokens`, then SMS-es the URL. Route: `POST /design-jobs/:id/versions/:version_id/send`, permission `services.design.manage`. `DesignJobDetail.tsx` adds a `wa.me` link. `sent_via` on `app.design_approval_links` is hardcoded `'sms'`.
- `system.Service.DebtReminders` (job `debt_reminders`) notifies staff only (`finance.debt.view`, `shop.customer.credit_limit`, `finance.expense.pay`). It does not message customers. The Debts screen (`frontend/src/pages/Debts.tsx`, `GET /debts`, permission `finance.debt.view`) uses `whatsappUrl(r.phone, …)` for the Remind button on "Owed to us".
- Shop receipt send is only `whatsappUrl` in `frontend/src/pages/Pos.tsx` (`ReceiptDialog`) and `frontend/src/pages/SaleDetail.tsx`. Receipt PDF is `GET /sales/:id/receipt?format=pdf` (`sales.Handler.Receipt`). No outbound row is written.
- Statement PDFs already exist: `GET /customers/:id/statement?format=pdf` (`customers.Handler.Statement`, permission `finance.debt.view`) and `GET /clients/:id/statement?format=pdf` (`services/handler.go`). Nothing sends them.
- Templates already live in settings, not in `app.message_templates`: `notifications.sms_template_receipt`, `notifications.sms_template_debt`, `notifications.sms_template_renewal`, `notifications.whatsapp_template_update` (`org/settings.go`, edited by `frontend/src/features/auth/SettingsGroups.tsx`). `POST /notifications/test` (`inbox.Service.SendTestNotification`, permission `settings.manage`) always inserts an in-app notification, including when the channel is `sms` or `email`. It never calls `sender.Sender`.

`frontend/src/features/services/helpers.ts` `whatsappUrl` builds `https://wa.me/…`. `PayrollRun.tsx` also builds a `wa.me` link for payslips. Leave that alone: staff SMS/email is out of the approved model.

## Out of scope

- A paid SMS or WhatsApp vendor, Meta Cloud API, or Twilio client. Delivery is `sender.Sender`: log/noop when explicitly enabled for local dev, HTTP webhook when `SMS_WEBHOOK_URL` or a WhatsApp webhook URL is set, existing SMTP for email.
- Staff notifications, payslip WhatsApp/email, and routing customer text through `jobs.SendNotification`.
- Rebuilding `app.message_templates` admin. Keep using the four settings keys. `template_id` may stay null.
- Debt write-off, PIN approval, and messaging suppliers or contractors. The Debts "We owe" tab has no Remind button today; do not add one.
- Production deploy, queue hosting, or a new database migration unless a column is actually missing. The outbox table is already there.

## Backend tasks

1. Add an outbound writer, next to `kernel/notify` or under `platform/sender`, that inserts `app.outbound_messages` inside the caller's transaction. Required arguments: tenant, channel, recipient, body, idempotency key, and at least one of the foreign keys listed above. Set `status = 'queued'`, `next_attempt_at = now()`. On unique `(tenant_id, idempotency_key)`, return the existing row instead of failing the sale or the reminder. Truncate `body` to 4000 and `last_error` to 1000. Reject a recipient shorter than 5 characters with 400 `no_recipient`.

2. Add a pluggable sender used only by the worker, reusing `sender.Sender` and `sender.FromConfig`. Email uses the existing SMTP sender when `SMTP_HOST` is set. SMS uses the existing `Webhook` when `SMS_WEBHOOK_URL` is set. WhatsApp uses the same webhook shape when `WHATSAPP_WEBHOOK_URL` is set (bearer `WHATSAPP_API_KEY`, from-name `WHATSAPP_SENDER`); do not invent a second payload. Local dev may set `OUTBOUND_LOG=1` to install a sender that logs `to` and `body` with `slog` and returns nil. When the chosen channel's sender is nil, do not insert a row: return 503 `channel_unavailable` with the same meaning as `recovery_unavailable` in `identity/recovery.go` ("This channel is not set up. Ask an administrator to configure it.").

3. Register a worker drain beside `jobs.Client` (the constant list in `backend/internal/platform/jobs/jobs.go`). Claim one row with `SELECT … FROM app.outbound_messages WHERE status IN ('queued','failed') AND next_attempt_at <= now() AND (locked_until IS NULL OR locked_until < now()) ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED`. Set `status = 'sending'`, `locked_until` a few minutes ahead, and increment `attempts`. On success set `status = 'sent'`, `sent_at`, `provider`. On failure set `status = 'failed'`, `last_error`, and `next_attempt_at` with backoff. At `attempts = 20`, set `status = 'cancelled'`. Run the claim on the worker role the same way `system.Service.SendNotification` sets `SET LOCAL ROLE bms_worker` for `app.notifications`. A send that the operator just requested should be picked up by the existing poll in `Client.Run`, either by enqueueing a `dispatch_outbound` job or by calling the claim from `drain`. Do not deliver inside the HTTP request.

4. Receipt. Add `POST /sales/:id/send-receipt` on the sales router, same permission as `GET /sales/:id/receipt`: `shop.sale.view_all` or `shop.sale.create`. Body: `channel` (`sms` or `whatsapp` or `email`). Load the sale the way `sales.Service.Receipt` does. Recipient is the customer phone, or email when `channel` is `email`. Walk-in with no phone returns 400 `no_recipient`. Render the body from `notifications.sms_template_receipt` (`{{number}}`, `{{total}}`, `{{balance}}`, `{{customer}}`). Idempotency key: `receipt:{sale_id}:{channel}:{Idempotency-Key header or new uuid}`. Set `sale_id` and `customer_id` when the sale has a customer. Return `{id, status: "queued"}`. Do not call `sender.Send` in the handler.

5. Statement. Add `POST /customers/:id/statement/send` with permission `finance.debt.view`, and `POST /clients/:id/statement/send` with the same permission set as `GET /clients/:id/statement` (`services.invoice.manage`, `services.invoice.draft`, `services.client.view_all`, `finance.debt.view`). Body: `channel`, optional `from`, `to` (same rules as `httpx.Range`). Body text is the customer or client name, period, and closing balance from the existing `Statement` / `ClientStatement` functions. Set `customer_id`. Optional: if a PDF is already stored as `app.files`, set `attachment_file_id`. Do not block the send on generating a file.

6. Debt reminder. Add `POST /customers/:id/debt-reminder` and `POST /clients/:id/debt-reminder`, permission `finance.debt.view`. Refuse when the balance is zero (409 `nothing_due`). Body from `notifications.sms_template_debt` (`{{balance}}`, `{{name}}`). Idempotency key includes the business date so one click per day per customer per channel is one row, and a later day can send again. Wire the Debts screen to these routes by customer id; supplier and contractor rows are not recipients. Leave `DebtReminders` as the staff in-app summary. Do not enqueue a customer SMS from that job unless the tenant's `notification_rules` row for `debt_due` has `sms` or `whatsapp` true, and then one outbound row per overdue customer (not one staff blast). Default rules in `org/settings.go` set `sms` and `whatsapp` to false, so the daily job stays staff-only until an admin turns a channel on.

7. Renewal. Change `RemindClient` so the transaction inserts `app.outbound_messages` (`renewal_id`, and `customer_id`) and `app.renewal_reminders` when the days-before slot is free. Keep the 409 `invalid_status` for a renewal that is not pending. Body from `notifications.sms_template_renewal` (`{{service}}`, `{{date}}`). `RunReminders` must enqueue the same way for each client phone it currently puts in `texts`, and still call `Notify.ToPermission` for staff. If the channel sender is nil, the manual `POST /renewals/:id/remind` returns 503 and writes nothing; the daily job skips outbound and still writes the in-app staff notification.

8. Project update. Change `SendUpdate` so the outbound insert is in the same transaction as `app.client_interactions`. Body is the operator's message, or `notifications.whatsapp_template_update` with `{{client}}`, `{{project}}`, `{{message}}` when the channel is `whatsapp`. Set `project_id` and `customer_id`. Response stays `{project, delivered, has_phone}` but `delivered` becomes `queued` (true only when a row was inserted). `has_phone` false is 400 `no_recipient`.

9. Design approval. Change `SendForApproval` so the outbound row (`design_job_id`, `customer_id`) is written in the same transaction as the approval link. Body is the absolute approval URL already built from `baseURL` and `/approve/{token}`. Set `sent_via` from the requested channel (`sms`, `whatsapp`, or `email`) instead of always `'sms'`. The link is still returned in `approval_path` when delivery is not configured, and the HTTP status is 503 only when the operator asked to send and the sender is nil. Creating the link must not roll back if you choose to return the path together with 503: write the link, skip the outbound insert, return 503 `channel_unavailable` plus `approval_path` in the error body so the screen can still show the link. Match that in one response shape and document it on the handler.

10. Test message. Extend `inbox.Service.SendTestNotification` so `channel` `sms`, `whatsapp`, or `email` calls the same sender. When that sender is nil, return 503 `channel_unavailable` and do not insert `app.notifications`. `in_app` keeps today's behavior. A test has no customer, so it must not insert `app.outbound_messages` (the check constraint requires a business foreign key).

11. Errors to use, via `httpx`: 400 `no_recipient`, 400 `invalid_body` for an unknown channel, 403 from the existing `middleware.Require` lines, 404 when the sale, customer, client, project, renewal, or design version is missing, 409 `nothing_due` / `invalid_status`, 503 `channel_unavailable`.

## Frontend tasks

1. `frontend/src/pages/Pos.tsx` `ReceiptDialog` and `frontend/src/pages/SaleDetail.tsx`: the primary control is "Send receipt". It asks for channel SMS, WhatsApp, or email, and `POST`s `/sales/{id}/send-receipt` with `useWrite`. On 200, toast "Receipt queued". On 503, toast the server message and show the existing `whatsappUrl` link as "Open in WhatsApp". On 400 `no_recipient`, say the customer has no phone or email. Pass the customer phone into `whatsappUrl` (today both call sites pass `undefined`).

2. `frontend/src/pages/CustomerDetail.tsx`: next to the statement block (visible only with `finance.debt.view`), add "Send statement" calling `POST /customers/{id}/statement/send`. `frontend/src/pages/ClientDetail.tsx`: next to the existing `PdfButton` for `/clients/{id}/statement`, add the same control for `POST /clients/{id}/statement/send`. Same 503 fallback.

3. `frontend/src/pages/Debts.tsx`: on "Owed to us" rows with a phone, replace the Remind `wa.me` anchor with a button that POSTs the shop or client reminder route based on `source` (`shop_customer` vs `service_client`). Keep `wa.me` only after a 503. Do not show Remind on supplier or contractor rows.

4. `frontend/src/pages/Renewals.tsx`: the Remind button already calls `POST /renewals/{id}/remind`. Show the queued/503 result. The extra WhatsApp anchor stays as the 503 fallback and should pass `client_phone` when the renewal payload includes it.

5. `frontend/src/pages/ProjectDetail.tsx`: "Send" on the update dialog already POSTs `/projects/{id}/updates`. Extend the body with `channel`. Remove the always-on "Open in WhatsApp" button; show it when the API returns 503. Keep the template chips.

6. `frontend/src/pages/DesignJobDetail.tsx`: "Send for approval" stays on `POST /design-jobs/{id}/versions/{version_id}/send`. If the response includes `approval_path`, keep the copy box. Show "Open in WhatsApp" when `delivered` is false or the status is 503.

7. `frontend/src/features/auth/SettingsGroups.tsx`: the test form's SMS and email options should surface a 503 from `POST /notifications/test` instead of claiming the message was logged in-app. Add WhatsApp as a channel option only if the backend accepts it (task 10). Do not add a new settings page.

## Acceptance checks

- With `OUTBOUND_LOG=1`, send a shop receipt, a customer statement, a debt reminder, a renewal reminder, and a project update. Each inserts one `app.outbound_messages` row linked to the right foreign key, and the worker moves it to `sent`. A second click with the same idempotency key does not create a second row.
- With no webhook, no SMTP, and `OUTBOUND_LOG` unset, each send returns 503 `channel_unavailable` and inserts nothing. Forgot-password for an unconfigured channel still returns 503 `recovery_unavailable`.
- A walk-in sale with no customer returns 400 `no_recipient`. A customer with a balance of zero returns 409 on the debt reminder.
- Staff notifications from `debt_reminders` and `service_reminders` still appear in the bell. No payslip row is written to `app.outbound_messages`.
- The Debts Remind button, POS send, and project send no longer open WhatsApp first. They open it only after a 503.

## Permissions

- Receipt send: `shop.sale.view_all` or `shop.sale.create` (same as `GET /sales/:id/receipt`).
- Customer statement send and shop debt reminder: `finance.debt.view`.
- Client statement send: `services.invoice.manage`, `services.invoice.draft`, `services.client.view_all`, or `finance.debt.view`.
- Client debt reminder: `finance.debt.view`.
- Renewal remind: `services.renewal.manage`.
- Project update: `services.project.manage`.
- Design approval send: `services.design.manage`.
- Test message: `settings.manage`.
- Reading templates: whoever can already open Settings (`settings.manage` to edit). Sending does not require `settings.manage`.
