package hr

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Service struct {
	DB     *db.DB
	Ledger *ledger.Service
	Clock  clock.Clock
}

func New(database *db.DB, led *ledger.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Clock: clk}
}

type EmployeeIn struct {
	Name           string          `json:"name"`
	Phone          string          `json:"phone"`
	BranchID       *uuid.UUID      `json:"branch_id"`
	HireDate       string          `json:"hire_date"`
	EmploymentType string          `json:"employment_type"`
	PayType        string          `json:"pay_type"`
	BasePay        decimal.Decimal `json:"base_pay"`
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in EmployeeIn) (map[string]any, error) {
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	if branch == nil || strings.TrimSpace(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Name and branch are required")
	}
	if in.EmploymentType == "" {
		in.EmploymentType = "permanent"
	}
	if in.PayType == "" {
		in.PayType = "monthly"
	}
	if in.HireDate == "" {
		in.HireDate = s.Clock.Now().Format("2006-01-02")
	}
	id := ids.New()
	var number string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		number, err = sequence.Next(ctx, tx, *branch, "employee")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO employees (id, business_id, branch_id, employee_no, name, phone, hire_date, employment_type, pay_type, base_pay)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10)
		`, id, p.BusinessID, branch, number, strings.TrimSpace(in.Name), in.Phone, in.HireDate, in.EmploymentType, in.PayType, money.Money(in.BasePay))
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "employee_no": number, "name": in.Name, "base_pay": money.Money(in.BasePay)}, nil
}

func (s *Service) Loan(ctx context.Context, p *auth.Principal, employee uuid.UUID, principal, monthly decimal.Decimal) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	id := ids.New()
	var number string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		number, err = sequence.Next(ctx, tx, *p.BranchID, "loan")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO employee_loans (id, business_id, branch_id, number, employee_id, kind, principal, monthly_deduction, balance, start_period, status, created_by)
			VALUES ($1,$2,$3,$4,$5,'loan',$6,$7,$6,date_trunc('month', current_date)::date,'approved',$8)
		`, id, p.BusinessID, *p.BranchID, number, employee, money.Money(principal), money.Money(monthly), p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "balance": money.Money(principal)}, nil
}

func (s *Service) Disburse(ctx context.Context, p *auth.Principal, loanID, account uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var amount decimal.Decimal
		var employee, branch uuid.UUID
		var st string
		if err := tx.QueryRow(ctx, `SELECT principal, employee_id, branch_id, status FROM employee_loans WHERE id=$1`, loanID).Scan(&amount, &employee, &branch, &st); err != nil {
			return httpx.NotFound("not_found", "Loan not found")
		}
		if st != "approved" {
			return httpx.Conflict("invalid_status", "This loan is not waiting to be paid out")
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, account, amount); err != nil {
			return err
		}
		date := s.Clock.Now().Format("2006-01-02")
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: account, UserID: p.UserID,
			Direction: "out", Kind: "loan_disbursement", Method: "cash", PartyType: "employee", PartyID: &employee,
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
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "loan", loanID, "Staff loan", &user, []ledger.Line{
			{Code: "1300", BranchID: &branch, PartyType: "employee", PartyID: &employee, Debit: amount},
			{Code: code, BranchID: &branch, MoneyAccountID: &account, Credit: amount},
		}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE employee_loans SET status='disbursed', payment_id=$2 WHERE id=$1`, loanID, res.ID)
		return err
	})
}
