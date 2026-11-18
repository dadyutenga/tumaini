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
	tokens := auth.Tokens{Secret: []byte(cfg.JWTSecret), Access: cfg.AccessTokenTTL, Now: clk.Now}
	ident := &identity.Service{DB: database, Auth: tokens, Hash: auth.Params{Memory: cfg.ArgonMemory, Time: cfg.ArgonTime, Threads: cfg.ArgonThreads}, Clock: clk, Audit: aud, Notify: n, Ledger: led, TTL: cfg.RefreshTokenTTL}
	orgSvc := org.New(database, aud, led, store, clk)
	catSvc := catalog.New(database, aud)
	invSvc := inventory.New(database, st, led, aud, n, ap, clk)
	salSvc := sales.New(database, st, led, aud, ap, clk)
	cusSvc := customers.New(database, led, aud, clk)
	expSvc := expenses.New(database, led, aud, ap, clk)
	finSvc := finance.New(database, led, aud, clk)
	purSvc := purchasing.New(database, st, led, aud, ap, clk)
	hrSvc := hr.New(database, led, clk)
	attSvc := attendance.New(database, clk)
	paySvc := payroll.New(database, led, clk)
	repSvc := reports.New(database)
	boxSvc := inbox.New(database, ap)

	ap.Register("discount", approval.Hooks{OnApproved: salSvc.OnApproved, OnRejected: salSvc.OnRejected})
	ap.Register("credit_override", approval.Hooks{OnApproved: salSvc.OnApproved, OnRejected: salSvc.OnRejected})
	ap.Register("refund", approval.Hooks{OnApproved: salSvc.OnReturnApproved})
	ap.Register("expense", approval.Hooks{OnApproved: expSvc.OnApproved, OnRejected: expSvc.OnRejected})
	ap.Register("stock_adjustment", approval.Hooks{OnApproved: invSvc.OnAdjustmentApproved, OnRejected: invSvc.OnAdjustmentRejected})
	ap.Register("purchase_order", approval.Hooks{OnApproved: purSvc.OnPOApproved})

	worker := jobs.New(database.Pool)
	worker.Register(jobs.CheckLowStock, invSvc.CheckLowStock)

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

	private := api.Group("", middleware.Auth(tokens, ident.Load), middleware.Branch(database))
	identity.Register(private, &identity.Handler{Svc: ident, CookieSecure: cfg.CookieSecure, CookieDomain: cfg.CookieDomain, RefreshTTL: cfg.RefreshTokenTTL})
	org.Register(private, &org.Handler{Svc: orgSvc})
	catalog.Register(private, &catalog.Handler{Svc: catSvc})
	inventory.Register(private, &inventory.Handler{Svc: invSvc})
	sales.Register(private, &sales.Handler{Svc: salSvc})
	customers.Register(private, &customers.Handler{Svc: cusSvc})
	expenses.Register(private, &expenses.Handler{Svc: expSvc})
	finance.Register(private, &finance.Handler{Svc: finSvc})
	purchasing.Register(private, &purchasing.Handler{Svc: purSvc})
	hr.Register(private, &hr.Handler{Svc: hrSvc})
	attendance.Register(private, &attendance.Handler{Svc: attSvc})
	payroll.Register(private, &payroll.Handler{Svc: paySvc})
	reports.Register(private, &reports.Handler{Svc: repSvc})
	inbox.Register(private, &inbox.Handler{Svc: boxSvc})

	_ = os.Stderr
	return &App{Fiber: fiberApp, DB: database, Jobs: worker}, nil
}

func (a *App) StartJobs(ctx context.Context) { go a.Jobs.Run(ctx) }

func (a *App) Close() { a.DB.Close() }

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}
