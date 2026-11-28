// Package sequence issues gap-free document numbers from app.document_sequences.
//
// Numbering is per business and document type (R-0001, INV-0001, ...): the database function
// app.next_document_number locks the sequence row, so a rolled-back transaction releases its number.
// Branches no longer have their own counters.
package sequence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DocTypes are the values of app.doc_type.
var DocTypes = map[string]bool{
	"sale_receipt": true, "payment_receipt": true, "sale_return": true, "invoice": true, "quotation": true,
	"project": true, "design_job": true, "purchase": true, "goods_receipt": true, "stock_adjustment": true,
	"stock_transfer": true, "expense": true, "payment_made": true, "account_transfer": true, "payslip": true,
	"contractor_bill": true, "employee": true,
}

// legacy maps the document names modules used before the schema replacement. A module that is ported
// should ask for its app.doc_type directly; this keeps the rest working meanwhile.
var legacy = map[string]string{
	"sale": "sale_receipt", "receipt": "payment_receipt", "return": "sale_return", "po": "purchase", "grn": "goods_receipt",
	"transfer": "stock_transfer", "adjustment": "stock_adjustment", "stock_entry": "stock_adjustment",
	"payment_in": "payment_receipt", "payment_out": "payment_made", "money_transfer": "account_transfer",
	"svc_project": "project", "svc_quotation": "quotation", "svc_invoice": "invoice",
}

// Next returns the next number for a document type and advances the counter in the caller's
// transaction. branchID is ignored: numbering is per business.
func Next(ctx context.Context, tx pgx.Tx, _ uuid.UUID, docType string) (string, error) {
	if m, ok := legacy[docType]; ok {
		docType = m
	}
	if !DocTypes[docType] {
		return "", fmt.Errorf("sequence: %q is not a document type of the approved schema", docType)
	}
	var n string
	if err := tx.QueryRow(ctx, `SELECT app.next_document_number($1::app.doc_type)`, docType).Scan(&n); err != nil {
		return "", fmt.Errorf("sequence %s: %w", docType, err)
	}
	return n, nil
}
