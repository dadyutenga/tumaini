package expenses

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Approval: ap, Notify: n, Clock: clk}
}

type Input struct {
	CategoryID     uuid.UUID       `json:"category_id" validate:"required"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Description    string          `json:"description" validate:"required,max=200"`
	Date           string          `json:"date" validate:"date"`
	ProjectID      *uuid.UUID      `json:"project_id,omitempty"`
	DesignJobID    *uuid.UUID      `json:"design_job_id,omitempty"`
	ReceiptFileID  *uuid.UUID      `json:"receipt_file_id,omitempty"`
	MoneyAccountID *uuid.UUID      `json:"money_account_id,omitempty"`
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func (s *Service) categoryOK(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var active bool
	if err := tx.QueryRow(ctx, "SELECT status = 'active' FROM app.expense_categories WHERE id = $1", id).Scan(&active); err != nil || !active {
		return httpx.NotFound("not_found", "Expense category not found")
	}
	return nil
}

// Create submits a new expense in 'submitted' status. Separation of duties on app.expenses requires
// another person with finance.expense.approve (within their expense_approve_amount limit) to approve it.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	in.Description = strings.TrimSpace(in.Description)
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
			return err
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "expense")
		if err != nil {
			return err
		}
		amount := money.Money(in.Amount)
		var id uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.expenses ( expense_no, category_id, description, amount, expense_date,\n\t\t\t                          business_unit_id, location_id, project_id, design_job_id, account_id, receipt_file_id, status)\n\t\t\tVALUES ($1,$2,$3,$4,$5::date,\n\t\t\t        (SELECT business_unit_id FROM app.locations WHERE id = $6), $6, $7, $8, $9, $10, 'submitted')\n\t\t\tRETURNING id", number, in.CategoryID, in.Description, amount, date, *p.BranchID,
			in.ProjectID, in.DesignJobID, in.MoneyAccountID, in.ReceiptFileID).Scan(&id); err != nil {
			return err
		}
		amtCopy := amount
		_ = s.Notify.ToApprovers(ctx, tx, p.BranchID, "finance.expense.approve", &amtCopy, p.UserID, notify.Message{
			Type: "approval_waiting", Title: "Expense awaiting approval",
			Body: "Expense " + number + ": " + in.Description,
			Link: "/expenses/" + id.String(), EntityType: "expense", EntityID: &id,
		})
		if err := s.Audit.Record(ctx, tx, p, "expense.created", "expense", id, nil, map[string]any{"number": number, "amount": amount, "status": "submitted"}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(map[string]any{"id": id, "number": number, "status": "submitted", "amount": amount}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

// Update edits a submitted expense. Only the requester (or an approver) may edit it before a decision.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in Input) (map[string]any, error) {
	in.Description = strings.TrimSpace(in.Description)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.get(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if before["status"] != "submitted" {
			return httpx.Conflict("invalid_status", "Only a submitted expense can be edited")
		}
		if before["requested_by"] != p.MembershipID && !p.Has("finance.expense.approve") {
			return httpx.Forbidden("permission_denied", "Only the person who submitted this expense can edit it")
		}
		if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
			return err
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			date = before["expense_date"].(string)
		}
		amount := money.Money(in.Amount)
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.expenses\n\t\t\tSET category_id = $2, amount = $3, description = $4, expense_date = $5::date,\n\t\t\t    project_id = $6, design_job_id = $7, account_id = COALESCE($8, account_id),\n\t\t\t    receipt_file_id = COALESCE($9, receipt_file_id)\n\t\t\tWHERE id = $1", id, in.CategoryID, amount, in.Description, date,
			in.ProjectID, in.DesignJobID, in.MoneyAccountID, in.ReceiptFileID); err != nil {
			return err
		}
		out, err = s.get(ctx, tx, p, id)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense.updated", "expense", id, before, out)
	})
	return out, err
}

// Decide approves or rejects a submitted expense. The database trigger tg_expense_guard enforces
// finance.expense.approve and the approver's expense_approve_amount limit, and the table CHECK
// enforces decided_by <> requested_by (except for recurring expenses).
func (s *Service) Decide(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (map[string]any, error) {
	note = strings.TrimSpace(note)
	if !approve && note == "" {
		return nil, httpx.BadRequest("note_required", "Give a reason for rejecting this expense")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var st string
		var requestedBy uuid.UUID
		var recurring *uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT status::text, requested_by, recurring_expense_id\n\t\t\tFROM app.expenses WHERE id = $1 FOR UPDATE", id).Scan(&st, &requestedBy, &recurring); err != nil {
			return httpx.NotFound("not_found", "Expense not found")
		}
		if st != "submitted" {
			return httpx.Conflict("invalid_status", "This expense is not waiting for approval")
		}
		if requestedBy == p.MembershipID && recurring == nil {
			return httpx.Forbidden("cannot_approve_own", "You cannot decide your own expense request")
		}
		next := "rejected"
		if approve {
			next = "approved"
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.expenses SET status = $2::app.expense_status, rejection_reason = CASE WHEN $2 = 'rejected' THEN $3 ELSE rejection_reason END\n\t\t\tWHERE id = $1", id, next, note); err != nil {
			return err
		}
		var reqUser uuid.UUID
		if tx.QueryRow(ctx, "SELECT user_id FROM app.memberships WHERE id = $1", requestedBy).Scan(&reqUser) == nil {
			_ = s.Notify.ToUser(ctx, tx, reqUser, notify.Message{
				Type: "approval_decided", Title: "Expense " + next, Body: "Your expense request was " + next,
				Link: "/expenses/" + id.String(), EntityType: "expense", EntityID: &id,
			})
		}
		if err := s.Audit.Record(ctx, tx, p, "expense."+next, "expense", id, map[string]any{"status": "submitted"}, map[string]any{"status": next, "note": note}); err != nil {
			return err
		}
		var err error
		out, err = s.get(ctx, tx, p, id)
		return err
	})
	return out, err
}

// Cancel cancels a submitted or approved expense before it is paid.
func (s *Service) Cancel(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var st string
		var requestedBy uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT status::text, requested_by FROM app.expenses WHERE id = $1 FOR UPDATE", id).Scan(&st, &requestedBy); err != nil {
			return httpx.NotFound("not_found", "Expense not found")
		}
		if st != "submitted" && st != "approved" {
			return httpx.Conflict("invalid_status", "This expense cannot be cancelled")
		}
		if requestedBy != p.MembershipID && !p.Has("finance.expense.approve") {
			return httpx.Forbidden("permission_denied", "You cannot cancel another person's expense")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.expenses SET status = 'cancelled' WHERE id = $1", id); err != nil {
			return err
		}
		out = map[string]any{"id": id, "status": "cancelled"}
		return s.Audit.Record(ctx, tx, p, "expense.cancelled", "expense", id, map[string]any{"status": st}, out)
	})
	return out, err
}

// Pay marks an approved expense paid from accountID. The database trigger tg_expense_post writes
// the outflow to app.financial_transactions and updates the account balance.
func (s *Service) Pay(ctx context.Context, p *auth.Principal, id, accountID uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var amount decimal.Decimal
		var st string
		if err := tx.QueryRow(ctx, "SELECT amount, status::text FROM app.expenses WHERE id = $1 FOR UPDATE", id).Scan(&amount, &st); err != nil {
			return httpx.NotFound("not_found", "Expense not found")
		}
		if st != "approved" {
			return httpx.Conflict("invalid_status", "Only an approved expense can be paid")
		}
		acc, err := payments.ActiveFinForUpdate(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if (acc.Kind == "petty_cash" || !acc.AllowNegative) && acc.Balance.LessThan(amount) {
			return httpx.Conflict("insufficient_funds", "That account does not have enough funds for this expense")
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.expenses SET status = 'paid', account_id = $2, paid_business_date = $3::date, paid_at = now()\n\t\t\tWHERE id = $1", id, accountID, date); err != nil {
			return err
		}
		out := map[string]any{"id": id, "status": "paid", "money_account_id": accountID, "paid_business_date": date}
		if err := s.Audit.Record(ctx, tx, p, "expense.paid", "expense", id, map[string]any{"status": "approved"}, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// Reverse is not a state on app.expenses in the approved model: mistakes on paid expenses are
// fixed with an approved transaction correction.
func (s *Service) Reverse(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	return 0, nil, &httpx.Error{Status: 410, Code: "feature_removed", Message: "Reversing a paid expense is not part of the approved model; use a transaction correction instead"}
}

const expenseCols = `e.id, e.expense_no, e.location_id, COALESCE(l.name::text, ''), e.category_id, ec.name::text, e.expense_date::text, e.amount, e.description::text,
	e.status::text, e.account_id, e.receipt_file_id, e.recurring_expense_id, e.project_id, e.design_job_id,
	e.requested_by, COALESCE(m.user_id, e.requested_by), COALESCE(u.full_name::text, ''),
	e.decided_by, e.decided_at, e.paid_at, e.paid_business_date::text, COALESCE(e.rejection_reason, ''), e.created_at`

const expenseFrom = ` FROM app.expenses e
	LEFT JOIN app.locations l ON l.id = e.location_id
	JOIN app.expense_categories ec ON ec.id = e.category_id
	LEFT JOIN app.memberships m ON m.id = e.requested_by
	LEFT JOIN iam.users u ON u.id = m.user_id`

func scanExpense(row pgx.Row) (map[string]any, error) {
	var id, branch, category, requestedBy, submitterUser uuid.UUID
	var number, locationName, catName, date, desc, status, submitterName, reject string
	var amount decimal.Decimal
	var account, receiptFile, recurring, project, designJob, decidedBy *uuid.UUID
	var decidedAt, paidAt *time.Time
	var paidDate *string
	var created time.Time
	if err := row.Scan(&id, &number, &branch, &locationName, &category, &catName, &date, &amount, &desc, &status, &account,
		&receiptFile, &recurring, &project, &designJob, &requestedBy, &submitterUser, &submitterName,
		&decidedBy, &decidedAt, &paidAt, &paidDate, &reject, &created); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "number": number, "branch_id": branch, "location_name": locationName, "category_id": category, "category_name": catName,
		"date": date, "expense_date": date, "amount": amount, "description": desc, "status": status,
		"money_account_id": account, "receipt_file_id": receiptFile, "recurring_id": recurring,
		"project_id": project, "design_job_id": designJob,
		"requested_by": requestedBy, "submitted_by": submitterUser, "submitted_by_name": submitterName,
		"approved_by": decidedBy, "approved_at": decidedAt, "paid_at": paidAt, "paid_business_date": paidDate,
		"reject_reason": reject, "created_at": created,
	}, nil
}

func canViewAllExpenses(p *auth.Principal) bool {
	return p.Has("finance.expense.approve") || p.Has("finance.expense.pay") || p.Has("finance.expense.enter") || p.Has("finance.transaction.view")
}

func (s *Service) get(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	out, err := scanExpense(tx.QueryRow(ctx, `SELECT `+expenseCols+expenseFrom+` WHERE e.id = $1`, id))
	if err != nil {
		return nil, httpx.NotFound("not_found", "Expense not found")
	}
	if !canViewAllExpenses(p) && out["requested_by"] != p.MembershipID {
		return nil, httpx.NotFound("not_found", "Expense not found")
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.get(ctx, tx, p, id)
		return err
	})
	return out, err
}

type ListFilter struct {
	Status      string
	CategoryID  *uuid.UUID
	SubmittedBy *uuid.UUID
	ProjectID   *uuid.UUID
	LocationID  *uuid.UUID
	From, To    string
	Q           string
	Sort        string
}

func (s *Service) List(ctx context.Context, p *auth.Principal, f ListFilter, page httpx.Page) ([]map[string]any, int, error) {
	if p.BranchID != nil && f.LocationID != nil && *f.LocationID != *p.BranchID {
		return nil, 0, httpx.BadRequest("invalid_query", "That location is not the one selected in the header")
	}
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if p.BranchID != nil {
			q.Add("e.location_id = ?", p.BranchID)
		} else if f.LocationID != nil {
			var active bool
			if err := tx.QueryRow(ctx, "SELECT status = 'active' FROM app.locations WHERE id = $1", *f.LocationID).Scan(&active); err != nil || !active {
				return httpx.NotFound("not_found", "Branch not found")
			}
			q.Add("e.location_id = ?", f.LocationID)
		}
		q.If(!canViewAllExpenses(p), "e.requested_by = ?", p.MembershipID)
		q.If(f.Status != "", "e.status = ?::app.expense_status", f.Status)
		q.If(f.CategoryID != nil, "e.category_id = ?", f.CategoryID)
		q.If(f.SubmittedBy != nil, "(m.user_id = ? OR e.requested_by = ?)", f.SubmittedBy, f.SubmittedBy)
		q.If(f.ProjectID != nil, "e.project_id = ?", f.ProjectID)
		q.If(f.From != "", "e.expense_date >= ?::date", f.From)
		q.If(f.To != "", "e.expense_date <= ?::date", f.To)
		q.If(f.Q != "", "(e.description ILIKE ? OR e.expense_no ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%")
		if err := tx.QueryRow(ctx, `SELECT count(*)`+expenseFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		order := f.Sort
		if order == "" {
			order = "e.expense_date DESC, e.created_at DESC"
		}
		rows, err := tx.Query(ctx, `SELECT `+expenseCols+expenseFrom+q.Where()+` ORDER BY `+order+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanExpense(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, total, err
}
