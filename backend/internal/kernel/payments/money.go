package payments

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

// Account is the part of a money account that posting code needs.
type Account struct {
	ID             uuid.UUID
	BranchID       *uuid.UUID
	Type           string
	Name           string
	Code           string
	AllowOverdraft bool
	IsActive       bool
}

// LoadAccount reads a money account with its ledger code. A missing account is 404.
func LoadAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Account, error) {
	var a Account
	err := tx.QueryRow(ctx, `
		SELECT ma.id, ma.branch_id, ma.type, ma.name, la.code, ma.allow_overdraft, ma.is_active
		FROM money_accounts ma JOIN ledger_accounts la ON la.id = ma.ledger_account_id
		WHERE ma.id = $1
	`, id).Scan(&a.ID, &a.BranchID, &a.Type, &a.Name, &a.Code, &a.AllowOverdraft, &a.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, httpx.NotFound("not_found", "Money account not found")
	}
	return a, err
}

// ActiveAccount is LoadAccount that also rejects an inactive account.
func ActiveAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Account, error) {
	a, err := LoadAccount(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if !a.IsActive {
		return a, httpx.Conflict("account_inactive", "That money account is not active")
	}
	return a, nil
}

// MethodFor maps a money account type to the payment method it records.
func MethodFor(accountType string) string {
	switch accountType {
	case "bank":
		return "bank"
	case "mobile_money":
		return "mobile_money"
	default:
		return "cash"
	}
}

type TransferInput struct {
	BusinessID    uuid.UUID
	BranchID      uuid.UUID
	UserID        uuid.UUID
	From          uuid.UUID
	To            uuid.UUID
	Amount        decimal.Decimal
	Fee           decimal.Decimal
	Date          string
	Reference     string
	Note          string
	TillSessionID *uuid.UUID
}

type TransferResult struct {
	ID     uuid.UUID
	Number string
	Amount decimal.Decimal
	Fee    decimal.Decimal
}

// Transfer moves money between two accounts in the caller's transaction:
// destination debited, source credited with amount plus fee, fee to 6400.
func Transfer(ctx context.Context, tx pgx.Tx, led *ledger.Service, in TransferInput) (TransferResult, error) {
	amount := money.Money(in.Amount)
	fee := money.Money(in.Fee)
	if !amount.IsPositive() || fee.IsNegative() {
		return TransferResult{}, httpx.BadRequest("invalid_body", "Amount must be greater than zero and fee cannot be negative")
	}
	if in.From == in.To {
		return TransferResult{}, httpx.BadRequest("invalid_body", "Choose two different accounts")
	}
	from, err := ActiveAccount(ctx, tx, in.From)
	if err != nil {
		return TransferResult{}, err
	}
	to, err := ActiveAccount(ctx, tx, in.To)
	if err != nil {
		return TransferResult{}, err
	}
	if err := led.EnsureFunds(ctx, tx, in.From, amount.Add(fee)); err != nil {
		return TransferResult{}, err
	}
	number, err := sequence.Next(ctx, tx, in.BranchID, "money_transfer")
	if err != nil {
		return TransferResult{}, err
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO money_transfers (id, business_id, number, from_account_id, to_account_id, amount, fee, transfer_date, reference, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),$11)
	`, id, in.BusinessID, number, in.From, in.To, amount, fee, in.Date, in.Reference, in.Note, in.UserID); err != nil {
		return TransferResult{}, err
	}
	if in.TillSessionID != nil {
		// Set only by till pay-outs; the column is added by the sales migration.
		if _, err := tx.Exec(ctx, `UPDATE money_transfers SET till_session_id=$2 WHERE id=$1`, id, *in.TillSessionID); err != nil {
			return TransferResult{}, err
		}
	}
	branch := in.BranchID
	user := in.UserID
	lines := []ledger.Line{
		{Code: to.Code, BranchID: &branch, MoneyAccountID: &to.ID, Debit: amount},
		{Code: from.Code, BranchID: &branch, MoneyAccountID: &from.ID, Credit: amount.Add(fee)},
	}
	if fee.IsPositive() {
		lines = append(lines, ledger.Line{Code: "6400", BranchID: &branch, Debit: fee})
	}
	if _, err := led.Post(ctx, tx, in.BusinessID, in.Date, "money_transfer", id, "Transfer "+number, &user, lines); err != nil {
		return TransferResult{}, err
	}
	return TransferResult{ID: id, Number: number, Amount: amount, Fee: fee}, nil
}

type ReconcileInput struct {
	BusinessID uuid.UUID
	BranchID   uuid.UUID
	UserID     uuid.UUID
	AccountID  uuid.UUID
	Actual     decimal.Decimal
	Date       string
	Note       string
	// DryRun only reports the difference (FR-FIN-16); nothing is recorded or posted.
	DryRun bool
}

type ReconcileResult struct {
	ID         uuid.UUID       `json:"id"`
	AccountID  uuid.UUID       `json:"money_account_id"`
	AsOf       string          `json:"as_of_date"`
	System     decimal.Decimal `json:"system_balance"`
	Actual     decimal.Decimal `json:"actual_balance"`
	Difference decimal.Decimal `json:"difference"`
	Posted     bool            `json:"posted"`
}

// Reconcile records a counted or statement balance and posts any difference
// to 6300 Cash over or short, so the account balance equals the actual one.
func Reconcile(ctx context.Context, tx pgx.Tx, led *ledger.Service, in ReconcileInput) (ReconcileResult, error) {
	acc, err := ActiveAccount(ctx, tx, in.AccountID)
	if err != nil {
		return ReconcileResult{}, err
	}
	actual := money.Money(in.Actual)
	if actual.IsNegative() && !acc.AllowOverdraft {
		return ReconcileResult{}, httpx.BadRequest("invalid_body", "Actual balance cannot be negative for this account")
	}
	// Lock the account row so two reconciliations cannot post on the same balance.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM money_accounts WHERE id=$1 FOR UPDATE`, in.AccountID); err != nil {
		return ReconcileResult{}, err
	}
	system, err := led.MoneyBalance(ctx, tx, in.AccountID)
	if err != nil {
		return ReconcileResult{}, err
	}
	diff := actual.Sub(system)
	if in.DryRun {
		return ReconcileResult{AccountID: in.AccountID, AsOf: in.Date, System: system, Actual: actual, Difference: diff}, nil
	}
	// Posting an adjustment needs a reason, so the correction can be explained afterwards.
	if !diff.IsZero() && strings.TrimSpace(in.Note) == "" {
		return ReconcileResult{}, &httpx.Error{Status: 422, Code: "validation_failed", Message: "Give a reason for the adjustment", Fields: map[string]string{"note": "required when the counted balance differs"}}
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_reconciliations (id, business_id, money_account_id, as_of_date, system_balance, actual_balance, difference, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)
	`, id, in.BusinessID, in.AccountID, in.Date, system, actual, diff, in.Note, in.UserID); err != nil {
		return ReconcileResult{}, err
	}
	if !diff.IsZero() {
		branch := in.BranchID
		user := in.UserID
		lines := []ledger.Line{
			{Code: acc.Code, BranchID: &branch, MoneyAccountID: &acc.ID, Debit: diff},
			{Code: "6300", BranchID: &branch, Credit: diff},
		}
		if diff.IsNegative() {
			lines = []ledger.Line{
				{Code: "6300", BranchID: &branch, Debit: diff.Neg()},
				{Code: acc.Code, BranchID: &branch, MoneyAccountID: &acc.ID, Credit: diff.Neg()},
			}
		}
		if _, err := led.Post(ctx, tx, in.BusinessID, in.Date, "reconciliation", id, "Reconciliation of "+acc.Name, &user, lines); err != nil {
			return ReconcileResult{}, err
		}
	}
	return ReconcileResult{ID: id, AccountID: in.AccountID, AsOf: in.Date, System: system, Actual: actual, Difference: diff, Posted: true}, nil
}
