package payments

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/sequence"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Input struct {
	BusinessID     uuid.UUID
	BranchID       uuid.UUID
	MoneyAccountID uuid.UUID
	UserID         uuid.UUID
	TillSessionID  *uuid.UUID
	Direction      string
	Kind           string
	Method         string
	PartyType      string
	PartyID        *uuid.UUID
	Amount         decimal.Decimal
	Reference      string
	Note           string
	Date           string
	AllocType      string
	AllocID        uuid.UUID
	AllocAmount    decimal.Decimal
}

type Result struct {
	ID     uuid.UUID
	Number string
}

func Insert(ctx context.Context, tx pgx.Tx, in Input) (Result, error) {
	doc := "payment_in"
	if in.Direction == "out" {
		doc = "payment_out"
	}
	number, err := sequence.Next(ctx, tx, in.BranchID, doc)
	if err != nil {
		return Result{}, err
	}
	id := ids.New()
	var till, party, partyType, ref, note any
	if in.TillSessionID != nil {
		till = *in.TillSessionID
	}
	if in.PartyID != nil {
		party = *in.PartyID
	}
	if in.PartyType != "" {
		partyType = in.PartyType
	}
	if in.Reference != "" {
		ref = in.Reference
	}
	if in.Note != "" {
		note = in.Note
	}
	amt := money.Money(in.Amount)
	if _, err := tx.Exec(ctx, `
		INSERT INTO payments (id, business_id, branch_id, number, direction, kind, party_type, party_id, money_account_id, method, amount, reference, payment_date, till_session_id, note, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, id, in.BusinessID, in.BranchID, number, in.Direction, in.Kind, partyType, party, in.MoneyAccountID, in.Method, amt, ref, in.Date, till, note, in.UserID); err != nil {
		return Result{}, err
	}
	if in.AllocType != "" && in.AllocAmount.IsPositive() {
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_allocations (id, business_id, payment_id, document_type, document_id, amount)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, ids.New(), in.BusinessID, id, in.AllocType, in.AllocID, money.Money(in.AllocAmount)); err != nil {
			return Result{}, err
		}
	}
	return Result{ID: id, Number: number}, nil
}
