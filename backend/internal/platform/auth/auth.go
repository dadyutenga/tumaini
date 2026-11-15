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
)

type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

func HashPassword(password string, p Params) (string, error) {
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
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
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

type Claims struct {
	jwt.RegisteredClaims
	BusinessID  uuid.UUID `json:"bid"`
	RoleID      uuid.UUID `json:"rid"`
	SessionID   uuid.UUID `json:"sid"`
	PermVersion int       `json:"pv"`
}

type Tokens struct {
	Secret []byte
	Access time.Duration
}

func (t Tokens) Sign(userID, businessID, roleID, sessionID uuid.UUID, permVersion int, now time.Time) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.Access)),
		},
		BusinessID:  businessID,
		RoleID:      roleID,
		SessionID:   sessionID,
		PermVersion: permVersion,
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
	})
	if err != nil {
		return Claims{}, err
	}
	return claims, nil
}

// Grant is the scope and optional approval limit for one permission.
type Grant struct {
	Scope string           `json:"scope"`
	Limit *decimal.Decimal `json:"limit"`
}

type Principal struct {
	UserID     uuid.UUID
	BusinessID uuid.UUID
	RoleID     uuid.UUID
	EmployeeID *uuid.UUID
	Name       string
	Language   string
	RoleSlug   string
	IsOwner    bool
	BranchIDs  []uuid.UUID
	Perms      map[string]Grant
	BranchID   *uuid.UUID
	SessionID  uuid.UUID
	IP         string
	UserAgent  string
	RequestID  string
}

func (p *Principal) Can(key string) (Grant, bool) {
	g, ok := p.Perms[key]
	return g, ok
}

func (p *Principal) AllowsBranch(id uuid.UUID) bool {
	if p.IsOwner {
		return true
	}
	for _, b := range p.BranchIDs {
		if b == id {
			return true
		}
	}
	return false
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
