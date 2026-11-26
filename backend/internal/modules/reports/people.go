package reports

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/sqlq"
)

// runFilter limits payroll runs to the range and location scope. Cancelled runs are excluded.
func runFilter(q *sqlq.Q, tenantID uuid.UUID, f Filter) {
	q.Add("r.tenant_id = ? AND r.status <> 'cancelled'", tenantID)
	q.If(f.From != "", "(make_date(r.period_year, r.period_month, 1) + interval '1 month' - interval '1 day')::date >= ?::date", f.From)
	q.If(f.To != "", "make_date(r.period_year, r.period_month, 1) <= ?::date", f.To)
	if f.Branches != nil {
		q.Add("r.location_id = ANY(?)", f.Branches)
	}
}

// PayrollSummary summarizes payroll runs in range.
func (s *Service) PayrollSummary(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, p.TenantID, f)
		var err error
		list, err = rows(ctx, tx, `
			SELECT r.id,
			       to_char(make_date(r.period_year, r.period_month, 1), 'YYYY-MM') AS run,
			       make_date(r.period_year, r.period_month, 1) AS period_start,
			       (make_date(r.period_year, r.period_month, 1) + interval '1 month' - interval '1 day')::date AS period_end,
			       r.status::text AS status, COALESCE(b.name::text, 'All branches') AS branch,
			       (SELECT count(*) FROM app.payroll_lines pl WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id) AS employees,
			       COALESCE((SELECT SUM(pl.gross_pay) FROM app.payroll_lines pl WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id), 0) AS gross,
			       COALESCE((SELECT SUM(pl.deductions + pl.advance_repayment + pl.statutory_deductions) FROM app.payroll_lines pl WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id), 0) AS deductions,
			       COALESCE((SELECT SUM(pl.net_pay) FROM app.payroll_lines pl WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id), 0) AS net,
			       COALESCE((SELECT SUM(psi.employer_amount) FROM app.payroll_statutory_items psi JOIN app.payroll_lines pl ON pl.tenant_id = psi.tenant_id AND pl.id = psi.payroll_line_id WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id), 0) AS employer,
			       COALESCE((SELECT SUM(pl.net_pay) FROM app.payroll_lines pl WHERE pl.tenant_id = r.tenant_id AND pl.payroll_run_id = r.id AND (r.status = 'paid' OR pl.paid_at IS NOT NULL)), 0) AS paid
			FROM app.payroll_runs r
			LEFT JOIN app.locations b ON b.tenant_id = r.tenant_id AND b.id = r.location_id`+q.Where()+`
			ORDER BY r.period_year DESC, r.period_month DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll summary", Subtitle: f.subtitle(), Columns: cols("run", "period_start", "period_end", "status", "branch", "employees", "gross", "deductions", "net", "employer", "paid"),
		Rows: list, Totals: totals(list, "Total", "employees", "gross", "deductions", "net", "employer", "paid")}, nil, err
}

// PayrollByDepartment groups payroll lines by employee position (the approved schema has no separate departments table).
func (s *Service) PayrollByDepartment(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, p.TenantID, f)
		var err error
		list, err = rows(ctx, tx, `
			SELECT COALESCE(NULLIF(e.position::text, ''), 'Unassigned') AS department,
			       count(DISTINCT pl.employee_id) AS employees,
			       SUM(pl.gross_pay) AS gross,
			       SUM(pl.deductions + pl.advance_repayment + pl.statutory_deductions) AS deductions,
			       SUM(pl.net_pay) AS net
			FROM app.payroll_lines pl
			JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id
			JOIN app.employees e ON e.tenant_id = pl.tenant_id AND e.id = pl.employee_id`+q.Where()+`
			GROUP BY e.position ORDER BY gross DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll by department", Subtitle: f.subtitle(), Columns: cols("department", "employees", "gross", "deductions", "net"),
		Rows: list, Totals: totals(list, "Total", "gross", "deductions", "net")}, nil, err
}

// PayrollDeductions sums statutory employee/employer items, deductions, and advance repayments.
func (s *Service) PayrollDeductions(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, p.TenantID, f)
		w := q.Where()
		var err error
		list, err = rows(ctx, tx, `
			SELECT kind, label, employees, amount FROM (
				SELECT 'statutory'::text AS kind, psi.code AS label, count(DISTINCT pl.employee_id) AS employees, SUM(psi.employee_amount) AS amount
				FROM app.payroll_statutory_items psi
				JOIN app.payroll_lines pl ON pl.tenant_id = psi.tenant_id AND pl.id = psi.payroll_line_id
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id`+w+`
				GROUP BY psi.code HAVING SUM(psi.employee_amount) > 0
				UNION ALL
				SELECT 'employer_contribution'::text, psi.code || ' (Employer)', count(DISTINCT pl.employee_id), SUM(psi.employer_amount)
				FROM app.payroll_statutory_items psi
				JOIN app.payroll_lines pl ON pl.tenant_id = psi.tenant_id AND pl.id = psi.payroll_line_id
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id`+w+`
				GROUP BY psi.code HAVING SUM(psi.employer_amount) > 0
				UNION ALL
				SELECT 'loan'::text, 'Advance / loan repayment', count(DISTINCT pl.employee_id), SUM(pl.advance_repayment)
				FROM app.payroll_lines pl
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id`+w+`
				HAVING COALESCE(SUM(pl.advance_repayment), 0) > 0
				UNION ALL
				SELECT 'deduction'::text, 'Other deductions', count(DISTINCT pl.employee_id), SUM(pl.deductions)
				FROM app.payroll_lines pl
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id`+w+`
				HAVING COALESCE(SUM(pl.deductions), 0) > 0
			) x ORDER BY kind, amount DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll deductions", Subtitle: f.subtitle(), Columns: cols("kind", "label", "employees", "amount"),
		Rows: list, Totals: totals(list, "Total", "amount")}, nil, err
}

// PayrollLoans lists employee advances and loans from app.employee_advances.
func (s *Service) PayrollLoans(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("l.tenant_id = $1")
		status := f.Q("status")
		switch status {
		case "", "approved", "disbursed":
			q.Add("l.status = 'active'")
		default:
			q.Add("l.status = ?::app.advance_status", status)
		}
		f.branch(q, "e.location_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT l.id::text AS number, e.full_name::text AS employee, e.employee_no, l.kind::text AS kind,
			       l.status::text AS status, l.principal, l.amount_repaid AS repaid, l.balance,
			       l.monthly_deduction, to_char(l.issued_on, 'YYYY-MM') AS start_period
			FROM app.employee_advances l
			JOIN app.employees e ON e.tenant_id = l.tenant_id AND e.id = l.employee_id`+q.Where()+`
			ORDER BY l.balance DESC, e.full_name`, q.Args...)
		return err
	})
	return export.Table{Title: "Staff loans outstanding", Columns: cols("number", "employee", "kind", "status", "principal", "repaid", "balance", "monthly_deduction"),
		Rows: list, Totals: totals(list, "Total", "principal", "repaid", "balance")}, nil, err
}

// StaffPerformance summarizes sales, attendance, and commissions per employee.
func (s *Service) StaffPerformance(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		src := linesSQL(q, p.TenantID, f)
		aq := " AND ad.tenant_id = $1"
		if f.From != "" {
			aq += " AND ad.work_date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			aq += " AND ad.work_date <= " + q.Arg(f.To) + "::date"
		}
		cq := " AND ce.tenant_id = $1 AND ce.status <> 'reversed'"
		if f.From != "" {
			cq += " AND ce.earned_on >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			cq += " AND ce.earned_on <= " + q.Arg(f.To) + "::date"
		}
		ew := " WHERE e.tenant_id = $1 AND e.status <> 'terminated'"
		if f.Branches != nil {
			ew += " AND e.location_id = ANY(" + q.Arg(f.Branches) + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT e.id, e.full_name::text AS employee, e.employee_no, COALESCE(b.name::text, '') AS branch,
			       COALESCE(sl.sales_count, 0) AS sales_count, COALESCE(sl.net_sales, 0) AS net_sales,
			       COALESCE(a.present, 0) AS days_present, COALESCE(a.absent, 0) AS days_absent,
			       COALESCE(a.late_count, 0) AS late_count, COALESCE(a.late_minutes, 0) AS late_minutes,
			       CASE WHEN COALESCE(a.present, 0) + COALESCE(a.absent, 0) = 0 THEN NULL
			            ELSE ROUND(a.present::numeric * 100 / (a.present + a.absent), 2) END AS attendance_rate,
			       COALESCE(cm.commission, 0) AS commission
			FROM app.employees e
			LEFT JOIN app.locations b ON b.tenant_id = e.tenant_id AND b.id = e.location_id
			LEFT JOIN (
				SELECT e2.id AS emp, COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count, ROUND(SUM(l.net), 2) AS net_sales
				FROM `+src+` JOIN app.employees e2 ON e2.tenant_id = $1 AND e2.membership_id = l.sold_by
				GROUP BY e2.id
			) sl ON sl.emp = e.id
			LEFT JOIN (
				SELECT ad.employee_id,
				       COUNT(*) FILTER (WHERE ad.status IN ('present','late','half_day')) AS present,
				       COUNT(*) FILTER (WHERE ad.status = 'absent') AS absent,
				       COUNT(*) FILTER (WHERE ad.minutes_late > 0) AS late_count,
				       COALESCE(SUM(ad.minutes_late), 0) AS late_minutes
				FROM app.attendance_records ad WHERE true`+aq+`
				GROUP BY ad.employee_id
			) a ON a.employee_id = e.id
			LEFT JOIN (
				SELECT ce.employee_id, SUM(ce.amount) AS commission
				FROM app.commission_entries ce WHERE true`+cq+`
				GROUP BY ce.employee_id
			) cm ON cm.employee_id = e.id`+ew+`
			ORDER BY net_sales DESC, e.full_name`, q.Args...)
		return err
	})
	return export.Table{Title: "Staff performance", Subtitle: f.subtitle(),
		Columns: cols("employee", "branch", "sales_count", "net_sales", "days_present", "days_absent", "late_count", "late_minutes", "attendance_rate", "commission"),
		Rows:    list, Totals: totals(list, "Total", "sales_count", "net_sales", "commission")}, nil, err
}

// Branches compares locations side by side for the period.
func (s *Service) Branches(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		src := linesSQL(q, p.TenantID, f)
		eq := " AND e.tenant_id = $1 AND e.status IN ('approved','paid')"
		if f.From != "" {
			eq += " AND e.expense_date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			eq += " AND e.expense_date <= " + q.Arg(f.To) + "::date"
		}
		bw := " WHERE b.tenant_id = $1 AND b.status = 'active'"
		if f.Branches != nil {
			bw += " AND b.id = ANY(" + q.Arg(f.Branches) + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT b.id, b.name::text AS branch,
			       COALESCE(sc.sales_count, 0) AS sales_count,
			       COALESCE(sc.net_sales, 0) AS net_sales,
			       COALESCE(sc.cost, 0) AS cost,
			       COALESCE(sc.net_sales, 0) - COALESCE(sc.cost, 0) AS gross_profit,
			       0::numeric AS other_income,
			       COALESCE(ex.expenses, 0) AS expenses,
			       COALESCE(sc.net_sales, 0) - COALESCE(sc.cost, 0) - COALESCE(ex.expenses, 0) AS net_profit,
			       COALESCE(st.value, 0) AS stock_value,
			       CASE WHEN COALESCE(sc.sales_count, 0) = 0 THEN 0 ELSE ROUND(sc.sales_total / sc.sales_count, 2) END AS average_sale
			FROM app.locations b
			LEFT JOIN (
				SELECT l.branch_id, COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count,
				       ROUND(SUM(l.total), 2) AS sales_total, ROUND(SUM(l.net), 2) AS net_sales, ROUND(SUM(l.cost), 2) AS cost
				FROM `+src+` GROUP BY l.branch_id
			) sc ON sc.branch_id = b.id
			LEFT JOIN (
				SELECT e.location_id, SUM(e.amount) AS expenses FROM app.expenses e WHERE true`+eq+` GROUP BY e.location_id
			) ex ON ex.location_id = b.id
			LEFT JOIN (
				SELECT sb.location_id, ROUND(SUM(sb.quantity_on_hand * pr.average_cost), 2) AS value
				FROM app.stock_balances sb JOIN app.products pr ON pr.tenant_id = sb.tenant_id AND pr.id = sb.product_id
				WHERE sb.tenant_id = $1 GROUP BY sb.location_id
			) st ON st.location_id = b.id`+bw+`
			ORDER BY net_sales DESC, b.name`, q.Args...)
		return err
	})
	keys := stripCost(p, []string{"branch", "sales_count", "net_sales", "cost", "gross_profit", "other_income", "expenses", "net_profit", "stock_value", "average_sale"})
	if !canCost(p) {
		dropKeys(list, "cost", "gross_profit", "net_profit", "stock_value")
		keys = filterOut(keys, "net_profit", "stock_value")
	}
	return export.Table{Title: "Branch comparison", Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", keys[1:]...)}, nil, err
}

func filterOut(keys []string, drop ...string) []string {
	out := keys[:0:0]
	for _, k := range keys {
		skip := false
		for _, d := range drop {
			if k == d {
				skip = true
			}
		}
		if !skip {
			out = append(out, k)
		}
	}
	return out
}
