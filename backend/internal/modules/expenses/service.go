package expenses

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
)

type Service struct {
	DB       *db.DB
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Clock: clk}
}

type Input struct {
	CategoryID  uuid.UUID       `json:"category_id"`
	Amount      decimal.Decimal `json:"amount"`
	Description string          `json:"description"`
	Payee       string          `json:"payee"`
	Date        string          `json:"date"`
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if in.Amount.LessThanOrEqual(money.Zero) || in.Description == "" || in.CategoryID == uuid.Nil {
		return 0, nil, httpx.BadRequest("invalid_body", "Category, description and a positive amount are required")
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
		number, err := sequence.Next(ctx, tx, *p.BranchID, "expense")
		if err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			date = s.Clock.Now().Format("2006-01-02")
		}
		amount := money.Money(in.Amount)
		g, ok := p.Can("expenses.create")
		state := "approved"
		if !auth.WithinLimit(g, ok, amount) {
			state = "submitted"
		}
		var approvedBy any
		if state == "approved" {
			approvedBy = p.UserID
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO expenses (id, business_id, branch_id, number, category_id, expense_date, amount, description, payee, status, submitted_by, approved_by, approved_at)
			VALUES (gen_random_uuid(), $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, CASE WHEN $11::uuid IS NULL THEN NULL ELSE now() END)
			RETURNING id
		`, p.BusinessID, *p.BranchID, number, in.CategoryID, date, amount, in.Description, in.Payee, state, p.UserID, approvedBy).Scan(&id); err != nil {
			return err
		}
		if state == "submitted" {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: p.BranchID, Kind: "expense", SubjectType: "expense", SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: amount, Valid: true}, Summary: "Expense " + number + " " + in.Description,
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"id": id, "number": number, "approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "status": "approved", "amount": amount}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) OnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	_, err := tx.Exec(ctx, `UPDATE expenses SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1 AND status='submitted'`, req.SubjectID, p.UserID)
	return err
}

func (s *Service) OnRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	_, err := tx.Exec(ctx, `UPDATE expenses SET status='rejected', reject_reason=$2 WHERE id=$1 AND status='submitted'`, req.SubjectID, req.Summary)
	return err
}

func (s *Service) Pay(ctx context.Context, p *auth.Principal, id, account uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var amount decimal.Decimal
		var st, date, desc string
		var branch, category uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT amount, status, expense_date::text, branch_id, category_id, description FROM expenses WHERE id=$1`, id).Scan(&amount, &st, &date, &branch, &category, &desc); err != nil {
			return httpx.NotFound("not_found", "Expense not found")
		}
		if st != "approved" {
			return httpx.Conflict("invalid_status", "Only an approved expense can be paid")
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, account, amount); err != nil {
			return err
		}
		var code string
		if err := tx.QueryRow(ctx, `SELECT la.code FROM expense_categories ec JOIN ledger_accounts la ON la.id=ec.ledger_account_id WHERE ec.id=$1`, category).Scan(&code); err != nil {
			return err
		}
		moneyCode, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, account)
		if err != nil {
			return err
		}
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: account, UserID: p.UserID,
			Direction: "out", Kind: "expense", Method: "cash", Amount: amount, Date: date, Note: desc,
			AllocType: "expense", AllocID: id, AllocAmount: amount,
		})
		if err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "expense", id, "Expense "+desc, &user, []ledger.Line{
			{Code: code, BranchID: &branch, Debit: amount},
			{Code: moneyCode, BranchID: &branch, MoneyAccountID: &account, Credit: amount},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE expenses SET status='paid', money_account_id=$2, payment_id=$3, paid_at=now() WHERE id=$1`, id, account, res.ID); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": "paid", "payment_id": res.ID}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name FROM expense_categories WHERE is_active ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var number, status, desc, date string
		var amount decimal.Decimal
		var submitter uuid.UUID
		q := `SELECT number, status, amount, description, expense_date::text, submitted_by FROM expenses WHERE id=$1`
		args := []any{id}
		if g, ok := p.Can("expenses.view"); ok && g.Scope == "own" && !p.IsOwner {
			q += ` AND submitted_by=$2`
			args = append(args, p.UserID)
		}
		if err := tx.QueryRow(ctx, q, args...).Scan(&number, &status, &amount, &desc, &date, &submitter); err != nil {
			return httpx.NotFound("not_found", "Expense not found")
		}
		out = map[string]any{"id": id, "number": number, "status": status, "amount": amount, "description": desc, "date": date, "submitted_by": submitter}
		return nil
	})
	return out, err
}
