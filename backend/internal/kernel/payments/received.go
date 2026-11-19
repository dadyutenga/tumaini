package payments

// Writers for the approved payment tables. The database posts the money (app.financial_transactions),
// applies allocations to sales, invoices, purchases and bills, and refuses what is not allowed (voided
// payments, over-allocation, a payee that does not match the document). These helpers only insert.
// They sit beside the older Insert/Transfer helpers, which are removed when the last module that calls
// them has been ported.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/sequence"
	"bms/internal/platform/httpx"
)

// FinAccount is a cash, bank, mobile-money or petty-cash account (app.financial_accounts).
type FinAccount struct {
	ID            uuid.UUID
	LocationID    *uuid.UUID
	BusinessUnit  *uuid.UUID
	Kind          string
	Name          string
	AllowNegative bool
	Active        bool
	Balance       decimal.Decimal
}

// LoadFin reads a financial account of the caller's tenant. A missing account is 404.
func LoadFin(ctx context.Context, tx pgx.Tx, id uuid.UUID) (FinAccount, error) {
	var a FinAccount
	err := tx.QueryRow(ctx, `
		SELECT id, location_id, business_unit_id, kind::text, name::text, allow_negative, status = 'active', current_balance
		FROM app.financial_accounts WHERE id = $1`, id).
		Scan(&a.ID, &a.LocationID, &a.BusinessUnit, &a.Kind, &a.Name, &a.AllowNegative, &a.Active, &a.Balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, httpx.NotFound("not_found", "Money account not found")
	}
	return a, err
}

// ActiveFin is LoadFin that also rejects an inactive account.
func ActiveFin(ctx context.Context, tx pgx.Tx, id uuid.UUID) (FinAccount, error) {
	a, err := LoadFin(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if !a.Active {
		return a, httpx.Conflict("account_inactive", "That money account is not active")
	}
	return a, nil
}

// MethodForKind is the payment method an account of this kind records by default.
func MethodForKind(kind string) string {
	switch kind {
	case "bank":
		return "bank_transfer"
	case "mobile_money":
		return "mobile_money"
	default:
		return "cash"
	}
}

// Method maps what a client may send ("bank" is the older name) to app.payment_method.
func Method(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "bank", "bank_transfer":
		return "bank_transfer"
	case "mobile_money", "card", "cheque", "cash":
		return strings.ToLower(strings.TrimSpace(m))
	}
	return ""
}

// MethodOut is the name the API has always used for a method.
func MethodOut(m string) string {
	if m == "bank_transfer" {
		return "bank"
	}
	return m
}

// Alloc settles part of a document with a payment.
type Alloc struct {
	SaleID, InvoiceID                *uuid.UUID // received
	PurchaseID, BillID, SaleReturnID *uuid.UUID // made
	Amount                           decimal.Decimal
}

type ReceiveInput struct {
	TenantID     uuid.UUID
	LocationID   uuid.UUID
	AccountID    uuid.UUID
	CustomerID   *uuid.UUID
	Purpose      string // sale, deposit, installment, final, debt_payment, advance
	Method       string // empty: derived from the account
	Amount       decimal.Decimal
	Reference    string
	BusinessDate string
	ClientRef    *uuid.UUID
	Notes        string
	Allocations  []Alloc
}

type Receipt struct {
	ID     uuid.UUID
	Number string
}

// Receive records money received and the documents it settles.
func Receive(ctx context.Context, tx pgx.Tx, in ReceiveInput) (Receipt, error) {
	acc, err := ActiveFin(ctx, tx, in.AccountID)
	if err != nil {
		return Receipt{}, err
	}
	method := in.Method
	if method == "" {
		method = MethodForKind(acc.Kind)
	}
	number, err := sequence.Next(ctx, tx, uuid.Nil, "payment_receipt")
	if err != nil {
		return Receipt{}, err
	}
	var r Receipt
	r.Number = number
	if err := tx.QueryRow(ctx, `
		INSERT INTO app.payments_received (tenant_id, receipt_no, customer_id, business_unit_id, location_id, account_id, method, purpose,
		                                   amount, reference, business_date, client_ref, notes)
		VALUES ($1,$2,$3,(SELECT business_unit_id FROM app.locations WHERE tenant_id = $1 AND id = $4),$4,$5,$6::app.payment_method,$7::app.payment_purpose,
		        $8,NULLIF($9,''),$10::date,$11,NULLIF($12,''))
		RETURNING id`,
		in.TenantID, number, in.CustomerID, in.LocationID, in.AccountID, method, in.Purpose, in.Amount, in.Reference, in.BusinessDate, in.ClientRef, in.Notes).Scan(&r.ID); err != nil {
		return Receipt{}, err
	}
	for _, a := range in.Allocations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.payment_allocations (tenant_id, payment_received_id, sale_id, invoice_id, amount) VALUES ($1,$2,$3,$4,$5)`,
			in.TenantID, r.ID, a.SaleID, a.InvoiceID, a.Amount); err != nil {
			return Receipt{}, err
		}
	}
	return r, nil
}

type PayInput struct {
	TenantID     uuid.UUID
	LocationID   uuid.UUID
	AccountID    uuid.UUID
	SupplierID   *uuid.UUID
	ContractorID *uuid.UUID
	EmployeeID   *uuid.UUID
	CustomerID   *uuid.UUID // refunds
	OtherPayee   string
	Method       string
	Amount       decimal.Decimal
	Reference    string
	BusinessDate string
	ClientRef    *uuid.UUID
	Notes        string
	Allocations  []Alloc
}

// Pay records money paid out and the documents it settles. Exactly one payee must be named.
func Pay(ctx context.Context, tx pgx.Tx, in PayInput) (Receipt, error) {
	acc, err := ActiveFin(ctx, tx, in.AccountID)
	if err != nil {
		return Receipt{}, err
	}
	method := in.Method
	if method == "" {
		method = MethodForKind(acc.Kind)
	}
	number, err := sequence.Next(ctx, tx, uuid.Nil, "payment_made")
	if err != nil {
		return Receipt{}, err
	}
	r := Receipt{Number: number}
	if err := tx.QueryRow(ctx, `
		INSERT INTO app.payments_made (tenant_id, payment_no, business_unit_id, location_id, account_id, method, amount, reference, business_date,
		                               supplier_id, contractor_id, employee_id, customer_id, other_payee_name, client_ref, notes)
		VALUES ($1,$2,(SELECT business_unit_id FROM app.locations WHERE tenant_id = $1 AND id = $3),$3,$4,$5::app.payment_method,$6,NULLIF($7,''),$8::date,
		        $9,$10,$11,$12,NULLIF($13,''),$14,NULLIF($15,''))
		RETURNING id`,
		in.TenantID, number, in.LocationID, in.AccountID, method, in.Amount, in.Reference, in.BusinessDate,
		in.SupplierID, in.ContractorID, in.EmployeeID, in.CustomerID, in.OtherPayee, in.ClientRef, in.Notes).Scan(&r.ID); err != nil {
		return Receipt{}, err
	}
	for _, a := range in.Allocations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.payment_made_allocations (tenant_id, payment_made_id, purchase_id, contractor_bill_id, sale_return_id, amount) VALUES ($1,$2,$3,$4,$5,$6)`,
			in.TenantID, r.ID, a.PurchaseID, a.BillID, a.SaleReturnID, a.Amount); err != nil {
			return Receipt{}, err
		}
	}
	return r, nil
}
