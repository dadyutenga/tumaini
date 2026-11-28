package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/argon2"

	"bms/internal/platform/db"
)

type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

func HashPassword(password string, p Params) (string, error) {
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return "", errors.New("invalid argon2 parameters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// DummyPasswordHash returns a valid Argon2id hash using p so login can perform
// constant-work verification even when an identifier does not match an account.
func DummyPasswordHash(p Params) string {
	if p.Memory == 0 {
		p.Memory = 19456
	}
	if p.Time == 0 {
		p.Time = 2
	}
	if p.Threads == 0 {
		p.Threads = 1
	}
	h, err := HashPassword("dummy-constant-time-password-placeholder", p)
	if err != nil {
		return ""
	}
	return h
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	if memory == 0 || timeCost == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var commonPasswords = map[string]struct{}{
	"password": {}, "password1": {}, "12345678": {}, "123456789": {}, "qwertyui": {},
	"iloveyou": {}, "admin123": {}, "letmein1": {}, "welcome1": {}, "changeme": {},
}

func PasswordOK(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 200 {
		return errors.New("password is too long")
	}
	if _, ok := commonPasswords[strings.ToLower(password)]; ok {
		return errors.New("password is too common")
	}
	return nil
}

func PINOK(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return errors.New("PIN must be 4 to 6 digits")
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return errors.New("PIN must be digits")
		}
	}
	return nil
}

func RandomToken(n int) (string, []byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(buf)
	return base64.RawURLEncoding.EncodeToString(buf), sum[:], nil
}

func HashToken(raw string) ([]byte, error) {
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(buf)
	return sum[:], nil
}

// Claims is what the access token proves: who, in which tenant, as which member, in which session.
// Permissions are not in the token; they are loaded on every request so a revoked permission or a
// disabled user takes effect immediately.
type Claims struct {
	jwt.RegisteredClaims
	TenantID     uuid.UUID `json:"tid"`
	MembershipID uuid.UUID `json:"mid"`
	SessionID    uuid.UUID `json:"sid"`
	Purpose      string    `json:"pur,omitempty"`
}

type Tokens struct {
	Secret []byte
	Access time.Duration
	// Now is the clock used to check expiry. It must match the clock used to sign,
	// otherwise a fixed or skewed application clock makes fresh tokens look expired.
	Now func() time.Time
}

func (t Tokens) Sign(userID, tenantID, membershipID, sessionID uuid.UUID, now time.Time) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.Access)),
		},
		TenantID:     tenantID,
		MembershipID: membershipID,
		SessionID:    sessionID,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(t.Secret)
}

func (t Tokens) Verify(raw string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return t.Secret, nil
	}, t.parserOptions()...)
	if err != nil {
		return Claims{}, err
	}
	if claims.Purpose != "" {
		return Claims{}, errors.New("unexpected token purpose")
	}
	return claims, nil
}

func (t Tokens) parserOptions() []jwt.ParserOption {
	opts := []jwt.ParserOption{jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired()}
	if t.Now != nil {
		opts = append(opts, jwt.WithTimeFunc(t.Now))
	}
	return opts
}

// Grant is the scope and optional approval limit for one permission.
type Grant struct {
	Scope string           `json:"scope"`
	Limit *decimal.Decimal `json:"limit"`
}

type Principal struct {
	UserID       uuid.UUID
	TenantID     uuid.UUID
	MembershipID uuid.UUID
	RoleID       uuid.UUID
	// RoleRank is the rank of the caller's role (app.roles.rank, 100 = Admin). A delegated administrator
	// may not change an account or hand out a role that outranks their own.
	RoleRank   int
	EmployeeID *uuid.UUID
	// Limits are the caller's authority limits (app.authority_limits), keyed by limit code. A missing
	// key means unlimited for a holder of the matching permission.
	Limits            map[string]decimal.Decimal
	Name              string
	Language          string
	RoleSlug          string
	IsOwner           bool
	BranchIDs         []uuid.UUID
	Perms             map[string]Grant
	BranchID          *uuid.UUID
	SessionID         uuid.UUID
	ReauthenticatedAt time.Time
	IP                string
	UserAgent         string
	RequestID         string
}

// Actor is the database request context for this caller.
func (p *Principal) Actor() db.Actor {
	return db.Actor{TenantID: p.TenantID, UserID: p.UserID, MembershipID: p.MembershipID, RequestID: p.RequestID, ClientIP: p.IP}
}

// Has reports whether the caller holds a permission code.
// The tenant owner/admin always holds every permission.
func (p *Principal) Has(code string) bool {
	if p.IsOwner {
		return true
	}
	_, ok := p.Perms[code]
	return ok
}

func (p *Principal) Can(key string) (Grant, bool) {
	if p.IsOwner {
		if g, ok := p.Perms[key]; ok {
			return g, true
		}
		return Grant{Scope: "all"}, true
	}
	g, ok := p.Perms[key]
	return g, ok
}

// CanAccessAllBranches reports whether the caller may switch to other branches or query
// across all branches ("X-Branch-Id: all"). Users pinned to a default location without
// cross-branch oversight permissions are restricted to their assigned branch.
func (p *Principal) CanAccessAllBranches() bool {
	if p.IsOwner || len(p.BranchIDs) == 0 {
		return true
	}
	for _, perm := range []string{
		"settings.manage",
		"reports.business.view",
		"reports.operations.view",
		"dashboard.business.view",
		"dashboard.operations.view",
		"finance.account.view",
		"shop.stock.manage",
	} {
		if p.Has(perm) {
			return true
		}
	}
	return false
}

// AllowsBranch reports whether the caller is authorized to operate on branch id.
func (p *Principal) AllowsBranch(id uuid.UUID) bool {
	if p.CanAccessAllBranches() {
		return true
	}
	for _, b := range p.BranchIDs {
		if b == id {
			return true
		}
	}
	return false
}

// BranchFilter returns the branches a list query must be limited to for a grant.
// nil means no branch filter. An active branch (X-Branch-Id or default) always
// narrows to that branch; the middleware has already checked it is allowed.
func (p *Principal) BranchFilter(g Grant) []uuid.UUID {
	if p.BranchID != nil {
		return []uuid.UUID{*p.BranchID}
	}
	if p.IsOwner || g.Scope == "all" {
		return nil
	}
	if p.BranchIDs == nil {
		return []uuid.UUID{}
	}
	return p.BranchIDs
}

// ScopeBranches is BranchFilter for the grant of key.
func (p *Principal) ScopeBranches(key string) []uuid.UUID {
	g, _ := p.Can(key)
	return p.BranchFilter(g)
}

// OwnOnly returns the user ID when the grant is limited to own records.
func (p *Principal) OwnOnly(key string) *uuid.UUID {
	if p.IsOwner {
		return nil
	}
	if g, ok := p.Can(key); ok && g.Scope == "own" {
		id := p.UserID
		return &id
	}
	return nil
}

// SeesBranch reports whether a single record in branch is inside the caller's scope for key.
// Detail routes return 404 when this is false, so existence is not revealed.
func (p *Principal) SeesBranch(key string, branch uuid.UUID) bool {
	if p.IsOwner {
		return true
	}
	g, ok := p.Can(key)
	if !ok {
		return false
	}
	if g.Scope == "all" {
		return true
	}
	return p.AllowsBranch(branch)
}

// WithinLimit reports whether amount is allowed without a further approval.
// A nil limit means no cap. A missing permission is not within limit.
func WithinLimit(g Grant, ok bool, amount decimal.Decimal) bool {
	if !ok {
		return false
	}
	if g.Limit == nil {
		return true
	}
	return amount.LessThanOrEqual(*g.Limit)
}
