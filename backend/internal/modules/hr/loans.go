package hr

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type LoanIn struct {
	EmployeeID       uuid.UUID        `json:"employee_id" validate:"required"`
	Kind             string           `json:"kind" validate:"omitempty,oneof=loan advance"`
	Principal        decimal.Decimal  `json:"principal" validate:"dpos"`
	MonthlyDeduction *decimal.Decimal `json:"monthly_deduction"`
	StartPeriod      string           `json:"start_period" validate:"date"`
	Reason           string           `json:"reason"`
}

const loanSQL = `SELECT l.id, l.number, l.employee_id, e.name AS employee_name, e.branch_id AS employee_branch_id, l.branch_id, l.kind,
	l.principal, l.monthly_deduction, l.balance, l.principal - l.balance AS repaid, l.start_period, l.status, l.payment_id,
	l.approved_by, l.created_by, l.created_at
	FROM employee_loans l JOIN employees e ON e.id = l.employee_id`

type LoanFilter struct {
	EmployeeID *uuid.UUID
	Status     string
	Kind       string
}

func (s *Service) Loans(ctx context.Context, p *auth.Principal, f LoanFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("loans.view"); br != nil {
			q.Add("e.branch_id = ANY(?)", br)
		}
		q.If(f.EmployeeID != nil, "l.employee_id = ?", f.EmployeeID)
		q.If(f.Status != "", "l.status = ?", f.Status)
		q.If(f.Kind != "", "l.kind = ?", f.Kind)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM employee_loans l JOIN employees e ON e.id = l.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, loanSQL+q.Where()+` ORDER BY l.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) loadLoan(ctx context.Context, tx pgx.Tx, p *auth.Principal, key string, id uuid.UUID, lock bool) (map[string]any, error) {
	if lock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM employee_loans WHERE id=$1 FOR UPDATE`, id); err != nil {
			return nil, err
		}
	}
	l, err := hrkit.One(ctx, tx, "Loan not found", loanSQL+` WHERE l.id=$1`, id)
	if err != nil {
		return nil, err
	}
	branch, _ := uuid.Parse(asString(l["employee_branch_id"]))
	own := p.EmployeeID != nil && asString(l["employee_id"]) == p.EmployeeID.String()
	if key != "" && !own && !p.SeesBranch(key, branch) {
		return nil, httpx.NotFound("not_found", "Loan not found")
	}
	return l, nil
}

// GetLoan returns the loan with its repayment schedule and repayments.
func (s *Service) GetLoan(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		l, err := s.loadLoan(ctx, tx, p, "loans.view", id, false)
		if err != nil {
			return err
		}
		reps, err := hrkit.All(ctx, tx, `
			SELECT lr.id, lr.amount, lr.source, lr.payslip_id, ps.number AS payslip_number, lr.payment_id, lr.created_at, lr.voided_at
			FROM loan_repayments lr LEFT JOIN payslips ps ON ps.id = lr.payslip_id
			WHERE lr.loan_id = $1 ORDER BY lr.created_at
		`, id)
		if err != nil {
			return err
		}
		l["repayments"] = reps
		l["schedule"] = schedule(l)
		out = l
		return nil
	})
	return out, err
}

// schedule projects the remaining monthly deductions from the next period.
func schedule(l map[string]any) []map[string]any {
	bal, _ := decimal.NewFromString(asString(l["balance"]))
	monthly, _ := decimal.NewFromString(asString(l["monthly_deduction"]))
	start := hrkit.MustDate(asString(l["start_period"]))
	out := []map[string]any{}
	if !monthly.IsPositive() {
		return out
	}
	for i := 0; bal.IsPositive() && i < 600; i++ {
		amt := decimal.Min(monthly, bal)
		bal = bal.Sub(amt)
		out = append(out, map[string]any{"period": start.AddDate(0, i, 0).Format("2006-01"), "amount": amt.StringFixed(2), "balance_after": bal.StringFixed(2)})
	}
	return out
}

// CreateLoan records a loan or advance. Within the creator's loans.create
// limit it is approved at once (201); otherwise it waits for approval (202).
func (s *Service) CreateLoan(ctx context.Context, p *auth.Principal, in LoanIn) (int, []byte, error) {
	if in.Kind == "" {
		in.Kind = "loan"
	}
	principal := money.Money(in.Principal)
	monthly := principal
	if in.MonthlyDeduction != nil {
		monthly = money.Money(*in.MonthlyDeduction)
	}
	if in.Kind == "advance" {
		monthly = principal
	}
	if !monthly.IsPositive() || monthly.GreaterThan(principal) {
		return 0, nil, httpx.BadRequest("invalid_body", "Monthly deduction must be positive and not more than the principal")
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		emp, err := s.loadEmployee(ctx, tx, p, "loans.create", in.EmployeeID)
		if err != nil {
			return err
		}
		if emp["status"] != "active" {
			return httpx.Conflict("invalid_status", "Only an active employee can take a loan")
		}
		branch, _ := uuid.Parse(asString(emp["branch_id"]))
		start := in.StartPeriod
		if start == "" {
			start = s.today(ctx, tx)
		}
		t := hrkit.MustDate(start)
		start = t.AddDate(0, 0, 1-t.Day()).Format(hrkit.DateFmt)
		g, ok := p.Can("loans.create")
		approved := p.IsOwner || auth.WithinLimit(g, ok, principal)
		state := "pending"
		var approvedBy any
		if approved {
			state, approvedBy = "approved", p.UserID
		}
		number, err := sequence.Next(ctx, tx, branch, "loan")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO employee_loans (id, business_id, branch_id, number, employee_id, kind, principal, monthly_deduction, balance, start_period, status, approved_by, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$7,$9,$10,$11,$12)
		`, id, p.BusinessID, branch, number, in.EmployeeID, in.Kind, principal, monthly, start, state, approvedBy, p.UserID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "loan.created", "loan", id, nil, map[string]any{"number": number, "principal": principal, "status": state, "reason": in.Reason}); err != nil {
			return err
		}
		if !approved {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &branch, Kind: "loan", SubjectType: "loan", SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: principal, Valid: true}, Summary: "Loan " + number + " for " + asString(emp["name"]),
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"id": id, "number": number, "approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		l, err := s.loadLoan(ctx, tx, p, "", id, false)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(l))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) onLoanDecided(approve bool) func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
		l, err := s.loadLoan(ctx, tx, p, "", req.SubjectID, true)
		if err != nil {
			return err
		}
		if l["status"] != "pending" {
			return httpx.Conflict("invalid_status", "This loan is no longer pending")
		}
		state := "rejected"
		var by any
		if approve {
			state, by = "approved", p.UserID
		}
		if _, err := tx.Exec(ctx, `UPDATE employee_loans SET status=$2, approved_by=$3 WHERE id=$1`, req.SubjectID, state, by); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "loan."+state, "loan", req.SubjectID, map[string]any{"status": "pending"}, map[string]any{"status": state})
	}
}

// Disburse pays out an approved loan from a money account.
func (s *Service) Disburse(ctx context.Context, p *auth.Principal, loanID, account uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		l, err := s.loadLoan(ctx, tx, p, "loans.approve", loanID, true)
		if err != nil {
			return err
		}
		if l["status"] != "approved" {
			return httpx.Conflict("invalid_status", "This loan is not waiting to be paid out")
		}
		g, ok := p.Can("loans.approve")
		amount, _ := decimal.NewFromString(asString(l["principal"]))
		if !p.IsOwner && !auth.WithinLimit(g, ok, amount) {
			return httpx.Forbidden("over_limit", "This loan is above your limit")
		}
		method, err := hrkit.MethodFor(ctx, tx, p, "loans.approve", account)
		if err != nil {
			return err
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, account, amount); err != nil {
			return err
		}
		employee, _ := uuid.Parse(asString(l["employee_id"]))
		branch, _ := uuid.Parse(asString(l["branch_id"]))
		date := s.today(ctx, tx)
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: account, UserID: p.UserID,
			Direction: "out", Kind: "loan_disbursement", Method: method, PartyType: "employee", PartyID: &employee,
			Amount: amount, Date: date, AllocType: "loan", AllocID: loanID, AllocAmount: amount,
		})
		if err != nil {
			return err
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, account)
		if err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "loan", loanID, "Staff "+asString(l["kind"])+" "+asString(l["number"]), &user, []ledger.Line{
			{Code: "1300", BranchID: &branch, PartyType: "employee", PartyID: &employee, Debit: amount},
			{Code: code, BranchID: &branch, MoneyAccountID: &account, Credit: amount},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE employee_loans SET status='disbursed', payment_id=$2 WHERE id=$1`, loanID, res.ID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "loan.disbursed", "loan", loanID, map[string]any{"status": "approved"}, map[string]any{"status": "disbursed", "payment_id": res.ID}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": loanID, "status": "disbursed", "payment_id": res.ID, "payment_number": res.Number}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type RepayIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference"`
	Date           string          `json:"date" validate:"date"`
}

// Repay records a direct cash repayment: Dr money account, Cr 1300.
func (s *Service) Repay(ctx context.Context, p *auth.Principal, loanID uuid.UUID, in RepayIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		l, err := s.loadLoan(ctx, tx, p, "payments.receive", loanID, true)
		if err != nil {
			return err
		}
		if l["status"] != "disbursed" {
			return httpx.Conflict("invalid_status", "Only a disbursed loan can be repaid")
		}
		bal, _ := decimal.NewFromString(asString(l["balance"]))
		amount := money.Money(in.Amount)
		if amount.GreaterThan(bal) {
			return httpx.Conflict("invalid_amount", "The repayment is more than the loan balance")
		}
		// Payroll deductions on an approved but unpaid payslip already count
		// against this balance, so keep room for them.
		var reserved decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(pl.amount),0) FROM payslip_lines pl JOIN payslips ps ON ps.id = pl.payslip_id JOIN payroll_runs r ON r.id = ps.run_id
			WHERE pl.loan_id = $1 AND pl.kind = 'loan' AND r.status IN ('approved','paid')
		`, loanID).Scan(&reserved); err != nil {
			return err
		}
		if amount.Add(reserved).GreaterThan(bal) {
			return httpx.Conflict("invalid_amount", "An approved payroll already deducts part of this balance")
		}
		method, err := hrkit.MethodFor(ctx, tx, p, "payments.receive", in.MoneyAccountID)
		if err != nil {
			return err
		}
		employee, _ := uuid.Parse(asString(l["employee_id"]))
		branch, _ := uuid.Parse(asString(l["branch_id"]))
		date := in.Date
		if date == "" {
			date = s.today(ctx, tx)
		}
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
			Direction: "in", Kind: "loan_repayment", Method: method, PartyType: "employee", PartyID: &employee,
			Amount: amount, Reference: in.Reference, Date: date, AllocType: "loan", AllocID: loanID, AllocAmount: amount,
		})
		if err != nil {
			return err
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.MoneyAccountID)
		if err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "loan_repayment", res.ID, "Loan repayment "+asString(l["number"]), &user, []ledger.Line{
			{Code: code, BranchID: &branch, MoneyAccountID: &in.MoneyAccountID, Debit: amount},
			{Code: "1300", BranchID: &branch, PartyType: "employee", PartyID: &employee, Credit: amount},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO loan_repayments (id, business_id, loan_id, amount, source, payment_id) VALUES ($1,$2,$3,$4,'cash',$5)`, ids.New(), p.BusinessID, loanID, amount, res.ID); err != nil {
			return err
		}
		newBal := bal.Sub(amount)
		if _, err := tx.Exec(ctx, `UPDATE employee_loans SET balance=$2::numeric, status=CASE WHEN $2::numeric <= 0 THEN 'settled' ELSE status END WHERE id=$1`, loanID, newBal); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "loan.repaid", "loan", loanID, map[string]any{"balance": bal}, map[string]any{"balance": newBal, "payment_id": res.ID}); err != nil {
			return err
		}
		out, err := s.loadLoan(ctx, tx, p, "", loanID, false)
		if err != nil {
			return err
		}
		out["payment_id"] = res.ID
		out["payment_number"] = res.Number
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
