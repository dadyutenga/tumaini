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
		from, to, pFrom, pTo, ok := periodRange(period, f.today)
		if !ok {
			return badQuery("period must be today, week or month")
		}
		out["period"] = map[string]any{"name": period, "from": from, "to": to, "previous_from": pFrom, "previous_to": pTo}
		out["branch_ids"] = f.Branches
		cur, prev := f, f
		cur.From, cur.To = from, to
		prev.From, prev.To = pFrom, pTo
		profit := canCost(p)

		if has(p, "sales.view", "reports.sales") {
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
			out["profit"] = map[string]any{
				"gross_profit": r.Revenue.Sub(r.COGS), "net_profit": r.Income.Sub(r.Expense),
				"income": r.Income, "expenses": r.Expense,
			}
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
		}
		return nil
	})
	return out, err
}

var _ = uuid.Nil
