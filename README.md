# BMS

Business management system for a company with one or more branches: sales, stock, purchasing, money, staff, and payroll. The backend (`backend/`) is a Go modular monolith. The frontend (`frontend/`) is a Vite + React template, imported without its git history; see `frontend/README.md`.

The product contract is the specification in `docs/spec/`: `SRS.md`, `Backend design.md`, `Database schema.md`, and `API reference.md`.

The full specification is not implemented yet. See [the implementation status and gaps](docs/IMPLEMENTATION_STATUS.md) for current coverage and the completion order.

## Layout

```
backend/    Go API (Fiber v3, PostgreSQL); see backend/README.md
frontend/   Vite + React app
deploy/     Docker Compose and Postgres init
docs/       spec/ (SRS, designs, schema, API reference), api/ (OpenAPI) and implementation reports
```

## Stack

- Go and Fiber v3
- PostgreSQL 16, with row-level security per business
- JWT access tokens and rotating refresh cookies
- Argon2id passwords

## Prerequisites

- Go 1.27 or newer (the module requires 1.27.4; the toolchain downloads itself when needed)
- PostgreSQL 16 or newer, or Podman/Docker

## Local database

The API role is `bms_app`. Migrations run as `bms_owner`. Create both roles and the database with `deploy/postgres/init.sql` as a superuser, or use Compose:

```bash
podman compose -f deploy/docker-compose.yml up --build
```

Compose publishes the API on `127.0.0.1:8080` and Postgres on `127.0.0.1:5433`.

## Run the API without Compose

```bash
cd backend
cp .env.example .env
# edit JWT_SECRET, then export the variables in .env
go run ./cmd/migrate up
go run ./cmd/api
```

Health checks: `GET /healthz` and `GET /readyz`. Copy `.env.example` for the full list of settings, including optional email and SMS.

## Run the frontend

With the API running on port 8080:

```bash
cd frontend
npm install
npm run dev
```

Open <http://localhost:5173> and register a business. See `frontend/README.md`.

## Tests

The integration tests drive the real HTTP handlers against a real PostgreSQL as the `bms_app` role. Each test registers its own business and does not wipe other data.

```bash
cd backend
export DATABASE_URL='postgres://bms_app:bms_app_dev@127.0.0.1:5433/bms?sslmode=disable'
export JWT_SECRET='0123456789abcdef0123456789abcdef'
go test -race -count=1 ./...
```

CI (`.github/workflows/backend-ci.yml`) runs the same commands against a PostgreSQL service, checks formatting and `go vet`, applies the migrations up, down and up again, and checks that `docs/api/openapi.yaml` is current.

## What is implemented

Every route of `docs/spec/API reference.md` (313) is implemented, documented in `docs/api/openapi.yaml` and covered by a contract test: authentication and password recovery, users, roles and delegation limits, business, branches and validated settings, products and stock (entries, adjustments, transfers, batch and expiry), purchasing, sales and tills, quotations, customers and credit, expenses and petty cash, money accounts, payments and period lock, employees, leave, loans, attendance, payroll, approvals, notifications, audit, data export, 29 reports and the dashboard, and the background jobs behind them.

- Backend details, configuration, jobs and deployment checklist: [`backend/README.md`](backend/README.md)
- What was built, what was fixed, what is not verified: [`docs/BACKEND_IMPLEMENTATION_REPORT.md`](docs/BACKEND_IMPLEMENTATION_REPORT.md)
- Progress and decisions: [`docs/BACKEND_COMPLETION_PROGRESS.md`](docs/BACKEND_COMPLETION_PROGRESS.md)
- API: [`docs/api/openapi.yaml`](docs/api/openapi.yaml), generated from `docs/api/base.yaml` and `docs/api/fragments/`

Passwords in `deploy/postgres/init.sql` are local development placeholders. Do not use them outside a private machine.
