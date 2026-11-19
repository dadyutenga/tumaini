package expenses

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

// PettyCash lists petty cash accounts with current balance and last reconciliation date.
func (s *Service) PettyCash(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("fa.tenant_id = $1").Add("fa.kind = 'petty_cash'")
		q.If(p.BranchID != nil, "(fa.location_id IS NULL OR fa.location_id = ?)", p.BranchID)
		rows, err := tx.Query(ctx, `
			SELECT fa.id, fa.name::text, fa.location_id, l.name::text, fa.status = 'active', fa.current_balance,
			       (SELECT max(r.period_end)::text FROM app.petty_cash_reconciliations r WHERE r.tenant_id = fa.tenant_id AND r.account_id = fa.id)
			FROM app.financial_accounts fa
			LEFT JOIN app.locations l ON l.tenant_id = fa.tenant_id AND l.id = fa.location_id`+q.Where()+` ORDER BY fa.name`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var branch *uuid.UUID
			var name string
			var branchName, lastCount *string
			var active bool
			var bal decimal.Decimal
			if err := rows.Scan(&id, &name, &branch, &branchName, &active, &bal, &lastCount); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "branch_id": branch, "branch_name": branchName,
				"is_active": active, "balance": bal, "last_reconciled": lastCount})
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) pettyAccount(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (payments.FinAccount, uuid.UUID, error) {
	acc, err := payments.LoadFin(ctx, tx, id)
	if err != nil || acc.Kind != "petty_cash" {
		return acc, uuid.Nil, httpx.NotFound("not_found", "Petty cash account not found")
	}
	if acc.LocationID != nil {
		return acc, *acc.LocationID, nil
	}
	if p.BranchID != nil {
		return acc, *p.BranchID, nil
	}
	var def uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM app.locations WHERE tenant_id = $1 AND is_default`, p.TenantID).Scan(&def); err != nil {
		return acc, uuid.Nil, err
	}
	return acc, def, nil
}

type TopUpIn struct {
	FromAccountID uuid.UUID       `json:"from_account_id" validate:"required"`
	Amount        decimal.Decimal `json:"amount" validate:"dpos"`
	Date          string          `json:"date" validate:"date"`
	Reference     string          `json:"reference" validate:"max=100"`
	Note          string          `json:"note" validate:"max=500"`
}

// TopUp moves money into petty cash through app.account_transfers; the database posts both ledger rows.
func (s *Service) TopUp(ctx context.Context, p *auth.Principal, id uuid.UUID, in TopUpIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	if in.FromAccountID == id {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose an account other than petty cash")
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		petty, branch, err := s.pettyAccount(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if !petty.Active {
			return httpx.Conflict("account_inactive", "That petty cash account is inactive")
		}
		if _, err := payments.ActiveFin(ctx, tx, in.FromAccountID); err != nil {
			return err
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "account_transfer")
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		if ref := strings.TrimSpace(in.Reference); ref != "" {
			note = strings.TrimSpace(note + " " + ref)
		}
		amt := money.Money(in.Amount)
		var tid uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.account_transfers (tenant_id, transfer_no, from_account_id, to_account_id, amount, location_id, business_date, note)
			VALUES ($1,$2,$3,$4,$5,$6,$7::date,NULLIF($8,''))
			RETURNING id`, p.TenantID, number, in.FromAccountID, id, amt, branch, date, note).Scan(&tid); err != nil {
			return err
		}
		var bal decimal.Decimal
		_ = tx.QueryRow(ctx, `SELECT current_balance FROM app.financial_accounts WHERE tenant_id = $1 AND id = $2`, p.TenantID, id).Scan(&bal)
		if err := s.Audit.Record(ctx, tx, p, "petty_cash.topped_up", "financial_accounts", id, nil, map[string]any{"transfer_id": tid, "amount": amt, "from_account_id": in.FromAccountID}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"transfer_id": tid, "number": number, "amount": amt, "balance": bal}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

type ReconcileIn struct {
	ActualBalance decimal.Decimal `json:"actual_balance" validate:"dgte0"`
	PeriodStart   string          `json:"period_start" validate:"date"`
	Date          string          `json:"date" validate:"date"`
	Note          string          `json:"note" validate:"max=1000"`
}

// Reconcile records a petty cash count in app.petty_cash_reconciliations.
func (s *Service) Reconcile(ctx context.Context, p *auth.Principal, id uuid.UUID, in ReconcileIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if _, _, err := s.pettyAccount(ctx, tx, p, id); err != nil {
			return err
		}
		periodEnd := strings.TrimSpace(in.Date)
		if periodEnd == "" {
			var err error
			if periodEnd, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		periodStart := strings.TrimSpace(in.PeriodStart)
		if periodStart == "" {
			var prev *string
			_ = tx.QueryRow(ctx, `SELECT max(period_end)::text FROM app.petty_cash_reconciliations WHERE tenant_id = $1 AND account_id = $2 AND period_end < $3::date`, p.TenantID, id, periodEnd).Scan(&prev)
			if prev != nil {
				if d, err := time.Parse("2006-01-02", *prev); err == nil {
					periodStart = d.AddDate(0, 0, 1).Format("2006-01-02")
				}
			}
			if periodStart == "" || periodStart > periodEnd {
				periodStart = periodEnd
			}
		}
		if periodStart > periodEnd {
			return httpx.BadRequest("invalid_body", "period_start cannot be after date")
		}
		var opening, expected decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END) FILTER (WHERE business_date < $3::date), 0),
			       COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END) FILTER (WHERE business_date <= $4::date), 0)
			FROM app.financial_transactions WHERE tenant_id = $1 AND account_id = $2`,
			p.TenantID, id, periodStart, periodEnd).Scan(&opening, &expected); err != nil {
			return err
		}
		counted := money.Money(in.ActualBalance)
		diff := counted.Sub(expected)
		note := strings.TrimSpace(in.Note)
		if !diff.IsZero() && note == "" {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "Explain the cash difference in note", Fields: map[string]string{"note": "required"}}
		}
		var rid uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.petty_cash_reconciliations (tenant_id, account_id, period_start, period_end, opening_balance, expected_closing, counted_closing, note)
			VALUES ($1,$2,$3::date,$4::date,$5,$6,$7,NULLIF($8,''))
			RETURNING id`, p.TenantID, id, periodStart, periodEnd, opening, expected, counted, note).Scan(&rid); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "Petty cash is already reconciled for that date")
			}
			return err
		}
		res := map[string]any{"id": rid, "account_id": id, "period_start": periodStart, "period_end": periodEnd,
			"opening_balance": opening, "expected_closing": expected, "counted_closing": counted, "difference": diff}
		if err := s.Audit.Record(ctx, tx, p, "petty_cash.reconciled", "petty_cash_reconciliations", rid, nil, res); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(res))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
