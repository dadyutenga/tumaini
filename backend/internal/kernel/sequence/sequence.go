package sequence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var Prefixes = map[string]string{
	"sale": "SAL", "invoice": "INV", "receipt": "RCT", "return": "RET", "quotation": "QUO",
	"po": "PO", "grn": "GRN", "pinv": "PINV", "preturn": "PRET", "transfer": "TRF",
	"adjustment": "ADJ", "expense": "EXP", "payment_in": "PIN", "payment_out": "POUT",
	"money_transfer": "MTX", "payroll": "PAY", "payslip": "PSL", "loan": "LON",
	"stock_entry": "STK", "employee": "EMP", "income": "INC", "drawing": "DRW",
}

func Ensure(ctx context.Context, tx pgx.Tx, businessID, branchID uuid.UUID) error {
	for doc, prefix := range Prefixes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO document_sequences (business_id, branch_id, doc_type, prefix, next_number)
			VALUES ($1, $2, $3, $4, 1)
			ON CONFLICT (branch_id, doc_type) DO NOTHING
		`, businessID, branchID, doc, prefix); err != nil {
			return err
		}
	}
	return nil
}

// Next returns BRANCH-PREFIX-000001 and advances the counter in the caller's transaction.
func Next(ctx context.Context, tx pgx.Tx, branchID uuid.UUID, docType string) (string, error) {
	var code, prefix string
	var n int64
	err := tx.QueryRow(ctx, `
		UPDATE document_sequences s
		SET next_number = s.next_number + 1
		FROM branches b
		WHERE s.branch_id = $1 AND s.doc_type = $2 AND b.id = s.branch_id
		RETURNING b.code, s.prefix, s.next_number - 1
	`, branchID, docType).Scan(&code, &prefix, &n)
	if err != nil {
		return "", fmt.Errorf("sequence %s: %w", docType, err)
	}
	return fmt.Sprintf("%s-%s-%06d", code, prefix, n), nil
}
