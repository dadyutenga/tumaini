package hr

import (
	"context"

	"github.com/jackc/pgx/v5"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Self-service reads. Every query is pinned to the caller's own employee id.

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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.One(ctx, tx, "Employee not found", `SELECT `+employeeCols+employeeFrom+` WHERE e.tenant_id = $1 AND e.id = $2`, p.TenantID, *p.EmployeeID)
		return err
	})
	return out, err
}

func (s *Service) MeAttendance(ctx context.Context, p *auth.Principal, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	if err := s.self(p); err != nil {
		return nil, 0, err
	}
	return s.selfList(ctx, p, page,
		`FROM app.attendance_records WHERE tenant_id = $1 AND employee_id = $2 AND ($3 = '' OR work_date >= $3::date) AND ($4 = '' OR work_date <= $4::date)`,
		`SELECT id, work_date AS day, status::text AS status, clock_in_at AS first_in, clock_out_at AS last_out,
		        CASE WHEN clock_in_at IS NOT NULL AND clock_out_at IS NOT NULL THEN GREATEST(0, EXTRACT(EPOCH FROM (clock_out_at - clock_in_at))::int / 60) ELSE 0 END AS worked_minutes,
		        minutes_late AS late_minutes, overtime_minutes, is_corrected, (clock_in_at IS NOT NULL AND clock_out_at IS NULL) AS needs_review`,
		` ORDER BY work_date DESC`, from, to)
}

func (s *Service) MeLeave(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if err := s.self(p); err != nil {
		return nil, err
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		reqs, err := hrkit.All(ctx, tx, leaveSQL+` WHERE lr.tenant_id = $1 AND lr.employee_id = $2 ORDER BY lr.start_date DESC LIMIT 100`, p.TenantID, *p.EmployeeID)
		if err != nil {
			return err
		}
		bal, err := leaveBalance(ctx, tx, p.TenantID, *p.EmployeeID, hrkit.MustDate(s.today(ctx, tx)).Year())
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, loanSQL+` WHERE l.tenant_id = $1 AND l.employee_id = $2 ORDER BY l.created_at DESC`, p.TenantID, *p.EmployeeID)
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
	return s.selfList(ctx, p, page,
		`FROM app.payroll_lines pl
		 JOIN app.payslips ps ON ps.tenant_id = pl.tenant_id AND ps.payroll_line_id = pl.id
		 LEFT JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id
		 WHERE pl.tenant_id = $1 AND pl.employee_id = $2 AND $3 = $4`,
		`SELECT ps.id, pl.id AS payroll_line_id, ps.payslip_no AS number, pl.payroll_run_id AS run_id,
		        COALESCE(r.period_year, EXTRACT(YEAR FROM ps.issued_at)::smallint) AS period_year,
		        COALESCE(r.period_month, EXTRACT(MONTH FROM ps.issued_at)::smallint) AS period_month,
		        COALESCE(r.status::text, CASE WHEN pl.paid_at IS NOT NULL THEN 'paid' ELSE 'approved' END) AS run_status,
		        pl.gross_pay AS gross, (pl.deductions + pl.advance_repayment + pl.statutory_deductions) AS total_deductions,
		        pl.net_pay AS net, CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END AS paid_total, pl.paid_at`,
		` ORDER BY ps.issued_at DESC`, "", "")
}

func (s *Service) MePayments(ctx context.Context, p *auth.Principal, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	if err := s.self(p); err != nil {
		return nil, 0, err
	}
	return s.selfList(ctx, p, page,
		`FROM app.payments_made WHERE tenant_id = $1 AND employee_id = $2 AND ($3 = '' OR business_date >= $3::date) AND ($4 = '' OR business_date <= $4::date)`,
		`SELECT id, payment_no AS number, 'out' AS direction, method::text AS method, amount, business_date AS payment_date, reference, status::text AS status`,
		` ORDER BY business_date DESC, created_at DESC`, from, to)
}

func (s *Service) selfList(ctx context.Context, p *auth.Principal, page httpx.Page, from, cols, order, a, b string) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) `+from, p.TenantID, *p.EmployeeID, a, b).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, cols+" "+from+order+` LIMIT $5 OFFSET $6`, p.TenantID, *p.EmployeeID, a, b, page.PerPage, page.Offset)
		return err
	})
	return out, total, err
}
