package middleware

import (
	"context"
	"log/slog"
	"strconv"
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
		// Write the error response here so the status logged is the one the client receives.
		if err := c.Next(); err != nil {
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}
		p, _ := c.Locals(principalKey).(*auth.Principal)
		uid := ""
		if p != nil {
			uid = p.UserID.String()
		}
		slog.Info("request",
			"request_id", requestid.FromContext(c),
			"method", c.Method(),
			"path", httpx.LogPath(c),
			"status", c.Response().StatusCode(),
			"duration_ms", time.Since(start).Milliseconds(),
			"user_id", uid,
		)
		return nil
	}
}

func SecurityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("X-Request-Id", requestid.FromContext(c))
		if strings.HasPrefix(c.Path(), "/api/") {
			c.Set("Cache-Control", "no-store")
		}
		if c.Protocol() == "https" || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https") {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
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
	// shared, when set, counts across processes and restarts; name keeps limiters apart there.
	shared Counter
	name   string
}

// Counter counts hits somewhere every API process can see.
type Counter interface {
	Hit(ctx context.Context, key string, window time.Duration) (hits, retryAfter int, err error)
}

// Share adds a shared counter behind the in-process one. name must be unique per limiter.
func (l *Limiter) Share(c Counter, name string) *Limiter {
	l.shared, l.name = c, name
	return l
}

// check applies the in-process budget and then the shared one. If the shared counter fails the
// in-process budget still holds, so a database hiccup does not lock everyone out of signing in.
func (l *Limiter) check(c fiber.Ctx, key string) (bool, int) {
	if ok, retryAfter := l.allow(key); !ok || l.shared == nil {
		return ok, retryAfter
	}
	hits, retryAfter, err := l.shared.Hit(c.Context(), l.name+"|"+key, l.win)
	if err != nil {
		slog.Warn("shared rate limit unavailable", "err", err)
		return true, 0
	}
	return hits <= l.max, retryAfter
}

func NewLimiter(max int, win time.Duration) *Limiter {
	return &Limiter{m: map[string]bucket{}, max: max, win: win}
}

func (l *Limiter) allow(key string) (bool, int) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
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
	if b.n > l.max {
		retryAfter := int(l.win.Seconds() - now.Sub(b.start).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		return false, retryAfter
	}
	return true, 0
}

func (l *Limiter) Handler() fiber.Handler {
	return func(c fiber.Ctx) error {
		ok, retryAfter := l.check(c, c.IP()+"|"+routeKey(c))
		if !ok {
			c.Set("Retry-After", strconv.Itoa(retryAfter))
			return httpx.TooMany("too_many_requests", "Too many attempts. Wait a minute and try again.")
		}
		return c.Next()
	}
}

// routeKey names the matched route, not the requested path. Routing ignores case and a trailing
// slash, and public routes carry a token in the path; keying on the raw path would give every
// spelling and every guessed token its own budget.
func routeKey(c fiber.Ctx) string { return c.Route().Method + " " + c.Route().Path }

// HandlerByUser rate limits authenticated requests by user id (falling back to IP) and route path.
func (l *Limiter) HandlerByUser() fiber.Handler {
	return func(c fiber.Ctx) error {
		subject := c.IP()
		if p := Principal(c); p != nil {
			subject = p.UserID.String()
		}
		ok, retryAfter := l.check(c, subject+"|"+routeKey(c))
		if !ok {
			c.Set("Retry-After", strconv.Itoa(retryAfter))
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

// Branch resolves the location a request is about and enforces branch-level access restrictions.
// X-Branch-Id names a location; "all" means no location filter (cross-branch callers only);
// no header defaults to the member's default location.
func Branch(database *db.DB) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := Principal(c)
		if p == nil {
			return httpx.Unauthorized("invalid_token", "Sign in again")
		}
		// Self-profile and auth lifecycle routes must remain accessible even if a shared browser
		// still carries a stale header from a previous session.
		path := c.Path()
		isSelfAuthPath := path == "/api/v1/me" || strings.HasPrefix(path, "/api/v1/me/") || strings.HasPrefix(path, "/api/v1/auth/")

		if err := businessScope(c.Get("X-Business-Unit"), p); err != nil && !isSelfAuthPath {
			return err
		}
		setDefault := func() error {
			if len(p.BranchIDs) == 0 {
				return c.Next()
			}
			id := p.BranchIDs[0]
			// A default branch of the business the switcher left out would show nothing.
			if len(p.Active) < len(p.Units) {
				var active bool
				err := database.InActorTx(c.Context(), p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
					return tx.QueryRow(ctx, `SELECT app.can_access_business(business_unit_id) FROM app.locations WHERE id = $1`, id).Scan(&active)
				})
				if err != nil {
					return err
				}
				if !active && p.CanAccessAllBranches() {
					return c.Next()
				}
			}
			p.BranchID = &id
			c.Locals(principalKey, p)
			return c.Next()
		}

		raw := c.Get("X-Branch-Id")
		if raw == "" {
			return setDefault()
		}
		if raw == "all" {
			if !p.CanAccessAllBranches() {
				if isSelfAuthPath {
					return setDefault()
				}
				return httpx.Forbidden("branch_forbidden", "You do not have access to all branches")
			}
			return c.Next()
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			if isSelfAuthPath {
				return setDefault()
			}
			return httpx.BadRequest("invalid_branch", "X-Branch-Id must be a location id")
		}
		if !p.AllowsBranch(id) {
			if isSelfAuthPath {
				return setDefault()
			}
			return httpx.Forbidden("branch_forbidden", "You do not have access to this branch")
		}
		var active bool
		var unit uuid.UUID
		err = database.InActorTx(c.Context(), p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT status = 'active', business_unit_id FROM app.locations WHERE id = $1`, id).Scan(&active, &unit)
		})
		if err == nil && active && !p.MayUseUnit(unit) {
			if isSelfAuthPath {
				return setDefault()
			}
			return httpx.Forbidden("branch_forbidden", "That branch belongs to a business you do not work in")
		}
		if err != nil || !active {
			if isSelfAuthPath {
				return setDefault()
			}
			return httpx.NotFound("not_found", "Branch not found")
		}
		p.BranchID = &id
		c.Locals(principalKey, p)
		return c.Next()
	}
}

// businessScope narrows the request to the business named by X-Business-Unit ("A", "B" or "all").
func businessScope(header string, p *auth.Principal) error {
	switch header {
	case "", "all":
		return nil
	case auth.Shop, auth.Services:
		for _, u := range p.Units {
			if u.Code == header {
				p.Active = []auth.Unit{u}
				return nil
			}
		}
		return httpx.Forbidden("business_forbidden", "You do not work in that business")
	}
	return httpx.BadRequest("invalid_business", "X-Business-Unit must be A, B or all")
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

// RequireRecentAuth ensures the caller verified their primary credentials within window.
// Sensitive account and privilege changes return 403 reauth_required once the window expires.
func RequireRecentAuth(window time.Duration, now func() time.Time) fiber.Handler {
	if window <= 0 {
		window = 10 * time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return func(c fiber.Ctx) error {
		p := Principal(c)
		if p == nil {
			return httpx.Unauthorized("invalid_token", "Sign in again")
		}
		if p.ReauthenticatedAt.IsZero() || now().Sub(p.ReauthenticatedAt) > window {
			return httpx.Forbidden("reauth_required", "Please confirm your password to continue")
		}
		return c.Next()
	}
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
