package reports

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/money"
)

type Service struct{ DB *db.DB }

func New(database *db.DB) *Service { return &Service{DB: database} }

func (s *Service) ProfitLoss(ctx context.Context, p *auth.Principal, from, to string) (map[string]any, error) {
	var lines []map[string]any
	income, expense := money.Zero, money.Zero
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.type, a.code, a.name,
			       COALESCE(SUM(CASE WHEN a.type='income' THEN l.credit-l.debit ELSE l.debit-l.credit END),0)
			FROM ledger_accounts a
			JOIN journal_lines l ON l.ledger_account_id=a.id
			WHERE a.type IN ('income','expense')
			  AND ($1='' OR l.entry_date >= $1::date)
			  AND ($2='' OR l.entry_date <= $2::date)
			GROUP BY a.id
			HAVING COALESCE(SUM(l.debit+l.credit),0) <> 0
			ORDER BY a.code
		`, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var typ, code, name string
			var amount decimal.Decimal
			if err := rows.Scan(&typ, &code, &name, &amount); err != nil {
				return err
			}
			if typ == "income" {
				income = income.Add(amount)
			} else {
				expense = expense.Add(amount)
			}
			lines = append(lines, map[string]any{"type": typ, "code": code, "name": name, "amount": amount})
		}
		return rows.Err()
	})
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"lines": lines, "income": income, "expenses": expense, "net_profit": income.Sub(expense)}, err
}
