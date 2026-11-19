package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/chart"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
)

type Service struct {
	DB     *db.DB
	Auth   auth.Tokens
	Hash   auth.Params
	Clock  clock.Clock
	Audit  *audit.Service
	Notify *notify.Service
	Ledger *ledger.Service
	TTL    time.Duration
	// DefaultTimezone and DefaultCurrency are given to every new business (DEFAULT_TIMEZONE, DEFAULT_CURRENCY).
	DefaultTimezone string
	DefaultCurrency string
}

func (s *Service) tz() string {
	if s.DefaultTimezone == "" {
		return "Africa/Dar_es_Salaam"
	}
	return s.DefaultTimezone
}

func (s *Service) ccy() string {
	if s.DefaultCurrency == "" {
		return "TZS"
	}
	return s.DefaultCurrency
}

type SessionResult struct {
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"`
	Refresh     string    `json:"-"`
	UserID      uuid.UUID `json:"user_id"`
	BusinessID  uuid.UUID `json:"business_id"`
	BranchID    uuid.UUID `json:"branch_id"`
}

type RegisterInput struct {
	BusinessName string
	OwnerName    string
	Phone        string
	Email        string
	Password     string
	IP           string
	UserAgent    string
}

func (s *Service) Register(ctx context.Context, in RegisterInput) (SessionResult, error) {
	if strings.TrimSpace(in.BusinessName) == "" || strings.TrimSpace(in.OwnerName) == "" || strings.TrimSpace(in.Phone) == "" {
		return SessionResult{}, httpx.Validation(errors.New("required"))
	}
	if err := auth.PasswordOK(in.Password); err != nil {
		return SessionResult{}, httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return SessionResult{}, err
	}
	businessID := ids.New()
	branchID := ids.New()
	ownerRole := ids.New()
	managerRole := ids.New()
	staffRole := ids.New()
	userID := ids.New()
	now := s.Clock.Now()
	var out SessionResult
	err = s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO businesses (id, name, phone, currency, timezone)
			VALUES ($1,$2,$3,$4,$5)
		`, businessID, strings.TrimSpace(in.BusinessName), strings.TrimSpace(in.Phone), s.ccy(), s.tz()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO branches (id, business_id, name, code, phone, is_main)
			VALUES ($1,$2,'Main','MAIN',$3,true)
		`, branchID, businessID, strings.TrimSpace(in.Phone)); err != nil {
			return err
		}
		for _, role := range []struct {
			id, name, slug string
			system         bool
		}{
			{ownerRole.String(), "Owner", "owner", true},
			{managerRole.String(), "Manager", "manager", true},
			{staffRole.String(), "Staff", "staff", true},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO roles (id, business_id, name, slug, is_system) VALUES ($1,$2,$3,$4,$5)`,
				role.id, businessID, role.name, role.slug, role.system); err != nil {
				return err
			}
		}
		if err := s.grantDefaults(ctx, tx, businessID, ownerRole, managerRole, staffRole); err != nil {
			return err
		}
		if err := chart.Seed(ctx, tx, businessID); err != nil {
			return err
		}
		if err := s.defaultSettings(ctx, tx, businessID); err != nil {
			return err
		}
		if err := s.defaultMaster(ctx, tx, businessID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, business_id, role_id, name, phone, email, password_hash, language)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::citext,$7,'en')
		`, userID, businessID, ownerRole, strings.TrimSpace(in.OwnerName), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), hash); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already registered")
			}
			return err
		}
		if _, err := chart.CreateMoneyAccount(ctx, tx, s.Ledger, chart.MoneyInput{
			BusinessID: businessID, BranchID: &branchID, Name: "Main till", Type: "cash", UserID: userID, EntryDate: now.Format("2006-01-02"),
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO customers (id, business_id, name, is_walk_in, created_by) VALUES ($1,$2,'Walk-in',true,$3)
		`, ids.New(), businessID, userID); err != nil {
			return err
		}
		if err := sequence.Ensure(ctx, tx, businessID, branchID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_branches (user_id, branch_id, business_id, is_default) VALUES ($1,$2,$3,true)
		`, userID, branchID, businessID); err != nil {
			return err
		}
		refresh, err := s.insertSession(ctx, tx, businessID, userID, in.IP, in.UserAgent, now)
		if err != nil {
			return err
		}
		access, sid, err := s.accessFor(ctx, tx, userID, businessID, ownerRole, now)
		if err != nil {
			return err
		}
		_ = sid
		p := &auth.Principal{UserID: userID, BusinessID: businessID, BranchID: &branchID, IP: in.IP, RequestID: ""}
		if err := s.Audit.Record(ctx, tx, p, "business.registered", "business", businessID, nil, map[string]any{"name": in.BusinessName}); err != nil {
			return err
		}
		out = SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: refresh, UserID: userID, BusinessID: businessID, BranchID: branchID}
		return nil
	})
	return out, err
}

func (s *Service) grantDefaults(ctx context.Context, tx pgx.Tx, businessID, owner, manager, staff uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT key FROM permissions`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	for _, k := range keys {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, business_id, permission_key, scope) VALUES ($1,$2,$3,'all')
		`, owner, businessID, k); err != nil {
			return err
		}
	}
	for _, p := range managerPerms() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, business_id, permission_key, scope, approval_limit) VALUES ($1,$2,$3,$4,$5)
		`, manager, businessID, p.Key, p.Scope, p.Limit); err != nil {
			return err
		}
	}
	for _, p := range staffPerms() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, business_id, permission_key, scope, approval_limit) VALUES ($1,$2,$3,$4,$5)
		`, staff, businessID, p.Key, p.Scope, p.Limit); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) defaultSettings(ctx context.Context, tx pgx.Tx, businessID uuid.UUID) error {
	vals := map[string]any{
		"tax.prices_include_tax": false,
		"currency.code":          s.ccy(), "currency.symbol": s.ccy(), "currency.decimals": 2, "currency.thousands_separator": ",",
		"invoice.prefix": "INV", "invoice.terms": "Payment is due as stated.", "invoice.footer": "", "invoice.due_days": 0,
		"receipt.header": "", "receipt.footer": "Thank you", "receipt.show_logo": true, "receipt.paper_width": 80,
		"receipt.show_cashier": true, "receipt.show_tax": true,
		"payroll.pay_day": 25, "payroll.working_days": 26, "payroll.standard_hours": 8, "payroll.overtime_multiplier": 1.5,
		"payroll.late_grace_minutes": 10, "payroll.lateness_deducts": false,
		"system.timezone": s.tz(), "system.date_format": "02 Jan 2006", "system.language": "en",
		"system.allow_negative_stock": false, "system.business_day_start": "00:00", "system.session_timeout_minutes": 30,
		"system.till_difference_threshold": 1000,
		"notifications.supplier_due_days":  7, "notifications.salary_due_days": 3,
		"invoice.quotation_valid_days": 14,
		"payment.methods": map[string]any{
			"cash": map[string]any{"enabled": true}, "bank": map[string]any{"enabled": true},
			"mobile_money": map[string]any{"enabled": true}, "credit": map[string]any{"enabled": true},
		},
	}
	for k, v := range vals {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO settings (business_id, key, value) VALUES ($1,$2,$3)`, businessID, k, raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) defaultMaster(ctx context.Context, tx pgx.Tx, businessID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO tax_rates (id, business_id, name, rate, is_default) VALUES ($1,$2,'Exempt',0,true), ($3,$2,'VAT',18,false)
	`, ids.New(), businessID, ids.New()); err != nil {
		return err
	}
	units := []struct {
		name, short string
		dec         bool
	}{
		{"piece", "pc", false}, {"kg", "kg", true}, {"litre", "L", true}, {"box", "box", false}, {"carton", "ctn", false},
	}
	for _, u := range units {
		if _, err := tx.Exec(ctx, `INSERT INTO units (id, business_id, name, short_name, allow_decimal) VALUES ($1,$2,$3,$4,$5)`,
			ids.New(), businessID, u.name, u.short, u.dec); err != nil {
			return err
		}
	}
	cats := []struct{ name, code string }{
		{"Rent", "6000"}, {"Utilities", "6010"}, {"Transport", "6020"}, {"Repairs", "6040"}, {"Marketing", "6050"}, {"Other", "6090"},
	}
	for _, c := range cats {
		if _, err := tx.Exec(ctx, `
			INSERT INTO expense_categories (id, business_id, name, ledger_account_id)
			SELECT $1, $2, $3, id FROM ledger_accounts WHERE code = $4
		`, ids.New(), businessID, c.name, c.code); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO income_categories (id, business_id, name, ledger_account_id)
		SELECT $1, $2, 'Other income', id FROM ledger_accounts WHERE code = '4100'
	`, ids.New(), businessID); err != nil {
		return err
	}
	leaves := []struct {
		name string
		days string
		paid bool
	}{{"Annual", "21", true}, {"Sick", "14", true}, {"Maternity", "84", true}, {"Unpaid", "0", false}}
	for _, l := range leaves {
		if _, err := tx.Exec(ctx, `INSERT INTO leave_types (id, business_id, name, days_per_year, is_paid) VALUES ($1,$2,$3,$4,$5)`,
			ids.New(), businessID, l.name, l.days, l.paid); err != nil {
			return err
		}
	}
	for _, t := range []string{"low_stock", "approval_request", "approval_decision", "salary_due", "customer_debt", "supplier_debt", "security", "daily_summary", "transfer_difference"} {
		if _, err := tx.Exec(ctx, `INSERT INTO notification_rules (business_id, type) VALUES ($1,$2)`, businessID, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) insertSession(ctx context.Context, tx pgx.Tx, businessID, userID uuid.UUID, ip, ua string, now time.Time) (string, error) {
	raw, hash, err := auth.RandomToken(32)
	if err != nil {
		return "", err
	}
	var ipVal any
	if ip != "" {
		ipVal = ip
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO sessions (id, business_id, user_id, refresh_hash, user_agent, ip, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6::inet,$7)
	`, ids.New(), businessID, userID, hash, ua, ipVal, now.Add(s.TTL))
	return raw, err
}

func (s *Service) accessFor(ctx context.Context, tx pgx.Tx, userID, businessID, roleID uuid.UUID, now time.Time) (string, uuid.UUID, error) {
	var sid uuid.UUID
	var version int
	if err := tx.QueryRow(ctx, `
		SELECT s.id, r.perm_version FROM sessions s JOIN roles r ON r.id = $2
		WHERE s.user_id = $1 ORDER BY s.created_at DESC LIMIT 1
	`, userID, roleID).Scan(&sid, &version); err != nil {
		return "", uuid.Nil, err
	}
	tok, err := s.Auth.Sign(userID, businessID, roleID, sid, version, now)
	return tok, sid, err
}

type foundUser struct {
	ID             uuid.UUID
	BusinessID     uuid.UUID
	RoleID         uuid.UUID
	Name           string
	PasswordHash   string
	Active         bool
	MustChange     bool
	LockedUntil    *time.Time
	BusinessActive bool
}

func (s *Service) Login(ctx context.Context, identifier, password, ip, ua string) (SessionResult, error) {
	identifier = strings.TrimSpace(identifier)
	var u foundUser
	err := s.DB.Pool.QueryRow(ctx, `SELECT id, business_id, role_id, name, password_hash, is_active, must_change_password, locked_until, business_active FROM auth_find_user($1)`, identifier).
		Scan(&u.ID, &u.BusinessID, &u.RoleID, &u.Name, &u.PasswordHash, &u.Active, &u.MustChange, &u.LockedUntil, &u.BusinessActive)
	now := s.Clock.Now()
	if err != nil {
		_ = s.recordLogin(ctx, uuid.Nil, nil, identifier, false, "unknown", ip, ua)
		return SessionResult{}, httpx.Unauthorized("invalid_credentials", "Phone, email or password is wrong")
	}
	if u.LockedUntil != nil && u.LockedUntil.After(now) {
		_ = s.recordLogin(ctx, u.BusinessID, &u.ID, identifier, false, "locked", ip, ua)
		return SessionResult{}, httpx.Unauthorized("account_locked", "This account is locked for 15 minutes after too many attempts")
	}
	if !u.Active || !u.BusinessActive {
		_ = s.recordLogin(ctx, u.BusinessID, &u.ID, identifier, false, "inactive", ip, ua)
		return SessionResult{}, httpx.Unauthorized("invalid_credentials", "Phone, email or password is wrong")
	}
	if !auth.VerifyPassword(u.PasswordHash, password) {
		_ = s.recordLogin(ctx, u.BusinessID, &u.ID, identifier, false, "bad_password", ip, ua)
		s.maybeLock(ctx, u, now)
		return SessionResult{}, httpx.Unauthorized("invalid_credentials", "Phone, email or password is wrong")
	}
	var out SessionResult
	err = s.DB.InTx(ctx, u.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET locked_until = NULL, last_login_at = $2 WHERE id = $1`, u.ID, now); err != nil {
			return err
		}
		refresh, err := s.insertSession(ctx, tx, u.BusinessID, u.ID, ip, ua, now)
		if err != nil {
			return err
		}
		access, _, err := s.accessFor(ctx, tx, u.ID, u.BusinessID, u.RoleID, now)
		if err != nil {
			return err
		}
		var branchID uuid.UUID
		_ = tx.QueryRow(ctx, `SELECT branch_id FROM user_branches WHERE user_id = $1 AND is_default LIMIT 1`, u.ID).Scan(&branchID)
		out = SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: refresh, UserID: u.ID, BusinessID: u.BusinessID, BranchID: branchID}
		return nil
	})
	if err != nil {
		return SessionResult{}, err
	}
	_ = s.recordLogin(ctx, u.BusinessID, &u.ID, identifier, true, "", ip, ua)
	return out, nil
}

func (s *Service) maybeLock(ctx context.Context, u foundUser, now time.Time) {
	var n int
	if err := s.DB.Pool.QueryRow(ctx, `SELECT auth_failure_count($1, $2)`, u.ID, now.Add(-15*time.Minute)).Scan(&n); err != nil {
		return
	}
	if n < 5 {
		return
	}
	until := now.Add(15 * time.Minute)
	_, _ = s.DB.Pool.Exec(ctx, `SELECT auth_set_lock($1, $2)`, u.ID, until)
	_ = s.DB.InTx(ctx, u.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return s.Notify.ToPermission(ctx, tx, u.BusinessID, nil, "users.manage", notify.Message{
			Type: "security", Title: "Account locked", Body: u.Name + " was locked after 5 failed sign-ins",
		})
	})
}

func (s *Service) recordLogin(ctx context.Context, businessID uuid.UUID, userID *uuid.UUID, identifier string, ok bool, reason, ip, ua string) error {
	var b, u any
	if businessID != uuid.Nil {
		b = businessID
	}
	if userID != nil {
		u = *userID
	}
	_, err := s.DB.Pool.Exec(ctx, `SELECT auth_record_login($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), b, u, identifier, ok, reason, ip, ua)
	return err
}

func (s *Service) Refresh(ctx context.Context, raw, ip, ua string) (SessionResult, error) {
	hash, err := auth.HashToken(raw)
	if err != nil {
		return SessionResult{}, httpx.Unauthorized("session_ended", "Sign in again")
	}
	var sess struct {
		ID, BusinessID, UserID, RoleID uuid.UUID
		Current, Prev                  []byte
		Expires, Last                  time.Time
		Version                        int
		Active                         bool
	}
	err = s.DB.Pool.QueryRow(ctx, `SELECT id, business_id, user_id, role_id, refresh_hash, prev_hash, expires_at, last_used_at, perm_version, user_active FROM auth_find_session($1)`, hash).
		Scan(&sess.ID, &sess.BusinessID, &sess.UserID, &sess.RoleID, &sess.Current, &sess.Prev, &sess.Expires, &sess.Last, &sess.Version, &sess.Active)
	if err != nil || !sess.Active {
		return SessionResult{}, httpx.Unauthorized("session_ended", "Sign in again")
	}
	if !bytesEqual(sess.Current, hash) {
		_ = s.DB.InTx(ctx, sess.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sess.ID)
			return err
		})
		return SessionResult{}, httpx.Unauthorized("session_ended", "This session was reused and has been signed out")
	}
	now := s.Clock.Now()
	if sess.Expires.Before(now) {
		_, _ = s.DB.Pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sess.ID)
		return SessionResult{}, httpx.Unauthorized("session_ended", "Sign in again")
	}
	var out SessionResult
	err = s.DB.InTx(ctx, sess.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rawNew, newHash, err := auth.RandomToken(32)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sessions SET prev_hash = refresh_hash, refresh_hash = $2, last_used_at = $3, user_agent = $4, ip = NULLIF($5,'')::inet
			WHERE id = $1
		`, sess.ID, newHash, now, ua, ip); err != nil {
			return err
		}
		access, err := s.Auth.Sign(sess.UserID, sess.BusinessID, sess.RoleID, sess.ID, sess.Version, now)
		if err != nil {
			return err
		}
		var branchID uuid.UUID
		_ = tx.QueryRow(ctx, `SELECT branch_id FROM user_branches WHERE user_id = $1 AND is_default LIMIT 1`, sess.UserID).Scan(&branchID)
		out = SessionResult{AccessToken: access, ExpiresIn: int(s.Auth.Access.Seconds()), Refresh: rawNew, UserID: sess.UserID, BusinessID: sess.BusinessID, BranchID: branchID}
		return nil
	})
	return out, err
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Service) Logout(ctx context.Context, p *auth.Principal) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, p.SessionID)
		return err
	})
}

func (s *Service) ChangePassword(ctx context.Context, p *auth.Principal, current, next string) error {
	if err := auth.PasswordOK(next); err != nil {
		return httpx.BadRequest("weak_password", err.Error())
	}
	if err := s.checkReauth(ctx, p, "password_change"); err != nil {
		return err
	}
	hash, err := auth.HashPassword(next, s.Hash)
	if err != nil {
		return err
	}
	wrong := false
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var old string
		if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, p.UserID).Scan(&old); err != nil {
			return err
		}
		if !auth.VerifyPassword(old, current) {
			wrong = true
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, must_change_password = false WHERE id = $1`, p.UserID, hash); err != nil {
			return err
		}
		// Everyone else signed in as this user is signed out: a stolen session must not survive a password change.
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, p.UserID, p.SessionID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "user.password_changed", "user", p.UserID, nil, map[string]any{"id": p.UserID})
	})
	if err != nil {
		return err
	}
	if wrong {
		s.recordReauth(ctx, p, "password_change", false)
		return httpx.Unauthorized("invalid_credentials", "Current password is wrong")
	}
	return nil
}

// reauthMaxFailures wrong answers in reauthWindow block further tries for the same user.
const (
	reauthMaxFailures = 5
	reauthWindow      = 15 * time.Minute
)

// checkReauth refuses a re-authentication attempt (PIN, current password) after too many recent failures.
func (s *Service) checkReauth(ctx context.Context, p *auth.Principal, reason string) error {
	var n int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM login_history WHERE user_id = $1 AND reason = $2 AND NOT success AND created_at > $3
		`, p.UserID, reason, s.Clock.Now().Add(-reauthWindow)).Scan(&n)
	})
	if err != nil {
		return err
	}
	if n >= reauthMaxFailures {
		return httpx.TooMany("too_many_requests", "Too many wrong answers. Wait 15 minutes, or sign in with your password again.")
	}
	return nil
}

// recordReauth logs the attempt in its own transaction, so a failure is counted even though the request fails.
func (s *Service) recordReauth(ctx context.Context, p *auth.Principal, reason string, ok bool) {
	var ip any
	if p.IP != "" {
		ip = p.IP
	}
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO login_history (id, business_id, user_id, identifier, success, reason, ip, user_agent, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::inet,$8,$9)
		`, ids.New(), p.BusinessID, p.UserID, p.UserID.String(), ok, reason, ip, p.UserAgent, s.Clock.Now())
		return err
	})
}

func (s *Service) SetPIN(ctx context.Context, p *auth.Principal, pin string) error {
	if err := auth.PINOK(pin); err != nil {
		return httpx.BadRequest("weak_pin", err.Error())
	}
	hash, err := auth.HashPassword(pin, s.Hash)
	if err != nil {
		return err
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE users SET pin_hash = $2 WHERE id = $1`, p.UserID, hash)
		return err
	})
}

func (s *Service) ExchangePIN(ctx context.Context, p *auth.Principal, pin string) (string, error) {
	if err := s.checkReauth(ctx, p, "pin"); err != nil {
		return "", err
	}
	var hash *string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT pin_hash FROM users WHERE id = $1`, p.UserID).Scan(&hash)
	})
	if err != nil {
		return "", err
	}
	if hash == nil || !auth.VerifyPassword(*hash, pin) {
		s.recordReauth(ctx, p, "pin", false)
		return "", httpx.Unauthorized("invalid_credentials", "PIN is wrong. After 5 failures you must wait 15 minutes or sign in with your password.")
	}
	var perm int
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT perm_version FROM roles WHERE id = $1`, p.RoleID).Scan(&perm)
	})
	return s.Auth.Sign(p.UserID, p.BusinessID, p.RoleID, p.SessionID, perm, s.Clock.Now())
}

func (s *Service) Load(ctx context.Context, claims auth.Claims) (*auth.Principal, error) {
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, httpx.Unauthorized("invalid_token", "Sign in again")
	}
	var p auth.Principal
	err = s.DB.InTx(ctx, claims.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var active, businessActive bool
		var slug string
		err := tx.QueryRow(ctx, `
			SELECT u.id, u.business_id, u.role_id, u.employee_id, u.name, u.language, u.is_active, b.is_active, r.slug
			FROM users u
			JOIN businesses b ON b.id = u.business_id
			JOIN roles r ON r.id = u.role_id
			WHERE u.id = $1
		`, userID).Scan(&p.UserID, &p.BusinessID, &p.RoleID, &p.EmployeeID, &p.Name, &p.Language, &active, &businessActive, &slug)
		if err != nil || !active || !businessActive {
			return httpx.Unauthorized("session_ended", "Sign in again")
		}
		var sessionOK bool
		if err := tx.QueryRow(ctx, `SELECT expires_at > now() FROM sessions WHERE id = $1 AND user_id = $2`, claims.SessionID, userID).Scan(&sessionOK); err != nil || !sessionOK {
			return httpx.Unauthorized("session_ended", "Sign in again")
		}
		p.IsOwner = slug == "owner"
		p.RoleSlug = slug
		p.SessionID = claims.SessionID
		p.Perms = map[string]auth.Grant{}
		rows, err := tx.Query(ctx, `SELECT permission_key, scope, approval_limit FROM role_permissions WHERE role_id = $1`, p.RoleID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key, scope string
			var limit *decimal.Decimal
			if err := rows.Scan(&key, &scope, &limit); err != nil {
				return err
			}
			p.Perms[key] = auth.Grant{Scope: scope, Limit: limit}
		}
		brows, err := tx.Query(ctx, `SELECT branch_id FROM user_branches WHERE user_id = $1`, p.UserID)
		if err != nil {
			return err
		}
		defer brows.Close()
		for brows.Next() {
			var id uuid.UUID
			if err := brows.Scan(&id); err != nil {
				return err
			}
			p.BranchIDs = append(p.BranchIDs, id)
		}
		return brows.Err()
	})
	return &p, err
}
