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
	Lines                                                                   []map[string]any
	Income, Expense, Revenue, COGS, OtherIncome, ServiceIncome, ServiceCosts decimal.Decimal
	ExpensesByCat                                                           []map[string]any
}

// pl reads income and expense accounts from journal lines for the range.
func (s *Service) pl(ctx context.Context, tx pgx.Tx, f Filter) (plResult, error) {
	q := sqlq.New()
	q.Add("a.type IN ('income','expense')")
	if biz := f.Q("business"); biz == "shop" {
		q.Add("a.code NOT IN ('4300','5200')")
	} else if biz == "services" {
		q.Add("a.code IN ('4300','5200')")
	}
	f.dates(q, "l.entry_date")
	f.branch(q, "l.branch_id")
	lines, err := rows(ctx, tx, `
		SELECT a.type, a.code, a.name,
		       SUM(CASE WHEN a.type = 'income' THEN l.credit - l.debit ELSE l.debit - l.credit END) AS amount
		FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id`+q.Where()+`
		GROUP BY a.id, a.type, a.code, a.name
		HAVING SUM(l.debit + l.credit) <> 0
		ORDER BY a.code`, q.Args...)
	if err != nil {
		return plResult{}, err
	}
	r := plResult{Lines: lines, ExpensesByCat: []map[string]any{}}
	for _, ln := range lines {
		amt := num(ln, "amount")
		code, _ := ln["code"].(string)
		if ln["type"] == "income" {
			r.Income = r.Income.Add(amt)
			switch code {
			case "4000", "4010":
				r.Revenue = r.Revenue.Add(amt)
			case "4300":
				r.ServiceIncome = r.ServiceIncome.Add(amt)
			default:
				r.OtherIncome = r.OtherIncome.Add(amt)
			}
			continue
		}
		r.Expense = r.Expense.Add(amt)
		switch code {
		case "5000":
			r.COGS = r.COGS.Add(amt)
		case "5200":
			r.ServiceCosts = r.ServiceCosts.Add(amt)
		default:
			r.ExpensesByCat = append(r.ExpensesByCat, map[string]any{"code": code, "name": ln["name"], "amount": amt})
		}
	}
	return r, nil
}

// ProfitLoss is FR-REP-05. The fields lines, income, expenses and net_profit
// are kept for existing clients.
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
	"category": {"e.category_id", "c.name AS category", "JOIN expense_categories c ON c.id = e.category_id"},
	"branch":   {"e.branch_id", "b.name AS branch", "JOIN branches b ON b.id = e.branch_id"},
	"payee":    {"COALESCE(NULLIF(e.payee, ''), '-')", "COALESCE(NULLIF(e.payee, ''), '-') AS payee", ""},
	"month":    {"date_trunc('month', e.expense_date)::date", "date_trunc('month', e.expense_date)::date AS month", ""},
}

// Expenses is FR-REP-06: approved and paid expenses grouped.
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
		f.branch(q, "e.branch_id")
		gb := g.key
		if group == "category" {
			gb = "e.category_id, c.name"
		} else if group == "branch" {
			gb = "e.branch_id, b.name"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT `+g.label+`, COUNT(*) AS count, SUM(e.amount) AS amount,
			       COALESCE(SUM(e.amount) FILTER (WHERE e.status = 'paid'), 0) AS paid
			FROM expenses e `+g.join+q.Where()+` GROUP BY `+gb+` ORDER BY amount DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Expenses by " + group, Subtitle: f.subtitle(), Columns: cols(group, "count", "amount", "paid"),
		Rows: list, Totals: totals(list, "Total", "count", "amount", "paid")}, map[string]any{"group_by": group}, err
}

// CashFlow is FR-REP-14: per money account opening, in, out and closing, and
// movements grouped by the kind of document that caused them.
func (s *Service) CashFlow(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list, byKind []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		from, to := q.Arg(nullDate(f.From)), q.Arg(nullDate(f.To))
		acc := ""
		if f.Branches != nil {
			acc = " AND (ma.branch_id IS NULL OR ma.branch_id = ANY(" + q.Arg(f.Branches) + "))"
		}
		if !p.IsOwner {
			if _, ok := p.Can("accounts.view_balance"); !ok {
				acc += " AND ma.type IN ('cash','petty_cash')"
			}
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT ma.name AS account, ma.type AS account_type,
			       COALESCE(SUM(l.debit - l.credit) FILTER (WHERE l.entry_date < `+from+`::date), 0) AS opening,
			       COALESCE(SUM(l.debit) FILTER (WHERE (`+from+`::date IS NULL OR l.entry_date >= `+from+`::date) AND (`+to+`::date IS NULL OR l.entry_date <= `+to+`::date)), 0) AS money_in,
			       COALESCE(SUM(l.credit) FILTER (WHERE (`+from+`::date IS NULL OR l.entry_date >= `+from+`::date) AND (`+to+`::date IS NULL OR l.entry_date <= `+to+`::date)), 0) AS money_out,
			       COALESCE(SUM(l.debit - l.credit) FILTER (WHERE `+to+`::date IS NULL OR l.entry_date <= `+to+`::date), 0) AS closing
			FROM money_accounts ma
			LEFT JOIN journal_lines l ON l.money_account_id = ma.id
			WHERE true`+acc+`
			GROUP BY ma.id, ma.name, ma.type ORDER BY ma.type, ma.name`, q.Args...)
		if err != nil {
			return err
		}
		k := sqlq.New()
		k.Add("l.money_account_id IS NOT NULL")
		f.dates(k, "l.entry_date")
		if f.Branches != nil {
			k.Add("(ma.branch_id IS NULL OR ma.branch_id = ANY(?))", f.Branches)
		}
		if !p.IsOwner {
			if _, ok := p.Can("accounts.view_balance"); !ok {
				k.Add("ma.type IN ('cash','petty_cash')")
			}
		}
		byKind, err = rows(ctx, tx, `
			SELECT e.source_type AS kind, SUM(l.debit) AS money_in, SUM(l.credit) AS money_out, SUM(l.debit - l.credit) AS net
			FROM journal_lines l
			JOIN journal_entries e ON e.id = l.entry_id
			JOIN money_accounts ma ON ma.id = l.money_account_id`+k.Where()+`
			GROUP BY e.source_type ORDER BY e.source_type`, k.Args...)
		return err
	})
	return export.Table{Title: "Cash flow", Subtitle: f.subtitle(), Columns: cols("account", "account_type", "opening", "money_in", "money_out", "closing"),
		Rows: list, Totals: totals(list, "Total", "opening", "money_in", "money_out", "closing")}, map[string]any{"by_kind": byKind}, err
}

// BalanceSheet is FR-REP-16. It is business-wide: some entries carry no branch,
// so a branch slice would not balance. Current earnings close income and expense into equity.
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
		bal, err := rows(ctx, tx, `
			SELECT a.type, a.code, a.name,
			       SUM(CASE WHEN a.type IN ('asset','expense') THEN l.debit - l.credit ELSE l.credit - l.debit END) AS amount
			FROM journal_lines l JOIN ledger_accounts a ON a.id = l.ledger_account_id
			WHERE l.entry_date <= $1::date
			GROUP BY a.id, a.type, a.code, a.name
			HAVING SUM(l.debit - l.credit) <> 0
			ORDER BY a.code`, asOf)
		if err != nil {
			return err
		}
		list = []map[string]any{}
		for _, r := range bal {
			amt := num(r, "amount")
			switch r["type"] {
			case "asset":
				assets = assets.Add(amt)
			case "liability":
				liab = liab.Add(amt)
			case "equity":
				equity = equity.Add(amt)
			case "income":
				earnings = earnings.Add(amt)
				continue
			case "expense":
				earnings = earnings.Sub(amt)
				continue
			}
			r["section"] = r["type"]
			list = append(list, r)
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

// Tax is FR-REP-17: output tax on sales net of returns and input tax on
// purchases net of returns, per month, from the tax ledger accounts.
func (s *Service) Tax(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("a.code IN ('2100','1500','4000','4010')")
		f.dates(q, "l.entry_date")
		f.branch(q, "l.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT date_trunc('month', l.entry_date)::date AS month,
			       COALESCE(SUM(l.credit - l.debit) FILTER (WHERE a.code IN ('4000','4010')), 0) AS taxable_sales,
			       COALESCE(SUM(l.credit - l.debit) FILTER (WHERE a.code = '2100' AND e.source_type IN ('sale','sale_return')), 0) AS output_tax,
			       COALESCE(SUM(l.debit - l.credit) FILTER (WHERE a.code = '1500'), 0) AS input_tax,
			       COALESCE(SUM(l.credit - l.debit) FILTER (WHERE a.code = '2100' AND e.source_type IN ('sale','sale_return')), 0)
			         - COALESCE(SUM(l.debit - l.credit) FILTER (WHERE a.code = '1500'), 0) AS net_tax
			FROM journal_lines l
			JOIN ledger_accounts a ON a.id = l.ledger_account_id
			JOIN journal_entries e ON e.id = l.entry_id`+q.Where()+`
			GROUP BY 1 ORDER BY 1`, q.Args...)
		return err
	})
	return export.Table{Title: "Tax report", Subtitle: f.subtitle(), Columns: cols("month", "taxable_sales", "output_tax", "input_tax", "net_tax"),
		Rows: list, Totals: totals(list, "Total", "taxable_sales", "output_tax", "input_tax", "net_tax")}, nil, err
}

// nullDate turns an empty date into SQL NULL.
func nullDate(d string) any {
	if d == "" {
		return nil
	}
	return d
}
