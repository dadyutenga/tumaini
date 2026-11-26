# 08 — In-app backup restore, or say the spec is wrong

## Goal

Decide one of the two outcomes below and do only that. The operator must not be able to wipe the database from a button that restores "whatever file they picked" while people are selling.

Spec section 10, Backup screen: "Download a backup; Restore (Owner only, asks for password); Run Backup Now."

The running page says the opposite on purpose. `frontend/src/pages/Backups.tsx` shows "Restore a backup (Owner only)" only when `me.is_owner`, and the text says full restores are done offline with `pg_restore` so a partial tenant restore cannot corrupt data while staff are selling. Download and "Run backup now" work. There is no restore control and no restore route.

## Current state

`GET /backups` and `POST /backups/export` are in `backend/internal/modules/inbox/handler.go`, both `security.backup.manage`. `inbox.Service` lists `app.backup_runs` where `backup_kind = 'tenant_export'`. Export enqueue is job `business_export` (`jobs.BusinessExport`), implemented by `system.Service` as a tenant JSON/ZIP export, not a full `pg_dump`. The page downloads `/files/{file_id}` as `business-backup-….zip` when `status === "done"`. Nightly dumps are described as `deploy/backup.sh`, and `last_automatic_backup_at` comes from the setting `backup.last_automatic`. `backend/internal/app/inbox_flow_test.go` expects a manager to get 403 on `POST /backups/export` and the owner to get 200, then 409 while one export is running.

`backend/cmd/migrate/main.go` says there is no down migration; recovery is a restore from backup. That comment is about schema recovery, not an in-app button.

Spec section 3: the Admin can restore data; the Manager cannot restore backups.

## Out of scope

- A restore that accepts an arbitrary upload, runs `pg_restore` in the API process, or restores one tenant by deleting rows live.
- Changing download, "Run backup now", or the nightly host script, unless the chosen outcome is the spec edit and the page text is already accurate.
- Staff-facing restore. Manager stays blocked.

## Choose one

### A. Update the spec to match the page

Edit both copies of the screen spec so the Backup row says: download a backup, run backup now, and restore only offline with `pg_restore` by the Owner. Remove "Restore (Owner only, asks for password)" from the in-app actions. Files:

- `docs/spec/Website Design and Screen Specification.md`
- The downloaded copy, if you keep it in sync: `/home/dadi/Downloads/Website Design and Screen Specification(1).md` is outside the repo. Change the repo spec. Mention the external file in the commit message only if someone asks to edit it.

Leave `Backups.tsx` and the handlers as they are. This is the outcome that matches the code comment already on the page.

### B. Add a guarded restore that matches the spec

Only if product chooses an in-app restore. Minimum bar, all of it, or do not ship the button:

1. Route `POST /backups/{id}/restore`. Caller must be `is_owner` and must have `security.backup.manage`. A manager with the permission and without `is_owner` gets 403. Re-check the account password in the body (`password`). Wrong password is 401, same idea as sign-in, without revealing a second factor.
2. `{id}` is an existing `app.backup_runs` row for this tenant with `status = 'done'` and a `file_id` this tenant owns. Anything else is 404. Do not accept a path, a URL, or an uploaded dump.
3. Refuse with 409 when any sale, payment, or export is in progress, or when another restore is running. The page already treats a second export as 409.
4. The handler does not stream SQL into the live database. It records the request (audit `backup.restore_requested`, owner, backup id, time) and returns 501 `restore_offline` with the sentence already on the page, until a real offline procedure exists. A later change may replace 501 with a maintenance-window job that restores only that export's own tenant file and takes the API out of service first. Do not implement that job in the same change as the button.
5. Frontend: on `Backups.tsx`, replace the static Owner card with a Restore button per completed row. It asks for the password and posts to the route. On 501, show the server message. On 403, show nothing to a non-owner (hide the button when `me.is_owner` is false, as the card is hidden today).

If the guardrails above feel like a fake button, choose A instead. A 501 button is only worth it if the spec must mention an in-app Restore while the dangerous half stays offline.

## Acceptance checks

- Outcome A: the spec Backup row no longer tells the operator to restore inside the app. The page still downloads a ZIP and starts an export. A manager still gets 403 on export.
- Outcome B: a non-owner does not see Restore. The owner, with the right password, on a finished backup, gets 501 (or a real maintenance restore if that job was built later under the rules above) and an audit row. A wrong password does not start a job. A second concurrent call gets 409. No request path can pass an arbitrary file.

## Permissions

List, download, and export: `security.backup.manage`. Restore, if built: `is_owner` and `security.backup.manage`. The manager role must not restore, matching spec section 3 ("Cannot … restore backups").
