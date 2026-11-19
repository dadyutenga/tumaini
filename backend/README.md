# Tumaini BMS backend

A Go modular monolith: one binary serves the REST API under `/api/v1` and runs the background jobs. PostgreSQL 16 is the only stateful dependency.

- **Stack:** Go 1.27, Fiber v3, pgx v5, PostgreSQL 16 with row-level security, golang-migrate, Argon2id, JWT access tokens and rotating refresh cookies.
- **Contract:** `../docs/spec/` (SRS, API reference, backend design, database schema). The OpenAPI file is `../docs/api/openapi.yaml`.
- **Money and quantities** are exact decimals, sent as strings at the scale of the value: money `"2500.00"`, quantities `"3.000"`, unit costs `"1200.0000"`.

## Layout

```
cmd/api           the server
cmd/migrate       migration runner (up, down, version)
cmd/openapi       builds docs/api/openapi.yaml from docs/api/base.yaml and fragments/
db/migrations     numbered .up.sql / .down.sql files, embedded in the binary
internal/app      wiring: builds the shared services, mounts every module
internal/config   environment parsing and validation
internal/kernel   shared services every module writes through: ledger, stock, sequence,
                  approval, audit, notify, payments, chart of accounts
internal/modules  identity, org, catalog, inventory, purchasing, sales, customers, expenses,
                  finance, hr, attendance, payroll, reports, inbox, system
internal/platform auth, db, httpx, middleware, idem, jobs, export, pdf, sender, storage, sqlq, ...
```

Each module has a `Mount(router, deps)` function that builds its service, registers its routes, its approval hooks and its background jobs. `internal/app/app.go` only builds the shared `deps.Deps` and calls each `Mount`.

## Run it locally

You need Go 1.27 and PostgreSQL 16 (or Docker/Podman).

```bash
# 1. Database: creates the bms_owner and bms_app roles and the bms database (run as a superuser).
psql -h 127.0.0.1 -p 5433 -U postgres -f ../deploy/postgres/init.sql

# 2. Configuration.
cp .env.example .env        # then set JWT_SECRET and export the variables
set -a; . ./.env; set +a

# 3. Schema, then the server.
go run ./cmd/migrate up
go run ./cmd/api
```

Or with Compose (Postgres, migrations and the API): `podman compose -f ../deploy/docker-compose.yml up --build`. The API listens on `127.0.0.1:8080`.

Check it: `GET /healthz` (process is up) and `GET /readyz` (database answers). Register a business with `POST /api/v1/auth/register`; `../deploy/seed-dev.sh` creates a demo business with one user per role.

### Two database roles, on purpose

| Role | Used by | Can |
| --- | --- | --- |
| `bms_owner` | `cmd/migrate` only | change the schema, bypass row-level security |
| `bms_app` | the API | read and write rows; every query is limited to one business by row-level security; cannot change the schema, update or delete audit logs, journal lines or stock movements |

Migrations are therefore never run by the API itself. Run `cmd/migrate up` as part of every deploy.

## Configuration

Every variable, with comments, is in [`.env.example`](.env.example). Bad values stop the server at start-up instead of silently using a default.

With `APP_ENV=production` the server also refuses to start if `COOKIE_SECURE` is not true, `CORS_ORIGINS` contains `*`, or `JWT_SECRET` is still the example value.

## Tests

```bash
export DATABASE_URL='postgres://bms_app:bms_app_dev@127.0.0.1:5433/bms?sslmode=disable'
export JWT_SECRET='0123456789abcdef0123456789abcdef'
go test ./...                 # unit and integration tests
go test -race -count=1 ./...  # what CI runs
make cover                    # statement coverage
make openapi-check            # docs/api/openapi.yaml is up to date
```

The integration tests drive the real HTTP handlers against a real PostgreSQL as `bms_app`, with a fixed clock. Each test registers its own business and does not wipe other data, so they are safe to run against a development database but **not against production data**. The contract test (`internal/app/openapi_contract_test.go`) fails if a registered route is missing from the OpenAPI file, if the file lists a route that does not exist, or if a route in `docs/spec/API reference.md` is not implemented.

## API documentation

`docs/api/openapi.yaml` is generated: edit `docs/api/base.yaml` (shared components) or `docs/api/fragments/<module>.yaml`, then run `make openapi`. CI runs `make openapi-check`. Every operation lists its `x-permission` key, its parameters, request body and the error codes it can return.

## How requests are protected

1. **Authentication.** `Authorization: Bearer <access token>`, 15 minute lifetime. Refresh tokens live in an HttpOnly cookie, are stored hashed, rotate on every use, and reusing an old one ends the session. Five failed logins in 15 minutes lock the account for 15 minutes and notify the Owner. Logout and deactivation end sessions at once, and a password change signs out every other session. Wrong PINs and wrong current passwords are limited to five per 15 minutes.
2. **Authorization.** Each route declares a permission key. A permission has a scope (all, branch, own) that every list and detail query applies; a record outside the caller's scope is a 404. Approval limits turn an over-limit action into a 202 approval request.
3. **Delegation limits.** Only an Owner can assign the Owner role or change an Owner's account. Nobody edits their own role, and nobody grants a permission, scope or approval limit they do not hold.
4. **Tenancy.** Every table has a `business_id` and a row-level-security policy, so a bug in a query cannot read another business's data.
5. **Idempotency.** Money-moving POST routes require an `Idempotency-Key` header; a retry with the same key and body returns the first response, with a different body it is a 409.
6. **Input.** Request bodies are validated (struct tags, then business rules, then database constraints). SQL is parameterised; sort and group-by fields go through whitelists. Uploads are checked by content, not by the name or the client's type, and downloads are forced to attachments.
7. **Rate limits.** Register, login, refresh and password recovery are limited per client address. Set `TRUSTED_PROXIES` behind a reverse proxy so the real client address is used.
8. **Audit.** Every state change writes an audit record (user, branch, IP, request ID, before and after) in the same transaction. Passwords, PINs and tokens are never logged or audited.

## Background jobs

Jobs run in the same process, from a PostgreSQL queue (`jobs` table, `FOR UPDATE SKIP LOCKED`, five attempts). Daily jobs run once per business per local date, at the time shown in the business timezone.

| Job | When | Does |
| --- | --- | --- |
| `check_low_stock` | after a stock-changing action | notifies when a product reaches its reorder level, once until restocked |
| `recurring_expenses` | daily 00:30 | creates submitted expenses from templates that are due and asks approvers |
| `attendance_close` | at the business day start | marks scheduled days with no clock event as absent, flags missing clock-outs |
| `debt_reminders` | daily 08:00 | overdue customer invoices, customers near their credit limit, supplier invoices due, approved expenses unpaid |
| `salary_due` | daily 08:00 | reminds before pay day, and when a run awaits approval or payment |
| `owner_daily_summary` | at the business day start | sales, expenses, cash, low stock and pending approvals for the day that ended |
| `ledger_balance_check` | daily 02:00 | every journal entry balances; alerts the Owner if not |
| `stock_rebuild_check` | daily 02:30 | stock levels equal the sum of movements; alerts the Owner if not |
| `session_cleanup` | daily 03:00 | removes expired sessions, resets and idempotency keys older than 48 hours |
| `payroll_calculate` | on request | calculates runs above 200 employees (the API answers 202) |
| `business_export` | on request | writes the business's data as a zip of JSON lines |
| `send_notification` | on a notification | email or SMS delivery, only for types the Owner switched on |

## Integrations

Email and SMS are optional. A channel with no configuration is simply off.

- **Email:** set `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM`. STARTTLS is required before credentials are sent; port 465 uses implicit TLS.
- **SMS:** set `SMS_WEBHOOK_URL`, `SMS_API_KEY`, `SMS_SENDER_ID`. The API posts `{"to","from","message"}` as JSON with `Authorization: Bearer <key>` and expects a 2xx answer. Put a small adapter in front of providers that use a different format.
- **Files:** stored on local disk under `STORAGE_PATH`. Mount it on a persistent volume and back it up with the database.

Neither sender has been run against a real provider in this repository; both are covered by tests against local fake servers only.

## Backups

`../deploy/backup.sh` takes a full `pg_dump`, verifies it, optionally uploads it off the server (`BACKUP_UPLOAD_CMD`), keeps 30 days locally and records the time that `GET /api/v1/backups` shows. Run it nightly from the host's cron with the `bms_owner` connection string. Owners can also download their business data from `POST /api/v1/backups/export`.

## Deployment checklist

1. Build the image from `backend/Dockerfile` (a static binary plus `migrate`). The container has a health check on `/healthz`.
2. Run `migrate up` against the database as `bms_owner` before starting the new version.
3. Set the environment from `.env.example`: a real `JWT_SECRET`, `APP_ENV=production`, `COOKIE_SECURE=true`, your `CORS_ORIGINS`, `TRUSTED_PROXIES`, and the Argon2 cost (`ARGON_MEMORY=65536`, `ARGON_TIME=3`).
4. Terminate HTTPS in front of the API (the cookie is `Secure` and the API assumes TLS 1.2 or newer upstream).
5. Mount `STORAGE_PATH` on a persistent volume and schedule `backup.sh`.
6. Send `SIGTERM` to stop it: in-flight requests are given 20 seconds to finish.

## Known limits

- Report exports run inside the request. A very large report is not turned into a background export.
- The balance sheet is business-wide, because some journal lines have no branch.
- A past-date stock valuation cuts on the database timestamp, not the business date, so a movement made close to midnight can fall on either side.
- PDFs come from a small built-in writer (Helvetica, Latin-1). Characters outside Latin-1 print as `?`.
- Only local file storage is implemented; there is no S3 driver.
- There is no fiscal receipt integration; the SRS lists it as optional.
