# Backend design: Go and Fiber v3

How the API server is structured and how each cross-cutting concern is implemented.

## 1. Stack and libraries

One Go binary serves the API and runs the background workers. PostgreSQL is the only stateful dependency, so there is no Redis to operate.

| Concern | Choice | Why |
| --- | --- | --- |
| Language | Go, the version Fiber v3 requires (1.25 or newer) | Fiber v3 minimum, see [What's New in v3](https://docs.gofiber.io/whats_new) |
| HTTP framework | Fiber v3, `github.com/gofiber/fiber/v3` | Decided. Handlers are `func(c fiber.Ctx) error` |
| Database driver | pgx v5 with `pgxpool` | Native PostgreSQL driver, good transaction and type support |
| Queries | sqlc generating typed Go from SQL files | SQL stays visible and reviewed, no ORM magic around money |
| Migrations | golang-migrate, plain `.up.sql` and `.down.sql` files | Simple and versioned |
| Money | `shopspring/decimal` in Go, `NUMERIC(18,2)` in PostgreSQL | Exact arithmetic |
| Validation | `go-playground/validator` v10 | Struct tag validation on request bodies |
| Auth tokens | `golang-jwt/jwt` v5 | Signed access tokens |
| Password hashing | Argon2id from `golang.org/x/crypto` | Current recommended hash |
| IDs | UUID version 7 from `google/uuid` | Time-ordered, index friendly, safe to expose |
| Background jobs | River, a PostgreSQL-backed job queue | Jobs enqueue inside the same transaction as the data |
| Logging | `log/slog` JSON handler | Standard library |
| Config | Environment variables parsed into one struct at start | Twelve-factor |
| Excel and PDF | `excelize` for Excel. PDF by rendering HTML on the frontend print view, or `chromedp` for server PDFs | Reports and payslips |
| File storage | Interface with local disk and S3-compatible implementations | Receipts, logos, exports |
| Tests | Standard `testing`, `testify`, `testcontainers-go` for a real PostgreSQL | Money logic is tested against a real database |

Fiber v3 changed many signatures from v2 (context is an interface, binding moved to `c.Bind()`, CORS origins are a slice). Check each middleware against the v3 docs when you write it, not v2 tutorials.

## 2. Project structure

One repository with two apps. The backend is a modular monolith: one folder per module, all with the same four layers.

```
bms/
  backend/
    cmd/
      api/main.go              start HTTP server and workers
      migrate/main.go          run migrations
      seed/main.go             seed permissions, demo data
    internal/
      config/                  env parsing
      platform/
        db/                    pgxpool, tx helper, tenant session setter
        httpx/                 error type, error handler, response helpers, pagination
        middleware/            requestid, logger, auth, tenant, branch, permission, idempotency, ratelimit
        auth/                  jwt, password hashing, pin
        storage/               file storage interface
        jobs/                  River client, worker registration
        money/                 decimal helpers, rounding
        clock/                 time source, business day
      kernel/
        ledger/                journal posting service and rules
        stock/                 stock movement service
        sequence/              document numbers
        approval/              approval requests
        audit/                 audit writer
        notify/                notification writer and senders
      modules/
        identity/              users, roles, permissions, sessions
        org/                   business, branches, settings
        catalog/               products, categories, units
        inventory/             stock in, out, adjustments, transfers, valuation
        sales/                 sales, tills, returns, quotations
        customers/
        purchasing/            suppliers, PO, GRN, invoices, returns
        expenses/
        finance/               money accounts, payments, transfers, income, period lock
        hr/                    employees, departments, positions, leave, loans
        attendance/
        payroll/
        reports/               read-only queries and exports
        dashboard/
      router/router.go         mounts every module's routes
    db/
      migrations/              0001_init.up.sql ...
      queries/                 sqlc SQL files per module
      sqlc.yaml
    Dockerfile
    Makefile
  frontend/                    Vite + React app (see Frontend design tab)
  deploy/
    docker-compose.yml
    Caddyfile
  docs/
```

Every module folder has the same files:

```
modules/sales/
  routes.go        Register(r fiber.Router, deps) and permission per route
  handler.go       parse and validate request, call service, write response
  dto.go           request and response structs
  service.go       business rules, opens the transaction, calls kernel services
  repository.go    wraps sqlc queries
  model.go         domain types and status constants
  errors.go        module error codes
  service_test.go
```

Dependency rule: handler calls service, service calls repository and kernel, kernel calls its own repository. A module imports another module only through an interface declared by the caller. Nothing imports a handler.

## 3. Bootstrap and request lifecycle

Every request passes the same chain of middleware before it reaches a handler.

1. `recover`: turn a panic into a 500 and log it.
2. `requestid`: attach an ID used in logs and returned in `X-Request-Id`.
3. Logger: one structured line per request with method, path, status, duration, user, business.
4. CORS and security headers.
5. Rate limit: strict on `/auth/*`, looser elsewhere.
6. `Auth`: verify the access token, load the principal (user ID, business ID, role ID, permissions map, branch IDs) into the request context.
7. `Branch`: read `X-Branch-Id`, check it is one of the user's branches, store the active branch.
8. `Require(permission)`: per route. Checks the permission key and stores its scope.
9. `Idempotency`: on marked POST routes, return the stored response if the key was seen.
10. Handler, then service, which opens a transaction and sets the tenant for row-level security.
11. Error handler: map any returned error to the standard JSON error shape.

```go
// cmd/api/main.go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gofiber/fiber/v3"
    "github.com/gofiber/fiber/v3/middleware/cors"
    "github.com/gofiber/fiber/v3/middleware/recover"
    "github.com/gofiber/fiber/v3/middleware/requestid"

    "bms/internal/config"
    "bms/internal/platform/db"
    "bms/internal/platform/httpx"
    "bms/internal/platform/jobs"
    "bms/internal/router"
)

func main() {
    cfg := config.MustLoad()
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    pool := db.MustConnect(ctx, cfg.DatabaseURL)
    defer pool.Close()

    workers := jobs.MustStart(ctx, pool, cfg)
    defer workers.Stop(context.Background())

    app := fiber.New(fiber.Config{
        AppName:      "bms-api",
        ErrorHandler: httpx.ErrorHandler,
        BodyLimit:    10 * 1024 * 1024,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 30 * time.Second,
    })

    app.Use(recover.New())
    app.Use(requestid.New())
    app.Use(httpx.RequestLogger())
    app.Use(cors.New(cors.Config{
        AllowOrigins:     cfg.CORSOrigins, // []string in v3
        AllowCredentials: true,
        AllowHeaders:     []string{"Authorization", "Content-Type", "X-Branch-Id", "Idempotency-Key"},
    }))

    app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })
    router.Mount(app, router.Deps{Pool: pool, Cfg: cfg, Jobs: workers})

    go func() {
        if err := app.Listen(cfg.HTTPAddr); err != nil {
            slog.Error("server stopped", "err", err)
            os.Exit(1)
        }
    }()

    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    _ = app.ShutdownWithContext(shutdownCtx)
}
```

```go
// internal/router/router.go
func Mount(app *fiber.App, d Deps) {
    api := app.Group("/api/v1")

    identity.RegisterPublic(api.Group("/auth"), d) // login, register, refresh

    private := api.Group("", middleware.Auth(d), middleware.Branch(d))
    identity.Register(private, d)
    org.Register(private, d)
    catalog.Register(private, d)
    inventory.Register(private, d)
    sales.Register(private, d)
    customers.Register(private, d)
    purchasing.Register(private, d)
    expenses.Register(private, d)
    finance.Register(private, d)
    hr.Register(private, d)
    attendance.Register(private, d)
    payroll.Register(private, d)
    approval.Register(private, d)
    notify.Register(private, d)
    audit.Register(private, d)
    reports.Register(private, d)
    dashboard.Register(private, d)
}
```

## 4. Authentication and sessions

Access is a short-lived JWT in memory on the client. The refresh token is a random string in an httpOnly cookie, stored hashed in the database so it can be revoked.

| Item | Design |
| --- | --- |
| Login | `POST /auth/login` with identifier and password. Verify Argon2id hash. Check user and business are active. Record login history |
| Access token | JWT signed HS256 or EdDSA, 15 minutes. Claims: `sub` user ID, `bid` business ID, `rid` role ID, `sid` session ID, `pv` permission version |
| Refresh token | 32 random bytes, 30 days, stored as SHA-256 hash in `sessions`. Cookie: httpOnly, Secure, SameSite=Strict, path `/api/v1/auth` |
| Rotation | Each refresh issues a new refresh token and invalidates the old one. Reuse of an old token revokes the whole session |
| Permissions | Loaded from the database on each request through a small in-process cache keyed by role ID and permission version. Changing a role bumps the version, so changes apply within one request |
| Logout | Delete the session row and clear the cookie |
| Lockout | Count failures in `login_history`. 5 in 15 minutes locks the user for 15 minutes |
| PIN | Argon2id hash of a 4 to 6 digit PIN on the user. `POST /auth/pin` exchanges PIN plus a valid device session for a new access token. 5 wrong PINs forces a full login |
| Password rules | Minimum 8 characters, checked against a short list of common passwords |
| Registration | One transaction: business, main branch, default roles and permissions, chart of accounts, default settings, cash account, walk-in customer, Owner user |

```go
// internal/platform/middleware/auth.go
type Principal struct {
    UserID     uuid.UUID
    BusinessID uuid.UUID
    RoleID     uuid.UUID
    EmployeeID *uuid.UUID
    IsOwner    bool
    BranchIDs  []uuid.UUID
    Perms      map[string]Grant // key -> scope and limit
    BranchID   *uuid.UUID       // active branch, set by Branch middleware
}

type Grant struct {
    Scope string           // "all" | "branch" | "own"
    Limit *decimal.Decimal // approval limit, nil = none
}

func Auth(d Deps) fiber.Handler {
    return func(c fiber.Ctx) error {
        raw := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
        claims, err := d.Tokens.Verify(raw)
        if err != nil {
            return httpx.Unauthorized("invalid_token")
        }
        p, err := d.Principals.Load(c.Context(), claims)
        if err != nil {
            return httpx.Unauthorized("session_ended")
        }
        c.Locals(principalKey, p)
        return c.Next()
    }
}

func PrincipalFrom(c fiber.Ctx) *Principal {
    return c.Locals(principalKey).(*Principal)
}
```

## 5. Permissions and scope enforcement

Permission keys are constants in code, seeded into the database, and attached to routes. Scope is applied in the repository query, so a forgotten check returns nothing instead of leaking.

**Permission key catalogue**

| Module | Keys |
| --- | --- |
| identity | `users.view`, `users.manage`, `roles.view`, `roles.manage`, `sessions.manage`, `login_history.view` |
| org | `business.manage`, `branches.view`, `branches.manage`, `settings.view`, `settings.manage` |
| catalog | `products.view`, `products.manage`, `products.view_cost`, `categories.manage`, `products.import` |
| inventory | `stock.view`, `stock.in`, `stock.out`, `stock.adjust`, `stock.adjust_approve`, `transfers.view`, `transfers.create`, `transfers.dispatch`, `transfers.receive`, `valuation.view` |
| sales | `sales.create`, `sales.view`, `sales.discount`, `sales.void`, `sales.refund_request`, `sales.refund_approve`, `tills.open_close`, `tills.view`, `quotations.manage` |
| customers | `customers.view`, `customers.manage`, `customers.credit_limit`, `customers.statement`, `customers.write_off` |
| purchasing | `suppliers.view`, `suppliers.manage`, `po.view`, `po.create`, `po.approve`, `grn.create`, `purchase_invoices.manage`, `purchase_returns.create` |
| expenses | `expenses.view`, `expenses.create`, `expenses.approve`, `expenses.pay`, `expenses.reverse`, `expense_categories.manage`, `petty_cash.manage`, `recurring_expenses.manage` |
| finance | `accounts.view`, `accounts.view_balance`, `accounts.manage`, `payments.view`, `payments.receive`, `payments.pay`, `payments.void`, `transfers_money.create`, `income.create`, `drawings.create`, `reconcile.manage`, `periods.lock`, `transactions.view` |
| hr | `employees.view`, `employees.manage`, `employees.view_salary`, `departments.manage`, `leave.request`, `leave.approve`, `loans.view`, `loans.create`, `loans.approve`, `adjustments.manage` |
| attendance | `attendance.clock`, `attendance.view`, `attendance.edit`, `overtime.approve`, `schedules.manage` |
| payroll | `payroll.view`, `payroll.prepare`, `payroll.approve`, `payroll.pay`, `payroll.reverse`, `payslips.view`, `components.manage` |
| approvals | `approvals.view`, `approvals.decide` |
| audit | `audit.view`, `backup.manage` |
| reports | `reports.sales`, `reports.inventory`, `reports.purchases`, `reports.expenses`, `reports.debts`, `reports.payroll`, `reports.staff`, `reports.branches`, `reports.financial`, `dashboard.view`, `dashboard.view_profit` |

```go
// internal/platform/middleware/permission.go
func Require(key string) fiber.Handler {
    return func(c fiber.Ctx) error {
        p := PrincipalFrom(c)
        g, ok := p.Perms[key]
        if !ok {
            return httpx.Forbidden("permission_denied")
        }
        c.Locals(scopeKey, Scope{Kind: g.Scope, Limit: g.Limit})
        return c.Next()
    }
}

// Scope becomes a filter every list and get query must accept.
type Filter struct {
    BusinessID uuid.UUID
    BranchIDs  []uuid.UUID // nil = all branches
    OwnerID    *uuid.UUID  // set when scope is "own"
}

func FilterFrom(c fiber.Ctx) Filter {
    p, s := PrincipalFrom(c), c.Locals(scopeKey).(Scope)
    f := Filter{BusinessID: p.BusinessID}
    switch s.Kind {
    case "branch":
        f.BranchIDs = p.BranchIDs
    case "own":
        f.BranchIDs = p.BranchIDs
        f.OwnerID = &p.UserID
    }
    return f
}
```

```go
// route registration: permission is declared next to the route
func Register(r fiber.Router, d Deps) {
    h := newHandler(d)
    g := r.Group("/sales")
    g.Get("/", middleware.Require("sales.view"), h.List)
    g.Post("/", middleware.Require("sales.create"), middleware.Idempotent(d), h.Create)
    g.Get("/:id", middleware.Require("sales.view"), h.Get)
    g.Post("/:id/void", middleware.Require("sales.void"), h.Void)
    g.Post("/:id/returns", middleware.Require("sales.refund_request"), h.CreateReturn)
}
```

**Three layers of defence**

1. Route middleware checks the permission key.
2. Repository queries always take a `Filter` and add `business_id = $1`, plus branch and owner conditions.
3. PostgreSQL row-level security rejects any row whose `business_id` differs from the session setting `app.business_id`, even if layers 1 and 2 have a bug.

A record outside the caller's scope returns 404, not 403, so its existence is not revealed. Approval limits are checked in the service: if the amount exceeds `Scope.Limit`, the service creates an approval request instead of posting.

## 6. Module pattern: worked example, create sale

The handler only translates HTTP. The service owns the rules and the transaction. This is the template for every write in the system.

```go
// modules/sales/dto.go
type CreateSaleRequest struct {
    CustomerID *uuid.UUID       `json:"customer_id"`
    Items      []SaleItemInput  `json:"items" validate:"required,min=1,dive"`
    Discount   *DiscountInput   `json:"discount"`
    Payments   []PaymentInput   `json:"payments" validate:"dive"`
    Note       string           `json:"note" validate:"max=500"`
}

type SaleItemInput struct {
    ProductID uuid.UUID       `json:"product_id" validate:"required"`
    Qty       decimal.Decimal `json:"qty" validate:"required"`
    Discount  *DiscountInput  `json:"discount"`
}

type PaymentInput struct {
    Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank"`
    MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
    Amount         decimal.Decimal `json:"amount" validate:"required"`
    Reference      string          `json:"reference"`
}
```

```go
// modules/sales/handler.go
func (h *handler) Create(c fiber.Ctx) error {
    var req CreateSaleRequest
    if err := c.Bind().Body(&req); err != nil {
        return httpx.BadRequest("invalid_body")
    }
    if err := h.validate.Struct(req); err != nil {
        return httpx.Validation(err)
    }
    p := middleware.PrincipalFrom(c)
    sale, err := h.svc.Create(c.Context(), p, req)
    if err != nil {
        return err
    }
    return c.Status(fiber.StatusCreated).JSON(httpx.Data(toSaleResponse(sale)))
}
```

```go
// modules/sales/service.go
func (s *Service) Create(ctx context.Context, p *auth.Principal, req CreateSaleRequest) (*Sale, error) {
    if p.BranchID == nil {
        return nil, httpx.BadRequest("branch_required")
    }
    var out *Sale
    err := s.db.InTx(ctx, p.BusinessID, func(tx pgx.Tx) error {
        // 1. till session must be open if any cash is taken
        till, err := s.tills.OpenSessionFor(ctx, tx, p.UserID, *p.BranchID)
        if err != nil && hasCash(req.Payments) {
            return ErrNoOpenTill
        }

        // 2. load products and price them on the server
        lines, err := s.priceLines(ctx, tx, *p.BranchID, req.Items)
        if err != nil {
            return err
        }
        totals := computeTotals(lines, req.Discount, s.settings.Tax(ctx, tx))

        // 3. discount limit: over the limit needs an approved request
        if err := s.checkDiscount(ctx, tx, p, totals); err != nil {
            return err
        }

        // 4. credit rules
        paid := sumPayments(req.Payments)
        if paid.LessThan(totals.Total) {
            if err := s.customers.CheckCredit(ctx, tx, req.CustomerID, totals.Total.Sub(paid)); err != nil {
                return err
            }
        }

        // 5. number and header
        number, err := s.seq.Next(ctx, tx, *p.BranchID, "sale")
        if err != nil {
            return err
        }
        sale, err := s.repo.InsertSale(ctx, tx, newSale(p, number, req, totals, paid, till))
        if err != nil {
            return err
        }

        // 6. stock: locks stock_levels rows, checks quantity, returns cost per line
        costs, err := s.stock.Deduct(ctx, tx, stock.DeductInput{
            BranchID: *p.BranchID, SourceType: "sale", SourceID: sale.ID, Lines: toStockLines(lines),
        })
        if err != nil {
            return err // stock.ErrInsufficient -> 409
        }
        if err := s.repo.InsertItems(ctx, tx, sale.ID, withCosts(lines, costs)); err != nil {
            return err
        }

        // 7. payments and journal
        if err := s.payments.ReceiveForSale(ctx, tx, sale, req.Payments); err != nil {
            return err
        }
        if err := s.ledger.PostSale(ctx, tx, sale, costs, req.Payments); err != nil {
            return err
        }

        // 8. audit and events, same transaction
        s.audit.Record(ctx, tx, p, "sale.completed", "sale", sale.ID, nil, sale)
        s.jobs.EnqueueTx(ctx, tx, jobs.CheckLowStock{BranchID: *p.BranchID, ProductIDs: productIDs(lines)})

        out = sale
        return nil
    })
    return out, err
}
```

```go
// platform/db/tx.go: every transaction sets the tenant for row-level security
func (d *DB) InTx(ctx context.Context, businessID uuid.UUID, fn func(pgx.Tx) error) error {
    tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx)
    if _, err := tx.Exec(ctx, "select set_config('app.business_id', $1, true)", businessID.String()); err != nil {
        return err
    }
    if err := fn(tx); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

Rules for every service method: one transaction per use case, lock rows in a fixed order (stock levels by product ID) to avoid deadlocks, never call an external service inside the transaction, and enqueue follow-up work as a job in the same transaction.

## 7. Shared kernel

Six services that every module writes through. They take the caller's transaction, so their writes commit or roll back together with the business document.

### 7.1 Ledger

`ledger.Post(ctx, tx, Entry)` inserts one journal entry with lines and fails if debits and credits differ. Modules never build lines by hand; they call a named posting function below. Account codes refer to the chart of accounts in the Database schema tab.

| Event | Debit | Credit |
| --- | --- | --- |
| Sale, paid part | Money account of each payment | 4000 Sales revenue (net of tax), 2100 Output tax |
| Sale, credit part | 1100 Accounts receivable | 4000 Sales revenue, 2100 Output tax |
| Sale, cost side | 5000 Cost of goods sold | 1200 Inventory |
| Sale discount | Recorded as lower revenue. Discount total kept on the sale for reports |  |
| Sale return | 4010 Sales returns, 2100 Output tax | Money account (refund) or 1100 Receivable (debt reduced) or 2300 Customer credit |
| Sale return, stock back | 1200 Inventory | 5000 Cost of goods sold |
| Customer payment | Money account | 1100 Accounts receivable. Excess to 2300 Customer credit |
| Stock in, opening | 1200 Inventory | 3100 Opening balance equity |
| Stock in, found | 1200 Inventory | 4200 Stock gain |
| Stock out, damage or loss | 5100 Stock loss | 1200 Inventory |
| Stock adjustment up / down | 1200 Inventory / 5100 Stock loss | 4200 Stock gain / 1200 Inventory |
| Goods received | 1200 Inventory | 2050 Goods received not invoiced |
| Purchase invoice | 2050 Goods received not invoiced, 1500 Input tax | 2000 Accounts payable |
| Supplier payment | 2000 Accounts payable. Excess to 1400 Supplier advances | Money account |
| Purchase return | 2000 Accounts payable | 1200 Inventory, 1500 Input tax |
| Transfer dispatch | 1210 Goods in transit | 1200 Inventory (sending branch) |
| Transfer receive | 1200 Inventory (receiving branch), 5110 Transfer loss for any shortfall | 1210 Goods in transit |
| Expense approved and paid | 6xxx Expense account of the category | Money account |
| Money transfer | Destination money account, 6400 Bank charges for fee | Source money account |
| Other income | Money account | 4100 Other income |
| Owner capital / drawings | Money account / 3200 Drawings | 3000 Owner equity / Money account |
| Money account opening balance | Money account | 3100 Opening balance equity |
| Loan or advance disbursed | 1300 Staff loans | Money account |
| Payroll approved | 6100 Salaries and wages (gross), 6110 Employer contributions | 2200 Salaries payable (net), 2210 Statutory deductions payable, 1300 Staff loans (repayments), 2210 for employer part |
| Salary paid | 2200 Salaries payable | Money account |
| Statutory remittance | 2210 Statutory deductions payable | Money account |
| Till over / short on close | 6300 Cash over or short / Till account | Till account / 6300 Cash over or short |
| Bad debt write-off | 6200 Bad debts | 1100 Accounts receivable |
| Void or reversal of anything | Mirror of the original entry, linked by `reverses_id` |  |

Each journal line also carries `branch_id` and, where relevant, `party_type` and `party_id`, so customer, supplier and employee balances are sums over lines.

### 7.2 Stock

| Function | Does |
| --- | --- |
| `Receive(tx, branch, lines, source)` | Lock stock level rows, insert positive movements, update quantity and weighted average cost |
| `Deduct(tx, branch, lines, source)` | Lock rows in product ID order, check quantity against the negative stock setting, insert negative movements at current average cost, return cost per line |
| `Return(tx, branch, lines, source)` | Positive movement at the original sale cost |
| `Adjust(tx, branch, lines, source)` | Signed movement to reach the counted quantity |
| `TransferOut / TransferIn` | Movements at sender's cost; receiver's average is recalculated |
| `Rebuild(branch)` | Job: recompute levels from movements and report mismatches |

### 7.3 Sequence

`seq.Next(tx, branchID, docType)` runs `UPDATE document_sequences SET next_number = next_number + 1 ... RETURNING` inside the caller's transaction and formats `<BRANCHCODE>-<PREFIX>-<000001>`. Document types: sale, invoice, receipt, return, quotation, po, grn, pinv, preturn, transfer, adjustment, expense, payment\_in, payment\_out, money\_transfer, payroll, payslip, loan.

### 7.4 Approval

| Function | Does |
| --- | --- |
| `Request(tx, kind, subject, amount, requester)` | Insert a pending request, notify eligible approvers |
| `Decide(tx, requestID, approver, approve, note)` | Check approver is not the requester (unless Owner), check limit, set status, then call the subject module's registered `OnApproved` or `OnRejected` hook in the same transaction |
| `Register(kind, hooks)` | Each module registers its hooks at start: sales (discount, refund, void), expenses, inventory (adjustment), purchasing (po), hr (leave, loan), attendance (overtime), payroll (run), customers (credit override), finance (payment void) |

### 7.5 Audit

`audit.Record(tx, principal, action, entityType, entityID, before, after)` stores JSON before and after, IP and request ID. Fields tagged `audit:"-"` (password hash, PIN, tokens) are dropped. The table grants INSERT and SELECT only to the application role.

### 7.6 Notify

`notify.ToPermission(tx, branchID, permissionKey, message)` and `notify.ToUser(tx, userID, message)` insert in-app rows. A job fans out to email or SMS if a sender is configured. The frontend polls `GET /notifications/unread-count` every 30 seconds; server-sent events can replace polling later.

## 8. Background jobs and schedules

Jobs run in the same binary through the PostgreSQL-backed queue. Each job is idempotent because it may run twice.

| Job | Trigger | Does |
| --- | --- | --- |
| `CheckLowStock` | After sale, stock out, transfer, adjustment | Raise low stock notifications for products that crossed reorder level |
| `RecurringExpenses` | Daily 00:30 business time | Create submitted expenses from templates due today |
| `DebtReminders` | Daily 08:00 | Notify overdue customer invoices and supplier invoices due soon |
| `SalaryDue` | Daily 08:00 | Notify days before pay day |
| `AttendanceClose` | Daily, after business day end | Build `attendance_days` from events, mark absent, flag missing clock-outs |
| `OwnerDailySummary` | Daily at day end | Summary notification to Owners |
| `StockRebuildCheck` | Nightly | Compare stock levels with movements, alert on mismatch |
| `LedgerBalanceCheck` | Nightly | Verify every entry balances and the trial balance is zero |
| `ReportExport` | On request | Build large Excel or PDF, store file, notify the user with a link |
| `PayrollCalculate` | On request | Calculate a run for many employees |
| `SendNotification` | On notification insert | Email or SMS fan-out |
| `SessionCleanup` | Daily | Delete expired sessions and idempotency keys older than 48 hours |
| `DatabaseBackup` | Nightly, outside the app by cron on the host | `pg_dump` to off-server storage |

## 9. Errors, validation, logging, configuration

**Errors.** Services return `*httpx.Error` with an HTTP status, a stable machine code and a human message. The Fiber error handler turns anything else into a 500 with the request ID and logs the real error.

```go
// internal/platform/httpx/errors.go
type Error struct {
    Status  int               `json:"-"`
    Code    string            `json:"code"`
    Message string            `json:"message"`
    Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code }

func ErrorHandler(c fiber.Ctx, err error) error {
    var e *Error
    if errors.As(err, &e) {
        return c.Status(e.Status).JSON(fiber.Map{"error": e})
    }
    var fe *fiber.Error
    if errors.As(err, &fe) {
        return c.Status(fe.Code).JSON(fiber.Map{"error": Error{Code: "http_error", Message: fe.Message}})
    }
    slog.Error("unhandled", "err", err, "path", c.Path())
    return c.Status(500).JSON(fiber.Map{"error": Error{Code: "internal_error", Message: "Something went wrong"}})
}
```

| Status | When | Example codes |
| --- | --- | --- |
| 400 | Malformed request | `invalid_body`, `branch_required` |
| 401 | Not authenticated | `invalid_token`, `session_ended`, `invalid_credentials`, `account_locked` |
| 403 | Authenticated, no permission | `permission_denied` |
| 404 | Missing or outside scope | `not_found` |
| 409 | State conflict | `insufficient_stock`, `invalid_status`, `period_locked`, `till_not_open`, `credit_limit_exceeded`, `duplicate` |
| 422 | Field validation | `validation_failed` with `fields` |
| 429 | Rate limited | `too_many_requests` |
| 202 | Accepted, waiting approval | body carries `approval_request_id` |

**Validation.** Three levels: struct tags for shape, service checks for business rules, database constraints (NOT NULL, CHECK, UNIQUE, foreign keys) as the last line.

**Logging.** JSON lines with `time`, `level`, `request_id`, `business_id`, `user_id`, `method`, `path`, `status`, `duration_ms`. Never log request bodies on auth routes.

**Configuration**

| Variable | Meaning |
| --- | --- |
| `HTTP_ADDR` | Listen address, for example `:8080` |
| `DATABASE_URL` | PostgreSQL connection string |
| `JWT_SECRET` | Signing key for access tokens |
| `ACCESS_TOKEN_TTL`, `REFRESH_TOKEN_TTL` | Token lifetimes |
| `CORS_ORIGINS` | Comma-separated list of allowed frontend origins |
| `COOKIE_DOMAIN`, `COOKIE_SECURE` | Refresh cookie settings |
| `STORAGE_DRIVER`, `STORAGE_PATH`, `S3_*` | File storage |
| `DEFAULT_TIMEZONE`, `DEFAULT_CURRENCY` | Defaults for new businesses |
| `SMS_*`, `SMTP_*` | Optional senders |
| `LOG_LEVEL`, `APP_ENV` | Logging and environment name |
