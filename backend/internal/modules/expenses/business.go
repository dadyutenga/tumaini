package expenses

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// chosenBusiness is the business picked on a form, or nil when none was: the person must work in it.
func chosenBusiness(p *auth.Principal, code string) (*uuid.UUID, error) {
	if code == "" {
		return nil, nil
	}
	for _, u := range p.Units {
		if u.Code == code {
			return &u.ID, nil
		}
	}
	return nil, httpx.Forbidden("business_forbidden", "You do not work in that business")
}

// businessFor is the business a new expense belongs to: the one chosen, else the branch's when the
// person works in it, else their main one.
func businessFor(ctx context.Context, tx pgx.Tx, p *auth.Principal, code string, location uuid.UUID) (uuid.UUID, error) {
	chosen, err := chosenBusiness(p, code)
	if err != nil {
		return uuid.Nil, err
	}
	if chosen != nil {
		return *chosen, nil
	}
	var unit uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT business_unit_id FROM app.locations WHERE id = $1`, location).Scan(&unit); err != nil {
		return uuid.Nil, httpx.NotFound("not_found", "Branch not found")
	}
	if p.MayUseUnit(unit) {
		return unit, nil
	}
	for _, u := range p.Units {
		if u.Primary {
			return u.ID, nil
		}
	}
	return p.Units[0].ID, nil
}
