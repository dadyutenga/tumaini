package identity

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
)

// A "user" in the API is a person with a seat in this tenant: an iam.users identity joined to its
// app.memberships row. The id is the person's iam.users id.
type User struct {
	ID           uuid.UUID   `json:"id"`
	MembershipID uuid.UUID   `json:"membership_id"`
	Name         string      `json:"name"`
	Phone        *string     `json:"phone"`
	Email        *string     `json:"email"`
	RoleID       *uuid.UUID  `json:"role_id"`
	RoleName     string      `json:"role_name"`
	RoleSlug     string      `json:"role_slug"`
	EmployeeID   *uuid.UUID  `json:"employee_id"`
	Language     string      `json:"language"`
	IsActive     bool        `json:"is_active"`
	Branches     []uuid.UUID `json:"branch_ids"`
	MustChange   bool        `json:"must_change_password"`
}

type UserInput struct {
	Name       string      `json:"name"`
	Phone      string      `json:"phone"`
	Email      string      `json:"email"`
	Password   string      `json:"password"`
	RoleID     uuid.UUID   `json:"role_id"`
	EmployeeID *uuid.UUID  `json:"employee_id"`
	BranchIDs  []uuid.UUID `json:"branch_ids"`
	Language   string      `json:"language"`
}

const userSelect = `
	SELECT u.id, m.id, u.full_name::text, u.phone::text, u.email::text, r.id, coalesce(r.name::text, ''), coalesce(r.code::text, ''),
	       e.id, u.preferred_locale, (m.status = 'active' AND u.status = 'active'), c.must_change_password, m.default_location_id
	FROM app.memberships m
	JOIN iam.users u ON u.id = m.user_id
	JOIN iam.user_credentials c ON c.user_id = u.id
	LEFT JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
	LEFT JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id
	LEFT JOIN app.employees e ON e.tenant_id = m.tenant_id AND e.membership_id = m.id
	WHERE m.tenant_id = $1`

func scanUser(row pgx.Row) (User, error) {
	var u User
	var loc *uuid.UUID
	err := row.Scan(&u.ID, &u.MembershipID, &u.Name, &u.Phone, &u.Email, &u.RoleID, &u.RoleName, &u.RoleSlug, &u.EmployeeID, &u.Language, &u.IsActive, &u.MustChange, &loc)
	u.Branches = []uuid.UUID{}
	if loc != nil {
		u.Branches = append(u.Branches, *loc)
	}
	return u, err
}

func (s *Service) ListUsers(ctx context.Context, p *auth.Principal, q string, page httpx.Page) ([]User, int, error) {
	out := []User{}
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.memberships m JOIN iam.users u ON u.id = m.user_id
			WHERE m.tenant_id = $1 AND ($2 = '' OR u.full_name ILIKE '%'||$2||'%' OR u.phone ILIKE '%'||$2||'%' OR u.email::text ILIKE '%'||$2||'%')`,
			p.TenantID, q).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, userSelect+`
			AND ($2 = '' OR u.full_name ILIKE '%'||$2||'%' OR u.phone ILIKE '%'||$2||'%' OR u.email::text ILIKE '%'||$2||'%')
			ORDER BY u.full_name LIMIT $3 OFFSET $4`, p.TenantID, q, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) getUser(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (User, error) {
	u, err := scanUser(tx.QueryRow(ctx, userSelect+` AND u.id = $2`, tenantID, id))
	if err != nil {
		return User{}, httpx.NotFound("not_found", "User not found")
	}
	return u, nil
}

func (s *Service) GetUser(ctx context.Context, p *auth.Principal, id uuid.UUID) (User, error) {
	var u User
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		u, err = s.getUser(ctx, tx, p.TenantID, id)
		return err
	})
	return u, err
}

func (s *Service) CreateUser(ctx context.Context, p *auth.Principal, in UserInput) (User, error) {
	if err := auth.PasswordOK(in.Password); err != nil {
		return User{}, httpx.BadRequest("weak_password", err.Error())
	}
	name, email := strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Email))
	if name == "" || (strings.TrimSpace(in.Phone) == "" && email == "") || in.RoleID == uuid.Nil {
		return User{}, httpx.BadRequest("invalid_body", "Name, phone or email, and a role are required")
	}
	phone := ""
	if strings.TrimSpace(in.Phone) != "" {
		var err error
		if phone, err = NormalizePhone(in.Phone); err != nil {
			return User{}, httpx.BadRequest("invalid_phone", err.Error())
		}
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return User{}, err
	}
	lang := in.Language
	if lang != "sw" && lang != "en" {
		lang = ""
	}
	var out User
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := s.guardAssignRole(ctx, tx, p, in.RoleID); err != nil {
			return err
		}
		var userID, membershipID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO iam.users (full_name, email, phone, preferred_locale)
			VALUES ($1, NULLIF($2,'')::app.email, NULLIF($3,'')::app.phone_e164,
			        coalesce(NULLIF($4,''), (SELECT default_locale FROM app.tenants WHERE id = $5), 'sw')) RETURNING id`,
			name, email, phone, lang, p.TenantID).Scan(&userID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already in use")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.user_credentials (user_id, password_hash, must_change_password) VALUES ($1,$2,true)`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash) VALUES ($1,$2)`, userID, hash); err != nil {
			return err
		}
		var loc *uuid.UUID
		if len(in.BranchIDs) > 0 {
			loc = &in.BranchIDs[0]
		} else {
			_ = tx.QueryRow(ctx, `SELECT id FROM app.locations WHERE tenant_id = $1 AND is_default`, p.TenantID).Scan(&loc)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.memberships (tenant_id, user_id, status, default_location_id, joined_at)
			VALUES ($1, $2, 'active', $3, now()) RETURNING id`, p.TenantID, userID, loc).Scan(&membershipID); err != nil {
			return httpx.BadRequest("invalid_branch", "That branch does not exist")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.membership_roles (tenant_id, membership_id, role_id) VALUES ($1,$2,$3)`, p.TenantID, membershipID, in.RoleID); err != nil {
			return err
		}
		if in.EmployeeID != nil {
			tag, err := tx.Exec(ctx, `UPDATE app.employees SET membership_id = $2 WHERE id = $1 AND membership_id IS NULL`, *in.EmployeeID, membershipID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.BadRequest("invalid_body", "That employee does not exist or already has a login")
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "user.created", "users", userID, nil, map[string]any{"name": name, "role_id": in.RoleID}); err != nil {
			return err
		}
		var err error
		out, err = s.getUser(ctx, tx, p.TenantID, userID)
		return err
	})
	return out, err
}

func (s *Service) UpdateUser(ctx context.Context, p *auth.Principal, id uuid.UUID, in UserInput) (User, error) {
	var out User
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.getUser(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if err := s.guardTarget(ctx, tx, p, id); err != nil {
			return err
		}
		newRole := in.RoleID
		if newRole == uuid.Nil && cur.RoleID != nil {
			newRole = *cur.RoleID
		}
		if cur.RoleID == nil || newRole != *cur.RoleID {
			if p.UserID == id {
				return httpx.Forbidden("permission_denied", "You cannot change your own role")
			}
			if err := s.guardAssignRole(ctx, tx, p, newRole); err != nil {
				return err
			}
			if cur.RoleSlug == "admin" {
				if err := s.ensureAnotherAdmin(ctx, tx, p.TenantID, id); err != nil {
					return err
				}
			}
		}
		phone := ""
		if strings.TrimSpace(in.Phone) != "" {
			if phone, err = NormalizePhone(in.Phone); err != nil {
				return httpx.BadRequest("invalid_phone", err.Error())
			}
		}
		lang := in.Language
		if lang != "sw" && lang != "en" {
			lang = ""
		}
		if _, err := tx.Exec(ctx, `
			UPDATE iam.users SET full_name = coalesce(NULLIF($2,''), full_name), phone = coalesce(NULLIF($3,'')::app.phone_e164, phone),
			       email = coalesce(NULLIF($4,'')::app.email, email), preferred_locale = coalesce(NULLIF($5,''), preferred_locale)
			WHERE id = $1`, id, strings.TrimSpace(in.Name), phone, strings.ToLower(strings.TrimSpace(in.Email)), lang); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already in use")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.membership_roles SET role_id = $3 WHERE tenant_id = $1 AND membership_id = $2`, p.TenantID, cur.MembershipID, newRole); err != nil {
			return err
		}
		if len(in.BranchIDs) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE app.memberships SET default_location_id = $2 WHERE id = $1`, cur.MembershipID, in.BranchIDs[0]); err != nil {
				return httpx.BadRequest("invalid_branch", "That branch does not exist")
			}
		}
		if in.EmployeeID != nil {
			if _, err := tx.Exec(ctx, `UPDATE app.employees SET membership_id = $2 WHERE id = $1 AND (membership_id IS NULL OR membership_id = $2)`, *in.EmployeeID, cur.MembershipID); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "user.updated", "users", id, nil, map[string]any{"name": in.Name, "role_id": newRole}); err != nil {
			return err
		}
		out, err = s.getUser(ctx, tx, p.TenantID, id)
		return err
	})
	return out, err
}

// SetActive suspends or restores the person's seat in this tenant and ends their sessions here.
func (s *Service) SetActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.getUser(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if err := s.guardTarget(ctx, tx, p, id); err != nil {
			return err
		}
		if !active {
			if p.UserID == id {
				return httpx.Forbidden("permission_denied", "You cannot deactivate yourself")
			}
			if cur.RoleSlug == "admin" {
				if err := s.ensureAnotherAdmin(ctx, tx, p.TenantID, id); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE app.memberships SET status = 'suspended', deactivated_at = now(), deactivation_reason = 'Deactivated by an administrator' WHERE id = $1`, cur.MembershipID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now(), revoked_reason = 'membership_revoked' WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, id, p.TenantID); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE app.memberships SET status = 'active', deactivated_at = NULL, deactivation_reason = NULL WHERE id = $1`, cur.MembershipID); err != nil {
			return err
		}
		action := "user.activated"
		if !active {
			action = "user.deactivated"
		}
		return s.Audit.Record(ctx, tx, p, action, "users", id, nil, map[string]any{"is_active": active})
	})
}

// ensureAnotherAdmin keeps the tenant from losing its last active Admin. The database enforces the
// same rule (app.tg_keep_one_admin); this gives a clear message first.
func (s *Service) ensureAnotherAdmin(ctx context.Context, tx pgx.Tx, tenantID, except uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM app.memberships m
		JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
		JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id AND r.code = 'admin'
		WHERE m.tenant_id = $1 AND m.status = 'active' AND m.user_id <> $2`, tenantID, except).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.Conflict("last_owner", "The business must keep at least one active admin")
	}
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, p *auth.Principal, id uuid.UUID, password string) error {
	if err := auth.PasswordOK(password); err != nil {
		return httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(password, s.Hash)
	if err != nil {
		return err
	}
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.getUser(ctx, tx, p.TenantID, id); err != nil {
			return err
		}
		if err := s.guardTarget(ctx, tx, p, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE iam.user_credentials SET password_hash = $2, password_changed_at = now(), must_change_password = true,
			       failed_attempts = 0, last_failed_at = NULL, locked_until = NULL, updated_at = now() WHERE user_id = $1`, id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash) VALUES ($1,$2)`, id, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now(), revoked_reason = 'password_changed' WHERE user_id = $1 AND revoked_at IS NULL`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "user.password_reset", "users", id, nil, map[string]any{"id": id})
	})
	if err == nil {
		s.event(ctx, "password_changed", &id, &p.TenantID, "", nil, p.IP, p.UserAgent, "")
	}
	return err
}

func (s *Service) EndSessions(ctx context.Context, p *auth.Principal, userID uuid.UUID) error {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.getUser(ctx, tx, p.TenantID, userID); err != nil {
			return err
		}
		if err := s.guardTarget(ctx, tx, p, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now(), revoked_reason = 'admin_force_signout' WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, userID, p.TenantID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "user.sessions_ended", "users", userID, nil, nil)
	})
	if err == nil {
		s.event(ctx, "forced_signout", &userID, &p.TenantID, "", nil, p.IP, p.UserAgent, "")
	}
	return err
}

type SessionView struct {
	ID         uuid.UUID `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         *string   `json:"ip"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	CreatedAt  time.Time `json:"created_at"`
	Current    bool      `json:"current"`
}

func (s *Service) ListSessions(ctx context.Context, p *auth.Principal) ([]SessionView, error) {
	out := []SessionView{}
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, coalesce(user_agent,''), ip::text, absolute_expires_at, last_seen_at, created_at
			FROM iam.sessions
			WHERE user_id = $1 AND revoked_at IS NULL AND absolute_expires_at > $2
			ORDER BY created_at DESC`, p.UserID, s.Clock.Now())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v SessionView
			if err := rows.Scan(&v.ID, &v.UserAgent, &v.IP, &v.ExpiresAt, &v.LastUsedAt, &v.CreatedAt); err != nil {
				return err
			}
			v.Current = v.ID == p.SessionID
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) EndOwnSession(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = $3, revoked_reason = 'logout' WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, p.UserID, s.Clock.Now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Session not found")
		}
		return nil
	})
}

// ---------------------------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------------------------

type Role struct {
	ID          uuid.UUID                  `json:"id"`
	Name        string                     `json:"name"`
	Slug        string                     `json:"slug"`
	Description *string                    `json:"description"`
	IsSystem    bool                       `json:"is_system"`
	Rank        int                        `json:"rank"`
	Perms       []RolePermView             `json:"permissions,omitempty"`
	Limits      map[string]decimal.Decimal `json:"limits,omitempty"`
}

// RolePermView is one permission of a role. Scope is always "all": the approved model has no branch
// or own-record scope on a grant; "view all" versus "own" is a separate permission code.
type RolePermView struct {
	Key   string           `json:"key"`
	Scope string           `json:"scope"`
	Limit *decimal.Decimal `json:"limit"`
}

type RoleInput struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Rank        int                        `json:"rank"`
	Perms       []RolePermView             `json:"permissions"`
	Limits      map[string]decimal.Decimal `json:"limits"`
}

func (s *Service) ListRoles(ctx context.Context, p *auth.Principal) ([]Role, error) {
	out := []Role{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name::text, code::text, description, is_system, rank FROM app.roles WHERE tenant_id = $1 AND status = 'active' ORDER BY rank DESC, name`, p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Role
			if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.Description, &r.IsSystem, &r.Rank); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) getRole(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (Role, error) {
	var r Role
	if err := tx.QueryRow(ctx, `SELECT id, name::text, code::text, description, is_system, rank FROM app.roles WHERE tenant_id = $1 AND id = $2 AND status = 'active'`, tenantID, id).
		Scan(&r.ID, &r.Name, &r.Slug, &r.Description, &r.IsSystem, &r.Rank); err != nil {
		return r, httpx.NotFound("not_found", "Role not found")
	}
	r.Perms = []RolePermView{}
	rows, err := tx.Query(ctx, `SELECT permission_code FROM app.role_permissions WHERE tenant_id = $1 AND role_id = $2 ORDER BY permission_code`, tenantID, id)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		v := RolePermView{Scope: "all"}
		if err := rows.Scan(&v.Key); err != nil {
			rows.Close()
			return r, err
		}
		r.Perms = append(r.Perms, v)
	}
	rows.Close()
	r.Limits = map[string]decimal.Decimal{}
	lrows, err := tx.Query(ctx, `SELECT limit_code::text, limit_value FROM app.authority_limits WHERE tenant_id = $1 AND role_id = $2`, tenantID, id)
	if err != nil {
		return r, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var code string
		var v decimal.Decimal
		if err := lrows.Scan(&code, &v); err != nil {
			return r, err
		}
		r.Limits[code] = v
	}
	// Mirror each limit onto its permission so a client can show "10 %" next to "Approve discounts".
	for i, pv := range r.Perms {
		if code, ok := limitCodeFor[pv.Key]; ok {
			if v, ok := r.Limits[code]; ok {
				v := v
				r.Perms[i].Limit = &v
			}
		}
	}
	return r, lrows.Err()
}

func (s *Service) GetRole(ctx context.Context, p *auth.Principal, id uuid.UUID) (Role, error) {
	var r Role
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		r, err = s.getRole(ctx, tx, p.TenantID, id)
		return err
	})
	return r, err
}

func roleCode(name string) string {
	c := strings.Trim(nonCode.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "_"), "_")
	if c == "" || c[0] < 'a' || c[0] > 'z' {
		c = "role_" + c
	}
	if len(c) > 40 {
		c = c[:40]
	}
	return c
}

// limitsOf folds the per-permission limits a client sends into authority-limit codes.
func limitsOf(in RoleInput) map[string]decimal.Decimal {
	out := map[string]decimal.Decimal{}
	for code, v := range in.Limits {
		out[code] = v
	}
	for _, pv := range in.Perms {
		if code, ok := limitCodeFor[pv.Key]; ok && pv.Limit != nil {
			out[code] = *pv.Limit
		}
	}
	return out
}

func (s *Service) CreateRole(ctx context.Context, p *auth.Principal, in RoleInput) (Role, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Role{}, httpx.BadRequest("invalid_body", "Role name is required")
	}
	limits := limitsOf(in)
	if err := s.guardGrant(p, in.Perms, limits); err != nil {
		return Role{}, err
	}
	rank := in.Rank
	if rank == 0 {
		rank = 30
	}
	if rank < 1 || rank > 100 || (!p.IsOwner && rank > p.RoleRank) {
		return Role{}, httpx.BadRequest("invalid_rank", "Rank must be between 1 and your own rank")
	}
	var out Role
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.roles (tenant_id, code, name, description, is_system, rank) VALUES ($1,$2,$3,NULLIF($4,''),false,$5) RETURNING id`,
			p.TenantID, roleCode(name), name, in.Description, rank).Scan(&id); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A role with that name already exists")
			}
			return err
		}
		if err := s.replacePerms(ctx, tx, p, id, in.Perms, limits); err != nil {
			return err
		}
		var err error
		out, err = s.getRole(ctx, tx, p.TenantID, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateRole(ctx context.Context, p *auth.Principal, id uuid.UUID, in RoleInput) (Role, error) {
	// Nobody changes the permissions of their own role.
	if p.RoleID == id && !p.IsOwner {
		return Role{}, forbidden("You cannot change the permissions of your own role")
	}
	limits := limitsOf(in)
	if err := s.guardGrant(p, in.Perms, limits); err != nil {
		return Role{}, err
	}
	var out Role
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.getRole(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if cur.Slug == "admin" {
			return httpx.Forbidden("permission_denied", "The admin role cannot be edited")
		}
		if !p.IsOwner && cur.Rank > p.RoleRank {
			return forbidden("You cannot edit a role that outranks your own")
		}
		if n := strings.TrimSpace(in.Name); n != "" && !cur.IsSystem {
			if _, err := tx.Exec(ctx, `UPDATE app.roles SET name = $2, description = coalesce(NULLIF($3,''), description) WHERE id = $1`, id, n, in.Description); err != nil {
				return err
			}
		}
		if err := s.replacePerms(ctx, tx, p, id, in.Perms, limits); err != nil {
			return err
		}
		out, err = s.getRole(ctx, tx, p.TenantID, id)
		return err
	})
	return out, err
}

func (s *Service) replacePerms(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID, perms []RolePermView, limits map[string]decimal.Decimal) error {
	if _, err := tx.Exec(ctx, `DELETE FROM app.role_permissions WHERE tenant_id = $1 AND role_id = $2`, p.TenantID, roleID); err != nil {
		return err
	}
	for _, perm := range perms {
		if _, err := tx.Exec(ctx, `INSERT INTO app.role_permissions (tenant_id, role_id, permission_code) VALUES ($1,$2,$3)`, p.TenantID, roleID, perm.Key); err != nil {
			return httpx.BadRequest("invalid_body", "Unknown permission "+perm.Key)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.authority_limits WHERE tenant_id = $1 AND role_id = $2`, p.TenantID, roleID); err != nil {
		return err
	}
	for code, v := range limits {
		if _, err := tx.Exec(ctx, `INSERT INTO app.authority_limits (tenant_id, role_id, limit_code, limit_value) VALUES ($1,$2,$3::app.limit_code,$4)`, p.TenantID, roleID, code, v); err != nil {
			return httpx.BadRequest("invalid_body", "Invalid limit "+code)
		}
	}
	return s.Audit.Record(ctx, tx, p, "role.updated", "roles", roleID, nil, map[string]any{"permissions": len(perms)})
}

// DeleteRole retires a role. Roles are never deleted (people and history point at them), so this sets
// the status to inactive; a role still held by anyone cannot be retired.
func (s *Service) DeleteRole(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		r, err := s.getRole(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if r.IsSystem {
			return httpx.Conflict("invalid_status", "System roles cannot be deleted")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.membership_roles WHERE tenant_id = $1 AND role_id = $2`, p.TenantID, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("invalid_status", "Reassign users before deleting this role")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.roles SET status = 'inactive' WHERE id = $1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "role.retired", "roles", id, nil, nil)
	})
}

// ---------------------------------------------------------------------------------------------
// Login history, permission catalogue, profile
// ---------------------------------------------------------------------------------------------

type LoginEvent struct {
	ID         uuid.UUID  `json:"id"`
	UserID     *uuid.UUID `json:"user_id"`
	Identifier string     `json:"identifier"`
	Event      string     `json:"event"`
	Success    bool       `json:"success"`
	Reason     *string    `json:"reason"`
	IP         *string    `json:"ip"`
	UserAgent  *string    `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
}

// LoginHistory lists sign-in events for people in this tenant. The identifier is shown from the
// person's profile, never from the event (events store only a keyed hash of what was typed).
func (s *Service) LoginHistory(ctx context.Context, p *auth.Principal, userID *uuid.UUID, from, to string, page httpx.Page) ([]LoginEvent, int, error) {
	out := []LoginEvent{}
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		const where = `
			FROM iam.auth_events e LEFT JOIN iam.users u ON u.id = e.user_id
			WHERE e.event_type IN ('login_success','login_failure','account_locked','logout','session_timeout','forced_signout')
			  AND (e.tenant_id = $1 OR e.user_id IN (SELECT m.user_id FROM app.memberships m WHERE m.tenant_id = $1))
			  AND ($2::uuid IS NULL OR e.user_id = $2)
			  AND ($3 = '' OR e.occurred_at::date >= $3::date) AND ($4 = '' OR e.occurred_at::date <= $4::date)`
		if err := tx.QueryRow(ctx, `SELECT count(*) `+where, p.TenantID, userID, from, to).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.user_id, coalesce(u.phone::text, u.email::text, ''), e.event_type::text, e.event_type = 'login_success',
			       e.failure_reason, e.ip::text, e.user_agent, e.occurred_at `+where+`
			ORDER BY e.occurred_at DESC LIMIT $5 OFFSET $6`, p.TenantID, userID, from, to, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e LoginEvent
			if err := rows.Scan(&e.ID, &e.UserID, &e.Identifier, &e.Event, &e.Success, &e.Reason, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) PermissionCatalogue(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT code, module, description, is_sensitive FROM app.permissions ORDER BY module, code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code, module, desc string
			var sensitive bool
			if err := rows.Scan(&code, &module, &desc, &sensitive); err != nil {
				return err
			}
			m := map[string]any{"key": code, "module": module, "description": desc, "is_sensitive": sensitive, "has_limit": false}
			if lc, ok := limitCodeFor[code]; ok {
				m["has_limit"], m["limit_code"] = true, lc
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) Me(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	u, err := s.GetUser(ctx, p, p.UserID)
	if err != nil {
		return nil, err
	}
	perms := map[string]auth.Grant{}
	for k, g := range p.Perms {
		perms[k] = g
	}
	limits := map[string]decimal.Decimal{}
	for k, v := range p.Limits {
		limits[k] = v
	}
	return map[string]any{
		"user": u, "permissions": perms, "limits": limits, "branches": u.Branches,
		"is_owner": p.IsOwner, "tenant_id": p.TenantID, "business_id": p.TenantID, "membership_id": p.MembershipID,
	}, nil
}

func (s *Service) UpdateMe(ctx context.Context, p *auth.Principal, name, language string) error {
	if language != "sw" && language != "en" {
		language = ""
	}
	return s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE iam.users SET full_name = coalesce(NULLIF($2,''), full_name), preferred_locale = coalesce(NULLIF($3,''), preferred_locale) WHERE id = $1`,
			p.UserID, strings.TrimSpace(name), language)
		return err
	})
}

// ---------------------------------------------------------------------------------------------
// Per-person permission overrides
// ---------------------------------------------------------------------------------------------

type UserPermissionOverride struct {
	PermissionKey string     `json:"permission_key"`
	Allowed       bool       `json:"allowed"`
	Reason        string     `json:"reason"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

func (s *Service) GetUserPermissions(ctx context.Context, p *auth.Principal, userID uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		u, err := s.getUser(ctx, tx, p.TenantID, userID)
		if err != nil {
			return err
		}
		rolePerms := []map[string]any{}
		if u.RoleID != nil {
			rows, err := tx.Query(ctx, `SELECT permission_code FROM app.role_permissions WHERE tenant_id = $1 AND role_id = $2 ORDER BY permission_code`, p.TenantID, *u.RoleID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err != nil {
					rows.Close()
					return err
				}
				rolePerms = append(rolePerms, map[string]any{"permission_key": k, "scope": "all", "approval_limit": nil})
			}
			rows.Close()
		}
		overrides := []UserPermissionOverride{}
		orows, err := tx.Query(ctx, `
			SELECT permission_code, effect = 'allow', reason, expires_at FROM app.membership_permission_overrides
			WHERE tenant_id = $1 AND membership_id = $2 ORDER BY permission_code`, p.TenantID, u.MembershipID)
		if err != nil {
			return err
		}
		defer orows.Close()
		for orows.Next() {
			var ov UserPermissionOverride
			if err := orows.Scan(&ov.PermissionKey, &ov.Allowed, &ov.Reason, &ov.ExpiresAt); err != nil {
				return err
			}
			overrides = append(overrides, ov)
		}
		out = map[string]any{"user_id": userID, "role_id": u.RoleID, "role_slug": u.RoleSlug, "role_permissions": rolePerms, "overrides": overrides}
		return orows.Err()
	})
	return out, err
}

func (s *Service) PutUserPermissions(ctx context.Context, p *auth.Principal, userID uuid.UUID, overrides []UserPermissionOverride) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		u, err := s.getUser(ctx, tx, p.TenantID, userID)
		if err != nil {
			return err
		}
		if u.RoleSlug == "admin" {
			return httpx.Forbidden("owner_immutable", "An admin's permissions cannot be overridden")
		}
		if err := s.guardTarget(ctx, tx, p, userID); err != nil {
			return err
		}
		for _, ov := range overrides {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app.permissions WHERE code = $1)`, ov.PermissionKey).Scan(&exists); err != nil || !exists {
				return httpx.BadRequest("invalid_body", "Unknown permission: "+ov.PermissionKey)
			}
			if ov.Allowed && !canGrant(p, ov.PermissionKey) {
				return httpx.Forbidden("privilege_escalation", "You cannot grant a permission you do not hold: "+ov.PermissionKey)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.membership_permission_overrides WHERE tenant_id = $1 AND membership_id = $2`, p.TenantID, u.MembershipID); err != nil {
			return err
		}
		for _, ov := range overrides {
			effect, reason := "deny", strings.TrimSpace(ov.Reason)
			if ov.Allowed {
				effect = "allow"
			}
			if len(reason) < 3 {
				reason = "Set by an administrator"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.membership_permission_overrides (tenant_id, membership_id, permission_code, effect, reason, expires_at)
				VALUES ($1,$2,$3,$4::app.permission_effect,$5,$6)`, p.TenantID, u.MembershipID, ov.PermissionKey, effect, reason, ov.ExpiresAt); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "user.permissions_updated", "users", userID, nil, map[string]any{"overrides_count": len(overrides)})
	})
	if err != nil {
		return nil, err
	}
	return s.GetUserPermissions(ctx, p, userID)
}
