package hr

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/business"
	"bms/internal/kernel/payments"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type LoanIn struct {
	EmployeeID       uuid.UUID        `json:"employee_id" validate:"required"`
	Kind             string           `json:"kind" validate:"omitempty,oneof=loan advance"`
	Principal        decimal.Decimal  `json:"principal" validate:"dpos"`
	MonthlyDeduction *decimal.Decimal `json:"monthly_deduction"`
	IssuedOn         string           `json:"issued_on" validate:"date"`
	StartPeriod      string           `json:"start_period" validate:"date"` // legacy alias for issued_on
	MoneyAccountID   *uuid.UUID       `json:"money_account_id"`
	Reference        string           `json:"reference" validate:"max=60"`
	Reason           string           `json:"reason" validate:"max=1000"`
}

const loanSQL = `SELECT l.id, l.employee_id, e.full_name::text AS employee_name, e.location_id AS branch_id,
	l.kind::text AS kind, l.principal, l.monthly_deduction, l.amount_repaid AS repaid, l.balance,
	l.issued_on AS start_period, l.status::text AS status, l.payment_made_id AS payment_id,
	l.approved_by, l.notes, l.created_by, l.created_at
	FROM app.employee_advances l
	JOIN app.employees e ON e.id = l.employee_id`

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
		q.If(p.BranchID != nil, "e.location_id = ?", p.BranchID)
		q.If(f.EmployeeID != nil, "l.employee_id = ?", f.EmployeeID)
		q.If(f.Status != "", "l.status = ?::app.advance_status", f.Status)
		q.If(f.Kind != "", "l.kind = ?::app.advance_kind", f.Kind)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.employee_advances l JOIN app.employees e ON e.id = l.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, loanSQL+q.Where()+` ORDER BY l.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) loadLoan(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (map[string]any, error) {
	if lock {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM app.employee_advances WHERE id = $1 FOR UPDATE", id); err != nil {
			return nil, err
		}
	}
	return hrkit.One(ctx, tx, "Loan not found", loanSQL+` WHERE l.id = $1`, id)
}

func (s *Service) GetLoan(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		l, err := s.loadLoan(ctx, tx, id, false)
		if err != nil {
			return err
		}
		reps, err := hrkit.All(ctx, tx, `
			SELECT ar.id, ar.amount, ar.payroll_line_id, ps.payslip_no AS payslip_number, ar.created_at
			FROM app.advance_repayments ar
			LEFT JOIN app.payslips ps ON ps.payroll_line_id = ar.payroll_line_id
			WHERE ar.advance_id = $1 ORDER BY ar.created_at`, id)
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

// CreateLoan records an active advance or loan on app.employee_advances. If money_account_id is
// supplied, the disbursement payment is posted immediately in the same transaction.
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
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		emp, err := s.loadEmployee(ctx, tx, p, in.EmployeeID)
		if err != nil {
			return err
		}
		if emp["status"] != "active" {
			return httpx.Conflict("invalid_status", "Only an active employee can take an advance or loan")
		}
		issuedOn := strings.TrimSpace(in.IssuedOn)
		if issuedOn == "" {
			issuedOn = strings.TrimSpace(in.StartPeriod)
		}
		if issuedOn == "" {
			issuedOn = s.today(ctx, tx)
		}
		var payID *uuid.UUID
		if in.MoneyAccountID != nil {
			locID, err := s.resolveLoanLoc(ctx, tx, p, emp)
			if err != nil {
				return err
			}
			empID := in.EmployeeID
			unit, err := business.EmployeeBusiness(ctx, tx, empID)
			if err != nil {
				return err
			}
			res, err := payments.Pay(ctx, tx, payments.PayInput{
				Business:   unit,
				LocationID: locID,
				AccountID:  *in.MoneyAccountID, EmployeeID: &empID,
				Amount: principal, Reference: strings.TrimSpace(in.Reference), BusinessDate: issuedOn,
				Notes: "Staff " + in.Kind + " for " + asString(emp["name"]),
			})
			if err != nil {
				return err
			}
			payID = &res.ID
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.employee_advances
				( employee_id, kind, principal, issued_on, monthly_deduction, status, approved_by, payment_made_id, notes)
			VALUES ($1,$2::app.advance_kind,$3,$4::date,$5,'active',$6,$7,NULLIF($8,''))
			RETURNING id`, in.EmployeeID, in.Kind, principal, issuedOn, monthly, p.MembershipID, payID, strings.TrimSpace(in.Reason)).Scan(&id); err != nil {
			return err
		}
		l, err := s.loadLoan(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "loan.created", "employee_advances", id, nil, l); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(l))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) resolveLoanLoc(ctx context.Context, tx pgx.Tx, p *auth.Principal, emp map[string]any) (uuid.UUID, error) {
	if b, err := uuid.Parse(asString(emp["branch_id"])); err == nil {
		return b, nil
	}
	if p.BranchID != nil {
		return *p.BranchID, nil
	}
	var loc uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT id FROM app.locations WHERE is_default").Scan(&loc); err != nil {
		return uuid.Nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	return loc, nil
}

// Disburse pays out an active advance/loan that has not been disbursed yet.
func (s *Service) Disburse(ctx context.Context, p *auth.Principal, loanID, account uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		l, err := s.loadLoan(ctx, tx, loanID, true)
		if err != nil {
			return err
		}
		if l["status"] != "active" || l["payment_id"] != nil {
			return httpx.Conflict("invalid_status", "This advance is already disbursed or closed")
		}
		amount, _ := decimal.NewFromString(asString(l["principal"]))
		empID, _ := uuid.Parse(asString(l["employee_id"]))
		locID, err := s.resolveLoanLoc(ctx, tx, p, l)
		if err != nil {
			return err
		}
		date := s.today(ctx, tx)
		unit, err := business.EmployeeBusiness(ctx, tx, empID)
		if err != nil {
			return err
		}
		res, err := payments.Pay(ctx, tx, payments.PayInput{
			Business:   unit,
			LocationID: locID,
			AccountID:  account, EmployeeID: &empID,
			Amount: amount, BusinessDate: date, Notes: "Staff " + asString(l["kind"]) + " disbursement",
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE app.employee_advances SET payment_made_id = $2 WHERE id = $1", loanID, res.ID); err != nil {
			return err
		}
		out := map[string]any{"id": loanID, "status": "active", "payment_id": res.ID, "payment_number": res.Number}
		if err := s.Audit.Record(ctx, tx, p, "loan.disbursed", "employee_advances", loanID, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(out))
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

// Repay records a direct cash repayment outside payroll.
func (s *Service) Repay(ctx context.Context, p *auth.Principal, loanID uuid.UUID, in RepayIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		l, err := s.loadLoan(ctx, tx, loanID, true)
		if err != nil {
			return err
		}
		if l["status"] != "active" {
			return httpx.Conflict("invalid_status", "Only an active advance can be repaid")
		}
		bal, _ := decimal.NewFromString(asString(l["balance"]))
		amount := money.Money(in.Amount)
		if amount.GreaterThan(bal) {
			return httpx.Conflict("invalid_amount", "The repayment is more than the remaining balance")
		}
		if _, err := payments.ActiveFin(ctx, tx, in.MoneyAccountID); err != nil {
			return err
		}
		locID, err := s.resolveLoanLoc(ctx, tx, p, l)
		if err != nil {
			return err
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			date = s.today(ctx, tx)
		}
		desc := "Staff advance repayment"
		if ref := strings.TrimSpace(in.Reference); ref != "" {
			desc += " (" + ref + ")"
		}
		if _, err := tx.Exec(ctx, `SELECT app.apply_advance_cash_repayment($1,$2,$3,$4,$5::date,$6)`,
			loanID, amount, in.MoneyAccountID, locID, date, desc); err != nil {
			return err
		}
		out, err := s.loadLoan(ctx, tx, loanID, false)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "loan.repaid", "employee_advances", loanID, map[string]any{"balance": bal}, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
