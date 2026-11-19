package finance

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Other income, owner drawings and statutory payments were postings to a general ledger. The approved
// money model has customer receipts, payments out (to suppliers, contractors, staff, refunds and
// others), expenses and transfers instead, so these answer 410. Money received from a customer is a
// payment received; money paid to a tax authority is an expense or an "other payment".

type IncomeCategoryIn struct {
	Name string `json:"name"`
}

type IncomeIn struct {
	Kind           string          `json:"kind" validate:"omitempty,oneof=income capital"`
	CategoryID     *uuid.UUID      `json:"category_id"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Date           string          `json:"date" validate:"date"`
	Description    string          `json:"description" validate:"required,max=500"`
	Reference      string          `json:"reference" validate:"max=100"`
}

type OutflowIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Date           string          `json:"date" validate:"date"`
	Reference      string          `json:"reference" validate:"max=100"`
	Note           string          `json:"note" validate:"max=500"`
}

func (s *Service) IncomeCategories(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) CreateIncomeCategory(ctx context.Context, p *auth.Principal, in IncomeCategoryIn) (map[string]any, error) {
	return nil, removed("Other income")
}

func (s *Service) RecordIncome(ctx context.Context, p *auth.Principal, in IncomeIn) (int, []byte, error) {
	return 0, nil, removed("Other income")
}

func (s *Service) Income(ctx context.Context, p *auth.Principal, kind string, category *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	return []map[string]any{}, 0, nil
}

func (s *Service) Drawing(ctx context.Context, p *auth.Principal, in OutflowIn) (int, []byte, error) {
	return 0, nil, removed("Owner drawings")
}

func (s *Service) Statutory(ctx context.Context, p *auth.Principal, in OutflowIn) (int, []byte, error) {
	return 0, nil, removed("Statutory payments as a separate posting (record them as an expense)")
}
