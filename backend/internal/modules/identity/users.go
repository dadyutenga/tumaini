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
	"bms/internal/platform/ids"
)

type User struct {
	ID         uuid.UUID   `json:"id"`
	Name       string      `json:"name"`
	Phone      *string     `json:"phone"`
	Email      *string     `json:"email"`
	RoleID     uuid.UUID   `json:"role_id"`
	RoleName   string      `json:"role_name"`
	RoleSlug   string      `json:"role_slug"`
	EmployeeID *uuid.UUID  `json:"employee_id"`
	Language   string      `json:"language"`
	IsActive   bool        `json:"is_active"`
	Branches   []uuid.UUID `json:"branch_ids"`
	MustChange bool        `json:"must_change_password"`
}

type UserInput struct {
	Name       string
	Phone      string
	Email      string
	Password   string
	RoleID     uuid.UUID
	EmployeeID *uuid.UUID
	BranchIDs  []uuid.UUID
	Language   string
}

func (s *Service) ListUsers(ctx context.Context, p *auth.Principal, q string, page httpx.Page) ([]User, int, error) {
	var out []User
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE ($1 = '' OR name ILIKE '%'||$1||'%' OR phone ILIKE '%'||$1||'%')`, q).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.name, u.phone, u.email::text, u.role_id, r.name, r.slug, u.employee_id, u.language, u.is_active, u.must_change_password
			FROM users u JOIN roles r ON r.id = u.role_id
			WHERE ($1 = '' OR u.name ILIKE '%'||$1||'%' OR u.phone ILIKE '%'||$1||'%')
			ORDER BY u.name LIMIT $2 OFFSET $3
		`, q, page.PerPage, page.Offset)
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
	if err != nil {
		return nil, 0, err
	}
	if out == nil {
		out = []User{}
	}
	return out, total, nil
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Phone, &u.Email, &u.RoleID, &u.RoleName, &u.RoleSlug, &u.EmployeeID, &u.Language, &u.IsActive, &u.MustChange)
	return u, err
}

func (s *Service) GetUser(ctx context.Context, p *auth.Principal, id uuid.UUID) (User, error) {
	var u User
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			SELECT u.id, u.name, u.phone, u.email::text, u.role_id, r.name, r.slug, u.employee_id, u.language, u.is_active, u.must_change_password
			FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1
		`, id)
		var err error
		u, err = scanUser(row)
		if err != nil {
			return httpx.NotFound("not_found", "User not found")
		}
		u.Branches, err = s.branchesOf(ctx, tx, id)
		return err
	})
	return u, err
}

func (s *Service) branchesOf(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT branch_id FROM user_branches WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var idsOut []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		idsOut = append(idsOut, id)
	}
	if idsOut == nil {
		idsOut = []uuid.UUID{}
	}
	return idsOut, rows.Err()
}

func (s *Service) CreateUser(ctx context.Context, p *auth.Principal, in UserInput) (User, error) {
	if err := auth.PasswordOK(in.Password); err != nil {
		return User{}, httpx.BadRequest("weak_password", err.Error())
	}
	if in.Name == "" || (in.Phone == "" && in.Email == "") || in.RoleID == uuid.Nil || len(in.BranchIDs) == 0 {
		return User{}, httpx.BadRequest("invalid_body", "Name, phone or email, role and at least one branch are required")
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return User{}, err
	}
	id := ids.New()
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.ensureRole(ctx, tx, in.RoleID); err != nil {
			return err
		}
		var emp any
		if in.EmployeeID != nil {
			emp = *in.EmployeeID
		}
		lang := in.Language
		if lang == "" {
			lang = "en"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, business_id, role_id, employee_id, name, phone, email, password_hash, language, must_change_password, created_by)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')::citext,$8,$9,true,$10)
		`, id, p.BusinessID, in.RoleID, emp, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), hash, lang, p.UserID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already in use")
			}
			return err
		}
		for i, b := range in.BranchIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO user_branches (user_id, branch_id, business_id, is_default) VALUES ($1,$2,$3,$4)`, id, b, p.BusinessID, i == 0); err != nil {
				return httpx.BadRequest("invalid_branch", "One of the branches does not exist")
			}
		}
		return s.Audit.Record(ctx, tx, p, "user.created", "user", id, nil, map[string]any{"name": in.Name, "role_id": in.RoleID})
	})
	if err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, p, id)
}

func (s *Service) UpdateUser(ctx context.Context, p *auth.Principal, id uuid.UUID, in UserInput) (User, error) {
	if p.UserID == id && in.RoleID != uuid.Nil {
		var current uuid.UUID
		_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT role_id FROM users WHERE id = $1`, id).Scan(&current)
		})
		if current != in.RoleID {
			return User{}, httpx.Forbidden("permission_denied", "You cannot change your own role")
		}
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var slug string
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT u.role_id, r.slug FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, id).Scan(&roleID, &slug); err != nil {
			return httpx.NotFound("not_found", "User not found")
		}
		newRole := in.RoleID
		if newRole == uuid.Nil {
			newRole = roleID
		}
		if slug == "owner" && newRole != roleID {
			if err := s.ensureAnotherOwner(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE users SET name = COALESCE(NULLIF($2,''), name), phone = COALESCE(NULLIF($3,''), phone),
			  email = COALESCE(NULLIF($4,'')::citext, email), role_id = $5, language = COALESCE(NULLIF($6,''), language)
			WHERE id = $1
		`, id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), newRole, in.Language); err != nil {
			return err
		}
		if len(in.BranchIDs) > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM user_branches WHERE user_id = $1`, id); err != nil {
				return err
			}
			for i, b := range in.BranchIDs {
				if _, err := tx.Exec(ctx, `INSERT INTO user_branches (user_id, branch_id, business_id, is_default) VALUES ($1,$2,$3,$4)`, id, b, p.BusinessID, i == 0); err != nil {
					return err
				}
			}
		}
		return s.Audit.Record(ctx, tx, p, "user.updated", "user", id, nil, map[string]any{"name": in.Name, "role_id": newRole})
	})
	if err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, p, id)
}

func (s *Service) SetActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var slug string
		if err := tx.QueryRow(ctx, `SELECT r.slug FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, id).Scan(&slug); err != nil {
			return httpx.NotFound("not_found", "User not found")
		}
		if !active && slug == "owner" {
			if err := s.ensureAnotherOwner(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET is_active = $2 WHERE id = $1`, id, active); err != nil {
			return err
		}
		if !active {
			if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
				return err
			}
		}
		action := "user.activated"
		if !active {
			action = "user.deactivated"
		}
		return s.Audit.Record(ctx, tx, p, action, "user", id, nil, map[string]any{"is_active": active})
	})
}

func (s *Service) ensureAnotherOwner(ctx context.Context, tx pgx.Tx, except uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM users u JOIN roles r ON r.id = u.role_id
		WHERE r.slug = 'owner' AND u.is_active AND u.id <> $1
	`, except).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.Conflict("last_owner", "The business must keep at least one active owner")
	}
	return nil
}

func (s *Service) ensureRole(ctx context.Context, tx pgx.Tx, roleID uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM roles WHERE id = $1`, roleID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return httpx.BadRequest("invalid_body", "Role not found")
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
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2, must_change_password = true WHERE id = $1`, id, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "User not found")
		}
		_, _ = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
		return s.Audit.Record(ctx, tx, p, "user.password_reset", "user", id, nil, map[string]any{"id": id})
	})
}

func (s *Service) EndSessions(ctx context.Context, p *auth.Principal, userID uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		return err
	})
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
	var out []SessionView
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, COALESCE(user_agent,''), ip::text, expires_at, last_used_at, created_at
			FROM sessions WHERE user_id = $1 ORDER BY created_at DESC
		`, p.UserID)
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
	if out == nil {
		out = []SessionView{}
	}
	return out, err
}

func (s *Service) EndOwnSession(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1 AND user_id = $2`, id, p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Session not found")
		}
		return nil
	})
}

type Role struct {
	ID          uuid.UUID      `json:"id"`
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	IsSystem    bool           `json:"is_system"`
	PermVersion int            `json:"perm_version"`
	Perms       []RolePermView `json:"permissions,omitempty"`
}

type RolePermView struct {
	Key   string           `json:"key"`
	Scope string           `json:"scope"`
	Limit *decimal.Decimal `json:"limit"`
}

type RoleInput struct {
	Name  string
	Perms []RolePermView
}

func (s *Service) ListRoles(ctx context.Context, p *auth.Principal) ([]Role, error) {
	var out []Role
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, slug, is_system, perm_version FROM roles ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Role
			if err := rows.Scan(&r.ID, &r.Name, &r.Slug, &r.IsSystem, &r.PermVersion); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if out == nil {
		out = []Role{}
	}
	return out, err
}

func (s *Service) GetRole(ctx context.Context, p *auth.Principal, id uuid.UUID) (Role, error) {
	var r Role
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id, name, slug, is_system, perm_version FROM roles WHERE id = $1`, id).
			Scan(&r.ID, &r.Name, &r.Slug, &r.IsSystem, &r.PermVersion); err != nil {
			return httpx.NotFound("not_found", "Role not found")
		}
		rows, err := tx.Query(ctx, `SELECT permission_key, scope, approval_limit FROM role_permissions WHERE role_id = $1 ORDER BY permission_key`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var perm RolePermView
			if err := rows.Scan(&perm.Key, &perm.Scope, &perm.Limit); err != nil {
				return err
			}
			r.Perms = append(r.Perms, perm)
		}
		if r.Perms == nil {
			r.Perms = []RolePermView{}
		}
		return rows.Err()
	})
	return r, err
}

func (s *Service) CreateRole(ctx context.Context, p *auth.Principal, in RoleInput) (Role, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Role{}, httpx.BadRequest("invalid_body", "Role name is required")
	}
	id := ids.New()
	slug := "custom-" + strings.ReplaceAll(strings.ToLower(strings.TrimSpace(in.Name)), " ", "-")
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO roles (id, business_id, name, slug, is_system) VALUES ($1,$2,$3,$4,false)`, id, p.BusinessID, strings.TrimSpace(in.Name), slug); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A role with that name already exists")
			}
			return err
		}
		return s.replacePerms(ctx, tx, p, id, in.Perms)
	})
	if err != nil {
		return Role{}, err
	}
	return s.GetRole(ctx, p, id)
}

func (s *Service) UpdateRole(ctx context.Context, p *auth.Principal, id uuid.UUID, in RoleInput) (Role, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var system bool
		var slug string
		if err := tx.QueryRow(ctx, `SELECT is_system, slug FROM roles WHERE id = $1`, id).Scan(&system, &slug); err != nil {
			return httpx.NotFound("not_found", "Role not found")
		}
		if in.Name != "" && !system {
			if _, err := tx.Exec(ctx, `UPDATE roles SET name = $2 WHERE id = $1`, id, strings.TrimSpace(in.Name)); err != nil {
				return err
			}
		}
		if slug == "owner" {
			return httpx.Forbidden("permission_denied", "The owner role cannot be edited")
		}
		if _, err := tx.Exec(ctx, `UPDATE roles SET perm_version = perm_version + 1 WHERE id = $1`, id); err != nil {
			return err
		}
		return s.replacePerms(ctx, tx, p, id, in.Perms)
	})
	if err != nil {
		return Role{}, err
	}
	return s.GetRole(ctx, p, id)
}

func (s *Service) replacePerms(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID, perms []RolePermView) error {
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, perm := range perms {
		if perm.Scope == "" {
			perm.Scope = "branch"
		}
		if perm.Scope != "all" && perm.Scope != "branch" && perm.Scope != "own" {
			return httpx.BadRequest("invalid_body", "Scope must be all, branch or own")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, business_id, permission_key, scope, approval_limit) VALUES ($1,$2,$3,$4,$5)
		`, roleID, p.BusinessID, perm.Key, perm.Scope, perm.Limit); err != nil {
			return httpx.BadRequest("invalid_body", "Unknown permission "+perm.Key)
		}
	}
	return s.Audit.Record(ctx, tx, p, "role.updated", "role", roleID, nil, map[string]any{"permissions": len(perms)})
}

func (s *Service) DeleteRole(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var system bool
		if err := tx.QueryRow(ctx, `SELECT is_system FROM roles WHERE id = $1`, id).Scan(&system); err != nil {
			return httpx.NotFound("not_found", "Role not found")
		}
		if system {
			return httpx.Conflict("invalid_status", "System roles cannot be deleted")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role_id = $1`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("invalid_status", "Reassign users before deleting this role")
		}
		_, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id)
		return err
	})
}

type LoginEvent struct {
	ID         uuid.UUID  `json:"id"`
	UserID     *uuid.UUID `json:"user_id"`
	Identifier string     `json:"identifier"`
	Success    bool       `json:"success"`
	Reason     *string    `json:"reason"`
	IP         *string    `json:"ip"`
	UserAgent  *string    `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Service) LoginHistory(ctx context.Context, p *auth.Principal, userID *uuid.UUID, from, to string, page httpx.Page) ([]LoginEvent, int, error) {
	var out []LoginEvent
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM login_history
			WHERE ($1::uuid IS NULL OR user_id = $1)
			  AND ($2 = '' OR created_at::date >= $2::date)
			  AND ($3 = '' OR created_at::date <= $3::date)
		`, userID, from, to).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, user_id, identifier, success, reason, ip::text, user_agent, created_at
			FROM login_history
			WHERE ($1::uuid IS NULL OR user_id = $1)
			  AND ($2 = '' OR created_at::date >= $2::date)
			  AND ($3 = '' OR created_at::date <= $3::date)
			ORDER BY created_at DESC LIMIT $4 OFFSET $5
		`, userID, from, to, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e LoginEvent
			if err := rows.Scan(&e.ID, &e.UserID, &e.Identifier, &e.Success, &e.Reason, &e.IP, &e.UserAgent, &e.CreatedAt); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if out == nil {
		out = []LoginEvent{}
	}
	return out, total, err
}

func (s *Service) PermissionCatalogue(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT key, module, description, is_sensitive, has_limit FROM permissions ORDER BY module, key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key, module, desc string
			var sensitive, limit bool
			if err := rows.Scan(&key, &module, &desc, &sensitive, &limit); err != nil {
				return err
			}
			out = append(out, map[string]any{"key": key, "module": module, "description": desc, "is_sensitive": sensitive, "has_limit": limit})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
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
	return map[string]any{
		"user": u, "permissions": perms, "branches": u.Branches, "is_owner": p.IsOwner,
	}, nil
}

func (s *Service) UpdateMe(ctx context.Context, p *auth.Principal, name, language string) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE users SET name = COALESCE(NULLIF($2,''), name), language = COALESCE(NULLIF($3,''), language) WHERE id = $1`, p.UserID, strings.TrimSpace(name), language)
		return err
	})
}
