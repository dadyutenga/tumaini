package app

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"

	"bms/internal/config"
	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/deps"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/stock"
	"bms/internal/modules/attendance"
	"bms/internal/modules/catalog"
	"bms/internal/modules/customers"
	"bms/internal/modules/expenses"
	"bms/internal/modules/finance"
	"bms/internal/modules/hr"
	"bms/internal/modules/identity"
	"bms/internal/modules/inbox"
	"bms/internal/modules/inventory"
	"bms/internal/modules/org"
	"bms/internal/modules/payroll"
	"bms/internal/modules/purchasing"
	"bms/internal/modules/reports"
	"bms/internal/modules/sales"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/jobs"
	"bms/internal/platform/middleware"
	"bms/internal/platform/storage"
)

type App struct {
	Fiber *fiber.App
	DB    *db.DB
	Jobs  *jobs.Client
}

func New(ctx context.Context, cfg config.Config, clk clock.Clock) (*App, error) {
	database, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	store, err := storage.NewLocal(cfg.StoragePath)
	if err != nil {
		database.Close()
		return nil, err
	}
	aud := audit.New()
	n := notify.New()
	led := ledger.New()
	st := stock.New()
	ap := approval.New(n)
	ident := &identity.Service{DB: database, Auth: auth.Tokens{Secret: []byte(cfg.JWTSecret), Access: cfg.AccessTokenTTL, Now: clk.Now}, Hash: auth.Params{Memory: cfg.ArgonMemory, Time: cfg.ArgonTime, Threads: cfg.ArgonThreads}, Clock: clk, Audit: aud, Notify: n, Ledger: led, TTL: cfg.RefreshTokenTTL}
	worker := jobs.New(database.Pool)
	worker.SetClock(clk.Now)
	d := deps.Deps{
		Config: cfg, DB: database, Ledger: led, Stock: st, Audit: aud, Notify: n, Approval: ap,
		Clock: clk, Store: store, Jobs: worker,
		Hash: auth.Params{Memory: cfg.ArgonMemory, Time: cfg.ArgonTime, Threads: cfg.ArgonThreads},
	}

	fiberApp := fiber.New(fiber.Config{
		AppName:      "bms-api",
		ErrorHandler: httpx.ErrorHandler,
		BodyLimit:    10 * 1024 * 1024,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	})
	fiberApp.Use(recover.New())
	fiberApp.Use(requestid.New())
	fiberApp.Use(middleware.RequestLogger())
	fiberApp.Use(middleware.SecurityHeaders())
	fiberApp.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSOrigins,
		AllowCredentials: true,
		AllowHeaders:     []string{"Authorization", "Content-Type", "X-Branch-Id", "Idempotency-Key"},
	}))
	fiberApp.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })
	fiberApp.Get("/readyz", func(c fiber.Ctx) error {
		if err := database.Pool.Ping(c.Context()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("db")
		}
		return c.SendString("ok")
	})

	api := fiberApp.Group("/api/v1")
	limit := middleware.NewLimiter(30, time.Minute).Handler()
	identity.RegisterPublic(api.Group("/auth"), &identity.Handler{Svc: ident, CookieSecure: cfg.CookieSecure, CookieDomain: cfg.CookieDomain, RefreshTTL: cfg.RefreshTokenTTL}, limit)

	private := api.Group("", middleware.Auth(auth.Tokens{Secret: []byte(cfg.JWTSecret), Access: cfg.AccessTokenTTL, Now: clk.Now}, ident.Load), middleware.Branch(database))
	identity.Register(private, &identity.Handler{Svc: ident, CookieSecure: cfg.CookieSecure, CookieDomain: cfg.CookieDomain, RefreshTTL: cfg.RefreshTokenTTL})
	for _, mount := range []func(fiber.Router, deps.Deps){
		mountAny(org.Mount), mountAny(catalog.Mount), mountAny(inventory.Mount), mountAny(sales.Mount),
		mountAny(customers.Mount), mountAny(expenses.Mount), mountAny(finance.Mount), mountAny(purchasing.Mount),
		mountAny(hr.Mount), mountAny(attendance.Mount), mountAny(payroll.Mount), mountAny(reports.Mount),
		mountAny(inbox.Mount),
	} {
		mount(private, d)
	}

	return &App{Fiber: fiberApp, DB: database, Jobs: worker}, nil
}

// mountAny adapts a module Mount that returns its service.
func mountAny[T any](m func(fiber.Router, deps.Deps) T) func(fiber.Router, deps.Deps) {
	return func(r fiber.Router, d deps.Deps) { m(r, d) }
}

func (a *App) StartJobs(ctx context.Context) { go a.Jobs.Run(ctx) }

func (a *App) Close() { a.DB.Close() }

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}
