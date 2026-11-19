package identity

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// These guards stop a delegated administrator (a user holding users.manage or
// roles.manage who is not the Owner) from promoting themselves or taking over
// the Owner's account. Route permissions alone cannot express that.

var scopeRank = map[string]int{"own": 1, "branch": 2, "all": 3}

func forbidden(msg string) error { return httpx.Forbidden("permission_denied", msg) }

// guardTarget refuses any change to an Owner's account by a non-Owner, and
// returns 404 when the user does not exist in this business.
func (s *Service) guardTarget(ctx context.Context, tx pgx.Tx, p *auth.Principal, userID uuid.UUID) error {
	var slug string
	if err := tx.QueryRow(ctx, `SELECT r.slug FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = $1`, userID).Scan(&slug); err != nil {
		return httpx.NotFound("not_found", "User not found")
	}
	if slug == "owner" && !p.IsOwner {
		return forbidden("Only an owner can change an owner's account")
	}
	return nil
}

// canGrant reports whether p may hand out perm: it must hold the permission,
// with at least that scope and at least that approval limit.
func canGrant(p *auth.Principal, perm RolePermView) bool {
	if p.IsOwner {
		return true
	}
	held, ok := p.Can(perm.Key)
	if !ok {
		return false
	}
	scope := perm.Scope
	if scope == "" {
		scope = "branch"
	}
	if scopeRank[scope] > scopeRank[held.Scope] {
		return false
	}
	if held.Limit != nil {
		// A capped approver cannot give out a higher or unlimited cap.
		if perm.Limit == nil || perm.Limit.GreaterThan(*held.Limit) {
			return false
		}
	}
	return true
}

// guardGrant checks every permission of a role being written. Unknown scopes
// are left for the validation that follows, so the caller sees a 400 for them.
func (s *Service) guardGrant(p *auth.Principal, perms []RolePermView) error {
	for _, perm := range perms {
		if perm.Scope != "" && scopeRank[perm.Scope] == 0 {
			continue
		}
		if !canGrant(p, perm) {
			return forbidden("You cannot grant " + perm.Key + " because you do not hold it with that scope and limit")
		}
	}
	return nil
}

// guardAssignRole checks that p may put a user into a role: only an Owner
// assigns the Owner role, and nobody assigns a role stronger than their own.
func (s *Service) guardAssignRole(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID) error {
	if p.IsOwner {
		return nil
	}
	var slug string
	if err := tx.QueryRow(ctx, `SELECT slug FROM roles WHERE id = $1`, roleID).Scan(&slug); err != nil {
		return httpx.BadRequest("invalid_body", "Role not found")
	}
	if slug == "owner" {
		return forbidden("Only an owner can assign the owner role")
	}
	rows, err := tx.Query(ctx, `SELECT permission_key, scope, approval_limit FROM role_permissions WHERE role_id = $1`, roleID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var perm RolePermView
		var limit *decimal.Decimal
		if err := rows.Scan(&perm.Key, &perm.Scope, &limit); err != nil {
			return err
		}
		perm.Limit = limit
		if !canGrant(p, perm) {
			return forbidden("You cannot assign a role that has more access than your own")
		}
	}
	return rows.Err()
}

// GuardAssignRole lets other modules that create logins (for example an employee's
// login) apply the same rule as POST /users.
func GuardAssignRole(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID) error {
	return (&Service{}).guardAssignRole(ctx, tx, p, roleID)
}
