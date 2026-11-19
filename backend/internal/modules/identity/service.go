package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
)

// Sign-in lockout. The tenant is not known until the person is identified, so these are fixed
// defaults; they match the tenant_settings defaults (lockout_threshold, lockout_minutes).
const (
	lockoutThreshold = 5
	lockoutFor       = 15 * time.Minute
	historyDepth     = 5
)

type Service struct {
	DB     *db.DB
	Auth   auth.Tokens
	Hash   auth.Params
	Clock  clock.Clock
	Audit  *audit.Service
	Notify *notify.Service
	// TTL is how long a session may live in total (REFRESH_TOKEN_TTL).
	TTL time.Duration
	// IdleTimeout is used until the tenant's own setting is known (SESSION_IDLE_TIMEOUT).
	IdleTimeout time.Duration
	// Pepper keys the HMAC that stands in for the typed identifier in auth events, so history
	// never holds a phone number or email in plain text.
	Pepper []byte
}

type SessionResult struct {
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	Refresh     string    `json:"-"`
	UserID      uuid.UUID `json:"user_id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	// BusinessID repeats TenantID under the name the web client already uses.
	BusinessID         uuid.UUID `json:"business_id"`
	MembershipID       uuid.UUID `json:"membership_id"`
	BranchID           uuid.UUID `json:"branch_id"`
	MustChangePassword bool      `json:"must_change_password"`
}

func (s *Service) identifierHMAC(identifier string) []byte {
	m := hmac.New(sha256.New, s.Pepper)
	m.Write([]byte(strings.ToLower(strings.TrimSpace(identifier))))
	return m.Sum(nil)
}

func (s *Service) idle() time.Duration {
	if s.IdleTimeout <= 0 {
		return 30 * time.Minute
	}
	return s.IdleTimeout
}

// ---------------------------------------------------------------------------------------------
// Register
// ---------------------------------------------------------------------------------------------

type RegisterInput struct {
	BusinessName string
	OwnerName    string
	Phone        string
	Email        string
	Password     string
	IP           string
	UserAgent    string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugFor(name string) string {
	base := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(base) < 3 {
		base = "business"
	}
	if len(base) > 50 {
		base = strings.Trim(base[:50], "-")
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return base + "-" + hex.EncodeToString(b)
}

// Register creates the owner's identity and the tenant in one transaction, then signs the owner in.
// The tenant, its business units, location, roles, permissions, numbering and notification rules
// come from app.provision_tenant; defaults.go adds the starting reference data.
func (s *Service) Register(ctx context.Context, in RegisterInput) (SessionResult, error) {
	name, bname := strings.TrimSpace(in.OwnerName), strings.TrimSpace(in.BusinessName)
	if name == "" || bname == "" || strings.TrimSpace(in.Phone) == "" {
		return SessionResult{}, httpx.Validation(errors.New("required"))
	}
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return SessionResult{}, httpx.BadRequest("invalid_phone", err.Error())
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if err := auth.PasswordOK(in.Password); err != nil {
		return SessionResult{}, httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return SessionResult{}, err
	}
	var userID, tenantID, membershipID uuid.UUID
	var locationID *uuid.UUID
	err = s.DB.InJobsTx(ctx, db.Actor{RequestID: "", ClientIP: in.IP}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO iam.users (full_name, email, phone, preferred_locale)
			VALUES ($1, NULLIF($2,'')::app.email, $3::app.phone_e164, 'en') RETURNING id
		`, name, email, phone).Scan(&userID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already registered")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.user_credentials (user_id, password_hash, must_change_password) VALUES ($1, $2, false)
		`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash) VALUES ($1, $2)`, userID, hash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT app.provision_tenant($1, $2, $3)`, slugFor(bname), bname, userID).Scan(&tenantID); err != nil {
			return err
		}
		// provision_tenant left app.tenant_id set for this transaction, so the reads below see the new tenant.
		if err := tx.QueryRow(ctx, `
			SELECT id, default_location_id FROM app.memberships WHERE user_id = $1 AND tenant_id = $2
		`, userID, tenantID).Scan(&membershipID, &locationID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.tenant_settings SET phone = $2::app.phone_e164, email = NULLIF($3,'')::app.email WHERE tenant_id = $1`,
			tenantID, phone, email); err != nil {
			return err
		}
		return seedDefaults(ctx, tx, tenantID, locationID)
	})
	if err != nil {
		return SessionResult{}, err
	}
	out, err := s.startSession(ctx, userID, tenantID, membershipID, in.IP, in.UserAgent)
	if err != nil {
		return SessionResult{}, err
	}
	s.event(ctx, "login_success", &userID, &tenantID, in.Phone, &out.sessionID, in.IP, in.UserAgent, "")
	return out.SessionResult, nil
}

// ---------------------------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------------------------

type started struct {
	SessionResult
	sessionID uuid.UUID
}

// startSession opens an iam.sessions row for the member, issues the opaque refresh token (stored only
// as its SHA-256) and the signed access token.
func (s *Service) startSession(ctx context.Context, userID, tenantID, membershipID uuid.UUID, ip, ua string) (started, error) {
	now := s.Clock.Now()
	raw, hash, err := auth.RandomToken(32)
	if err != nil {
		return started{}, err
	}
	var idleMinutes int
	var location *uuid.UUID
	err = s.DB.InActorTx(ctx, db.Actor{TenantID: tenantID, UserID: userID, MembershipID: membershipID}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT session_idle_minutes FROM app.tenant_settings WHERE tenant_id = $1`, tenantID).Scan(&idleMinutes); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT default_location_id FROM app.memberships WHERE id = $1`, membershipID).Scan(&location)
	})
	if err != nil {
		return started{}, err
	}
	var sid uuid.UUID
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO iam.sessions (user_id, tenant_id, membership_id, token_hash, created_at, last_seen_at,
			                          idle_timeout, absolute_expires_at, ip, user_agent)
			VALUES ($1,$2,$3,$4,$5,$5, make_interval(mins => $6), $7, NULLIF($8,'')::inet, LEFT($9, 512))
			RETURNING id
		`, userID, tenantID, membershipID, hash, now, idleMinutes, now.Add(s.TTL), ip, ua).Scan(&sid)
	})
	if err != nil {
		return started{}, err
	}
	access, err := s.Auth.Sign(userID, tenantID, membershipID, sid, now)
	if err != nil {
		return started{}, err
	}
	r := SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: raw,
		UserID: userID, TenantID: tenantID, BusinessID: tenantID, MembershipID: membershipID}
	if location != nil {
		r.BranchID = *location
	}
	return started{SessionResult: r, sessionID: sid}, nil
}

// event appends to iam.auth_events (the Login History screen). It never fails the request.
func (s *Service) event(ctx context.Context, kind string, userID, tenantID *uuid.UUID, identifier string, sessionID *uuid.UUID, ip, ua, reason string) {
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO iam.auth_events (occurred_at, event_type, user_id, tenant_id, identifier_hmac, session_id, ip, user_agent, failure_reason)
			VALUES ($1, $2::iam.auth_event_type, $3, $4, $5, $6, NULLIF($7,'')::inet, LEFT($8, 512), NULLIF($9,''))
		`, s.Clock.Now(), kind, userID, tenantID, s.identifierHMAC(identifier), sessionID, ip, ua, reason)
		return err
	})
}

var errInvalid = httpx.Unauthorized("invalid_credentials", "Phone, email or password is wrong")

type found struct {
	ID          uuid.UUID
	Hash        string
	MustChange  bool
	LockedUntil *time.Time
	Disabled    bool
}

func (s *Service) findUser(ctx context.Context, identifier string) (found, error) {
	var u found
	col, val := "phone", identifier
	if IsEmail(identifier) {
		col, val = "email", strings.ToLower(identifier)
	} else if p, err := NormalizePhone(identifier); err == nil {
		val = p
	} else {
		return u, pgx.ErrNoRows
	}
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT u.id, c.password_hash, c.must_change_password, c.locked_until, u.status <> 'active'
			FROM iam.users u JOIN iam.user_credentials c ON c.user_id = u.id
			WHERE u.`+col+` = $1`, val).Scan(&u.ID, &u.Hash, &u.MustChange, &u.LockedUntil, &u.Disabled)
	})
	return u, err
}

type LoginInput struct {
	Identifier, Password, IP, UserAgent string
	// TenantID picks the business when the person belongs to several; empty picks the one they joined first.
	TenantID *uuid.UUID
}

func (s *Service) Login(ctx context.Context, in LoginInput) (SessionResult, error) {
	id := strings.TrimSpace(in.Identifier)
	now := s.Clock.Now()
	u, err := s.findUser(ctx, id)
	if err != nil {
		s.event(ctx, "login_failure", nil, nil, id, nil, in.IP, in.UserAgent, "unknown_identifier")
		return SessionResult{}, errInvalid
	}
	if u.LockedUntil != nil && u.LockedUntil.After(now) {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "locked")
		return SessionResult{}, httpx.Unauthorized("account_locked", "This account is locked for 15 minutes after too many attempts")
	}
	if u.Disabled {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "disabled")
		return SessionResult{}, errInvalid
	}
	if !auth.VerifyPassword(u.Hash, in.Password) {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "bad_password")
		s.failed(ctx, u.ID, id, in, now)
		return SessionResult{}, errInvalid
	}
	tenantID, membershipID, err := s.pickMembership(ctx, u.ID, in.TenantID)
	if err != nil {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "membership_inactive")
		return SessionResult{}, errInvalid
	}
	out, err := s.startSession(ctx, u.ID, tenantID, membershipID, in.IP, in.UserAgent)
	if err != nil {
		return SessionResult{}, err
	}
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE iam.user_credentials SET failed_attempts = 0, last_failed_at = NULL, locked_until = NULL, updated_at = $2 WHERE user_id = $1`, u.ID, now)
		return err
	})
	s.event(ctx, "login_success", &u.ID, &tenantID, id, &out.sessionID, in.IP, in.UserAgent, "")
	out.MustChangePassword = u.MustChange
	return out.SessionResult, nil
}

// failed counts a wrong password and locks the account at the threshold.
func (s *Service) failed(ctx context.Context, userID uuid.UUID, identifier string, in LoginInput, now time.Time) {
	var attempts int
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE iam.user_credentials
			   SET failed_attempts = failed_attempts + 1, last_failed_at = $2, updated_at = $2,
			       locked_until = CASE WHEN failed_attempts + 1 >= $3 THEN $4::timestamptz ELSE locked_until END
			 WHERE user_id = $1 RETURNING failed_attempts`, userID, now, lockoutThreshold, now.Add(lockoutFor)).Scan(&attempts)
	})
	if err == nil && attempts == lockoutThreshold {
		s.event(ctx, "account_locked", &userID, nil, identifier, nil, in.IP, in.UserAgent, "")
	}
}

// pickMembership chooses the tenant to sign in to: the requested one, else the earliest joined active seat.
func (s *Service) pickMembership(ctx context.Context, userID uuid.UUID, want *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var tenantID, membershipID uuid.UUID
	err := s.DB.InUserTx(ctx, userID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT m.tenant_id, m.id FROM app.memberships m
			  JOIN app.tenants t ON t.id = m.tenant_id AND t.status = 'active'
			 WHERE m.user_id = $1 AND m.status = 'active' AND ($2::uuid IS NULL OR m.tenant_id = $2)
			 ORDER BY m.joined_at NULLS LAST, m.created_at LIMIT 1`, userID, want).Scan(&tenantID, &membershipID)
	})
	return tenantID, membershipID, err
}

type sessionRow struct {
	ID, UserID                   uuid.UUID
	TenantID, MembershipID       *uuid.UUID
	Revoked                      bool
	Absolute, LastSeen           time.Time
	Idle                         time.Duration
	UserActive, MembershipActive bool
}

func (s *Service) Refresh(ctx context.Context, raw, ip, ua string) (SessionResult, error) {
	ended := httpx.Unauthorized("session_ended", "Sign in again")
	hash, err := auth.HashToken(raw)
	if err != nil {
		return SessionResult{}, ended
	}
	now := s.Clock.Now()
	var sr sessionRow
	var idleSecs float64
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT s.id, s.user_id, s.tenant_id, s.membership_id, s.revoked_at IS NOT NULL, s.absolute_expires_at,
			       s.last_seen_at, extract(epoch FROM s.idle_timeout), u.status = 'active'
			FROM iam.sessions s JOIN iam.users u ON u.id = s.user_id WHERE s.token_hash = $1`, hash).
			Scan(&sr.ID, &sr.UserID, &sr.TenantID, &sr.MembershipID, &sr.Revoked, &sr.Absolute, &sr.LastSeen, &idleSecs, &sr.UserActive)
	})
	if err != nil || sr.Revoked || !sr.UserActive || sr.TenantID == nil || sr.MembershipID == nil {
		return SessionResult{}, ended
	}
	sr.Idle = time.Duration(idleSecs * float64(time.Second))
	switch {
	case !sr.Absolute.After(now):
		s.revoke(ctx, sr.ID, "expired")
		return SessionResult{}, ended
	case now.Sub(sr.LastSeen) > sr.Idle:
		s.revoke(ctx, sr.ID, "idle_timeout")
		s.event(ctx, "session_timeout", &sr.UserID, sr.TenantID, "", &sr.ID, ip, ua, "")
		return SessionResult{}, httpx.Unauthorized("session_ended", "You were signed out after a period of inactivity. Sign in again.")
	}
	var active bool
	var location *uuid.UUID
	err = s.DB.InActorTx(ctx, db.Actor{TenantID: *sr.TenantID, UserID: sr.UserID, MembershipID: *sr.MembershipID}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT m.status = 'active' AND t.status = 'active', m.default_location_id
			FROM app.memberships m JOIN app.tenants t ON t.id = m.tenant_id WHERE m.id = $1`, *sr.MembershipID).Scan(&active, &location)
	})
	if err != nil || !active {
		s.revoke(ctx, sr.ID, "membership_revoked")
		return SessionResult{}, ended
	}
	rawNew, newHash, err := auth.RandomToken(32)
	if err != nil {
		return SessionResult{}, err
	}
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE iam.sessions SET token_hash = $2, last_seen_at = $3, ip = NULLIF($4,'')::inet, user_agent = LEFT($5, 512) WHERE id = $1`,
			sr.ID, newHash, now, ip, ua)
		return err
	})
	if err != nil {
		return SessionResult{}, err
	}
	access, err := s.Auth.Sign(sr.UserID, *sr.TenantID, *sr.MembershipID, sr.ID, now)
	if err != nil {
		return SessionResult{}, err
	}
	out := SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: rawNew,
		UserID: sr.UserID, TenantID: *sr.TenantID, BusinessID: *sr.TenantID, MembershipID: *sr.MembershipID}
	if location != nil {
		out.BranchID = *location
	}
	return out, nil
}

func (s *Service) revoke(ctx context.Context, sessionID uuid.UUID, reason string) {
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $2, revoked_reason = $3 WHERE id = $1 AND revoked_at IS NULL`, sessionID, s.Clock.Now(), reason)
		return err
	})
}

func (s *Service) Logout(ctx context.Context, p *auth.Principal) error {
	s.revoke(ctx, p.SessionID, "logout")
	s.event(ctx, "logout", &p.UserID, &p.TenantID, "", &p.SessionID, p.IP, p.UserAgent, "")
	return nil
}

// ---------------------------------------------------------------------------------------------
// Password
// ---------------------------------------------------------------------------------------------

func (s *Service) ChangePassword(ctx context.Context, p *auth.Principal, current, next string) error {
	if err := auth.PasswordOK(next); err != nil {
		return httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(next, s.Hash)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	var failure error
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var old string
		var attempts int
		var last *time.Time
		if err := tx.QueryRow(ctx, `SELECT password_hash, failed_attempts, last_failed_at FROM iam.user_credentials WHERE user_id = $1 FOR UPDATE`, p.UserID).Scan(&old, &attempts, &last); err != nil {
			return err
		}
		if attempts >= lockoutThreshold && last != nil && now.Sub(*last) < lockoutFor {
			failure = httpx.TooMany("too_many_requests", "Too many wrong answers. Wait 15 minutes.")
			return nil
		}
		if !auth.VerifyPassword(old, current) {
			failure = httpx.Unauthorized("invalid_credentials", "Current password is wrong")
			_, err := tx.Exec(ctx, `UPDATE iam.user_credentials SET failed_attempts = failed_attempts + 1, last_failed_at = $2 WHERE user_id = $1`, p.UserID, now)
			return err
		}
		rows, err := tx.Query(ctx, `SELECT password_hash FROM iam.password_history WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, p.UserID, historyDepth)
		if err != nil {
			return err
		}
		var prior []string
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return err
			}
			prior = append(prior, h)
		}
		rows.Close()
		for _, h := range prior {
			if auth.VerifyPassword(h, next) {
				failure = httpx.BadRequest("password_reused", "Choose a password you have not used recently")
				return nil
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE iam.user_credentials SET password_hash = $2, password_changed_at = $3, must_change_password = false,
			       failed_attempts = 0, last_failed_at = NULL, updated_at = $3 WHERE user_id = $1`, p.UserID, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash, created_at) VALUES ($1, $2, $3)`, p.UserID, hash, now); err != nil {
			return err
		}
		// Everyone else signed in as this user is signed out: a stolen session must not survive a password change.
		_, err = tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $3, revoked_reason = 'password_changed'
			WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, p.UserID, p.SessionID, now)
		return err
	})
	if err != nil {
		return err
	}
	if failure != nil {
		return failure
	}
	s.event(ctx, "password_changed", &p.UserID, &p.TenantID, "", &p.SessionID, p.IP, p.UserAgent, "")
	return nil
}

// ---------------------------------------------------------------------------------------------
// Per-request principal
// ---------------------------------------------------------------------------------------------

// Load turns verified token claims into the caller for this request. Everything is read fresh, so a
// disabled user, a revoked membership, an ended session or a changed permission takes effect on the
// next request rather than when the access token expires.
func (s *Service) Load(ctx context.Context, claims auth.Claims) (*auth.Principal, error) {
	ended := httpx.Unauthorized("session_ended", "Sign in again")
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, httpx.Unauthorized("invalid_token", "Sign in again")
	}
	now := s.Clock.Now()
	p := &auth.Principal{UserID: userID, TenantID: claims.TenantID, BusinessID: claims.TenantID,
		MembershipID: claims.MembershipID, SessionID: claims.SessionID, Perms: map[string]auth.Grant{}, Limits: map[string]decimal.Decimal{}}

	var idle bool
	var lastSeen time.Time
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var revoked, userOK bool
		var absolute time.Time
		var idleSecs float64
		var tid, mid *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT s.revoked_at IS NOT NULL, s.absolute_expires_at, s.last_seen_at, extract(epoch FROM s.idle_timeout),
			       s.tenant_id, s.membership_id, u.status = 'active', u.full_name, u.preferred_locale
			FROM iam.sessions s JOIN iam.users u ON u.id = s.user_id WHERE s.id = $1 AND s.user_id = $2`, claims.SessionID, userID).
			Scan(&revoked, &absolute, &lastSeen, &idleSecs, &tid, &mid, &userOK, &p.Name, &p.Language); err != nil {
			return ended
		}
		if revoked || !userOK || tid == nil || mid == nil || *tid != claims.TenantID || *mid != claims.MembershipID || !absolute.After(now) {
			return ended
		}
		if now.Sub(lastSeen) > time.Duration(idleSecs*float64(time.Second)) {
			idle = true
			return httpx.Unauthorized("session_ended", "You were signed out after a period of inactivity. Sign in again.")
		}
		return nil
	})
	if idle {
		s.revoke(ctx, claims.SessionID, "idle_timeout")
	}
	if err != nil {
		return nil, err
	}
	if now.Sub(lastSeen) > time.Minute { // at most one write a minute for ordinary traffic
		_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE iam.sessions SET last_seen_at = $2 WHERE id = $1`, claims.SessionID, now)
			return err
		})
	}
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var active bool
		var roleCode string
		var employee *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT m.status = 'active' AND t.status = 'active', coalesce(r.code::text, ''), r.id, e.id
			FROM app.memberships m
			JOIN app.tenants t ON t.id = m.tenant_id
			LEFT JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
			LEFT JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id
			LEFT JOIN app.employees e ON e.tenant_id = m.tenant_id AND e.membership_id = m.id
			WHERE m.id = $1`, claims.MembershipID).Scan(&active, &roleCode, &p.RoleID, &employee); err != nil || !active {
			return ended
		}
		p.RoleSlug, p.IsOwner, p.EmployeeID = roleCode, roleCode == "admin", employee
		// Effective permissions: a user override (allow or deny, unexpired) beats the role, exactly as app.has_permission does.
		rows, err := tx.Query(ctx, `
			SELECT p.code FROM app.permissions p
			WHERE coalesce(
			  (SELECT o.effect = 'allow' FROM app.membership_permission_overrides o
			    WHERE o.membership_id = $1 AND o.permission_code = p.code AND (o.expires_at IS NULL OR o.expires_at > $2)),
			  EXISTS (SELECT 1 FROM app.membership_roles mr
			            JOIN app.role_permissions rp ON rp.tenant_id = mr.tenant_id AND rp.role_id = mr.role_id
			            JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id AND r.status = 'active'
			           WHERE mr.membership_id = $1 AND rp.permission_code = p.code))`, claims.MembershipID, now)
		if err != nil {
			return err
		}
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				rows.Close()
				return err
			}
			p.Perms[code] = auth.Grant{Scope: "all"}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		lr, err := tx.Query(ctx, `
			SELECT code::text, app.authority_limit($1, code) FROM unnest(enum_range(NULL::app.limit_code)) AS code`, claims.MembershipID)
		if err != nil {
			return err
		}
		defer lr.Close()
		for lr.Next() {
			var code string
			var v *decimal.Decimal
			if err := lr.Scan(&code, &v); err != nil {
				return err
			}
			if v != nil {
				p.Limits[code] = *v
			}
		}
		return lr.Err()
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}
