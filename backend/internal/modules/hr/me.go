package hr

import (
	"context"

	"github.com/jackc/pgx/v5"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Self-service reads. Every query is pinned to the caller's own employee id,
// so these routes never expose another employee's records.

func (s *Service) self(p *auth.Principal) error {
	if p.EmployeeID == nil {
		return httpx.NotFound("employee_required", "This login is not linked to an employee")
	}
	return nil
}

func (s *Service) MeEmployee(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := s.self(p); err != nil {
		return nil, err
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.One(ctx, tx, "Employee not found", `SELECT `+employeeCols+employeeFrom+` WHERE e.id = $1`, *p.EmployeeID)
		return err
	})
	return out, err
}

func (s *Service) MeAttendance(ctx context.Context, p *auth.Principal, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	if err := s.self(p); err != nil {
		return nil, 0, err
	}
	return s.selfList(ctx, p, page,
		`FROM attendance_days WHERE employee_id = $1 AND ($2 = '' OR day >= $2::date) AND ($3 = '' OR day <= $3::date)`,
		`SELECT id, day, status, first_in, last_out, worked_minutes, late_minutes, overtime_minutes, overtime_status, needs_review, note`,
		` ORDER BY day DESC`, from, to)
}

func (s *Service) MeLeave(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := s.self(p); err != nil {
		return nil, err
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		reqs, err := hrkit.All(ctx, tx, leaveSQL+` WHERE lr.employee_id = $1 ORDER BY lr.start_date DESC LIMIT 100`, *p.EmployeeID)
		if err != nil {
			return err
		}
		bal, err := leaveBalance(ctx, tx, *p.EmployeeID, hrkit.MustDate(s.today(ctx, tx)).Year())
		if err != nil {
			return err
		}
		out = map[string]any{"requests": reqs, "balance": bal}
		return nil
	})
	return out, err
}

func (s *Service) MeLoans(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	if err := s.self(p); err != nil {
		return nil, err
	}
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, loanSQL+` WHERE l.employee_id = $1 ORDER BY l.created_at DESC`, *p.EmployeeID)
		for _, l := range out {
			l["schedule"] = schedule(l)
		}
		return err
	})
	return out, err
}

func (s *Service) MePayslips(ctx context.Context, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
	if err := s.self(p); err != nil {
		return nil, 0, err
	}
	// Draft payslips are not final, so the employee sees approved ones only.
	return s.selfList(ctx, p, page,
		`FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id WHERE ps.employee_id = $1 AND r.status IN ('approved','paid','locked') AND $2 = $3`,
		`SELECT ps.id, ps.number, r.number AS run_number, r.period_start, r.period_end, ps.gross, ps.total_deductions, ps.net, ps.paid_total, ps.status`,
		` ORDER BY r.period_start DESC`, "", "")
}

func (s *Service) MePayments(ctx context.Context, p *auth.Principal, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	if err := s.self(p); err != nil {
		return nil, 0, err
	}
	return s.selfList(ctx, p, page,
		`FROM payments WHERE party_type = 'employee' AND party_id = $1 AND ($2 = '' OR payment_date >= $2::date) AND ($3 = '' OR payment_date <= $3::date)`,
		`SELECT id, number, kind, direction, method, amount, payment_date, reference, status`,
		` ORDER BY payment_date DESC, created_at DESC`, from, to)
}

// selfList runs count and page queries whose $1 is the caller's employee id
// and $2, $3 are a date range.
func (s *Service) selfList(ctx context.Context, p *auth.Principal, page httpx.Page, from, cols, order, a, b string) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) `+from, *p.EmployeeID, a, b).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, cols+" "+from+order+` LIMIT $4 OFFSET $5`, *p.EmployeeID, a, b, page.PerPage, page.Offset)
		return err
	})
	return out, total, err
}
