package expenses

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
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
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Notify: n, Clock: clk}
}

type Input struct {
	CategoryID     uuid.UUID       `json:"category_id" validate:"required"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Description    string          `json:"description" validate:"required,max=500"`
	Payee          string          `json:"payee" validate:"max=200"`
	Date           string          `json:"date" validate:"date"`
	ReceiptFile    string          `json:"receipt_file" validate:"max=500"`
	MoneyAccountID *uuid.UUID      `json:"money_account_id"`
}

// bizDate is today's business date in the business timezone and day start (BR-15).
func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func (s *Service) categoryOK(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM expense_categories WHERE id=$1`, id).Scan(&active); err != nil || !active {
		return httpx.NotFound("not_found", "Expense category not found")
	}
	return nil
}

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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "expense")
		if err != nil {
			return err
		}
		amount := money.Money(in.Amount)
		g, ok := p.Can("expenses.create")
		state := "approved"
		if !p.IsOwner && !auth.WithinLimit(g, ok, amount) {
			state = "submitted"
		}
		var approvedBy any
		if state == "approved" {
			approvedBy = p.UserID
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO expenses (id, business_id, branch_id, number, category_id, expense_date, amount, description, payee, status, receipt_file, submitted_by, approved_by, approved_at)
			VALUES (gen_random_uuid(), $1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,NULLIF($10,''),$11,$12, CASE WHEN $12::uuid IS NULL THEN NULL ELSE now() END)
			RETURNING id
		`, p.BusinessID, *p.BranchID, number, in.CategoryID, date, amount, in.Description, in.Payee, state, in.ReceiptFile, p.UserID, approvedBy).Scan(&id); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "expense.created", "expense", id, nil, map[string]any{"number": number, "amount": amount, "status": state}); err != nil {
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
		out := map[string]any{"id": id, "number": number, "status": "approved", "amount": amount}
		if in.MoneyAccountID != nil {
			if _, ok := p.Can("expenses.pay"); !ok && !p.IsOwner {
				return httpx.Forbidden("permission_denied", "You cannot pay expenses")
			}
			pid, err := s.pay(ctx, tx, p, id, *in.MoneyAccountID)
			if err != nil {
				return err
			}
			out["status"], out["payment_id"] = "paid", pid
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) OnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	tag, err := tx.Exec(ctx, `UPDATE expenses SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1 AND status='submitted'`, req.SubjectID, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.Conflict("invalid_status", "This expense is no longer waiting for approval")
	}
	return s.Audit.Record(ctx, tx, p, "expense.approved", "expense", req.SubjectID, nil, map[string]any{"note": req.Note})
}

// OnRejected handles both a rejection and a requester cancelling the request.
func (s *Service) OnRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	reason := req.Note
	if req.Status == "cancelled" {
		reason = "Cancelled by the requester"
	}
	if _, err := tx.Exec(ctx, `UPDATE expenses SET status='rejected', reject_reason=NULLIF($2,'') WHERE id=$1 AND status='submitted'`, req.SubjectID, reason); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "expense."+req.Status, "expense", req.SubjectID, nil, map[string]any{"note": reason})
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
		pid, err := s.pay(ctx, tx, p, id, account)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": "paid", "payment_id": pid}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// pay records the payment and journal for an approved expense (FR-EXP-04).
func (s *Service) pay(ctx context.Context, tx pgx.Tx, p *auth.Principal, id, accountID uuid.UUID) (uuid.UUID, error) {
	var amount decimal.Decimal
	var st, desc, number string
	var branch, category uuid.UUID
	err := tx.QueryRow(ctx, `SELECT amount, status, branch_id, category_id, description, number FROM expenses WHERE id=$1 FOR UPDATE`, id).Scan(&amount, &st, &branch, &category, &desc, &number)
	if err != nil || !p.SeesBranch("expenses.pay", branch) {
		return uuid.Nil, httpx.NotFound("not_found", "Expense not found")
	}
	if st != "approved" {
		return uuid.Nil, httpx.Conflict("invalid_status", "Only an approved expense can be paid")
	}
	acc, err := payments.ActiveAccount(ctx, tx, accountID)
	if err != nil {
		return uuid.Nil, err
	}
	if acc.BranchID != nil && !p.SeesBranch("expenses.pay", *acc.BranchID) {
		return uuid.Nil, httpx.NotFound("not_found", "Money account not found")
	}
	if err := s.Ledger.EnsureFunds(ctx, tx, accountID, amount); err != nil {
		return uuid.Nil, err
	}
	if acc.Type == "petty_cash" {
		// FR-EXP-07: petty cash never goes below zero, whatever the overdraft flag says.
		bal, err := s.Ledger.MoneyBalance(ctx, tx, accountID)
		if err != nil {
			return uuid.Nil, err
		}
		if bal.LessThan(amount) {
			return uuid.Nil, httpx.Conflict("insufficient_funds", "Petty cash does not have enough money for this expense")
		}
	}
	var code string
	if err := tx.QueryRow(ctx, `SELECT la.code FROM expense_categories ec JOIN ledger_accounts la ON la.id=ec.ledger_account_id WHERE ec.id=$1`, category).Scan(&code); err != nil {
		return uuid.Nil, err
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return uuid.Nil, err
	}
	res, err := payments.Insert(ctx, tx, payments.Input{
		BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: accountID, UserID: p.UserID,
		Direction: "out", Kind: "expense", Method: payments.MethodFor(acc.Type), Amount: amount, Date: date, Note: desc,
		AllocType: "expense", AllocID: id, AllocAmount: amount,
	})
	if err != nil {
		return uuid.Nil, err
	}
	user := p.UserID
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "expense", id, "Expense "+number+" "+desc, &user, []ledger.Line{
		{Code: code, BranchID: &branch, Debit: amount},
		{Code: acc.Code, BranchID: &branch, MoneyAccountID: &accountID, Credit: amount},
	}); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE expenses SET status='paid', money_account_id=$2, payment_id=$3, paid_at=now() WHERE id=$1`, id, accountID, res.ID); err != nil {
		return uuid.Nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "expense.paid", "expense", id, map[string]any{"status": "approved"}, map[string]any{"status": "paid", "payment_id": res.ID, "money_account_id": accountID}); err != nil {
		return uuid.Nil, err
	}
	return res.ID, nil
}

// Reverse undoes a paid expense: the journal is mirrored and the payment voided (FR-EXP-05).
func (s *Service) Reverse(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, nil, httpx.Validation(errors.New("reason"))
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
		var st, number string
		var branch uuid.UUID
		var paymentID *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, number, branch_id, payment_id FROM expenses WHERE id=$1 FOR UPDATE`, id).Scan(&st, &number, &branch, &paymentID); err != nil || !p.SeesBranch("expenses.reverse", branch) {
			return httpx.NotFound("not_found", "Expense not found")
		}
		if st != "paid" {
			return httpx.Conflict("invalid_status", "Only a paid expense can be reversed")
		}
		if paymentID != nil {
			var payDate string
			if err := tx.QueryRow(ctx, `SELECT payment_date::text FROM payments WHERE id=$1`, *paymentID).Scan(&payDate); err != nil {
				return err
			}
			if err := s.Ledger.EnsureOpen(ctx, tx, payDate); err != nil {
				return err
			}
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		user := p.UserID
		if err := s.Ledger.Reverse(ctx, tx, p.BusinessID, "expense", id, date, "Reversal of expense "+number+": "+reason, &user); err != nil {
			return err
		}
		if paymentID != nil {
			if _, err := tx.Exec(ctx, `UPDATE payments SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3 WHERE id=$1 AND status='posted'`, *paymentID, p.UserID, reason); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE expenses SET status='reversed', reversed_at=now(), reversed_by=$2, reverse_reason=$3 WHERE id=$1`, id, p.UserID, reason); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "expense.reversed", "expense", id, map[string]any{"status": "paid"}, map[string]any{"status": "reversed", "reason": reason}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": number, "status": "reversed"}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// Update edits a submitted expense. Only the submitter (or the Owner) may edit it.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in Input) (map[string]any, error) {
	in.Description = strings.TrimSpace(in.Description)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.get(ctx, tx, p, id, "expenses.create")
		if err != nil {
			return err
		}
		if before["status"] != "submitted" {
			return httpx.Conflict("invalid_status", "Only a submitted expense can be edited")
		}
		if before["submitted_by"] != p.UserID && !p.IsOwner {
			return httpx.Forbidden("permission_denied", "Only the person who submitted this expense can edit it")
		}
		if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			date = before["expense_date"].(string)
		}
		amount := money.Money(in.Amount)
		if _, err := tx.Exec(ctx, `
			UPDATE expenses SET category_id=$2, amount=$3, description=$4, payee=NULLIF($5,''), expense_date=$6, receipt_file=COALESCE(NULLIF($7,''), receipt_file)
			WHERE id=$1
		`, id, in.CategoryID, amount, in.Description, in.Payee, date, in.ReceiptFile); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE approval_requests SET amount=$2, summary=$3 WHERE subject_type='expense' AND subject_id=$1 AND status='pending'`,
			id, amount, "Expense "+before["number"].(string)+" "+in.Description); err != nil {
			return err
		}
		out, err = s.get(ctx, tx, p, id, "expenses.create")
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense.updated", "expense", id, before, out)
	})
	return out, err
}

// Decide approves or rejects the expense's pending approval request. The
// expenses.approve grant and its limit stand in for approvals.decide, so the
// request and the expense change together (FR-EXP-03, FR-APR-03).
func (s *Service) Decide(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		cur, err := s.get(ctx, tx, p, id, "expenses.approve")
		if err != nil {
			return err
		}
		reqID, err := s.Approval.PendingFor(ctx, tx, "expense", id)
		if err != nil {
			return err
		}
		if reqID == nil || cur["status"] != "submitted" {
			return httpx.Conflict("invalid_status", "This expense is not waiting for approval")
		}
		decider := *p
		if !p.IsOwner {
			g, _ := p.Can("expenses.approve")
			decider.Perms = make(map[string]auth.Grant, len(p.Perms)+1)
			for k, v := range p.Perms {
				decider.Perms[k] = v
			}
			decider.Perms["approvals.decide"] = g
		}
		if err := s.Approval.Decide(ctx, tx, &decider, *reqID, approve, note); err != nil {
			return err
		}
		out, err = s.get(ctx, tx, p, id, "expenses.approve")
		return err
	})
	return out, err
}

const expenseCols = `e.id, e.number, e.branch_id, e.category_id, ec.name, e.expense_date::text, e.amount, e.description, COALESCE(e.payee,''),
	e.status, e.money_account_id, e.payment_id, COALESCE(e.receipt_file,''), e.recurring_id, e.submitted_by, COALESCE(u.name,''),
	e.approved_by, e.approved_at, e.paid_at, COALESCE(e.reject_reason,''), e.reversed_at, COALESCE(e.reverse_reason,''), e.created_at`

const expenseFrom = ` FROM expenses e JOIN expense_categories ec ON ec.id=e.category_id LEFT JOIN users u ON u.id=e.submitted_by`

func scanExpense(row pgx.Row) (map[string]any, error) {
	var id, branch, category, submitter uuid.UUID
	var number, catName, date, desc, payee, status, receipt, submitterName, reject, reverseReason string
	var amount decimal.Decimal
	var account, payment, recurring, approvedBy *uuid.UUID
	var approvedAt, paidAt, reversedAt *time.Time
	var created time.Time
	if err := row.Scan(&id, &number, &branch, &category, &catName, &date, &amount, &desc, &payee, &status, &account, &payment, &receipt, &recurring,
		&submitter, &submitterName, &approvedBy, &approvedAt, &paidAt, &reject, &reversedAt, &reverseReason, &created); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "number": number, "branch_id": branch, "category_id": category, "category_name": catName,
		"date": date, "expense_date": date, "amount": amount, "description": desc, "payee": payee, "status": status,
		"money_account_id": account, "payment_id": payment, "receipt_file": receipt, "recurring_id": recurring,
		"submitted_by": submitter, "submitted_by_name": submitterName, "approved_by": approvedBy, "approved_at": approvedAt,
		"paid_at": paidAt, "reject_reason": reject, "reversed_at": reversedAt, "reverse_reason": reverseReason, "created_at": created,
	}, nil
}

// get loads one expense inside the caller's scope for perm; staff with own
// scope on expenses.view only see their own.
func (s *Service) get(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, perm string) (map[string]any, error) {
	out, err := scanExpense(tx.QueryRow(ctx, `SELECT `+expenseCols+expenseFrom+` WHERE e.id=$1`, id))
	if err != nil {
		return nil, httpx.NotFound("not_found", "Expense not found")
	}
	if !p.SeesBranch(perm, out["branch_id"].(uuid.UUID)) && !p.SeesBranch("expenses.view", out["branch_id"].(uuid.UUID)) {
		return nil, httpx.NotFound("not_found", "Expense not found")
	}
	if own := p.OwnOnly("expenses.view"); own != nil && out["submitted_by"] != *own {
		if _, ok := p.Can("expenses.approve"); !ok {
			return nil, httpx.NotFound("not_found", "Expense not found")
		}
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.get(ctx, tx, p, id, "expenses.view")
		if err != nil {
			return err
		}
		reqID, err := s.Approval.PendingFor(ctx, tx, "expense", id)
		if err != nil {
			return err
		}
		out["approval_request_id"] = reqID
		return nil
	})
	return out, err
}

type ListFilter struct {
	Status      string
	CategoryID  *uuid.UUID
	SubmittedBy *uuid.UUID
	From, To    string
	Q           string
	Sort        string
}

func (s *Service) List(ctx context.Context, p *auth.Principal, f ListFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("expenses.view"); b != nil {
			q.Add("e.branch_id = ANY(?)", b)
		}
		if own := p.OwnOnly("expenses.view"); own != nil {
			q.Add("e.submitted_by = ?", *own)
		}
		q.If(f.Status != "", "e.status = ?", f.Status)
		q.If(f.CategoryID != nil, "e.category_id = ?", f.CategoryID)
		q.If(f.SubmittedBy != nil, "e.submitted_by = ?", f.SubmittedBy)
		q.If(f.From != "", "e.expense_date >= ?::date", f.From)
		q.If(f.To != "", "e.expense_date <= ?::date", f.To)
		q.If(f.Q != "", "(e.description ILIKE ? OR e.payee ILIKE ? OR e.number ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%", "%"+f.Q+"%")
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
