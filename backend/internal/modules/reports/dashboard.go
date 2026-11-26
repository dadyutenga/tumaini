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
// (Monday to today) or month (first of month to today).
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

func hasAny(p *auth.Principal, keys ...string) bool {
	for _, k := range keys {
		if p.Has(k) {
			return true
		}
	}
	return false
}

// Dashboard assembles permission-scoped business & operations cards from the approved BMS schema.
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

		if biz != "services" && hasAny(p, "dashboard.business.view", "dashboard.operations.view", "shop.sale.view_all", "reports.operations.view") {
			c, err := salesTotals(ctx, tx, p.TenantID, cur)
			if err != nil {
				return err
			}
			pv, err := salesTotals(ctx, tx, p.TenantID, prev)
			if err != nil {
				return err
			}
			out["sales"] = map[string]any{
				"total": num(c, "total"), "count": num(c, "sales_count"), "previous_total": num(pv, "total"),
				"change_pct": pct(num(c, "total").Sub(num(pv, "total")), num(pv, "total")),
			}
			if profit {
				gp := num(c, "net_sales").Sub(num(c, "cost"))
				out["gross_profit"] = map[string]any{"amount": gp, "previous": num(pv, "net_sales").Sub(num(pv, "cost"))}
			}
			trendFrom, _ := time.Parse("2006-01-02", f.today)
			tf := f
			tf.From, tf.To = trendFrom.AddDate(0, 0, -29).Format("2006-01-02"), f.today
			q := sqlq.New()
			src := linesSQL(q, p.TenantID, tf)
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
			src = linesSQL(q, p.TenantID, cur)
			top, err := rows(ctx, tx, `
				SELECT l.product_id, pr.name::text AS product, ROUND(SUM(l.qty), 3) AS qty, ROUND(SUM(l.total), 2) AS total
				FROM `+src+` JOIN app.products pr ON pr.tenant_id = `+q.Arg(p.TenantID)+` AND pr.id = l.product_id
				GROUP BY l.product_id, pr.name ORDER BY total DESC LIMIT 5`, q.Args...)
			if err != nil {
				return err
			}
			out["top_products"] = top

			q = sqlq.New()
			src = linesSQL(q, p.TenantID, cur)
			byBranch, err := rows(ctx, tx, `
				SELECT l.branch_id, b.name::text AS branch, ROUND(SUM(l.total), 2) AS total, COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count
				FROM `+src+` JOIN app.locations b ON b.tenant_id = `+q.Arg(p.TenantID)+` AND b.id = l.branch_id
				GROUP BY l.branch_id, b.name ORDER BY total DESC`, q.Args...)
			if err != nil {
				return err
			}
			out["sales_by_branch"] = byBranch

			q = sqlq.New(p.TenantID)
			q.Add("pr.tenant_id = $1 AND pr.status = 'posted'")
			cur.dates(q, "pr.business_date")
			cur.branch(q, "pr.location_id")
			byMethod, err := rows(ctx, tx, `
				SELECT CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
				       COALESCE(SUM(pr.amount), 0) AS total
				FROM app.payments_received pr`+q.Where()+` GROUP BY 1 ORDER BY total DESC`, q.Args...)
			if err != nil {
				return err
			}
			out["sales_by_payment_method"] = byMethod
		}

		if biz != "shop" && hasAny(p, "dashboard.business.view", "dashboard.operations.view", "services.invoice.manage", "services.project.view_all", "reports.business.view") {
			svcInc := func(rng Filter) (decimal.Decimal, error) {
				q := sqlq.New(p.TenantID)
				q.Add("i.tenant_id = $1 AND i.status IN ('issued','partially_paid','paid')")
				rng.dates(q, "i.issue_date")
				v, err := one(ctx, tx, `SELECT COALESCE(SUM(i.total - i.tax_amount), 0) AS v FROM app.invoices i`+q.Where(), q.Args...)
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

		if hasAny(p, "dashboard.business.view", "reports.business.view", "finance.expense.approve", "finance.expense.enter") {
			e, err := expenseTotal(ctx, tx, p.TenantID, cur)
			if err != nil {
				return err
			}
			pe, err := expenseTotal(ctx, tx, p.TenantID, prev)
			if err != nil {
				return err
			}
			out["expenses"] = map[string]any{"total": e, "previous_total": pe, "change_pct": pct(e.Sub(pe), pe)}
		}

		if profit && hasAny(p, "dashboard.business.view", "reports.business.view") {
			r, err := s.pl(ctx, tx, p.TenantID, cur)
			if err != nil {
				return err
			}
			pr, err := s.pl(ctx, tx, p.TenantID, prev)
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

		// Daily income vs expenses line chart (at least 14 days).
		if hasAny(p, "dashboard.business.view", "dashboard.operations.view", "reports.business.view") {
			tEnd, _ := time.Parse("2006-01-02", to)
			tStart, _ := time.Parse("2006-01-02", from)
			if tEnd.Sub(tStart) < 13*24*time.Hour {
				tStart = tEnd.AddDate(0, 0, -13)
			}
			dFrom, dTo := tStart.Format("2006-01-02"), tEnd.Format("2006-01-02")
			q := sqlq.New(p.TenantID, dFrom, dTo)
			branchCond := ""
			if f.Branches != nil {
				branchCond = " AND location_id = ANY(" + q.Arg(f.Branches) + ")"
			}
			ie, err := rows(ctx, tx, `
				SELECT gs.day::date AS date,
				       COALESCE(x.income, 0) AS income,
				       COALESCE(x.expenses, 0) AS expenses
				FROM generate_series($2::date, $3::date, interval '1 day') AS gs(day)
				LEFT JOIN (
					SELECT d, ROUND(SUM(inc), 2) AS income, ROUND(SUM(exp), 2) AS expenses FROM (
						SELECT business_date AS d, total AS inc, 0::numeric AS exp FROM app.sales WHERE tenant_id = $1 AND status = 'completed' AND business_date BETWEEN $2::date AND $3::date`+branchCond+`
						UNION ALL
						SELECT issue_date, total, 0 FROM app.invoices WHERE tenant_id = $1 AND status IN ('issued','partially_paid','paid') AND issue_date BETWEEN $2::date AND $3::date
						UNION ALL
						SELECT expense_date, 0, amount FROM app.expenses WHERE tenant_id = $1 AND status IN ('approved','paid') AND expense_date BETWEEN $2::date AND $3::date`+branchCond+`
						UNION ALL
						SELECT bill_date, 0, amount FROM app.contractor_bills WHERE tenant_id = $1 AND status <> 'void' AND bill_date BETWEEN $2::date AND $3::date
					) u GROUP BY d
				) x ON x.d = gs.day::date
				ORDER BY 1`, q.Args...)
			if err != nil {
				return err
			}
			out["income_vs_expenses"] = ie
		}

		if hasAny(p, "finance.account.view", "dashboard.business.view") {
			q := sqlq.New(p.TenantID)
			q.Add("ma.tenant_id = $1 AND ma.status = 'active'")
			if f.Branches != nil {
				q.Add("(ma.location_id IS NULL OR ma.location_id = ANY(?))", f.Branches)
			}
			accs, err := rows(ctx, tx, `
				SELECT ma.id, ma.name::text AS name, ma.kind::text AS type, ma.current_balance AS balance
				FROM app.financial_accounts ma`+q.Where()+` ORDER BY ma.kind, ma.name`, q.Args...)
			if err != nil {
				return err
			}
			out["cash"] = map[string]any{"total": totals(accs, "", "balance")["balance"], "accounts": accs, "includes_bank_and_mobile": true}
		}

		if hasAny(p, "shop.stock.view", "reports.operations.view", "dashboard.operations.view", "dashboard.business.view") {
			q := sqlq.New(p.TenantID)
			q.Add("sl.tenant_id = $1")
			f.branch(q, "sl.location_id")
			st, err := one(ctx, tx, `
				SELECT ROUND(COALESCE(SUM(sl.quantity_on_hand * pr.average_cost), 0), 2) AS value,
				       COUNT(*) FILTER (WHERE pr.status = 'active' AND COALESCE(pr.min_stock_level, ts.low_stock_default) > 0 AND sl.quantity_on_hand <= COALESCE(pr.min_stock_level, ts.low_stock_default)) AS low_stock_count
				FROM app.stock_balances sl
				JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
				JOIN app.tenant_settings ts ON ts.tenant_id = sl.tenant_id`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			card := map[string]any{"low_stock_count": num(st, "low_stock_count")}
			if profit {
				card["value"] = num(st, "value")
			}
			lq := sqlq.New(p.TenantID)
			lq.Add("sl.tenant_id = $1 AND pr.status = 'active' AND COALESCE(pr.min_stock_level, ts.low_stock_default) > 0 AND sl.quantity_on_hand <= COALESCE(pr.min_stock_level, ts.low_stock_default)")
			f.branch(lq, "sl.location_id")
			lowList, err := rows(ctx, tx, `
				SELECT pr.id, pr.name::text AS name, sl.quantity_on_hand AS qty_on_hand, COALESCE(pr.min_stock_level, ts.low_stock_default) AS reorder_level
				FROM app.stock_balances sl
				JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
				JOIN app.tenant_settings ts ON ts.tenant_id = sl.tenant_id`+lq.Where()+`
				ORDER BY sl.quantity_on_hand ASC LIMIT 6`, lq.Args...)
			if err != nil {
				return err
			}
			card["items"] = lowList
			out["stock"] = card
		}

		outstanding := map[string]any{}
		if hasAny(p, "finance.debt.view", "shop.customer.view_all", "dashboard.business.view", "dashboard.operations.view") {
			q := sqlq.New(p.TenantID)
			q.Add("s.tenant_id = $1 AND s.status = 'completed' AND s.balance_due > 0")
			f.branch(q, "s.location_id")
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(s.balance_due), 0) AS v FROM app.sales s`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["customers_owe"] = num(v, "v")
		}
		if hasAny(p, "finance.debt.view", "services.invoice.manage", "services.client.view_all", "dashboard.business.view", "dashboard.operations.view") {
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(balance_due), 0) AS v FROM app.invoices WHERE tenant_id = $1 AND status IN ('issued','partially_paid') AND balance_due > 0`, p.TenantID)
			if err != nil {
				return err
			}
			outstanding["clients_owe"] = num(v, "v")
		}
		if hasAny(p, "finance.debt.view", "shop.supplier.manage", "dashboard.business.view", "dashboard.operations.view") {
			q := sqlq.New(p.TenantID)
			q.Add("pu.tenant_id = $1 AND pu.status <> 'cancelled' AND pu.balance_due > 0")
			f.branch(q, "pu.location_id")
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(pu.balance_due), 0) AS v FROM app.purchases pu`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["we_owe_suppliers"] = num(v, "v")
		}
		if hasAny(p, "finance.debt.view", "services.contractor.manage", "dashboard.business.view", "dashboard.operations.view") {
			v, err := one(ctx, tx, `SELECT COALESCE(SUM(balance_due), 0) AS v FROM app.contractor_bills WHERE tenant_id = $1 AND status IN ('open','partially_paid') AND balance_due > 0`, p.TenantID)
			if err != nil {
				return err
			}
			outstanding["we_owe_contractors"] = num(v, "v")
		}
		if hasAny(p, "hr.payroll.view", "dashboard.business.view") {
			q := sqlq.New(p.TenantID)
			q.Add("r.tenant_id = $1 AND r.status = 'approved'")
			f.branch(q, "r.location_id")
			v, err := one(ctx, tx, `
				SELECT COALESCE(SUM(pl.net_pay), 0) AS v
				FROM app.payroll_lines pl
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			outstanding["salaries_due"] = num(v, "v")
		}
		if len(outstanding) > 0 {
			out["outstanding"] = outstanding
		}

		if hasAny(p, "services.project.view_all", "services.project.manage") {
			stages, err := rows(ctx, tx, `
				SELECT st.name::text AS stage, count(*) AS count,
				       count(*) FILTER (WHERE st.status = 'stuck' OR (st.deadline IS NOT NULL AND st.deadline < $2::date)) AS late
				FROM app.projects pr
				JOIN LATERAL (
					SELECT name, status, deadline FROM app.project_stages
					WHERE tenant_id = pr.tenant_id AND project_id = pr.id AND status <> 'done'
					ORDER BY seq LIMIT 1
				) st ON true
				WHERE pr.tenant_id = $1 AND pr.status IN ('not_started','in_progress')
				GROUP BY st.name ORDER BY count DESC, st.name`, p.TenantID, f.today)
			if err != nil {
				return err
			}
			out["projects_by_stage"] = stages
		}
		if hasAny(p, "services.task.view_all", "services.task.manage") {
			tasks, err := rows(ctx, tx, `
				SELECT t.id, t.title::text AS title, t.due_at::date AS due_date,
				       CASE WHEN t.status = 'open' THEN 'todo' ELSE t.status::text END AS status,
				       au.full_name::text AS assignee_name, pr.project_no AS project_number
				FROM app.tasks t
				LEFT JOIN app.memberships am ON am.tenant_id = t.tenant_id AND am.id = t.assignee_id
				LEFT JOIN iam.users au ON au.id = am.user_id
				LEFT JOIN app.projects pr ON pr.tenant_id = t.tenant_id AND pr.id = t.project_id
				WHERE t.tenant_id = $1 AND t.status IN ('open','in_progress') AND t.due_at IS NOT NULL AND t.due_at::date < $2::date
				ORDER BY t.due_at ASC LIMIT 6`, p.TenantID, f.today)
			if err != nil {
				return err
			}
			out["overdue_tasks"] = tasks
		}
		if p.Has("services.renewal.manage") {
			rens, err := rows(ctx, tx, `
				SELECT r.id, r.description::text AS item, r.due_date, (r.due_date - $2::date) AS days_left, c.full_name::text AS client_name
				FROM app.renewals r JOIN app.customers c ON c.tenant_id = r.tenant_id AND c.id = r.customer_id
				WHERE r.tenant_id = $1 AND r.status = 'upcoming' AND r.due_date <= ($2::date + 30)
				ORDER BY r.due_date ASC LIMIT 6`, p.TenantID, f.today)
			if err != nil {
				return err
			}
			out["renewals_due"] = rens
		}

		if hasAny(p, "shop.discount.approve", "shop.return.approve", "shop.sale.void", "shop.product.price_approve", "services.price.approve", "finance.expense.approve", "finance.payment.void", "finance.transaction.correct", "finance.day.reopen", "hr.leave.approve") {
			list, err := rows(ctx, tx, `
				SELECT id, kind, COALESCE(reason, kind) AS summary, amount, requested_at AS created_at, source
				FROM app.v_pending_approvals WHERE tenant_id = $1
				ORDER BY requested_at DESC LIMIT 5`, p.TenantID)
			if err != nil {
				return err
			}
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.v_pending_approvals WHERE tenant_id = $1`, p.TenantID).Scan(&n); err != nil {
				return err
			}
			out["pending_approvals"] = map[string]any{"count": n, "latest": list}
		}
		if p.Has("security.audit.view") {
			list, err := rows(ctx, tx, `
				SELECT a.id, a.action, a.entity_table AS entity_type, a.entity_id, a.occurred_at AS created_at, u.full_name::text AS user_name
				FROM audit.audit_log a LEFT JOIN iam.users u ON u.id = a.actor_user_id
				WHERE a.tenant_id = $1 ORDER BY a.occurred_at DESC LIMIT 10`, p.TenantID)
			if err != nil {
				return err
			}
			out["recent_activity"] = list
		}
		return nil
	})
	return out, err
}

func salesTotals(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, f Filter) (map[string]any, error) {
	q := sqlq.New()
	src := linesSQL(q, tenantID, f)
	return one(ctx, tx, `SELECT `+saleAgg+` FROM `+src, q.Args...)
}

func expenseTotal(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, f Filter) (decimal.Decimal, error) {
	q := sqlq.New(tenantID)
	q.Add("e.tenant_id = $1 AND e.status IN ('approved','paid')")
	f.dates(q, "e.expense_date")
	f.branch(q, "e.location_id")
	v, err := one(ctx, tx, `SELECT COALESCE(SUM(e.amount), 0) AS v FROM app.expenses e`+q.Where(), q.Args...)
	return num(v, "v"), err
}

// Me returns the personal home summary for a staff member or cashier.
func (s *Service) Me(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	out := map[string]any{}
	f := Filter{}
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		out["business_date"] = f.today
		sales, err := one(ctx, tx, `
			SELECT count(*) AS count, COALESCE(SUM(total), 0) AS total, COALESCE(SUM(balance_due), 0) AS on_credit
			FROM app.sales WHERE tenant_id = $1 AND sold_by = $2 AND business_date = $3::date AND status = 'completed'`,
			p.TenantID, p.MembershipID, f.today)
		if err != nil {
			return err
		}
		out["sales_today"] = sales
		out["till"] = nil
		out["attendance"] = nil
		if p.EmployeeID != nil {
			day, err := rows(ctx, tx, `
				SELECT work_date AS day, status::text AS status, clock_in_at AS first_in, clock_out_at AS last_out,
				       minutes_late AS late_minutes,
				       CASE WHEN clock_in_at IS NOT NULL AND clock_out_at IS NOT NULL
				            THEN GREATEST(0, ROUND(EXTRACT(EPOCH FROM (clock_out_at - clock_in_at)) / 60)::int) ELSE 0 END AS worked_minutes
				FROM app.attendance_records WHERE tenant_id = $1 AND employee_id = $2 AND work_date = $3::date`, p.TenantID, *p.EmployeeID, f.today)
			if err != nil {
				return err
			}
			clockedIn := len(day) > 0 && day[0]["first_in"] != nil && day[0]["last_out"] == nil
			att := map[string]any{"clocked_in": clockedIn}
			if len(day) > 0 {
				att["today"] = day[0]
			}
			out["attendance"] = att

			mStart := f.today[:8] + "01"
			var comm decimal.Decimal
			_ = tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(amount), 0) FROM app.commission_entries
				WHERE tenant_id = $1 AND employee_id = $2 AND earned_on >= $3::date AND status <> 'reversed'`,
				p.TenantID, *p.EmployeeID, mStart).Scan(&comm)
			out["commission_month"] = comm
		}
		myJobs, err := rows(ctx, tx, `
			SELECT * FROM (
				SELECT j.id, 'design_job' AS kind, j.title::text AS title, c.full_name::text AS client_name, j.deadline AS due_date, j.status::text AS status
				FROM app.design_jobs j JOIN app.customers c ON c.tenant_id = j.tenant_id AND c.id = j.customer_id
				WHERE j.tenant_id = $1 AND (j.designer_id = $2 OR j.created_by = $2) AND j.status NOT IN ('delivered','cancelled')
				UNION ALL
				SELECT pr.id, 'project' AS kind, pr.title::text AS title, c.full_name::text AS client_name,
				       (SELECT MIN(deadline) FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done') AS due_date,
				       pr.status::text AS status
				FROM app.projects pr JOIN app.customers c ON c.tenant_id = pr.tenant_id AND c.id = pr.customer_id
				WHERE pr.tenant_id = $1
				  AND (pr.owner_id = $2 OR pr.created_by = $2 OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.owner_id = $2))
				  AND pr.status IN ('not_started','in_progress','on_hold')
			) x ORDER BY due_date ASC NULLS LAST LIMIT 10`, p.TenantID, p.MembershipID)
		if err != nil {
			return err
		}
		out["my_jobs"] = myJobs
		myTasks, err := rows(ctx, tx, `
			SELECT t.id, t.title::text AS title, t.due_at::date AS due_date,
			       CASE WHEN t.status = 'open' THEN 'todo' ELSE t.status::text END AS status,
			       (t.due_at IS NOT NULL AND t.due_at::date < $3::date) AS overdue,
			       pr.project_no AS project_number
			FROM app.tasks t LEFT JOIN app.projects pr ON pr.tenant_id = t.tenant_id AND pr.id = t.project_id
			WHERE t.tenant_id = $1 AND t.assignee_id = $2 AND t.status IN ('open','in_progress')
			ORDER BY t.due_at ASC NULLS LAST LIMIT 10`, p.TenantID, p.MembershipID, f.today)
		if err != nil {
			return err
		}
		out["my_tasks"] = myTasks
		if p.BranchID != nil {
			low, err := rows(ctx, tx, `
				SELECT pr.id, pr.name::text AS name, sl.quantity_on_hand AS qty_on_hand, COALESCE(pr.min_stock_level, ts.low_stock_default) AS reorder_level
				FROM app.stock_balances sl
				JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
				JOIN app.tenant_settings ts ON ts.tenant_id = sl.tenant_id
				WHERE sl.tenant_id = $1 AND sl.location_id = $2 AND pr.status = 'active'
				  AND COALESCE(pr.min_stock_level, ts.low_stock_default) > 0 AND sl.quantity_on_hand <= COALESCE(pr.min_stock_level, ts.low_stock_default)
				ORDER BY sl.quantity_on_hand ASC LIMIT 8`, p.TenantID, *p.BranchID)
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
