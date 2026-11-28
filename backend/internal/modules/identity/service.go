package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"sync"
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
	"bms/internal/platform/ids"
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
	// ReauthTTL is how long a recent credential check remains valid for sensitive actions.
	ReauthTTL time.Duration
	// Pepper keys the HMAC that stands in for the typed identifier in auth events, so history
	// never holds a phone number or email in plain text.
	Pepper          []byte
	DefaultTimezone string
	DefaultCurrency string

	dummyOnce sync.Once
	dummyHash string
}

type SessionResult struct {
	AccessToken        string    `json:"access_token,omitempty"`
	ExpiresIn          int       `json:"expires_in"`
	Refresh            string    `json:"-"`
	UserID             uuid.UUID `json:"user_id,omitempty"`
	MembershipID       uuid.UUID `json:"membership_id,omitempty"`
	BranchID           uuid.UUID `json:"branch_id,omitempty"`
	MustChangePassword bool      `json:"must_change_password,omitempty"`
}

func (s *Service) reauthWindow() time.Duration {
	if s.ReauthTTL <= 0 {
		return 10 * time.Minute
	}
	return s.ReauthTTL
}

// verifyDummy runs Argon2id against a precomputed hash so failed logins for unknown,
// disabled, or locked accounts take the same time as a wrong password for a real user.
func (s *Service) verifyDummy(password string) {
	s.dummyOnce.Do(func() {
		s.dummyHash = auth.DummyPasswordHash(s.Hash)
	})
	if s.dummyHash != "" {
		_ = auth.VerifyPassword(s.dummyHash, password)
	}
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

type ReauthResult struct {
	ReauthenticatedAt time.Time `json:"reauthenticated_at"`
	ExpiresIn         int       `json:"expires_in"`
}

func (s *Service) Reauthenticate(ctx context.Context, p *auth.Principal, password string) (ReauthResult, error) {
	now := s.Clock.Now()
	var hash string
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT password_hash FROM iam.user_credentials WHERE user_id=$1`, p.UserID).Scan(&hash)
	})
	if err != nil || !auth.VerifyPassword(hash, password) {
		return ReauthResult{}, httpx.Unauthorized("invalid_password", "Password is incorrect")
	}
	if _, err := s.DB.Pool.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at=$2 WHERE id=$1`, p.SessionID, now); err != nil {
		return ReauthResult{}, err
	}
	return ReauthResult{ReauthenticatedAt: now, ExpiresIn: int(s.reauthWindow().Seconds())}, nil
}

// ---------------------------------------------------------------------------------------------
// First-run setup
// ---------------------------------------------------------------------------------------------

type SetupInput struct {
	BusinessName string
	OwnerName    string
	Phone        string
	Email        string
	Password     string
	IP           string
	UserAgent    string
}

var (
	nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
	nonCode = regexp.MustCompile(`[^a-z0-9_]+`)
)

func (s *Service) SetupStatus(ctx context.Context) (bool, error) {
	var exists bool
	err := s.DB.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.business_profile)`).Scan(&exists)
	return !exists, err
}

func (s *Service) Setup(ctx context.Context, in SetupInput) (SessionResult, error) {
	name, businessName := strings.TrimSpace(in.OwnerName), strings.TrimSpace(in.BusinessName)
	if name == "" || businessName == "" || strings.TrimSpace(in.Phone) == "" {
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
	var userID, membershipID, locationID uuid.UUID
	err = s.DB.InJobsTx(ctx, db.Actor{RequestID: "", ClientIP: in.IP}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(872341)`); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.business_profile)`).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return httpx.Conflict("setup_already_done", "Setup has already been completed")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.business_profile(name,timezone,default_currency,status,phone,email)
			VALUES ($1,$2,$3,'active',$4,NULLIF($5,'')::app.email)
		`, businessName, s.DefaultTimezone, s.DefaultCurrency, phone, email); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.business_settings(business_name,phone,email,currency)
			VALUES ($1,$2,NULLIF($3,'')::app.email,$4::app.currency)
		`, businessName, phone, email, s.DefaultCurrency); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO iam.users(full_name,email,phone,preferred_locale)
			VALUES ($1,NULLIF($2,'')::app.email,$3::app.phone_e164,'en') RETURNING id
		`, name, email, phone).Scan(&userID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already registered")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.user_credentials(user_id,password_hash,must_change_password)
			VALUES ($1,$2,false)
		`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history(user_id,password_hash) VALUES ($1,$2)`, userID, hash); err != nil {
			return err
		}
		var buA uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.business_units(code,name,kind) VALUES ('A','Shop','retail_shop') RETURNING id
		`).Scan(&buA); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.business_units(code,name,kind) VALUES ('B','Services','professional_services')
		`); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.locations(business_unit_id,name,kind,is_default)
			VALUES ($1,'Main shop','shop',true) RETURNING id
		`, buA).Scan(&locationID); err != nil {
			return err
		}
		var adminRole uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.roles(code,name,is_system,rank) VALUES ('admin','Admin',true,100) RETURNING id
		`).Scan(&adminRole); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.role_permissions(role_id,permission_code)
			SELECT $1,code FROM app.permissions
		`, adminRole); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.memberships(user_id,status,default_location_id,joined_at)
			VALUES ($1,'active',$2,now()) RETURNING id
		`, userID, locationID).Scan(&membershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.membership_roles(membership_id,role_id) VALUES ($1,$2)
		`, membershipID, adminRole); err != nil {
			return err
		}
		return seedDefaults(ctx, tx, &locationID)
	})
	if err != nil {
		return SessionResult{}, err
	}
	out, err := s.startSession(ctx, userID, uuid.Nil, membershipID, in.IP, in.UserAgent, nil)
	if err != nil {
		return SessionResult{}, err
	}
	s.event(ctx, "login_success", &userID, nil, in.Phone, &out.sessionID, in.IP, in.UserAgent, "")
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
func (s *Service) startSession(ctx context.Context, userID, tenantID, membershipID uuid.UUID, ip, ua string, mfaVerifiedAt *time.Time) (started, error) {
	now := s.Clock.Now()
	raw, hash, err := auth.RandomToken(32)
	if err != nil {
		return started{}, err
	}
	var idleMinutes int
	var location *uuid.UUID
	err = s.DB.InActorTx(ctx, db.Actor{UserID: userID, MembershipID: membershipID}, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT session_idle_minutes FROM app.business_settings`).Scan(&idleMinutes); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT default_location_id FROM app.memberships WHERE id = $1`, membershipID).Scan(&location)
	})
	if err != nil {
		return started{}, err
	}
	if idleMinutes <= 0 {
		idleMinutes = int(s.idle().Minutes())
	}
	var sid uuid.UUID
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO iam.sessions (user_id, membership_id, token_hash, created_at, last_seen_at,
			                          reauthenticated_at, idle_timeout, absolute_expires_at, ip, user_agent)
			VALUES ($1,$2,$3,$4,$4,$4, make_interval(mins => $5), $6, NULLIF($7,'')::inet, LEFT($8, 512))
			RETURNING id
		`, userID, membershipID, hash, now, idleMinutes, now.Add(s.TTL), ip, ua).Scan(&sid)
	})
	if err != nil {
		return started{}, err
	}
	access, err := s.Auth.Sign(userID, tenantID, membershipID, sid, now)
	if err != nil {
		return started{}, err
	}
	r := SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: raw,
		UserID: userID, MembershipID: membershipID}
	if location != nil {
		r.BranchID = *location
	}
	return started{SessionResult: r, sessionID: sid}, nil
}

// event appends to iam.auth_events (the Login History screen). It never fails the request.
func (s *Service) event(ctx context.Context, kind string, userID, tenantID *uuid.UUID, identifier string, sessionID *uuid.UUID, ip, ua, reason string) {
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO iam.auth_events (id, occurred_at, event_type, user_id, identifier_hmac, session_id, ip, user_agent, failure_reason)
			VALUES ($1, $2, $3::iam.auth_event_type, $4, $5, $6, NULLIF($7,'')::inet, LEFT($8, 512), NULLIF($9,''))
		`, ids.New(), s.Clock.Now(), kind, userID, s.identifierHMAC(identifier), sessionID, ip, ua, reason)
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
}

func (s *Service) Login(ctx context.Context, in LoginInput) (SessionResult, error) {
	id := strings.TrimSpace(in.Identifier)
	now := s.Clock.Now()
	u, err := s.findUser(ctx, id)
	if err != nil {
		s.verifyDummy(in.Password)
		s.event(ctx, "login_failure", nil, nil, id, nil, in.IP, in.UserAgent, "unknown_identifier")
		return SessionResult{}, errInvalid
	}
	if u.LockedUntil != nil && u.LockedUntil.After(now) {
		s.verifyDummy(in.Password)
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "locked")
		// Return the exact same error as invalid credentials so lockouts cannot be used to enumerate accounts.
		return SessionResult{}, errInvalid
	}
	if u.Disabled {
		s.verifyDummy(in.Password)
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "disabled")
		return SessionResult{}, errInvalid
	}
	if !auth.VerifyPassword(u.Hash, in.Password) {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "bad_password")
		s.failed(ctx, u.ID, id, in.IP, in.UserAgent, now)
		return SessionResult{}, errInvalid
	}
	_, membershipID, err := s.pickMembership(ctx, u.ID, nil)
	if err != nil {
		s.event(ctx, "login_failure", &u.ID, nil, id, nil, in.IP, in.UserAgent, "membership_inactive")
		return SessionResult{}, errInvalid
	}
	return s.completeLogin(ctx, u.ID, uuid.Nil, membershipID, u.MustChange, id, in.IP, in.UserAgent, nil, now)
}

func (s *Service) completeLogin(ctx context.Context, userID, tenantID, membershipID uuid.UUID, mustChange bool, id, ip, ua string, mfaVerifiedAt *time.Time, now time.Time) (SessionResult, error) {
	var notifyNewDevice bool
	if trimmedUA := strings.TrimSpace(ua); trimmedUA != "" && s.Notify != nil {
		_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var hadAny, hadSame bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM iam.sessions WHERE user_id = $1),
				       EXISTS (SELECT 1 FROM iam.sessions WHERE user_id = $1 AND user_agent = LEFT($2, 512))`,
				userID, trimmedUA).Scan(&hadAny, &hadSame); err == nil && hadAny && !hadSame {
				notifyNewDevice = true
			}
			return nil
		})
	}
	out, err := s.startSession(ctx, userID, tenantID, membershipID, ip, ua, mfaVerifiedAt)
	if err != nil {
		return SessionResult{}, err
	}
	if notifyNewDevice {
		_ = s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
			return s.Notify.ToUser(ctx, tx, tenantID, userID, notify.Message{
				Type: "account_locked", Title: "Sign-in from a new device",
				Body: "Your account was signed in from " + ua,
				Link: "/me/sessions",
			})
		})
	}
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE iam.user_credentials SET failed_attempts = 0, last_failed_at = NULL, locked_until = NULL, updated_at = $2 WHERE user_id = $1`, userID, now)
		return err
	})
	s.event(ctx, "login_success", &userID, &tenantID, id, &out.sessionID, ip, ua, "")
	out.MustChangePassword = mustChange
	return out.SessionResult, nil
}

// failed counts a wrong password or MFA code and locks the account at the threshold.
// If the previous failure window has elapsed, counting restarts at 1.
func (s *Service) failed(ctx context.Context, userID uuid.UUID, identifier, ip, ua string, now time.Time) {
	var attempts int
	windowStart := now.Add(-lockoutFor)
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE iam.user_credentials
			   SET failed_attempts = CASE WHEN last_failed_at IS NULL OR last_failed_at < $5 THEN 1 ELSE failed_attempts + 1 END,
			       last_failed_at = $2,
			       updated_at = $2,
			       locked_until = CASE
			         WHEN (CASE WHEN last_failed_at IS NULL OR last_failed_at < $5 THEN 1 ELSE failed_attempts + 1 END) >= $3
			         THEN $4::timestamptz
			         ELSE NULL
			       END
			 WHERE user_id = $1 RETURNING failed_attempts`,
			userID, now, lockoutThreshold, now.Add(lockoutFor), windowStart).Scan(&attempts)
	})
	if err == nil && attempts == lockoutThreshold {
		s.event(ctx, "account_locked", &userID, nil, identifier, nil, ip, ua, "")
		if s.Notify != nil {
			_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				return s.Notify.ToPermission(ctx, tx, uuid.Nil, nil, "security.user.manage", notify.Message{
					Type: "account_locked", Title: "Account locked",
					Body: "Account " + identifier + " was locked for 15 minutes after repeated failed sign-in attempts.",
					Link: "/users",
				})
			})
		}
	}
}

// pickMembership chooses the tenant to sign in to: the requested one, else the earliest joined active seat.
// A seat linked to a suspended or terminated employee cannot sign in.
func (s *Service) pickMembership(ctx context.Context, userID uuid.UUID, want *uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var membershipID uuid.UUID
	err := s.DB.InUserTx(ctx, userID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT m.id FROM app.memberships m
			  LEFT JOIN app.employees e ON e.membership_id = m.id
			 WHERE m.user_id = $1 AND m.status = 'active' AND (e.id IS NULL OR e.status = 'active')
			 ORDER BY m.joined_at NULLS LAST LIMIT 1`, userID).Scan(&membershipID)
	})
	return uuid.Nil, membershipID, err
}

type sessionRow struct {
	ID, UserID                   uuid.UUID
	MembershipID                 *uuid.UUID
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
	var reusedSessionID, reusedUserID uuid.UUID
	var tokenReused bool
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		qerr := tx.QueryRow(ctx, `
			SELECT s.id, s.user_id, s.membership_id, s.revoked_at IS NOT NULL, s.absolute_expires_at,
			       s.last_seen_at, extract(epoch FROM s.idle_timeout), u.status = 'active'
			FROM iam.sessions s JOIN iam.users u ON u.id = s.user_id WHERE s.token_hash = $1 FOR UPDATE OF s`, hash).
			Scan(&sr.ID, &sr.UserID, &sr.MembershipID, &sr.Revoked, &sr.Absolute, &sr.LastSeen, &idleSecs, &sr.UserActive)
		if errors.Is(qerr, pgx.ErrNoRows) {
			// Check if this is a previously rotated refresh token (reuse/theft detection).
			var sid, uid uuid.UUID
			if rerr := tx.QueryRow(ctx, `
				SELECT rt.session_id, rt.user_id
				  FROM iam.session_refresh_tokens rt
				  JOIN iam.sessions s ON s.id = rt.session_id
				 WHERE rt.token_hash = $1`, hash).Scan(&sid, &uid); rerr == nil {
				tokenReused = true
				if _, uerr := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $2, revoked_reason = 'token_reuse' WHERE id = $1 AND revoked_at IS NULL`, sid, now); uerr != nil {
					return uerr
				}
				return nil // commit the session revocation
			}
			return pgx.ErrNoRows
		}
		return qerr
	})
	if tokenReused {
		s.event(ctx, "forced_signout", &reusedUserID, nil, "", &reusedSessionID, ip, ua, "")
		return SessionResult{}, ended
	}
	if err != nil || sr.Revoked || !sr.UserActive || sr.MembershipID == nil {
		return SessionResult{}, ended
	}
	sr.Idle = time.Duration(idleSecs * float64(time.Second))
	switch {
	case !sr.Absolute.After(now):
		s.revoke(ctx, sr.ID, "expired")
		return SessionResult{}, ended
	case now.Sub(sr.LastSeen) > sr.Idle:
		s.revoke(ctx, sr.ID, "idle_timeout")
		s.event(ctx, "session_timeout", &sr.UserID, nil, "", &sr.ID, ip, ua, "")
		return SessionResult{}, httpx.Unauthorized("session_ended", "You were signed out after a period of inactivity. Sign in again.")
	}
	var active bool
	var location *uuid.UUID
	err = s.DB.InActorTx(ctx, db.Actor{UserID: sr.UserID, MembershipID: *sr.MembershipID}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT m.status = 'active' AND (e.id IS NULL OR e.status = 'active'), m.default_location_id
			  FROM app.memberships m
			  LEFT JOIN app.employees e ON e.membership_id = m.id
			 WHERE m.id = $1`, *sr.MembershipID).Scan(&active, &location)
	})
	if err != nil || !active {
		s.revoke(ctx, sr.ID, "membership_revoked")
		return SessionResult{}, ended
	}
	rawNew, newHash, err := auth.RandomToken(32)
	if err != nil {
		return SessionResult{}, err
	}
	var rotated bool
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE iam.sessions
			   SET token_hash = $2, last_seen_at = $3, ip = NULLIF($4,'')::inet, user_agent = LEFT($5, 512)
			 WHERE id = $1 AND token_hash = $6 AND revoked_at IS NULL`,
			sr.ID, newHash, now, ip, ua, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			// Concurrent refresh raced and rotated the token: revoke the session as suspected replay.
			_, _ = tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $2, revoked_reason = 'token_reuse' WHERE id = $1 AND revoked_at IS NULL`, sr.ID, now)
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.session_refresh_tokens (token_hash, session_id, user_id, replaced_at)
			VALUES ($1, $2, $3, $4) ON CONFLICT (token_hash) DO NOTHING`,
			hash, sr.ID, sr.UserID, now); err != nil {
			return err
		}
		rotated = true
		return nil
	})
	if err != nil {
		return SessionResult{}, err
	}
	if !rotated {
		s.event(ctx, "forced_signout", &sr.UserID, nil, "", &sr.ID, ip, ua, "")
		return SessionResult{}, ended
	}
	access, err := s.Auth.Sign(sr.UserID, uuid.Nil, *sr.MembershipID, sr.ID, now)
	if err != nil {
		return SessionResult{}, err
	}
	out := SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: rawNew,
		UserID: sr.UserID, MembershipID: *sr.MembershipID}
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

func (s *Service) Logout(ctx context.Context, p *auth.Principal, refreshCookie string) error {
	s.revoke(ctx, p.SessionID, "logout")
	if refreshCookie != "" {
		if hash, err := auth.HashToken(refreshCookie); err == nil {
			_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $3, revoked_reason = 'logout' WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL`, hash, p.UserID, s.Clock.Now())
				return err
			})
		}
	}
	s.event(ctx, "logout", &p.UserID, nil, "", &p.SessionID, p.IP, p.UserAgent, "")
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
			windowStart := now.Add(-lockoutFor)
			_, err := tx.Exec(ctx, `
				UPDATE iam.user_credentials
				   SET failed_attempts = CASE WHEN last_failed_at IS NULL OR last_failed_at < $3 THEN 1 ELSE failed_attempts + 1 END,
				       last_failed_at = $2
				 WHERE user_id = $1`, p.UserID, now, windowStart)
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
			       failed_attempts = 0, last_failed_at = NULL, locked_until = NULL, updated_at = $3 WHERE user_id = $1`, p.UserID, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash, created_at) VALUES ($1, $2, $3)`, p.UserID, hash, now); err != nil {
			return err
		}
		// Refresh reauthenticated_at on the current session since the caller just verified their password.
		if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now); err != nil {
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
	s.event(ctx, "password_changed", &p.UserID, nil, "", &p.SessionID, p.IP, p.UserAgent, "")
	return nil
}

// ---------------------------------------------------------------------------------------------
// Per-request principal
// ---------------------------------------------------------------------------------------------

// Load turns verified token claims into the caller for this request. Everything is read fresh, so a
// disabled user, a suspended employee, a revoked membership, an ended session or a changed permission
// takes effect on the next request rather than when the access token expires.
func (s *Service) Load(ctx context.Context, claims auth.Claims) (*auth.Principal, error) {
	ended := httpx.Unauthorized("session_ended", "Sign in again")
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, httpx.Unauthorized("invalid_token", "Sign in again")
	}
	now := s.Clock.Now()
	p := &auth.Principal{UserID: userID,
		MembershipID: claims.MembershipID, SessionID: claims.SessionID, Perms: map[string]auth.Grant{}, Limits: map[string]decimal.Decimal{}}

	var idle, expired bool
	var lastSeen time.Time
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var revoked, userOK bool
		var absolute time.Time
		var idleSecs float64
		var mid *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT s.revoked_at IS NOT NULL, s.absolute_expires_at, s.last_seen_at, extract(epoch FROM s.idle_timeout),
			       s.reauthenticated_at, s.membership_id, u.status = 'active', u.full_name, u.preferred_locale
			FROM iam.sessions s JOIN iam.users u ON u.id = s.user_id WHERE s.id = $1 AND s.user_id = $2`, claims.SessionID, userID).
			Scan(&revoked, &absolute, &lastSeen, &idleSecs, &p.ReauthenticatedAt, &mid, &userOK, &p.Name, &p.Language); err != nil {
			return ended
		}
		if revoked || !userOK || mid == nil || *mid != claims.MembershipID {
			return ended
		}
		if !absolute.After(now) {
			expired = true
			return ended
		}
		if now.Sub(lastSeen) > time.Duration(idleSecs*float64(time.Second)) {
			idle = true
			return httpx.Unauthorized("session_ended", "You were signed out after a period of inactivity. Sign in again.")
		}
		return nil
	})
	if expired {
		s.revoke(ctx, claims.SessionID, "expired")
	}
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
	var inactiveSeat bool
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var active bool
		var roleCode string
		var employee, defaultLoc *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT m.status = 'active' AND (e.id IS NULL OR e.status = 'active'),
			       coalesce(r.code::text, ''), r.id, coalesce(r.rank, 0), e.id, m.default_location_id
			FROM app.memberships m
			LEFT JOIN app.membership_roles mr ON mr.membership_id = m.id
			LEFT JOIN app.roles r ON r.id = mr.role_id
			LEFT JOIN app.employees e ON e.membership_id = m.id
			WHERE m.id = $1`, claims.MembershipID).Scan(&active, &roleCode, &p.RoleID, &p.RoleRank, &employee, &defaultLoc); err != nil || !active {
			inactiveSeat = true
			return ended
		}
		p.RoleSlug, p.IsOwner, p.EmployeeID = roleCode, roleCode == "admin", employee
		if defaultLoc != nil {
			p.BranchIDs = []uuid.UUID{*defaultLoc}
		}
		// Effective permissions: a user override (allow or deny, unexpired) beats the role, exactly as app.has_permission does.
		rows, err := tx.Query(ctx, `
			SELECT p.code FROM app.permissions p
			WHERE coalesce(
			  (SELECT o.effect = 'allow' FROM app.membership_permission_overrides o
			    WHERE o.membership_id = $1 AND o.permission_code = p.code AND (o.expires_at IS NULL OR o.expires_at > $2)),
			  EXISTS (SELECT 1 FROM app.membership_roles mr
			            JOIN app.role_permissions rp ON rp.role_id = mr.role_id
			            JOIN app.roles r ON r.id = mr.role_id AND r.status = 'active'
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
	if inactiveSeat {
		s.revoke(ctx, claims.SessionID, "membership_revoked")
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}
