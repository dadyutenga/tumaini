package sales

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Tills have no table in the approved schema: end-of-day cash closing per location (app.day_closings)
// replaces cashier till sessions. Read endpoints return empty values so legacy screens stay quiet;
// mutating endpoints answer 410.

func removed(what string) error {
	return &httpx.Error{Status: 410, Code: "feature_removed", Message: what + " is not part of the approved model"}
}

type TillFilter struct {
	Status    string
	CashierID *uuid.UUID
	From, To  string
}

type PayOutIn struct {
	ToAccountID uuid.UUID       `json:"to_account_id"`
	Amount      decimal.Decimal `json:"amount"`
	Reference   string          `json:"reference"`
	Note        string          `json:"note"`
}

func (s *Service) OpenTill(ctx context.Context, p *auth.Principal, account uuid.UUID, float decimal.Decimal) (map[string]any, error) {
	return nil, removed("Cashier tills (use end-of-day closing per location)")
}

func (s *Service) CurrentTill(ctx context.Context, p *auth.Principal) (any, error) {
	return nil, nil
}

func (s *Service) CloseTill(ctx context.Context, p *auth.Principal, id uuid.UUID, counted decimal.Decimal, note string) (map[string]any, error) {
	return nil, removed("Cashier tills (use end-of-day closing per location)")
}

func (s *Service) Tills(ctx context.Context, p *auth.Principal, f TillFilter, page httpx.Page) ([]map[string]any, int, error) {
	return []map[string]any{}, 0, nil
}

func (s *Service) Till(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Cashier tills (use end-of-day closing per location)")
}

func (s *Service) PayOut(ctx context.Context, p *auth.Principal, tillID uuid.UUID, in PayOutIn) (int, []byte, error) {
	return 0, nil, removed("Till pay-outs (use an account transfer)")
}
