package reports

import (
	"context"

	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/sqlq"
)

// runFilter limits payroll runs to the range and scope. Reversed runs are excluded.
func runFilter(q *sqlq.Q, f Filter) {
	q.Add("r.status <> 'reversed'")
	q.If(f.From != "", "r.period_end >= ?::date", f.From)
	q.If(f.To != "", "r.period_start <= ?::date", f.To)
	if f.Branches != nil {
		q.Add("(r.branch_id IS NULL OR r.branch_id = ANY(?))", f.Branches)
	}
}

// PayrollSummary is FR-REP-11 run summary.
func (s *Service) PayrollSummary(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, f)
		var err error
		list, err = rows(ctx, tx, `
			SELECT r.id, r.number AS run, r.period_start, r.period_end, r.status, COALESCE(b.name, 'All branches') AS branch,
			       (SELECT count(*) FROM payslips ps WHERE ps.run_id = r.id) AS employees,
			       r.gross_total AS gross, r.deduction_total AS deductions, r.net_total AS net, r.employer_total AS employer,
			       COALESCE((SELECT SUM(ps.paid_total) FROM payslips ps WHERE ps.run_id = r.id), 0) AS paid
			FROM payroll_runs r LEFT JOIN branches b ON b.id = r.branch_id`+q.Where()+`
			ORDER BY r.period_start DESC, r.number`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll summary", Subtitle: f.subtitle(), Columns: cols("run", "period_start", "period_end", "status", "branch", "employees", "gross", "deductions", "net", "employer", "paid"),
		Rows: list, Totals: totals(list, "Total", "employees", "gross", "deductions", "net", "employer", "paid")}, nil, err
}

// PayrollByDepartment groups payslips of the runs in range by department.
func (s *Service) PayrollByDepartment(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, f)
		var err error
		list, err = rows(ctx, tx, `
			SELECT COALESCE(d.name, 'No department') AS department, count(DISTINCT ps.employee_id) AS employees,
			       SUM(ps.gross) AS gross, SUM(ps.total_deductions) AS deductions, SUM(ps.net) AS net
			FROM payslips ps
			JOIN payroll_runs r ON r.id = ps.run_id
			JOIN employees e ON e.id = ps.employee_id
			LEFT JOIN departments d ON d.id = e.department_id`+q.Where()+`
			GROUP BY d.name ORDER BY gross DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll by department", Subtitle: f.subtitle(), Columns: cols("department", "employees", "gross", "deductions", "net"),
		Rows: list, Totals: totals(list, "Total", "gross", "deductions", "net")}, nil, err
}

// PayrollDeductions sums deduction, statutory, loan and employer lines by item.
func (s *Service) PayrollDeductions(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		runFilter(q, f)
		q.Add("pl.kind IN ('deduction','statutory','loan','employer_contribution')")
		var err error
		list, err = rows(ctx, tx, `
			SELECT pl.kind, pl.label, count(DISTINCT ps.employee_id) AS employees, SUM(pl.amount) AS amount
			FROM payslip_lines pl
			JOIN payslips ps ON ps.id = pl.payslip_id
			JOIN payroll_runs r ON r.id = ps.run_id`+q.Where()+`
			GROUP BY pl.kind, pl.label ORDER BY pl.kind, amount DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Payroll deductions", Subtitle: f.subtitle(), Columns: cols("kind", "label", "employees", "amount"),
		Rows: list, Totals: totals(list, "Total", "amount")}, nil, err
}

// PayrollLoans lists loans and advances that are not settled.
func (s *Service) PayrollLoans(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		status := f.Q("status")
		if status == "" {
			q.Add("l.status IN ('approved','disbursed')")
		} else {
			q.Add("l.status = ?", status)
		}
		f.branch(q, "e.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT l.number, e.name AS employee, e.employee_no, l.kind, l.status, l.principal,
			       l.principal - l.balance AS repaid, l.balance, l.monthly_deduction, l.start_period
			FROM employee_loans l JOIN employees e ON e.id = l.employee_id`+q.Where()+`
			ORDER BY l.balance DESC, e.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Staff loans outstanding", Columns: cols("number", "employee", "kind", "status", "principal", "repaid", "balance", "monthly_deduction"),
		Rows: list, Totals: totals(list, "Total", "principal", "repaid", "balance")}, nil, err
}

// StaffPerformance is FR-REP-12: sales, attendance and commission per employee.
// A sale counts for the employee recorded on it, or the employee linked to the cashier's login.
func (s *Service) StaffPerformance(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		src := linesSQL(q, f)
		aq := ""
		if f.From != "" {
			aq += " AND ad.day >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			aq += " AND ad.day <= " + q.Arg(f.To) + "::date"
		}
		rq := " AND r.status <> 'reversed'"
		if f.From != "" {
			rq += " AND r.period_end >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			rq += " AND r.period_start <= " + q.Arg(f.To) + "::date"
		}
		ew := " WHERE e.status <> 'terminated'"
		if f.Branches != nil {
			ew += " AND e.branch_id = ANY(" + q.Arg(f.Branches) + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT e.id, e.name AS employee, e.employee_no, b.name AS branch,
			       COALESCE(sl.sales_count, 0) AS sales_count, COALESCE(sl.net_sales, 0) AS net_sales,
			       COALESCE(a.present, 0) AS days_present, COALESCE(a.absent, 0) AS days_absent,
			       COALESCE(a.late_count, 0) AS late_count, COALESCE(a.late_minutes, 0) AS late_minutes,
			       CASE WHEN COALESCE(a.present, 0) + COALESCE(a.absent, 0) = 0 THEN NULL
			            ELSE ROUND(a.present::numeric * 100 / (a.present + a.absent), 2) END AS attendance_rate,
			       COALESCE(cm.commission, 0) AS commission
			FROM employees e
			JOIN branches b ON b.id = e.branch_id
			LEFT JOIN (
				SELECT COALESCE(l.employee_id, u.employee_id) AS emp,
				       COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count, ROUND(SUM(l.net), 2) AS net_sales
				FROM `+src+` LEFT JOIN users u ON u.id = l.cashier_id
				GROUP BY 1
			) sl ON sl.emp = e.id
			LEFT JOIN (
				SELECT ad.employee_id,
				       COUNT(*) FILTER (WHERE ad.status IN ('present','late','half_day')) AS present,
				       COUNT(*) FILTER (WHERE ad.status = 'absent') AS absent,
				       COUNT(*) FILTER (WHERE ad.late_minutes > 0) AS late_count,
				       COALESCE(SUM(ad.late_minutes), 0) AS late_minutes
				FROM attendance_days ad WHERE true`+aq+`
				GROUP BY ad.employee_id
			) a ON a.employee_id = e.id
			LEFT JOIN (
				SELECT ps.employee_id, SUM(pl.amount) AS commission
				FROM payslip_lines pl JOIN payslips ps ON ps.id = pl.payslip_id JOIN payroll_runs r ON r.id = ps.run_id
				WHERE pl.kind = 'commission'`+rq+`
				GROUP BY ps.employee_id
			) cm ON cm.employee_id = e.id`+ew+`
			ORDER BY net_sales DESC, e.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Staff performance", Subtitle: f.subtitle(),
		Columns: cols("employee", "branch", "sales_count", "net_sales", "days_present", "days_absent", "late_count", "late_minutes", "attendance_rate", "commission"),
		Rows:    list, Totals: totals(list, "Total", "sales_count", "net_sales", "commission")}, nil, err
}

// Branches is FR-BRN-06: side by side branch figures for the period. Profit
// figures come from the ledger, counts from sales, stock value from levels.
func (s *Service) Branches(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		jl := ""
		if f.From != "" {
			jl += " AND l.entry_date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			jl += " AND l.entry_date <= " + q.Arg(f.To) + "::date"
		}
		sq := ""
		if f.From != "" {
			sq += " AND s.business_date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			sq += " AND s.business_date <= " + q.Arg(f.To) + "::date"
		}
		bw := " WHERE b.is_active"
		if f.Branches != nil {
			bw += " AND b.id = ANY(" + q.Arg(f.Branches) + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT b.id, b.name AS branch,
			       COALESCE(sc.sales_count, 0) AS sales_count,
			       COALESCE(g.net_sales, 0) AS net_sales, COALESCE(g.cost, 0) AS cost,
			       COALESCE(g.net_sales, 0) - COALESCE(g.cost, 0) AS gross_profit,
			       COALESCE(g.other_income, 0) AS other_income, COALESCE(g.expenses, 0) AS expenses,
			       COALESCE(g.net_sales, 0) + COALESCE(g.other_income, 0) - COALESCE(g.cost, 0) - COALESCE(g.expenses, 0) AS net_profit,
			       COALESCE(st.value, 0) AS stock_value,
			       CASE WHEN COALESCE(sc.sales_count, 0) = 0 THEN 0 ELSE ROUND(sc.sales_total / sc.sales_count, 2) END AS average_sale
			FROM branches b
			LEFT JOIN (
				SELECT l.branch_id,
				       SUM(l.credit - l.debit) FILTER (WHERE a.code IN ('4000','4010')) AS net_sales,
				       SUM(l.credit - l.debit) FILTER (WHERE a.type = 'income' AND a.code NOT IN ('4000','4010')) AS other_income,
				       SUM(l.debit - l.credit) FILTER (WHERE a.code = '5000') AS cost,
				       SUM(l.debit - l.credit) FILTER (WHERE a.type = 'expense' AND a.code <> '5000') AS expenses
				FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id
				WHERE a.type IN ('income','expense')`+jl+`
				GROUP BY l.branch_id
			) g ON g.branch_id = b.id
			LEFT JOIN (
				SELECT s.branch_id, count(*) AS sales_count, SUM(s.total) AS sales_total
				FROM sales s WHERE s.kind = 'sale' AND s.status IN ('completed','partially_refunded','refunded')`+sq+`
				GROUP BY s.branch_id
			) sc ON sc.branch_id = b.id
			LEFT JOIN (SELECT branch_id, ROUND(SUM(qty_on_hand * avg_cost), 2) AS value FROM stock_levels GROUP BY branch_id) st ON st.branch_id = b.id`+bw+`
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
