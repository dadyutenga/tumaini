package expenses

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
)

type CategoryIn struct {
	Name       string `json:"name" validate:"required,max=100"`
	LedgerCode string `json:"ledger_code" validate:"omitempty,max=10"`
	IsActive   *bool  `json:"is_active"`
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal, all bool) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ec.id, ec.name, ec.is_active, la.id, la.code, la.name
			FROM expense_categories ec JOIN ledger_accounts la ON la.id=ec.ledger_account_id
			WHERE ec.is_active OR $1 ORDER BY ec.name
		`, all)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, ledgerID uuid.UUID
			var name, code, ledgerName string
			var active bool
			if err := rows.Scan(&id, &name, &active, &ledgerID, &code, &ledgerName); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "is_active": active, "ledger_account_id": ledgerID, "ledger_code": code, "ledger_name": ledgerName})
		}
		return rows.Err()
	})
	return out, err
}

// expenseLedger resolves a ledger code to an active expense account.
func expenseLedger(ctx context.Context, tx pgx.Tx, code string) (uuid.UUID, error) {
	if code == "" {
		code = "6090"
	}
	var id uuid.UUID
	var typ string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT id, type, is_active FROM ledger_accounts WHERE code=$1`, code).Scan(&id, &typ, &active); err != nil {
		return uuid.Nil, httpx.NotFound("not_found", "Ledger account not found")
	}
	// Salaries, cost of goods and bad debts are posted by their own modules only.
	if typ != "expense" || !active || code == "5000" || code == "6100" || code == "6110" || code == "6200" {
		return uuid.Nil, &httpx.Error{Status: 422, Code: "validation_failed", Message: "Choose an expense ledger account", Fields: map[string]string{"ledger_code": "expense_account"}}
	}
	return id, nil
}

func (s *Service) CreateCategory(ctx context.Context, p *auth.Principal, in CategoryIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	id := ids.New()
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		ledgerID, err := expenseLedger(ctx, tx, in.LedgerCode)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO expense_categories (id, business_id, name, ledger_account_id) VALUES ($1,$2,$3,$4)`, id, p.BusinessID, in.Name, ledgerID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A category with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense_category.created", "expense_category", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.category(ctx, p, id)
}

func (s *Service) UpdateCategory(ctx context.Context, p *auth.Principal, id uuid.UUID, in CategoryIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var oldName, oldCode string
		var oldActive bool
		if err := tx.QueryRow(ctx, `SELECT ec.name, la.code, ec.is_active FROM expense_categories ec JOIN ledger_accounts la ON la.id=ec.ledger_account_id WHERE ec.id=$1`, id).Scan(&oldName, &oldCode, &oldActive); err != nil {
			return httpx.NotFound("not_found", "Expense category not found")
		}
		code := in.LedgerCode
		if code == "" {
			code = oldCode
		}
		ledgerID, err := expenseLedger(ctx, tx, code)
		if err != nil {
			return err
		}
		active := oldActive
		if in.IsActive != nil {
			active = *in.IsActive
		}
		if _, err := tx.Exec(ctx, `UPDATE expense_categories SET name=$2, ledger_account_id=$3, is_active=$4 WHERE id=$1`, id, in.Name, ledgerID, active); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A category with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense_category.updated", "expense_category", id,
			map[string]any{"name": oldName, "ledger_code": oldCode, "is_active": oldActive},
			map[string]any{"name": in.Name, "ledger_code": code, "is_active": active})
	})
	if err != nil {
		return nil, err
	}
	return s.category(ctx, p, id)
}

func (s *Service) category(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	list, err := s.Categories(ctx, p, true)
	if err != nil {
		return nil, err
	}
	for _, c := range list {
		if c["id"] == id {
			return c, nil
		}
	}
	return nil, httpx.NotFound("not_found", "Expense category not found")
}
