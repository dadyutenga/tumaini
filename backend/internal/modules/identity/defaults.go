package identity

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedDefaults adds the starting reference data for a new tenant. Roles, permissions, units,
// locations, numbering and notification rules already come from app.provision_tenant; this adds what
// a business needs on day one and can rename or extend later: its till, expense categories, leave
// types, the VAT rate, lead sources and the document types services staff ask clients for.
func seedDefaults(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, location *uuid.UUID) error {
	if location != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.financial_accounts (tenant_id, name, kind, location_id)
			VALUES ($1, 'Main till', 'cash', $2)`, tenantID, *location); err != nil {
			return err
		}
	}
	steps := []struct {
		sql   string
		names []string
	}{
		{`INSERT INTO app.expense_categories (tenant_id, name) SELECT $1, n FROM unnest($2::text[]) n`,
			[]string{"Rent", "Utilities", "Transport", "Repairs", "Marketing", "Salaries", "Other"}},
		{`INSERT INTO app.expense_categories (tenant_id, name, is_project_cost) SELECT $1, n, true FROM unnest($2::text[]) n`,
			[]string{"Government fees", "Materials", "Printing", "Stock images", "Fonts"}},
		{`INSERT INTO app.lead_sources (tenant_id, name) SELECT $1, n FROM unnest($2::text[]) n`,
			[]string{"Walk-in", "Referral", "Social media", "Phone call", "Repeat client"}},
		{`INSERT INTO app.document_types (tenant_id, name) SELECT $1, n FROM unnest($2::text[]) n`,
			[]string{"National ID copy", "TIN certificate", "Business licence", "Passport photo", "Brief / reference"}},
	}
	for _, st := range steps {
		if _, err := tx.Exec(ctx, st.sql, tenantID, st.names); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.leave_types (tenant_id, name, is_paid, annual_days) VALUES
		($1, 'Annual', true, 28), ($1, 'Sick', true, 14), ($1, 'Maternity', true, 84), ($1, 'Unpaid', false, NULL)`, tenantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO app.tax_rates (tenant_id, name, rate_pct, is_default) VALUES ($1, 'Exempt', 0, true), ($1, 'VAT', 18, false)`, tenantID)
	return err
}
