package expenses

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/sqlq"
)

// PettyCash lists petty cash accounts in scope with balance and custodian (FR-EXP-06).
func (s *Service) PettyCash(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New().Add("ma.type = 'petty_cash'")
		if b := p.ScopeBranches("petty_cash.manage"); b != nil {
			q.Add("(ma.branch_id IS NULL OR ma.branch_id = ANY(?))", b)
		}
		rows, err := tx.Query(ctx, `
			SELECT ma.id, ma.name, ma.branch_id, br.name, ma.custodian_id, u.name, ma.is_active,
			       COALESCE((SELECT SUM(debit-credit) FROM journal_lines jl WHERE jl.money_account_id=ma.id),0),
			       (SELECT max(r.as_of_date)::text FROM account_reconciliations r WHERE r.money_account_id=ma.id)
			FROM money_accounts ma
			LEFT JOIN branches br ON br.id=ma.branch_id
			LEFT JOIN users u ON u.id=ma.custodian_id`+q.Where()+` ORDER BY ma.name`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var branch, custodian *uuid.UUID
			var name string
			var branchName, custodianName, lastCount *string
			var active bool
			var bal decimal.Decimal
			if err := rows.Scan(&id, &name, &branch, &branchName, &custodian, &custodianName, &active, &bal, &lastCount); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "branch_id": branch, "branch_name": branchName,
				"custodian_id": custodian, "custodian_name": custodianName, "is_active": active, "balance": bal, "last_reconciled": lastCount})
		}
		return rows.Err()
	})
	return out, err
}

// pettyAccount checks the account is a petty cash account in the caller's scope.
func (s *Service) pettyAccount(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (payments.Account, uuid.UUID, error) {
	acc, err := payments.LoadAccount(ctx, tx, id)
	if err != nil || acc.Type != "petty_cash" {
		return acc, uuid.Nil, httpx.NotFound("not_found", "Petty cash account not found")
	}
	if acc.BranchID != nil {
		if !p.SeesBranch("petty_cash.manage", *acc.BranchID) {
			return acc, uuid.Nil, httpx.NotFound("not_found", "Petty cash account not found")
		}
		return acc, *acc.BranchID, nil
	}
	if p.BranchID != nil {
		return acc, *p.BranchID, nil
	}
	var main uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&main); err != nil {
		return acc, uuid.Nil, err
	}
	return acc, main, nil
}

type TopUpIn struct {
	FromAccountID uuid.UUID       `json:"from_account_id" validate:"required"`
	Amount        decimal.Decimal `json:"amount" validate:"dpos"`
	Date          string          `json:"date" validate:"date"`
	Reference     string          `json:"reference" validate:"max=100"`
	Note          string          `json:"note" validate:"max=500"`
}

// TopUp moves money into petty cash as a money transfer.
func (s *Service) TopUp(ctx context.Context, p *auth.Principal, id uuid.UUID, in TopUpIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		_, branch, err := s.pettyAccount(ctx, tx, p, id)
		if err != nil {
			return err
		}
		from, err := payments.LoadAccount(ctx, tx, in.FromAccountID)
		if err != nil {
			return err
		}
		if from.BranchID != nil && !p.SeesBranch("petty_cash.manage", *from.BranchID) {
			return httpx.NotFound("not_found", "Money account not found")
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		res, err := payments.Transfer(ctx, tx, s.Ledger, payments.TransferInput{
			BusinessID: p.BusinessID, BranchID: branch, UserID: p.UserID, From: in.FromAccountID, To: id,
			Amount: in.Amount, Date: date, Reference: in.Reference, Note: in.Note,
		})
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "petty_cash.topped_up", "money_account", id, nil, map[string]any{"transfer_id": res.ID, "amount": res.Amount, "from_account_id": in.FromAccountID}); err != nil {
			return err
		}
		bal, err := s.Ledger.MoneyBalance(ctx, tx, id)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"transfer_id": res.ID, "number": res.Number, "amount": res.Amount, "balance": bal}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

type ReconcileIn struct {
	ActualBalance decimal.Decimal `json:"actual_balance" validate:"dgte0"`
	Date          string          `json:"date" validate:"date"`
	Note          string          `json:"note" validate:"max=500"`
}

// Reconcile records a petty cash count and posts any difference.
func (s *Service) Reconcile(ctx context.Context, p *auth.Principal, id uuid.UUID, in ReconcileIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		_, branch, err := s.pettyAccount(ctx, tx, p, id)
		if err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		res, err := payments.Reconcile(ctx, tx, s.Ledger, payments.ReconcileInput{
			BusinessID: p.BusinessID, BranchID: branch, UserID: p.UserID, AccountID: id, Actual: in.ActualBalance, Date: date, Note: in.Note,
		})
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "petty_cash.reconciled", "money_account", id, nil, res); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(res))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
