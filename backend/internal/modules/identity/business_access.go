package identity

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// accessChange reports whether in asks for businesses or branches other than u has.
func accessChange(u User, in UserInput) bool {
	if in.BusinessUnits != nil && !slices.Equal(sortedCodes(in.BusinessUnits), u.BusinessUnits) {
		return true
	}
	if in.PrimaryUnit != "" && in.PrimaryUnit != u.PrimaryUnit {
		return true
	}
	if in.Locations != nil && !sameIDs(*in.Locations, u.Locations) {
		return true
	}
	return false
}

func sortedCodes(codes []string) []string {
	out := slices.Clone(codes)
	slices.Sort(out)
	return slices.Compact(out)
}

func sameIDs(a, b []uuid.UUID) bool {
	set := func(ids []uuid.UUID) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	x, y := set(a), set(b)
	if len(x) != len(y) {
		return false
	}
	for id := range x {
		if !y[id] {
			return false
		}
	}
	return true
}

// AssignEmployeeLogin gives a login created from an employee record its businesses. When access
// names none, a Manager works in every business the creator has and anyone else in the employee's.
// Call it after the role is set. A branch asked for must belong to those businesses.
func AssignEmployeeLogin(ctx context.Context, tx pgx.Tx, p *auth.Principal, membershipID, roleID, employeeID uuid.UUID, access UserInput) error {
	role, err := roleCodeOf(ctx, tx, roleID)
	if err != nil {
		return err
	}
	var code string
	if err := tx.QueryRow(ctx, `
		SELECT u.code FROM app.employees e JOIN app.business_units u ON u.id = e.business_unit_id WHERE e.id = $1`, employeeID).Scan(&code); err != nil {
		return err
	}
	if access.BusinessUnits == nil {
		access.BusinessUnits, access.PrimaryUnit = []string{code}, code
		switch role {
		case "admin":
			access.BusinessUnits = []string{auth.Shop, auth.Services}
		case "manager":
			access.BusinessUnits = append(access.BusinessUnits, auth.UnitCodes(p.Units)...)
		}
	}
	return setBusinessAccess(ctx, tx, p, membershipID, role, nil, access)
}

// setBusinessAccess writes the businesses and branches of a membership and keeps its default branch
// inside them. current is nil for a new person. Nobody can give access to a business they do not
// work in themselves; the database additionally keeps Admins in every business.
func setBusinessAccess(ctx context.Context, tx pgx.Tx, p *auth.Principal, membershipID uuid.UUID, roleCode string, current *User, in UserInput) error {
	codes := in.BusinessUnits
	// Left to the database a new person gets every business, which only someone who has every
	// business may hand out.
	if codes == nil && current == nil && roleCode != "admin" {
		codes = auth.UnitCodes(p.Units)
	}
	if codes != nil {
		codes = sortedCodes(codes)
		if len(codes) == 0 {
			return httpx.BadRequest("invalid_body", "Choose at least one business")
		}
		for _, code := range codes {
			if code != auth.Shop && code != auth.Services {
				return httpx.BadRequest("invalid_body", "A business is A (shop) or B (services)")
			}
			had := current != nil && slices.Contains(current.BusinessUnits, code)
			if !had && !p.MayUseBusiness(code) {
				return httpx.Forbidden("business_forbidden", "You cannot give access to a business you do not work in")
			}
		}
		if roleCode == "admin" && len(codes) < 2 {
			return httpx.BadRequest("invalid_body", "An Admin always works in both businesses")
		}
		primary := in.PrimaryUnit
		if primary == "" {
			primary = codes[0]
			if current != nil && slices.Contains(codes, current.PrimaryUnit) {
				primary = current.PrimaryUnit
			}
		}
		if !slices.Contains(codes, primary) {
			return httpx.BadRequest("invalid_body", "The main business must be one of the chosen businesses")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.membership_business_units WHERE membership_id = $1`, membershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.membership_business_units (membership_id, business_unit_id, is_primary)
			SELECT $1, id, code = $3 FROM app.business_units WHERE code = ANY($2)`, membershipID, codes, primary); err != nil {
			return err
		}
	} else if in.PrimaryUnit != "" {
		if current != nil && !slices.Contains(current.BusinessUnits, in.PrimaryUnit) {
			return httpx.BadRequest("invalid_body", "The main business must be one of the chosen businesses")
		}
		// Two statements: only one row may be primary at any moment.
		if _, err := tx.Exec(ctx, `UPDATE app.membership_business_units SET is_primary = false WHERE membership_id = $1`, membershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.membership_business_units SET is_primary = true
			WHERE membership_id = $1 AND business_unit_id = (SELECT id FROM app.business_units WHERE code = $2)`, membershipID, in.PrimaryUnit); err != nil {
			return err
		}
	}

	if in.Locations != nil {
		if roleCode == "admin" && len(*in.Locations) > 0 {
			return httpx.BadRequest("invalid_body", "An Admin cannot be limited to some branches")
		}
		var outside int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM unnest($2::uuid[]) AS wanted(id)
			WHERE NOT EXISTS (SELECT 1 FROM app.locations l
			                  JOIN app.membership_business_units b ON b.business_unit_id = l.business_unit_id AND b.membership_id = $1
			                  WHERE l.id = wanted.id AND l.status = 'active')`, membershipID, *in.Locations).Scan(&outside); err != nil {
			return err
		}
		if outside > 0 {
			return httpx.BadRequest("invalid_branch", "Each branch must be an active branch of the chosen businesses")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.membership_locations WHERE membership_id = $1`, membershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.membership_locations (membership_id, location_id)
			SELECT DISTINCT $1::uuid, id FROM unnest($2::uuid[]) AS wanted(id)`, membershipID, *in.Locations); err != nil {
			return err
		}
	}

	// A branch the caller asked for must fit; otherwise move the default to one that does.
	var wanted *uuid.UUID
	if len(in.BranchIDs) > 0 {
		wanted = &in.BranchIDs[0]
	}
	var fits *bool
	var chosen *uuid.UUID
	if err := tx.QueryRow(ctx, `
		WITH allowed AS (
			SELECT l.id, l.is_default, l.created_at FROM app.locations l
			JOIN app.membership_business_units b ON b.business_unit_id = l.business_unit_id AND b.membership_id = $1
			WHERE l.status = 'active'
			  AND (NOT EXISTS (SELECT 1 FROM app.membership_locations ml WHERE ml.membership_id = $1)
			       OR l.id IN (SELECT ml.location_id FROM app.membership_locations ml WHERE ml.membership_id = $1)))
		SELECT $2::uuid IN (SELECT id FROM allowed),
		       (SELECT id FROM allowed
		        ORDER BY (id IS NOT DISTINCT FROM $2) DESC,
		                 (id IS NOT DISTINCT FROM (SELECT default_location_id FROM app.memberships WHERE id = $1)) DESC,
		                 is_default DESC, created_at, id LIMIT 1)`, membershipID, wanted).Scan(&fits, &chosen); err != nil {
		return err
	}
	if wanted != nil && (fits == nil || !*fits) {
		return httpx.BadRequest("invalid_branch", "That branch does not exist or belongs to a business this person does not work in")
	}
	_, err := tx.Exec(ctx, `UPDATE app.memberships SET default_location_id = $2 WHERE id = $1 AND default_location_id IS DISTINCT FROM $2`, membershipID, chosen)
	return err
}
