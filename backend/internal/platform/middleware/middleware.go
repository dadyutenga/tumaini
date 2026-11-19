package middleware

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
)

const principalKey = "principal"
const scopeKey = "scope"

type Scope struct {
	Kind  string
	Limit any
}

func RequestLogger() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		p, _ := c.Locals(principalKey).(*auth.Principal)
		uid, tid := "", ""
		if p != nil {
			uid = p.UserID.String()
			tid = p.TenantID.String()
		}
		slog.Info("request",
			"request_id", requestid.FromContext(c),
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"duration_ms", time.Since(start).Milliseconds(),
			"user_id", uid,
			"tenant_id", tid,
		)
		return err
	}
}

func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("X-Request-Id", requestid.FromContext(c))
		return c.Next()
	}
}

// limiterMaxKeys is when the limiter starts evicting expired buckets.
const limiterMaxKeys = 10000

type bucket struct {
	start time.Time
	n     int
}

type Limiter struct {
	mu    sync.Mutex
	m     map[string]bucket
	max   int
	win   time.Duration
	swept time.Time
}

func NewLimiter(max int, win time.Duration) *Limiter {
	return &Limiter{m: map[string]bucket{}, max: max, win: win}
}

func (l *Limiter) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.IP() + "|" + c.Path()
		now := time.Now()
		l.mu.Lock()
		// Evict expired buckets so a flood of distinct addresses cannot grow the map without bound.
		if len(l.m) >= limiterMaxKeys && now.Sub(l.swept) >= time.Second {
			l.swept = now
			for k, v := range l.m {
				if now.Sub(v.start) > l.win {
					delete(l.m, k)
				}
			}
			// Still over the hard cap inside one window: memory matters more than remembering.
			if len(l.m) >= 4*limiterMaxKeys {
				l.m = map[string]bucket{}
			}
		}
		b := l.m[key]
		if now.Sub(b.start) > l.win {
			b = bucket{start: now}
		}
		b.n++
		l.m[key] = b
		tooMany := b.n > l.max
		l.mu.Unlock()
		if tooMany {
			return httpx.TooMany("too_many_requests", "Too many attempts. Wait a minute and try again.")
		}
		return c.Next()
	}
}

type PrincipalLoader func(ctx context.Context, claims auth.Claims) (*auth.Principal, error)

func Auth(tokens auth.Tokens, load PrincipalLoader) fiber.Handler {
	return func(c fiber.Ctx) error {
		raw := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
		if raw == "" || raw == c.Get("Authorization") {
			return httpx.Unauthorized("invalid_token", "Sign in again")
		}
		claims, err := tokens.Verify(raw)
		if err != nil {
			return httpx.Unauthorized("invalid_token", "Sign in again")
		}
		p, err := load(c.Context(), claims)
		if err != nil {
			return err
		}
		p.IP = c.IP()
		p.UserAgent = c.Get("User-Agent")
		p.RequestID = requestid.FromContext(c)
		c.Locals(principalKey, p)
		return c.Next()
	}
}

func Principal(c fiber.Ctx) *auth.Principal {
	p, _ := c.Locals(principalKey).(*auth.Principal)
	return p
}

// Branch resolves the location a request is about. X-Branch-Id names a location (kept under its
// old header name so clients do not change); "all" means no location filter; no header means the
// member's default location. The approved model has no per-person location assignment, so any
// active location of the tenant is allowed; row-level security already limits ids to the tenant.
func Branch(database *db.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := Principal(c)
		if p == nil {
			return httpx.Unauthorized("invalid_token", "Sign in again")
		}
		raw := c.Get("X-Branch-Id")
		if raw == "all" {
			return c.Next()
		}
		if raw == "" {
			var id *uuid.UUID
			err := database.InActorTx(c.Context(), p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT default_location_id FROM app.memberships WHERE id = $1`, p.MembershipID).Scan(&id)
			})
			if err == nil && id != nil {
				p.BranchID = id
				c.Locals(principalKey, p)
			}
			return c.Next()
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("invalid_branch", "X-Branch-Id must be a location id")
		}
		var active bool
		err = database.InActorTx(c.Context(), p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT status = 'active' FROM app.locations WHERE id = $1`, id).Scan(&active)
		})
		if err != nil || !active {
			return httpx.NotFound("not_found", "Branch not found")
		}
		p.BranchID = &id
		c.Locals(principalKey, p)
		return c.Next()
	}
}

func hasAllScope(p *auth.Principal) bool {
	for _, g := range p.Perms {
		if g.Scope == "all" {
			return true
		}
	}
	return false
}

func RequireAny(keys ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := Principal(c)
		for _, key := range keys {
			if g, ok := p.Can(key); ok {
				c.Locals(scopeKey, g)
				c.Locals("perm_used", key)
				return c.Next()
			}
		}
		return httpx.Forbidden("permission_denied", "You do not have permission to do that")
	}
}

func Require(key string) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := Principal(c)
		g, ok := p.Can(key)
		if !ok {
			return httpx.Forbidden("permission_denied", "You do not have permission to do that")
		}
		c.Locals(scopeKey, g)
		return c.Next()
	}
}

func GrantFrom(c fiber.Ctx) auth.Grant {
	g, _ := c.Locals(scopeKey).(auth.Grant)
	return g
}

// Idempotent requires a key and stores it for the service to claim inside its transaction.
func Idempotent() fiber.Handler {
	return func(c fiber.Ctx) error {
		key := c.Get("Idempotency-Key")
		if key == "" {
			return httpx.BadRequest("idempotency_key_required", "Send an Idempotency-Key header so a retry cannot double-post")
		}
		if _, err := uuid.Parse(key); err != nil {
			return httpx.BadRequest("idempotency_key_required", "Idempotency-Key must be a UUID")
		}
		c.SetContext(idem.With(c.Context(), idem.New(key, append([]byte(nil), c.Body()...), c.Method(), c.Path())))
		return c.Next()
	}
}
