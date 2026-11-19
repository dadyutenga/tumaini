package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/sqlq"
)

// periodRange returns the current and previous ranges for today, week
// (Monday to today) or month (first of month to today). The previous range
// has the same length and ends the day before the current one starts.
func periodRange(period, today string) (from, to, pFrom, pTo string, ok bool) {
	t, err := time.Parse("2006-01-02", today)
	if err != nil {
		return "", "", "", "", false
	}
	var f time.Time
	switch period {
	case "", "today":
		f = t
	case "week":
		wd := (int(t.Weekday()) + 6) % 7
		f = t.AddDate(0, 0, -wd)
	case "month":
		f = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return "", "", "", "", false
	}
	from, to = f.Format("2006-01-02"), today
	pFrom, pTo = previousRange(from, to)
	return from, to, pFrom, pTo, true
}

func has(p *auth.Principal, keys ...string) bool {
	if p.IsOwner {
		return true
	}
	for _, k := range keys {
		if _, ok := p.Can(k); ok {
			return true
		}
	}
	return false
}

// Dashboard is FR-DSH-01..10. Each card is present only when the caller holds
// the permission behind it; cost and profit need dashboard.view_profit or
// products.view_cost, bank and mobile balances need accounts.view_balance.
func (s *Service) Dashboard(ctx context.Context, p *auth.Principal, f Filter) (map[string]any, error) {
	out := map[string]any{}
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		period := f.Q("period")
		if period == "" {
			period = "today"
		}
		var from, to, pFrom, pTo string
		if period == "custom" {
			if f.From == "" || f.To == "" {
				return badQuery("custom period requires from and to dates")
			}
			from, to = f.From, f.To
			pFrom, pTo = previousRange(from, to)
		} else {
			var ok bool
			from, to, pFrom, pTo, ok = periodRange(period, f.today)
			if !ok {
				return badQuery("period must be today, week, month or custom")
			}
		}
		biz := f.Q("business")
		if biz == "" {
			biz = "all"
		}
		if biz != "all" && biz != "shop" && biz != "services" {
			return badQuery("business must be all, shop or services")
		}
		out["period"] = map[string]any{"name": period, "from": from, "to": to, "previous_from": pFrom, "previous_to": pTo, "business": biz}
		out["branch_ids"] = f.Branches
		cur, prev := f, f
		cur.From, cur.To = from, to
		prev.From, prev.To = pFrom, pTo
		profit := canCost(p)

		if biz != "services" && has(p, "sales.view", "reports.sales") {
			c, err := salesTotals(ctx, tx, cur)
			if err != nil {
				return err
			}
			pv, err := salesTotals(ctx, tx, prev)
			if err != nil {
				return err
			}
			card := map[string]any{
				"total": num(c, "total"), "count": num(c, "sales_count"), "previous_total": num(pv, "total"),
				"change_pct": pct(num(c, "total").Sub(num(pv, "total")), num(pv, "total")),
			}
			out["sales"] = card
			if profit {
				gp := num(c, "net_sales").Sub(num(c, "cost"))
				out["gross_profit"] = map[string]any{"amount": gp, "previous": num(pv, "net_sales").Sub(num(pv, "cost"))}
			}
			trendFrom, _ := time.Parse("2006-01-02", f.today)
			tf := f
			tf.From, tf.To = trendFrom.AddDate(0, 0, -29).Format("2006-01-02"), f.today
			q := sqlq.New()
			src := linesSQL(q, tf)
			trend, err := rows(ctx, tx, `
				SELECT gs.day::date AS date, COALESCE(x.total, 0) AS total
				FROM generate_series(`+q.Arg(tf.From)+`::date, `+q.Arg(tf.To)+`::date, interval '1 day') AS gs(day)
				LEFT JOIN (SELECT l.d AS sale_day, ROUND(SUM(l.total), 2) AS total FROM `+src+` GROUP BY l.d) x ON x.sale_day = gs.day::date
				ORDER BY 1`, q.Args...)
			if err != nil {
				return err
			}
			out["sales_trend"] = trend
			q = sqlq.New()
			src = linesSQL(q, cur)
			top, err := rows(ctx, tx, `
				SELECT l.product_id, pr.name AS product, ROUND(SUM(l.qty), 3) AS qty, ROUND(SUM(l.total), 2) AS total
				FROM `+src+` JOIN products pr ON pr.id = l.product_id
				GROUP BY l.product_id, pr.name ORDER BY total DESC LIMIT 5`, q.Args...)
			if err != nil {
				return err
			}
			out["top_products"] = top
			q = sqlq.New()
			src = linesSQL(q, cur)
			byBranch, err := rows(ctx, tx, `
				SELECT l.branch_id, b.name AS branch, ROUND(SUM(l.total), 2) AS total, COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count
				FROM `+src+` JOIN branches b ON b.id = l.branch_id GROUP BY l.branch_id, b.name ORDER BY total DESC`, q.Args...)
			if err != nil {
				return err
			}
			out["sales_by_branch"] = byBranch
			q = sqlq.New()
			q.Add("pay.status = 'posted' AND pay.kind IN ('sale','sale_refund')")
			cur.dates(q, "pay.payment_date")
			cur.branch(q, "pay.branch_id")
			byMethod, err := rows(ctx, tx, `
				SELECT pay.method, SUM(CASE WHEN pay.direction = 'in' THEN pay.amount ELSE -pay.amount END) AS total
				FROM payments pay`+q.Where()+` GROUP BY pay.method ORDER BY total DESC`, q.Args...)
			if err != nil {
				return err
			}
			out["sales_by_payment_method"] = byMethod
		}

		if biz != "shop" && has(p, "service_billing.view", "projects.view", "reports.services", "reports.financial") {
			svcInc := func(rng Filter) (decimal.Decimal, error) {
				q := sqlq.New()
				q.Add("a.code = '4300'")
				rng.dates(q, "l.entry_date")
				rng.branch(q, "l.branch_id")
				v, err := one(ctx, tx, `SELECT COALESCE(SUM(l.credit - l.debit), 0) AS v FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id`+q.Where(), q.Args...)
				return num(v, "v"), err
			}
			si, err := svcInc(cur)
			if err != nil {
				return err
			}
			psi, err := svcInc(prev)
			if err != nil {
				return err
			}
			out["service_income"] = map[string]any{"total": si, "previous_total": psi, "change_pct": pct(si.Sub(psi), psi)}
		}

		if has(p, "expenses.view", "reports.expenses") {
			e, err := expenseTotal(ctx, tx, cur)
			if err != nil {
				return err
			}
			pe, err := expenseTotal(ctx, tx, prev)
			if err != nil {
				return err
			}
			out["expenses"] = map[string]any{"total": e, "previous_total": pe, "change_pct": pct(e.Sub(pe), pe)}
		}

		if profit && has(p, "reports.financial", "dashboard.view_profit") {
			r, err := s.pl(ctx, tx, cur)
			if err != nil {
				return err
			}
			pr, err := s.pl(ctx, tx, prev)
			if err != nil {
				return err
			}
			net := r.Income.Sub(r.Expense)
			prevNet := pr.Income.Sub(pr.Expense)
			if biz == "shop" {
				net = r.Revenue.Sub(r.COGS)
				prevNet = pr.Revenue.Sub(pr.COGS)
			} else if biz == "services" {
				net = r.ServiceIncome.Sub(r.ServiceCosts)
				prevNet = pr.ServiceIncome.Sub(pr.ServiceCosts)
			}
			out["profit"] = map[string]any{
				"gross_profit": r.Revenue.Sub(r.COGS), "net_profit": net,
				"previous_net_profit": prevNet, "change_pct": pct(net.Sub(prevNet), prevNet.Abs()),
				"income": r.Income, "expenses": r.Expense,
			}
		}

		// Daily income vs expenses series for the SRS Section 5 line chart.
		if has(p, "sales.view", "expenses.view", "reports.financial", "reports.sales") {
			tEnd, _ := time.Parse("2006-01-02", to)
			tStart, _ := time.Parse("2006-01-02", from)
			if tEnd.Sub(tStart) < 13*24*time.Hour {
				tStart = tEnd.AddDate(0, 0, -13)
			}
			q := sqlq.New()
			q.Add("a.type IN ('income','expense')")
			if biz == "shop" {
				q.Add("a.code NOT IN ('4300','5200')")
			} else if biz == "services" {
				q.Add("a.code IN ('4300','5200')")
			}
			f.branch(q, "l.branch_id")
			q.Add("l.entry_date >= ?::date", tStart.Format("2006-01-02"))
			q.Add("l.entry_date <= ?::date", tEnd.Format("2006-01-02"))
			ie, err := rows(ctx, tx, `
				SELECT gs.day::date AS date,
				       COALESCE(x.income, 0) AS income,
				       COALESCE(x.expenses, 0) AS expenses
				FROM generate_series(`+q.Arg(tStart.Format("2006-01-02"))+`::date, `+q.Arg(tEnd.Format("2006-01-02"))+`::date, interval '1 day') AS gs(day)
				LEFT JOIN (
					SELECT l.entry_date,
					       ROUND(SUM(CASE WHEN a.type = 'income' THEN l.credit - l.debit ELSE 0 END), 2) AS income,
					       ROUND(SUM(CASE WHEN a.type = 'expense' THEN l.debit - l.credit ELSE 0 END), 2) AS expenses
					FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id`+q.Where()+`
					GROUP BY l.entry_date
				) x ON x.entry_date = gs.day::date
				ORDER BY 1`, q.Args...)
			if err != nil {
				return err
			}
			out["income_vs_expenses"] = ie
		}

		if has(p, "accounts.view") {
			balances := p.IsOwner || has(p, "accounts.view_balance")
			q := sqlq.New()
			q.Add("ma.is_active")
			if f.Branches != nil {
				q.Add("(ma.branch_id IS NULL OR ma.branch_id = ANY(?))", f.Branches)
			}
			if !balances {
				q.Add("ma.type IN ('cash','petty_cash')")
			}
			accs, err := rows(ctx, tx, `
				SELECT ma.id, ma.name, ma.type, COALESCE((SELECT SUM(debit - credit) FROM journal_lines jl WHERE jl.money_account_id = ma.id), 0) AS balance
				FROM money_accounts ma`+q.Where()+` ORDER BY ma.type, ma.name`, q.Args...)
			if err != nil {
				return err
			}
			out["cash"] = map[string]any{"total": totals(accs, "", "balance")["balance"], "accounts": accs, "includes_bank_and_mobile": balances}
		}

		if has(p, "stock.view", "reports.inventory") {
			q := sqlq.New()
			q.Add("pr.kind = 'stock'")
			f.branch(q, "sl.branch_id")
			st, err := one(ctx, tx, `
				SELECT ROUND(COALESCE(SUM(sl.qty_on_hand * sl.avg_cost), 0), 2) AS value,
				       COUNT(*) FILTER (WHERE pr.is_active AND pr.reorder_level > 0 AND sl.qty_on_hand <= pr.reorder_level) AS low_stock_count
				FROM stock_levels sl JOIN products pr ON pr.id = sl.product_id`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			card := map[string]any{"low_stock_count": num(st, "low_stock_count")}
			if profit || has(p, "valuation.view") {
				card["value"] = num(st, "value")
			}
			lq := sqlq.New()
			lq.Add("pr.kind = 'stock' AND pr.is_active AND pr.reorder_level > 0 AND sl.qty_on_hand <= pr.reorder_level")
			f.branch(lq, "sl.branch_id")
			lowList, err := rows(ctx, tx, `
				SELECT pr.id, pr.name, sl.qty_on_hand, pr.reorder_level
				FROM stock_levels sl JOIN products pr ON pr.id = sl.product_id`+lq.Where()+`
				ORDER BY sl.qty_on_hand ASC LIMIT 6`, lq.Args...)
			if err != nil {
				return err
			}
			card["items"] = lowList
			out["stock"] = card
		}

		outstanding := map[string]any{}
		if has(p, "customers.view", "reports.debts") {
			q := sqlq.New()
			q.Add("s.kind = 'sale' AND s.status IN ('completed','partially_refunded') AND s.balance > 0")
			f.branch(q, "s.branch_id")
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(s.balance), 0) AS v FROM sales s`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["customers_owe"] = num(v, "v")
		}
		if has(p, "service_billing.view", "clients.view", "reports.debts") {
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(balance), 0) AS v FROM svc_invoices WHERE status IN ('issued','partial') AND balance > 0`)
			if err != nil {
				return err
			}
			outstanding["clients_owe"] = num(v, "v")
		}
		if has(p, "suppliers.view", "reports.debts") {
			q := sqlq.New()
			q.Add("i.status = 'posted' AND i.balance > 0")
			f.branch(q, "i.branch_id")
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(i.balance), 0) AS v FROM purchase_invoices i`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["we_owe_suppliers"] = num(v, "v")
		}
		if has(p, "contractors.view", "service_billing.view", "reports.debts") {
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(amount - settled), 0) AS v FROM svc_costs WHERE status = 'owed' AND amount > settled`)
			if err != nil {
				return err
			}
			outstanding["we_owe_contractors"] = num(v, "v")
		}
		if has(p, "payroll.view", "reports.payroll") {
			q := sqlq.New()
			q.Add("ps.status IN ('approved','partially_paid') AND r.status IN ('approved','paid')")
			if f.Branches != nil {
				q.Add("(r.branch_id IS NULL OR r.branch_id = ANY(?))", f.Branches)
			}
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(ps.net - ps.paid_total), 0) AS v FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["salaries_due"] = num(v, "v")
		}
		if len(outstanding) > 0 {
			out["outstanding"] = outstanding
		}

		// Services operational widgets for unified SRS Dashboard (projects by stage, overdue tasks, renewals due).
		if has(p, "projects.view") {
			stages, err := rows(ctx, tx, `
				SELECT st.name AS stage, count(*) AS count,
				       count(*) FILTER (WHERE st.status = 'stuck' OR (st.deadline IS NOT NULL AND st.deadline < $1::date)) AS late
				FROM svc_projects pr
				JOIN LATERAL (
					SELECT name, status, deadline FROM svc_project_stages
					WHERE project_id = pr.id AND status <> 'done'
					ORDER BY position LIMIT 1
				) st ON true
				WHERE pr.status = 'in_progress'
				GROUP BY st.name ORDER BY count DESC, st.name`, f.today)
			if err != nil {
				return err
			}
			out["projects_by_stage"] = stages
		}
		if has(p, "tasks.view") {
			tasks, err := rows(ctx, tx, `
				SELECT t.id, t.title, t.due_date, t.status, u.name AS assignee_name, pr.number AS project_number
				FROM svc_tasks t
				LEFT JOIN users u ON u.id = t.assignee_id
				LEFT JOIN svc_projects pr ON pr.id = t.project_id
				WHERE t.status IN ('todo','in_progress') AND t.due_date IS NOT NULL AND t.due_date < $1::date
				ORDER BY t.due_date ASC LIMIT 6`, f.today)
			if err != nil {
				return err
			}
			out["overdue_tasks"] = tasks
		}
		if has(p, "renewals.view") {
			rens, err := rows(ctx, tx, `
				SELECT r.id, r.item, r.due_date, (r.due_date - $1::date) AS days_left, c.name AS client_name
				FROM svc_renewals r JOIN svc_clients c ON c.id = r.client_id
				WHERE r.status = 'pending' AND r.due_date <= ($1::date + 30)
				ORDER BY r.due_date ASC LIMIT 6`, f.today)
			if err != nil {
				return err
			}
			out["renewals_due"] = rens
		}

		if has(p, "approvals.view") {
			q := sqlq.New()
			q.Add("status = 'pending'")
			if f.Branches != nil {
				q.Add("(branch_id IS NULL OR branch_id = ANY(?))", f.Branches)
			}
			list, err := rows(ctx, tx, `SELECT id, kind, summary, amount, created_at FROM approval_requests`+q.Where()+` ORDER BY created_at DESC LIMIT 5`, q.Args...)
			if err != nil {
				return err
			}
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM approval_requests`+q.Where(), q.Args...).Scan(&n); err != nil {
				return err
			}
			out["pending_approvals"] = map[string]any{"count": n, "latest": list}
		}
		if has(p, "audit.view") {
			q := sqlq.New()
			if f.Branches != nil {
				q.Add("(a.branch_id IS NULL OR a.branch_id = ANY(?))", f.Branches)
			}
			list, err := rows(ctx, tx, `
				SELECT a.id, a.action, a.entity_type, a.entity_id, a.created_at, u.name AS user_name
				FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id`+q.Where()+` ORDER BY a.created_at DESC LIMIT 10`, q.Args...)
			if err != nil {
				return err
			}
			out["recent_activity"] = list
		}
		return nil
	})
	return out, err
}

func salesTotals(ctx context.Context, tx pgx.Tx, f Filter) (map[string]any, error) {
	q := sqlq.New()
	src := linesSQL(q, f)
	return one(ctx, tx, `SELECT `+saleAgg+` FROM `+src, q.Args...)
}

func expenseTotal(ctx context.Context, tx pgx.Tx, f Filter) (decimal.Decimal, error) {
	q := sqlq.New()
	q.Add("e.status IN ('approved','paid')")
	f.dates(q, "e.expense_date")
	f.branch(q, "e.branch_id")
	v, err := one(ctx, tx, `SELECT COALESCE(SUM(e.amount), 0) AS v FROM expenses e`+q.Where(), q.Args...)
	return num(v, "v"), err
}

// Me is FR-DSH-09 for a cashier: own sales today, own till and clock status.
func (s *Service) Me(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	out := map[string]any{}
	f := Filter{}
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		out["business_date"] = f.today
		sales, err := one(ctx, tx, `
			SELECT count(*) AS count, COALESCE(SUM(total), 0) AS total, COALESCE(SUM(balance), 0) AS on_credit
			FROM sales WHERE cashier_id = $1 AND business_date = $2::date AND kind = 'sale'
			  AND status IN ('completed','partially_refunded','refunded')`, p.UserID, f.today)
		if err != nil {
			return err
		}
		out["sales_today"] = sales
		till, err := rows(ctx, tx, `
			SELECT t.id, t.status, t.opened_at, t.opening_float, ma.name AS account,
			       t.opening_float + COALESCE((SELECT SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END)
			         FROM payments WHERE till_session_id = t.id AND method = 'cash' AND status = 'posted'), 0) AS expected_cash
			FROM till_sessions t JOIN money_accounts ma ON ma.id = t.money_account_id
			WHERE t.cashier_id = $1 AND t.status = 'open'`, p.UserID)
		if err != nil {
			return err
		}
		if len(till) > 0 {
			out["till"] = till[0]
		} else {
			out["till"] = nil
		}
		out["attendance"] = nil
		if p.EmployeeID != nil {
			day, err := rows(ctx, tx, `
				SELECT day, status, first_in, last_out, late_minutes, worked_minutes
				FROM attendance_days WHERE employee_id = $1 AND day = $2::date`, *p.EmployeeID, f.today)
			if err != nil {
				return err
			}
			clockedIn := false
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE((SELECT type = 'in' FROM attendance_events WHERE employee_id = $1 ORDER BY at DESC LIMIT 1), false)`, *p.EmployeeID).Scan(&clockedIn); err != nil {
				return err
			}
			att := map[string]any{"clocked_in": clockedIn}
			if len(day) > 0 {
				att["today"] = day[0]
			}
			out["attendance"] = att
			// Commission earned this month (from posted/approved payslips or live commission estimate).
			mStart := f.today[:8] + "01"
			var comm decimal.Decimal
			_ = tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(pl.amount), 0)
				FROM payslip_lines pl
				JOIN payslips ps ON ps.id = pl.payslip_id
				JOIN payroll_runs r ON r.id = ps.run_id
				WHERE ps.employee_id = $1 AND pl.kind = 'commission' AND r.period_start >= $2::date AND r.status <> 'cancelled'`,
				*p.EmployeeID, mStart).Scan(&comm)
			out["commission_month"] = comm
		}
		// Staff assigned jobs (projects + design jobs), tasks and low-stock products (SRS Section 2A).
		myJobs, err := rows(ctx, tx, `
			SELECT * FROM (
				SELECT j.id, 'design_job' AS kind, j.title, c.name AS client_name, j.deadline AS due_date, j.status
				FROM design_jobs j JOIN svc_clients c ON c.id = j.client_id
				WHERE (j.designer_id = $1 OR j.created_by = $1) AND j.status NOT IN ('delivered','cancelled')
				UNION ALL
				SELECT pr.id, 'project' AS kind, pr.name AS title, c.name AS client_name,
				       (SELECT MIN(deadline) FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done') AS due_date,
				       pr.status
				FROM svc_projects pr JOIN svc_clients c ON c.id = pr.client_id
				WHERE (pr.owner_id = $1 OR pr.created_by = $1 OR EXISTS (SELECT 1 FROM svc_project_stages st WHERE st.project_id = pr.id AND st.owner_id = $1))
				  AND pr.status IN ('in_progress','on_hold')
			) x ORDER BY due_date ASC NULLS LAST LIMIT 10`, p.UserID)
		if err != nil {
			return err
		}
		out["my_jobs"] = myJobs
		myTasks, err := rows(ctx, tx, `
			SELECT t.id, t.title, t.due_date, t.status, (t.due_date IS NOT NULL AND t.due_date < $2::date) AS overdue,
			       pr.number AS project_number
			FROM svc_tasks t LEFT JOIN svc_projects pr ON pr.id = t.project_id
			WHERE t.assignee_id = $1 AND t.status IN ('todo','in_progress')
			ORDER BY t.due_date ASC NULLS LAST LIMIT 10`, p.UserID, f.today)
		if err != nil {
			return err
		}
		out["my_tasks"] = myTasks
		if p.BranchID != nil {
			low, err := rows(ctx, tx, `
				SELECT pr.id, pr.name, sl.qty_on_hand, pr.reorder_level
				FROM stock_levels sl JOIN products pr ON pr.id = sl.product_id
				WHERE sl.branch_id = $1 AND pr.kind = 'stock' AND pr.is_active AND pr.reorder_level > 0 AND sl.qty_on_hand <= pr.reorder_level
				ORDER BY sl.qty_on_hand ASC LIMIT 8`, *p.BranchID)
			if err != nil {
				return err
			}
			out["low_stock"] = low
		} else {
			out["low_stock"] = []map[string]any{}
		}
		return nil
	})
	return out, err
}

var _ = uuid.Nil
