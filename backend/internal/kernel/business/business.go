// Package business reads what each business (A the shop, B services) prints about itself.
package business

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Header is the identity printed on a business's receipts, invoices and quotations. Anything the
// business does not set for itself comes from the business profile and settings.
type Header struct {
	Name          string
	Phone         *string
	Email         *string
	Address       *string
	TaxNumber     *string
	RegNumber     *string
	Currency      string
	LogoFileID    *uuid.UUID
	ReceiptHeader *string
	ReceiptFooter *string
}

// HeaderFor returns the header of the business with this code.
func HeaderFor(ctx context.Context, tx pgx.Tx, code string) (Header, error) {
	var h Header
	err := tx.QueryRow(ctx, `
		SELECT coalesce(u.display_name::text, nullif(bs.business_name::text, ''), bp.name::text),
		       coalesce(u.phone, bs.phone)::text, bs.email::text, coalesce(u.address, bs.address), coalesce(u.tin, bs.tin),
		       bs.registration_no, bs.currency::text, coalesce(u.logo_file_id, bs.logo_file_id),
		       u.receipt_header, coalesce(u.receipt_footer, bs.receipt_footer)
		FROM app.business_profile bp
		CROSS JOIN app.business_settings bs
		LEFT JOIN app.business_units u ON u.code = $1`, code).
		Scan(&h.Name, &h.Phone, &h.Email, &h.Address, &h.TaxNumber, &h.RegNumber, &h.Currency, &h.LogoFileID, &h.ReceiptHeader, &h.ReceiptFooter)
	return h, err
}

// EmployeeBusiness is the code of the business an employee's pay is posted to.
func EmployeeBusiness(ctx context.Context, tx pgx.Tx, employee uuid.UUID) (string, error) {
	var code string
	err := tx.QueryRow(ctx, `
		SELECT u.code FROM app.employees e JOIN app.business_units u ON u.id = e.business_unit_id WHERE e.id = $1`, employee).Scan(&code)
	return code, err
}
