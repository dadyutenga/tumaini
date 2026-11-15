# Tumaini BMS

Business management system for a company with one or more branches: sales, stock, purchasing, money, staff, and payroll. The backend is a Go modular monolith. The frontend is not in this repository.

The product contract is the specification in this folder: `SRS.md`, `Backend design.md`, `Database schema.md`, and `API reference.md`.

## Stack

- Go and Fiber v3
- PostgreSQL 16, with row-level security per business
- JWT access tokens and rotating refresh cookies
- Argon2id passwords

## Prerequisites

- Go 1.25 or newer
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

Health checks: `GET /healthz` and `GET /readyz`.

## Tests

The acceptance test uses the database in `DATABASE_URL`. It registers its own businesses and does not wipe other data.

```bash
cd backend
export DATABASE_URL='postgres://bms_app:bms_app_dev@127.0.0.1:5433/bms?sslmode=disable'
export JWT_SECRET='0123456789abcdef0123456789abcdef'
go test ./...
```

## What is implemented

Registration, users and roles, branches and settings, products and stock, sales and tills, customers and credit, purchases, expenses and approvals, money accounts and transfers, attendance, and payroll. See `docs/BACKEND_IMPLEMENTATION_REPORT.md` for the exact coverage and what is still open.

Passwords in `deploy/postgres/init.sql` are local development placeholders. Do not use them outside a private machine.
