package identity

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// seedDefaults adds the starting reference data for the one-business installation.
func seedDefaults(ctx context.Context, tx pgx.Tx, location *uuid.UUID) error {
	if location != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.financial_accounts (name, kind, location_id)
			VALUES ('Main till', 'cash', $1)`, *location); err != nil {
			return err
		}
	}
	steps := []struct {
		sql   string
		names []string
	}{
		{`INSERT INTO app.expense_categories (name) SELECT n FROM unnest($1::text[]) n`,
			[]string{"Rent", "Utilities", "Transport", "Repairs", "Marketing", "Salaries", "Other"}},
		{`INSERT INTO app.expense_categories (name, is_project_cost) SELECT n, true FROM unnest($1::text[]) n`,
			[]string{"Government fees", "Materials", "Printing", "Stock images", "Fonts"}},
		{`INSERT INTO app.lead_sources (name) SELECT n FROM unnest($1::text[]) n`,
			[]string{"Walk-in", "Referral", "Social media", "Phone call", "Repeat client"}},
		{`INSERT INTO app.document_types (name) SELECT n FROM unnest($1::text[]) n`,
			[]string{"National ID copy", "TIN certificate", "Business licence", "Passport photo", "Brief / reference"}},
	}
	for _, st := range steps {
		if _, err := tx.Exec(ctx, st.sql, st.names); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.leave_types (name, is_paid, annual_days) VALUES
		('Annual', true, 28), ('Sick', true, 14), ('Maternity', true, 84), ('Unpaid', false, NULL)`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO app.tax_rates (name, rate_pct, is_default) VALUES ('Exempt', 0, true), ('VAT', 18, false)`)
	return err
}
