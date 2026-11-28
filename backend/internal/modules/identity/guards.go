package identity

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// These guards stop a delegated administrator (someone holding security.user.manage who is not an
// Admin) from promoting themselves or taking over a more senior account. Route permissions alone
// cannot express that; role rank (app.roles.rank, Admin = 100) can.

func forbidden(msg string) error { return httpx.Forbidden("permission_denied", msg) }

// limitCodeFor names the authority limit that goes with a permission, if it has one.
var limitCodeFor = map[string]string{
	"shop.discount.apply":     "sale_discount_pct",
	"shop.discount.approve":   "discount_approve_pct",
	"shop.return.approve":     "refund_approve_amount",
	"finance.expense.approve": "expense_approve_amount",
	"services.price.approve":  "price_change_approve_pct",
}

// guardTarget refuses a change to an account that outranks the caller, and returns 404 when the
// person is not a member of this business.
func (s *Service) guardTarget(ctx context.Context, tx pgx.Tx, p *auth.Principal, userID uuid.UUID) error {
	var rank int
	if err := tx.QueryRow(ctx, "\n\t\tSELECT coalesce(r.rank, 0) FROM app.memberships m\n\t\tLEFT JOIN app.membership_roles mr ON mr.membership_id = m.id\n\t\tLEFT JOIN app.roles r ON r.id = mr.role_id\n\t\tWHERE m.user_id = $1", userID).Scan(&rank); err != nil {
		return httpx.NotFound("not_found", "User not found")
	}
	if !p.IsOwner && (rank > p.RoleRank || (rank == p.RoleRank && p.UserID != userID)) {
		return forbidden("You cannot change the account of someone at or above your role rank")
	}
	return nil
}

// canGrant reports whether p may hand out a permission: they must hold it themselves.
func canGrant(p *auth.Principal, code string) bool {
	return p.IsOwner || p.Has(code)
}

// canGrantLimit reports whether p may set a limit: a capped approver cannot give out a higher or
// unlimited cap.
func canGrantLimit(p *auth.Principal, code string, v *decimal.Decimal) bool {
	if p.IsOwner {
		return true
	}
	held, capped := p.Limits[code]
	if !capped {
		return true
	}
	return v != nil && v.LessThanOrEqual(held)
}

// guardGrant checks every permission and limit of a role being written.
func (s *Service) guardGrant(p *auth.Principal, perms []RolePermView, limits map[string]decimal.Decimal) error {
	for _, perm := range perms {
		if !canGrant(p, perm.Key) {
			return forbidden("You cannot grant " + perm.Key + " because you do not hold it")
		}
	}
	for code, v := range limits {
		v := v
		if !canGrantLimit(p, code, &v) {
			return forbidden("You cannot set " + code + " higher than your own limit")
		}
	}
	return nil
}

// guardAssignRole checks that p may put a person into a role: nobody assigns a role that outranks
// their own, nor one that carries a permission they do not hold.
func (s *Service) guardAssignRole(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID) error {
	if p.IsOwner {
		return nil
	}
	var rank int
	if err := tx.QueryRow(ctx, "SELECT rank FROM app.roles WHERE id = $1  AND status = 'active'", roleID).Scan(&rank); err != nil {
		return httpx.BadRequest("invalid_body", "Role not found")
	}
	if rank > p.RoleRank {
		return forbidden("You cannot assign a role that outranks your own")
	}
	rows, err := tx.Query(ctx, `SELECT permission_code FROM app.role_permissions WHERE role_id = $1`, roleID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return err
		}
		if !canGrant(p, code) {
			return forbidden("You cannot assign a role that has more access than your own")
		}
	}
	return rows.Err()
}

// GuardAssignRole lets other modules that create logins (for example an employee's login) apply the
// same rule as POST /users.
func GuardAssignRole(ctx context.Context, tx pgx.Tx, p *auth.Principal, roleID uuid.UUID) error {
	return (&Service{}).guardAssignRole(ctx, tx, p, roleID)
}
