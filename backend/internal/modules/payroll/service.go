package payroll

import (
	"context"

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
	"bms/internal/platform/setting"
)

type Service struct {
	DB     *db.DB
	Ledger *ledger.Service
	Clock  clock.Clock
}

func New(database *db.DB, led *ledger.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Clock: clk}
}

func (s *Service) CreateRun(ctx context.Context, p *auth.Principal, start, end string) (map[string]any, error) {
	branch := p.BranchID
	var id uuid.UUID
	var number string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		b := branch
		if b == nil {
			var main uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&main); err != nil {
				return err
			}
			b = &main
		}
		var err error
		number, err = sequence.Next(ctx, tx, *b, "payroll")
		if err != nil {
			return err
		}
		id = ids.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO payroll_runs (id, business_id, branch_id, number, period_start, period_end, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, b, number, start, end, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "status": "draft"}, nil
}

func (s *Service) Calculate(ctx context.Context, p *auth.Principal, runID uuid.UUID) (map[string]any, error) {
	var summary map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var start, end string
		var branch uuid.UUID
		var st string
		if err := tx.QueryRow(ctx, `SELECT period_start::text, period_end::text, branch_id, status FROM payroll_runs WHERE id=$1`, runID).Scan(&start, &end, &branch, &st); err != nil {
			return httpx.NotFound("not_found", "Payroll run not found")
		}
		if st != "draft" && st != "calculated" {
			return httpx.Conflict("invalid_status", "This run can no longer be calculated")
		}
		_, _ = tx.Exec(ctx, `DELETE FROM payslips WHERE run_id=$1`, runID)
		days := setting.Int(ctx, tx, "payroll.working_days", 26)
		var allowance decimal.Decimal
		_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(value),0) FROM salary_components WHERE kind='allowance' AND calc='fixed' AND applies_to_all AND is_active`).Scan(&allowance)
		rows, err := tx.Query(ctx, `SELECT id, name, base_pay FROM employees WHERE status='active' AND branch_id=$1`, branch)
		if err != nil {
			return err
		}
		type emp struct {
			id   uuid.UUID
			name string
			base decimal.Decimal
		}
		var emps []emp
		for rows.Next() {
			var e emp
			if err := rows.Scan(&e.id, &e.name, &e.base); err != nil {
				rows.Close()
				return err
			}
			emps = append(emps, e)
		}
		rows.Close()
		grossTotal, netTotal := money.Zero, money.Zero
		var slips []map[string]any
		for _, e := range emps {
			var unpaid int
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM attendance_days WHERE employee_id=$1 AND day BETWEEN $2 AND $3 AND status IN ('absent')`, e.id, start, end).Scan(&unpaid)
			var loanBal, loanMonthly decimal.Decimal
			var loanID *uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT id, balance, monthly_deduction FROM employee_loans WHERE employee_id=$1 AND status='disbursed' AND balance>0 ORDER BY created_at LIMIT 1`, e.id).Scan(&loanID, &loanBal, &loanMonthly)
			deduct := loanMonthly
			if deduct.GreaterThan(loanBal) {
				deduct = loanBal
			}
			slip := Monthly(e.base, allowance, days, unpaid, deduct)
			pid := ids.New()
			num, err := sequence.Next(ctx, tx, branch, "payslip")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO payslips (id, business_id, run_id, employee_id, number, days_absent, base_pay, gross, total_deductions, net)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			`, pid, p.BusinessID, runID, e.id, num, unpaid, e.base, slip.Gross, slip.Absence.Add(slip.Loan), slip.Net); err != nil {
				return err
			}
			lines := []struct {
				kind, label string
				amt         decimal.Decimal
				loan        *uuid.UUID
			}{
				{"base", "Base pay", e.base, nil},
				{"absence", "Unpaid days", slip.Absence, nil},
				{"allowance", "Allowance", slip.Allowance, nil},
				{"loan", "Loan repayment", slip.Loan, loanID},
			}
			for _, ln := range lines {
				if ln.amt.IsZero() {
					continue
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO payslip_lines (id, business_id, payslip_id, kind, loan_id, label, amount)
					VALUES ($1,$2,$3,$4,$5,$6,$7)
				`, ids.New(), p.BusinessID, pid, ln.kind, ln.loan, ln.label, ln.amt); err != nil {
					return err
				}
			}
			grossTotal = grossTotal.Add(slip.Gross)
			netTotal = netTotal.Add(slip.Net)
			slips = append(slips, map[string]any{"id": pid, "employee_id": e.id, "name": e.name, "gross": slip.Gross, "absence": slip.Absence, "allowance": slip.Allowance, "loan": slip.Loan, "net": slip.Net})
		}
		_, err = tx.Exec(ctx, `UPDATE payroll_runs SET status='calculated', gross_total=$2, net_total=$3, deduction_total=$4 WHERE id=$1`, runID, grossTotal, netTotal, grossTotal.Sub(netTotal))
		summary = map[string]any{"id": runID, "status": "calculated", "gross_total": grossTotal, "net_total": netTotal, "payslips": slips}
		return err
	})
	return summary, err
}

func (s *Service) Approve(ctx context.Context, p *auth.Principal, runID uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var gross, net, ded decimal.Decimal
		var st, end string
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, gross_total, net_total, deduction_total, period_end::text, branch_id FROM payroll_runs WHERE id=$1`, runID).Scan(&st, &gross, &net, &ded, &end, &branch); err != nil {
			return httpx.NotFound("not_found", "Payroll run not found")
		}
		if st != "calculated" {
			return httpx.Conflict("invalid_status", "Calculate the run before approving it")
		}
		loan := ded
		if loan.GreaterThan(gross.Sub(net)) {
			loan = gross.Sub(net)
		}
		user := p.UserID
		lines := []ledger.Line{{Code: "6100", BranchID: &branch, Debit: gross}, {Code: "2200", BranchID: &branch, Credit: net}}
		if gross.Sub(net).IsPositive() {
			lines = append(lines, ledger.Line{Code: "1300", BranchID: &branch, Credit: gross.Sub(net)})
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, end, "payroll_run", runID, "Payroll", &user, lines); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, runID, p.UserID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payslips SET status='approved' WHERE run_id=$1`, runID)
		return err
	})
}

func (s *Service) Pay(ctx context.Context, p *auth.Principal, runID, account uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var net decimal.Decimal
		var st, end string
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, net_total, period_end::text, branch_id FROM payroll_runs WHERE id=$1`, runID).Scan(&st, &net, &end, &branch); err != nil {
			return httpx.NotFound("not_found", "Payroll run not found")
		}
		if st != "approved" {
			return httpx.Conflict("invalid_status", "Approve the run before paying it")
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, account, net); err != nil {
			return err
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, account)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, employee_id, net FROM payslips WHERE run_id=$1`, runID)
		if err != nil {
			return err
		}
		type slip struct {
			id, emp uuid.UUID
			net     decimal.Decimal
		}
		var slips []slip
		for rows.Next() {
			var s slip
			if err := rows.Scan(&s.id, &s.emp, &s.net); err != nil {
				rows.Close()
				return err
			}
			slips = append(slips, s)
		}
		rows.Close()
		user := p.UserID
		for _, sl := range slips {
			if !sl.net.IsPositive() {
				continue
			}
			if _, err := payments.Insert(ctx, tx, payments.Input{
				BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: account, UserID: p.UserID,
				Direction: "out", Kind: "salary", Method: "cash", PartyType: "employee", PartyID: &sl.emp,
				Amount: sl.net, Date: end, AllocType: "payslip", AllocID: sl.id, AllocAmount: sl.net,
			}); err != nil {
				return err
			}
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, end, "salary_payment", runID, "Salary payment", &user, []ledger.Line{
			{Code: "2200", BranchID: &branch, Debit: net},
			{Code: code, BranchID: &branch, MoneyAccountID: &account, Credit: net},
		}); err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `
			UPDATE employee_loans l SET balance = l.balance - pl.amount, status = CASE WHEN l.balance - pl.amount <= 0 THEN 'settled' ELSE l.status END
			FROM payslip_lines pl JOIN payslips ps ON ps.id=pl.payslip_id
			WHERE pl.loan_id=l.id AND ps.run_id=$1 AND pl.kind='loan'
		`, runID)
		_, err = tx.Exec(ctx, `UPDATE payslips SET status='paid', paid_total=net WHERE run_id=$1`, runID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE payroll_runs SET status='locked' WHERE id=$1`, runID)
		return err
	})
}

func (s *Service) Component(ctx context.Context, p *auth.Principal, name, kind string, value decimal.Decimal) (uuid.UUID, error) {
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO salary_components (id, business_id, name, kind, calc, value, applies_to_all)
			VALUES ($1,$2,$3,$4,'fixed',$5,true)
		`, id, p.BusinessID, name, kind, value)
		return err
	})
	return id, err
}
