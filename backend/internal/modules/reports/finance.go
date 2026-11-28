package reports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/sqlq"
)

type plResult struct {
	Lines                                                                    []map[string]any
	Income, Expense, Revenue, COGS, OtherIncome, ServiceIncome, ServiceCosts decimal.Decimal
	ExpensesByCat                                                            []map[string]any
}

// pl builds the accrual-basis Profit & Loss from completed sales, returns, issued invoices,
// approved/paid expenses, contractor bills, and approved/paid payroll runs.
func (s *Service) pl(ctx context.Context, tx pgx.Tx, f Filter) (plResult, error) {
	biz := f.Q("business")
	r := plResult{Lines: []map[string]any{}, ExpensesByCat: []map[string]any{}}

	if biz != "services" {
		q := sqlq.New()
		src := linesSQL(q, f)
		st, err := one(ctx, tx, `SELECT ROUND(COALESCE(SUM(l.net), 0), 2) AS net_sales, ROUND(COALESCE(SUM(l.cost), 0), 2) AS cost FROM `+src, q.Args...)
		if err != nil {
			return r, err
		}
		r.Revenue = num(st, "net_sales")
		r.COGS = num(st, "cost")
		if !r.Revenue.IsZero() {
			r.Lines = append(r.Lines, map[string]any{"code": "4000", "name": "Sales revenue", "type": "income", "amount": r.Revenue})
		}
		if !r.COGS.IsZero() {
			r.Lines = append(r.Lines, map[string]any{"code": "5000", "name": "Cost of goods sold", "type": "expense", "amount": r.COGS})
		}
	}

	if biz != "shop" {
		iq := sqlq.New()
		iq.Add("i.status IN ('issued','partially_paid','paid')")
		f.dates(iq, "i.issue_date")
		iv, err := one(ctx, tx, `SELECT COALESCE(SUM(i.total - i.tax_amount), 0) AS amt FROM app.invoices i`+iq.Where(), iq.Args...)
		if err != nil {
			return r, err
		}
		r.ServiceIncome = num(iv, "amt")
		if !r.ServiceIncome.IsZero() {
			r.Lines = append(r.Lines, map[string]any{"code": "4300", "name": "Service income", "type": "income", "amount": r.ServiceIncome})
		}

		cbq := sqlq.New()
		cbq.Add("b.status <> 'void'")
		f.dates(cbq, "b.bill_date")
		cb, err := one(ctx, tx, `SELECT COALESCE(SUM(b.amount), 0) AS amt FROM app.contractor_bills b`+cbq.Where(), cbq.Args...)
		if err != nil {
			return r, err
		}
		peq := sqlq.New()
		peq.Add("e.status IN ('approved','paid') AND (e.project_id IS NOT NULL OR e.design_job_id IS NOT NULL)")
		f.dates(peq, "e.expense_date")
		f.branch(peq, "e.location_id")
		pe, err := one(ctx, tx, `SELECT COALESCE(SUM(e.amount), 0) AS amt FROM app.expenses e`+peq.Where(), peq.Args...)
		if err != nil {
			return r, err
		}
		r.ServiceCosts = num(cb, "amt").Add(num(pe, "amt"))
		if !r.ServiceCosts.IsZero() {
			r.Lines = append(r.Lines, map[string]any{"code": "5200", "name": "Service direct costs", "type": "expense", "amount": r.ServiceCosts})
		}
	}

	if biz != "services" {
		eq := sqlq.New()
		eq.Add("e.status IN ('approved','paid') AND e.project_id IS NULL AND e.design_job_id IS NULL")
		f.dates(eq, "e.expense_date")
		f.branch(eq, "e.location_id")
		expCats, err := rows(ctx, tx, `
			SELECT c.name::text AS name, COALESCE(SUM(e.amount), 0) AS amount
			FROM app.expenses e
			JOIN app.expense_categories c ON c.id = e.category_id`+eq.Where()+`
			GROUP BY c.id, c.name HAVING SUM(e.amount) <> 0 ORDER BY amount DESC`, eq.Args...)
		if err != nil {
			return r, err
		}
		for _, ec := range expCats {
			amt := num(ec, "amount")
			r.Expense = r.Expense.Add(amt)
			r.ExpensesByCat = append(r.ExpensesByCat, map[string]any{"code": "6000", "name": ec["name"], "amount": amt})
			r.Lines = append(r.Lines, map[string]any{"code": "6000", "name": ec["name"], "type": "expense", "amount": amt})
		}

		// Include approved/paid payroll runs in operating expenses.
		pq := sqlq.New()
		pq.Add("pr.status IN ('approved','paid')")
		if f.From != "" {
			pq.Add("make_date(pr.period_year, pr.period_month, 1) >= date_trunc('month', ?::date)::date", f.From)
		}
		if f.To != "" {
			pq.Add("make_date(pr.period_year, pr.period_month, 1) <= date_trunc('month', ?::date)::date", f.To)
		}
		f.branch(pq, "pr.location_id")
		payRow, err := one(ctx, tx, `
			SELECT COALESCE(SUM(pl.gross_pay), 0) + COALESCE(SUM(si.employer_total), 0) AS amt
			FROM app.payroll_runs pr
			JOIN app.payroll_lines pl ON pl.payroll_run_id = pr.id
			LEFT JOIN (
				SELECT payroll_line_id, SUM(employer_amount) AS employer_total
				FROM app.payroll_statutory_items
				GROUP BY payroll_line_id
			) si ON si.payroll_line_id = pl.id`+pq.Where(), pq.Args...)
		if err != nil {
			return r, err
		}
		payAmt := num(payRow, "amt")
		if !payAmt.IsZero() {
			r.Expense = r.Expense.Add(payAmt)
			r.ExpensesByCat = append(r.ExpensesByCat, map[string]any{"code": "6100", "name": "Salaries and payroll", "amount": payAmt})
			r.Lines = append(r.Lines, map[string]any{"code": "6100", "name": "Salaries and payroll", "type": "expense", "amount": payAmt})
		}
	}

	r.Income = r.Revenue.Add(r.ServiceIncome).Add(r.OtherIncome)
	r.Expense = r.Expense.Add(r.COGS).Add(r.ServiceCosts)
	return r, nil
}

// ProfitLoss computes P&L with optional previous-period comparison.
func (s *Service) ProfitLoss(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var cur, prev plResult
	var prevRange map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if cur, err = s.pl(ctx, tx, f); err != nil {
			return err
		}
		if f.Q("compare") == "previous" {
			if f.From == "" || f.To == "" {
				return badQuery("compare=previous needs both from and to")
			}
			pf := f
			pf.From, pf.To = previousRange(f.From, f.To)
			if prev, err = s.pl(ctx, tx, pf); err != nil {
				return err
			}
			prevRange = map[string]any{"from": pf.From, "to": pf.To}
		}
		return nil
	})
	extra := summary(cur)
	extra["lines"] = cur.Lines
	if prevRange != nil {
		ps := summary(prev)
		ps["from"], ps["to"] = prevRange["from"], prevRange["to"]
		ps["lines"] = prev.Lines
		extra["previous"] = ps
		extra["change"] = map[string]any{
			"net_profit":     cur.Income.Sub(cur.Expense).Sub(prev.Income.Sub(prev.Expense)),
			"gross_profit":   cur.Revenue.Sub(cur.COGS).Sub(prev.Revenue.Sub(prev.COGS)),
			"net_profit_pct": pct(cur.Income.Sub(cur.Expense).Sub(prev.Income.Sub(prev.Expense)), prev.Income.Sub(prev.Expense).Abs()),
		}
	}
	return export.Table{Title: "Profit and loss", Subtitle: f.subtitle(), Columns: cols("code", "name", "type", "amount"), Rows: cur.Lines,
		Totals: map[string]any{"name": "Net profit", "amount": cur.Income.Sub(cur.Expense)}}, extra, err
}

func summary(r plResult) map[string]any {
	opex := r.Expense.Sub(r.COGS).Sub(r.ServiceCosts)
	return map[string]any{
		"income": r.Income, "expenses": r.Expense, "net_profit": r.Income.Sub(r.Expense),
		"sales_revenue": r.Revenue, "cost_of_goods_sold": r.COGS, "gross_profit": r.Revenue.Sub(r.COGS),
		"service_income": r.ServiceIncome, "service_costs": r.ServiceCosts, "service_profit": r.ServiceIncome.Sub(r.ServiceCosts),
		"other_income": r.OtherIncome, "operating_expenses": opex, "expenses_by_category": r.ExpensesByCat,
		"businesses": map[string]any{
			"shop":     map[string]any{"income": r.Revenue, "cost_of_goods": r.COGS, "profit": r.Revenue.Sub(r.COGS)},
			"services": map[string]any{"income": r.ServiceIncome, "direct_costs": r.ServiceCosts, "profit": r.ServiceIncome.Sub(r.ServiceCosts)},
			"shared":   map[string]any{"other_income": r.OtherIncome, "operating_expenses": opex},
			"total":    map[string]any{"income": r.Income, "expenses": r.Expense, "net_profit": r.Income.Sub(r.Expense)},
		},
	}
}

// previousRange returns the range of equal length that ends the day before from.
func previousRange(from, to string) (string, string) {
	f, _ := time.Parse("2006-01-02", from)
	t, _ := time.Parse("2006-01-02", to)
	days := int(t.Sub(f).Hours()/24) + 1
	pt := f.AddDate(0, 0, -1)
	pf := pt.AddDate(0, 0, -(days - 1))
	return pf.Format("2006-01-02"), pt.Format("2006-01-02")
}

var expenseGroups = map[string]struct{ key, label, join string }{
	"category": {"e.category_id", "c.name::text AS category", "JOIN app.expense_categories c ON c.id = e.category_id"},
	"branch":   {"e.location_id", "b.name::text AS branch", "JOIN app.locations b ON b.id = e.location_id"},
	"payee":    {"COALESCE(NULLIF(e.description::text, ''), '-')", "COALESCE(NULLIF(e.description::text, ''), '-') AS payee", ""},
	"month":    {"date_trunc('month', e.expense_date)::date", "date_trunc('month', e.expense_date)::date AS month", ""},
}

// Expenses groups approved and paid expenses.
func (s *Service) Expenses(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	group := f.Q("group_by")
	if group == "" {
		group = "category"
	}
	g, ok := expenseGroups[group]
	if !ok {
		return export.Table{}, nil, badQuery("group_by must be category, branch, payee or month")
	}
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("e.status IN ('approved','paid')")
		f.dates(q, "e.expense_date")
		f.branch(q, "e.location_id")
		gb := g.key
		if group == "category" {
			gb = "e.category_id, c.name"
		} else if group == "branch" {
			gb = "e.location_id, b.name"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT `+g.label+`, COUNT(*) AS count, SUM(e.amount) AS amount,
			       COALESCE(SUM(e.amount) FILTER (WHERE e.status = 'paid'), 0) AS paid
			FROM app.expenses e `+g.join+q.Where()+` GROUP BY `+gb+` ORDER BY amount DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Expenses by " + group, Subtitle: f.subtitle(), Columns: cols(group, "count", "amount", "paid"),
		Rows: list, Totals: totals(list, "Total", "count", "amount", "paid")}, map[string]any{"group_by": group}, err
}

// CashFlow summarizes inflows and outflows per financial account and by transaction type.
func (s *Service) CashFlow(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list, byKind []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		from, to := q.Arg(nullDate(f.From)), q.Arg(nullDate(f.To))
		acc := " "
		if f.Branches != nil {
			acc += " AND (ma.location_id IS NULL OR ma.location_id = ANY(" + q.Arg(f.Branches) + "))"
		}
		if !p.Has("finance.account.view") {
			acc += " AND ma.kind IN ('cash','petty_cash')"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT ma.name::text AS account, ma.kind::text AS account_type,
			       COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END) FILTER (WHERE t.business_date < `+from+`::date), 0) AS opening,
			       COALESCE(SUM(t.amount) FILTER (WHERE t.direction = 'in' AND (`+from+`::date IS NULL OR t.business_date >= `+from+`::date) AND (`+to+`::date IS NULL OR t.business_date <= `+to+`::date)), 0) AS money_in,
			       COALESCE(SUM(t.amount) FILTER (WHERE t.direction = 'out' AND (`+from+`::date IS NULL OR t.business_date >= `+from+`::date) AND (`+to+`::date IS NULL OR t.business_date <= `+to+`::date)), 0) AS money_out,
			       COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END) FILTER (WHERE `+to+`::date IS NULL OR t.business_date <= `+to+`::date), 0) AS closing
			FROM app.financial_accounts ma
			LEFT JOIN app.financial_transactions t ON t.account_id = ma.id
			WHERE true`+acc+`
			GROUP BY ma.id, ma.name, ma.kind ORDER BY ma.kind, ma.name`, q.Args...)
		if err != nil {
			return err
		}
		k := sqlq.New()
		f.dates(k, "t.business_date")
		if f.Branches != nil {
			k.Add("(ma.location_id IS NULL OR ma.location_id = ANY(?))", f.Branches)
		}
		if !p.Has("finance.account.view") {
			k.Add("ma.kind IN ('cash','petty_cash')")
		}
		byKind, err = rows(ctx, tx, `
			SELECT t.txn_type::text AS kind,
			       COALESCE(SUM(t.amount) FILTER (WHERE t.direction = 'in'), 0) AS money_in,
			       COALESCE(SUM(t.amount) FILTER (WHERE t.direction = 'out'), 0) AS money_out,
			       COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END), 0) AS net
			FROM app.financial_transactions t
			JOIN app.financial_accounts ma ON ma.id = t.account_id`+k.Where()+`
			GROUP BY t.txn_type ORDER BY t.txn_type`, k.Args...)
		return err
	})
	return export.Table{Title: "Cash flow", Subtitle: f.subtitle(), Columns: cols("account", "account_type", "opening", "money_in", "money_out", "closing"),
		Rows: list, Totals: totals(list, "Total", "opening", "money_in", "money_out", "closing")}, map[string]any{"by_kind": byKind}, err
}

// BalanceSheet produces a management snapshot of cash, receivables, stock, payables, and equity as of a date.
func (s *Service) BalanceSheet(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	var asOf string
	assets, liab, equity, earnings := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		asOf = f.Q("as_of")
		if asOf == "" {
			asOf = f.To
		}
		if asOf == "" {
			asOf = f.today
		}
		list = []map[string]any{}

		// Cash & financial accounts as of date.
		cashRows, err := rows(ctx, tx, `
			SELECT ma.kind::text AS kind, ma.name::text AS name,
			       COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END), 0) AS amount
			FROM app.financial_accounts ma
			LEFT JOIN app.financial_transactions t ON t.account_id = ma.id AND t.business_date <= $1::date
			GROUP BY ma.id, ma.kind, ma.name
			HAVING COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END), 0) <> 0
			ORDER BY ma.kind, ma.name`, asOf)
		if err != nil {
			return err
		}
		for _, cr := range cashRows {
			amt := num(cr, "amount")
			assets = assets.Add(amt)
			list = append(list, map[string]any{"section": "asset", "code": "1000", "name": cr["name"], "amount": amt})
		}

		// Receivables (credit sales + invoices).
		arRow, err := one(ctx, tx, `
			SELECT COALESCE((SELECT SUM(balance_due) FROM app.sales WHERE status = 'completed' AND business_date <= $1::date AND balance_due > 0), 0)
			     + COALESCE((SELECT SUM(balance_due) FROM app.invoices WHERE status IN ('issued','partially_paid') AND issue_date <= $1::date AND balance_due > 0), 0) AS amt`,
			asOf)
		if err != nil {
			return err
		}
		if ar := num(arRow, "amt"); !ar.IsZero() {
			assets = assets.Add(ar)
			list = append(list, map[string]any{"section": "asset", "code": "1100", "name": "Accounts receivable", "amount": ar})
		}

		// Inventory valuation as of date.
		invRow, err := one(ctx, tx, `
			SELECT COALESCE(ROUND(SUM(q.qty * pr.average_cost), 2), 0) AS amt
			FROM (
				SELECT product_id, SUM(quantity_delta) AS qty
				FROM app.stock_movements WHERE business_date <= $1::date
				GROUP BY product_id
			) q
			JOIN app.products pr ON pr.id = q.product_id`, asOf)
		if err != nil {
			return err
		}
		if inv := num(invRow, "amt"); !inv.IsZero() {
			assets = assets.Add(inv)
			list = append(list, map[string]any{"section": "asset", "code": "1200", "name": "Inventory", "amount": inv})
		}

		// Payables (suppliers + contractors).
		apRow, err := one(ctx, tx, `
			SELECT COALESCE((SELECT SUM(balance_due) FROM app.purchases WHERE status <> 'cancelled' AND order_date <= $1::date AND balance_due > 0), 0)
			     + COALESCE((SELECT SUM(balance_due) FROM app.contractor_bills WHERE status IN ('open','partially_paid') AND bill_date <= $1::date AND balance_due > 0), 0) AS amt`,
			asOf)
		if err != nil {
			return err
		}
		if ap := num(apRow, "amt"); !ap.IsZero() {
			liab = liab.Add(ap)
			list = append(list, map[string]any{"section": "liability", "code": "2000", "name": "Accounts payable", "amount": ap})
		}

		// Unpaid approved payroll.
		prRow, err := one(ctx, tx, `
			SELECT COALESCE(SUM(pl.net_pay), 0) AS amt
			FROM app.payroll_lines pl
			JOIN app.payroll_runs r ON r.id = pl.payroll_run_id
			WHERE r.status = 'approved' AND make_date(r.period_year, r.period_month, 1) <= $1::date`, asOf)
		if err != nil {
			return err
		}
		if prAmt := num(prRow, "amt"); !prAmt.IsZero() {
			liab = liab.Add(prAmt)
			list = append(list, map[string]any{"section": "liability", "code": "2200", "name": "Salaries payable", "amount": prAmt})
		}

		// Current earnings (cumulative P&L up to asOf) & owner capital balancing line.
		plRes, err := s.pl(ctx, tx, Filter{To: asOf, Query: map[string]string{}})
		if err != nil {
			return err
		}
		earnings = plRes.Income.Sub(plRes.Expense)
		equity = assets.Sub(liab).Sub(earnings)
		if !equity.IsZero() {
			list = append(list, map[string]any{"section": "equity", "code": "3000", "name": "Owner equity / opening capital", "amount": equity})
		}
		list = append(list, map[string]any{"section": "equity", "code": "", "name": "Current earnings", "amount": earnings})
		return nil
	})
	totalEquity := equity.Add(earnings)
	extra := map[string]any{
		"as_of": asOf, "assets": assets, "liabilities": liab, "equity": totalEquity, "current_earnings": earnings,
		"liabilities_and_equity": liab.Add(totalEquity), "balanced": assets.Equal(liab.Add(totalEquity)),
	}
	return export.Table{Title: "Balance sheet", Subtitle: "As of " + asOf, Columns: cols("section", "code", "name", "amount"), Rows: list,
		Totals: map[string]any{"name": "Assets / liabilities and equity", "amount": assets}}, extra, err
}

// Tax summarizes output tax (sales + invoices - returns) and input tax (purchases) by month.
func (s *Service) Tax(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		sc := "s.status = 'completed'"
		rc := "r.status = 'completed'"
		ic := "i.status IN ('issued','partially_paid','paid')"
		pc := "pu.status <> 'cancelled'"
		if f.From != "" {
			a := q.Arg(f.From)
			sc += " AND s.business_date >= " + a + "::date"
			rc += " AND r.business_date >= " + a + "::date"
			ic += " AND i.issue_date >= " + a + "::date"
			pc += " AND pu.order_date >= " + a + "::date"
		}
		if f.To != "" {
			a := q.Arg(f.To)
			sc += " AND s.business_date <= " + a + "::date"
			rc += " AND r.business_date <= " + a + "::date"
			ic += " AND i.issue_date <= " + a + "::date"
			pc += " AND pu.order_date <= " + a + "::date"
		}
		if f.Branches != nil {
			a := q.Arg(f.Branches)
			sc += " AND s.location_id = ANY(" + a + ")"
			rc += " AND r.location_id = ANY(" + a + ")"
			pc += " AND pu.location_id = ANY(" + a + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT month,
			       ROUND(SUM(taxable_sales), 2) AS taxable_sales,
			       ROUND(SUM(output_tax), 2) AS output_tax,
			       ROUND(SUM(input_tax), 2) AS input_tax,
			       ROUND(SUM(output_tax) - SUM(input_tax), 2) AS net_tax
			FROM (
				SELECT date_trunc('month', s.business_date)::date AS month, (s.total - s.tax_amount) AS taxable_sales, s.tax_amount AS output_tax, 0::numeric AS input_tax
				FROM app.sales s WHERE `+sc+`
				UNION ALL
				SELECT date_trunc('month', r.business_date)::date,
				       -(ROUND(sl.line_total * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2)
				       - ROUND(sl.tax_amount * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2)),
				       -ROUND(sl.tax_amount * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2), 0
				FROM app.sale_returns r
				JOIN app.sales s ON s.id = r.sale_id
				JOIN app.sale_return_lines rl ON rl.sale_return_id = r.id
				JOIN app.sale_lines sl ON sl.id = rl.sale_line_id
				WHERE `+rc+`
				UNION ALL
				SELECT date_trunc('month', i.issue_date)::date, (i.total - i.tax_amount), i.tax_amount, 0
				FROM app.invoices i WHERE `+ic+`
				UNION ALL
				SELECT date_trunc('month', pu.order_date)::date, 0, 0, pu.tax_amount
				FROM app.purchases pu WHERE `+pc+`
			) x
			GROUP BY month ORDER BY month`, q.Args...)
		return err
	})
	return export.Table{Title: "Tax report", Subtitle: f.subtitle(), Columns: cols("month", "taxable_sales", "output_tax", "input_tax", "net_tax"),
		Rows: list, Totals: totals(list, "Total", "taxable_sales", "output_tax", "input_tax", "net_tax")}, nil, err
}

func nullDate(d string) any {
	if d == "" {
		return nil
	}
	return d
}
