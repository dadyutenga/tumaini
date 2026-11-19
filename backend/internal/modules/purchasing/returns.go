package purchasing

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Purchase returns and separate purchase invoices are not part of the approved model: payables live
// on app.purchases (supplier_invoice_ref, due_date, total, amount_paid, balance_due) and stock
// corrections use stock adjustments. These endpoints answer 410 / empty list.

func removed(what string) error {
	return &httpx.Error{Status: 410, Code: "feature_removed", Message: what + " is not part of the approved model"}
}

type ReturnItem struct {
	ReceiptItemID uuid.UUID       `json:"receipt_item_id" validate:"required"`
	Qty           decimal.Decimal `json:"qty" validate:"dpos"`
}

type ReturnIn struct {
	ReceiptID      uuid.UUID    `json:"receipt_id" validate:"required"`
	Reason         string       `json:"reason" validate:"required,max=500"`
	Settlement     string       `json:"settlement" validate:"oneof=reduce_debt refund"`
	MoneyAccountID *uuid.UUID   `json:"money_account_id"`
	Method         string       `json:"method" validate:"omitempty,oneof=cash bank mobile_money"`
	Reference      string       `json:"reference" validate:"max=100"`
	Items          []ReturnItem `json:"items" validate:"min=1,max=500,dive"`
}

func (s *Service) CreateReturn(ctx context.Context, p *auth.Principal, in ReturnIn) (int, []byte, error) {
	return 0, nil, removed("Purchase returns (record a stock adjustment instead)")
}

func (s *Service) ListReturns(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	return []map[string]any{}, 0, nil
}

func (s *Service) GetReturn(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Purchase returns")
}
