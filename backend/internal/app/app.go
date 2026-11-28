package app

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"

	"bms/internal/config"
	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/deps"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
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
	"bms/internal/modules/services"
	"bms/internal/modules/system"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/jobs"
	"bms/internal/platform/middleware"
	"bms/internal/platform/ratelimit"
	"bms/internal/platform/scan"
	"bms/internal/platform/sender"
	"bms/internal/platform/storage"
)

type App struct {
	Fiber *fiber.App
	DB    *db.DB
	Jobs  *jobs.Client
	// Recovery and Outbound are exposed so tests can plug in recording senders.
	Recovery *identity.Recovery
	Outbound *outbound.Service
}

func New(ctx context.Context, cfg config.Config, clk clock.Clock) (*App, error) {
	setupLogging(cfg.LogLevel)
	database, err := db.Connect(ctx, cfg.DatabaseURL, cfg.JobsDatabaseURL)
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
	outb := outbound.New(database, clk, sender.OutboundFromConfig(cfg))
	ap := approval.New(n)
	pepper := sha256.Sum256([]byte("auth-events:" + cfg.JWTSecret))
	ident := &identity.Service{
		DB: database, Auth: auth.Tokens{Secret: []byte(cfg.JWTSecret), Access: cfg.AccessTokenTTL, Now: clk.Now},
		Hash:  auth.Params{Memory: cfg.ArgonMemory, Time: cfg.ArgonTime, Threads: cfg.ArgonThreads},
		Clock: clk, Audit: aud, Notify: n, TTL: cfg.RefreshTokenTTL, IdleTimeout: cfg.SessionIdleTimeout,
		ReauthTTL: cfg.ReauthTTL, Pepper: pepper[:],
		DefaultTimezone: cfg.DefaultTimezone, DefaultCurrency: cfg.DefaultCurrency,
	}
	jobPool := database.Jobs
	if jobPool == nil {
		jobPool = database.Pool
	}
	worker := jobs.New(jobPool)
	worker.SetClock(clk.Now)
	worker.Register(jobs.DispatchOutbound, outb.Dispatch)
	var uploads storage.Store = store
	if cfg.ClamdAddr != "" {
		uploads = scan.Store{Store: store, Scanner: scan.Clamd{Addr: cfg.ClamdAddr}}
	}
	shared := ratelimit.DB{DB: database, Scope: cfg.RateLimitScope}
	d := deps.Deps{
		Config: cfg, DB: database, Audit: aud, Notify: n, Outbound: outb, Approval: ap,
		Clock: clk, Store: store, Uploads: uploads, Jobs: worker,
		Hash: auth.Params{Memory: cfg.ArgonMemory, Time: cfg.ArgonTime, Threads: cfg.ArgonThreads},
	}

	fcfg := fiber.Config{
		AppName:      "bms-api",
		ErrorHandler: httpx.ErrorHandler,
		BodyLimit:    10 * 1024 * 1024,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if len(cfg.TrustedProxies) > 0 {
		// Only these proxies may say who the client is; anyone else is seen at the socket.
		fcfg.ProxyHeader = fiber.HeaderXForwardedFor
		fcfg.TrustProxy = true
		fcfg.TrustProxyConfig = fiber.TrustProxyConfig{Proxies: cfg.TrustedProxies}
		fcfg.EnableIPValidation = true
	}
	fiberApp := fiber.New(fcfg)
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
	limit := middleware.NewLimiter(30, time.Minute).Share(shared, "public").Handler()
	authGroup := api.Group("/auth")
	identityHandler := &identity.Handler{Svc: ident, CookieSecure: cfg.CookieSecure, CookieDomain: cfg.CookieDomain, RefreshTTL: cfg.RefreshTokenTTL}
	identity.RegisterPublic(authGroup, identityHandler, limit)
	setupGroup := api.Group("/setup")
	setupGroup.Get("/status", limit, identityHandler.SetupStatus)
	setupGroup.Post("", limit, identityHandler.Setup)
	recovery := &identity.Recovery{Svc: ident}
	recovery.Email, recovery.SMS = sender.FromConfig(cfg)
	authGroup.Post("/forgot-password", limit, recovery.ForgotHandler)
	authGroup.Post("/reset-password", limit, recovery.ResetHandler)

	svcHandler := services.Build(d)
	svcHandler.RegisterPublic(api.Group("/public"), limit)

	private := api.Group("", middleware.Auth(auth.Tokens{Secret: []byte(cfg.JWTSecret), Access: cfg.AccessTokenTTL, Now: clk.Now}, ident.Load), middleware.Branch(database))
	identity.Register(private, &identity.Handler{Svc: ident, CookieSecure: cfg.CookieSecure, CookieDomain: cfg.CookieDomain, RefreshTTL: cfg.RefreshTokenTTL, Shared: shared})
	limitCostly(private, shared)
	for _, mount := range []func(fiber.Router, deps.Deps){
		mountAny(org.Mount), mountAny(catalog.Mount), mountAny(inventory.Mount), mountAny(sales.Mount),
		mountAny(customers.Mount), mountAny(expenses.Mount), mountAny(finance.Mount), mountAny(purchasing.Mount),
		mountAny(hr.Mount), mountAny(attendance.Mount), mountAny(payroll.Mount), mountAny(reports.Mount),
		mountAny(inbox.Mount), mountAny(system.Mount),
	} {
		mount(private, d)
	}
	svcHandler.Register(private)

	return &App{Fiber: fiberApp, DB: database, Jobs: worker, Recovery: recovery, Outbound: outb}, nil
}

// limitCostly gives each member a per-minute budget on the routes that spend money (messages to
// customers) or capacity (a full export, search). It must be registered before the modules' own handlers.
func limitCostly(r fiber.Router, shared middleware.Counter) {
	export := middleware.NewLimiter(5, time.Minute).Share(shared, "export").HandlerByUser()
	send := middleware.NewLimiter(60, time.Minute).Share(shared, "send").HandlerByUser()
	r.Post("/backups/export", export)
	// Search runs several queries per request. Typing sends a few requests a second at most.
	r.Get("/search", middleware.NewLimiter(240, time.Minute).HandlerByUser())
	for _, path := range []string{
		"/sales/:id/send-receipt", "/customers/:id/statement/send", "/customers/:id/debt-reminder",
		"/clients/:id/statement/send", "/clients/:id/debt-reminder", "/service-quotations/:id/send",
		"/design-jobs/:id/versions/:version_id/send",
	} {
		r.Post(path, send)
	}
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

// setupLogging applies LOG_LEVEL to the structured JSON logger.
func setupLogging(level string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}
