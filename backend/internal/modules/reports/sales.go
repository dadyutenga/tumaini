package reports

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/sqlq"
)

var labels = map[string]string{
	"period": "Period", "sales_count": "Sales", "qty": "Quantity", "gross_sales": "Gross sales", "discounts": "Discounts",
	"returns": "Returns", "tax": "Tax", "total": "Total", "net_sales": "Net sales", "cost": "Cost", "gross_profit": "Gross profit",
	"margin": "Margin %", "product": "Product", "sku": "SKU", "category": "Category", "cashier": "Cashier", "customer": "Customer",
	"method": "Method", "payments": "Payments", "amount_in": "Received", "refunds": "Refunds", "net": "Net",
	"number": "Session", "branch": "Branch", "opened_at": "Opened", "closed_at": "Closed", "status": "Status",
	"opening_float": "Opening float", "cash_sales": "Cash sales", "cash_in_other": "Other cash in", "pay_outs": "Pay-outs",
	"expected_cash": "Expected", "counted_cash": "Counted", "difference": "Difference", "average_sale": "Average sale",
	"count": "Count", "amount": "Amount", "paid": "Paid", "payee": "Payee", "month": "Month",
	"qty_on_hand": "On hand", "avg_cost": "Average cost", "value": "Value", "reorder_level": "Reorder level",
	"suggested_qty": "Suggested order", "last_sold": "Last sold", "days_idle": "Days without sale",
	"date": "Date", "type": "Type", "unit_cost": "Unit cost", "qty_after": "Balance", "source": "Source", "reason": "Reason",
	"supplier": "Supplier", "ordered_qty": "Ordered qty", "ordered_value": "Ordered", "received_qty": "Received qty",
	"received_value": "Received", "invoiced": "Invoiced", "outstanding_qty": "Outstanding qty",
	"current": "0-30 days", "d31_60": "31-60 days", "d61_90": "61-90 days", "over_90": "Over 90 days", "balance": "Balance",
	"overdue": "Overdue", "invoices": "Invoices", "credit_limit": "Credit limit",
	"run": "Run", "period_start": "From", "period_end": "To", "employees": "Employees", "gross": "Gross",
	"deductions": "Deductions", "employer": "Employer cost", "department": "Department", "label": "Item", "kind": "Kind",
	"employee": "Employee", "principal": "Principal", "repaid": "Repaid", "monthly_deduction": "Monthly deduction",
	"days_present": "Present", "days_absent": "Absent", "late_count": "Late", "late_minutes": "Late minutes",
	"attendance_rate": "Attendance %", "commission": "Commission", "leave_days": "Leave", "overtime_hours": "Overtime hours",
	"expenses": "Expenses", "other_income": "Other income", "net_profit": "Net profit", "stock_value": "Stock value",
	"account": "Account", "account_type": "Account type", "opening": "Opening", "money_in": "In", "money_out": "Out", "closing": "Closing",
	"code": "Code", "name": "Name", "debit": "Debit", "credit": "Credit", "section": "Section",
	"output_tax": "Output tax", "input_tax": "Input tax", "net_tax": "Tax payable", "taxable_sales": "Taxable sales",
	"taxable_purchases": "Taxable purchases",
}

func cols(keys ...string) []export.Col {
	out := make([]export.Col, len(keys))
	for i, k := range keys {
		l, ok := labels[k]
		if !ok {
			l = strings.ReplaceAll(k, "_", " ")
		}
		out[i] = export.Col{Key: k, Label: l}
	}
	return out
}

func badQuery(msg string) error { return httpx.BadRequest("invalid_query", msg) }

// SalesSummary is FR-REP-01: totals per day, week or month.
func (s *Service) SalesSummary(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	group := f.Q("group_by")
	if group == "" {
		group = "day"
	}
	if group != "day" && group != "week" && group != "month" {
		return export.Table{}, nil, badQuery("group_by must be day, week or month")
	}
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		g := q.Arg(group)
		src := linesSQL(q, f)
		var err error
		list, err = rows(ctx, tx, `SELECT date_trunc(`+g+`, l.d)::date AS period,`+saleAgg+` FROM `+src+` GROUP BY 1 ORDER BY 1`, q.Args...)
		return err
	})
	keys := stripCost(p, []string{"period", "sales_count", "gross_sales", "discounts", "returns", "tax", "net_sales", "cost", "gross_profit"})
	if !canCost(p) {
		dropKeys(list, "cost", "gross_profit")
	}
	return export.Table{Title: "Sales summary", Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", keys[1:]...)}, map[string]any{"group_by": group}, err
}

var breakdowns = map[string]struct {
	title, key, label, join string
	extra                   []string
}{
	"product":  {"Sales by product", "l.product_id", "pr.name AS product, pr.sku", "JOIN products pr ON pr.id = l.product_id", []string{"product", "sku"}},
	"category": {"Sales by category", "pr.category_id", "COALESCE(c.name, 'Uncategorised') AS category", "JOIN products pr ON pr.id = l.product_id LEFT JOIN categories c ON c.id = pr.category_id", []string{"category"}},
	"cashier":  {"Sales by cashier", "l.cashier_id", "u.name AS cashier", "JOIN users u ON u.id = l.cashier_id", []string{"cashier"}},
	"customer": {"Sales by customer", "l.customer_id", "COALESCE(cu.name, 'Walk-in') AS customer", "LEFT JOIN customers cu ON cu.id = l.customer_id", []string{"customer"}},
}

var breakdownSort = map[string]string{
	"revenue": "net_sales", "net_sales": "net_sales", "qty": "qty", "profit": "gross_profit", "gross_profit": "gross_profit",
	"margin": "margin", "count": "sales_count", "sales_count": "sales_count", "total": "total",
}

// Breakdown is FR-REP-02 and FR-REP-03 for product, category, cashier and customer.
// sort=-net_sales lists top sellers; sort=net_sales lists slow sellers.
func (s *Service) Breakdown(ctx context.Context, p *auth.Principal, f Filter, by string) (export.Table, map[string]any, error) {
	b := breakdowns[by]
	order := httpx.Sort(f.Q("sort"), breakdownSort, "net_sales DESC")
	if !canCost(p) && (strings.HasPrefix(order, "gross_profit") || strings.HasPrefix(order, "margin")) {
		order = "net_sales DESC"
	}
	limit := f.Int("limit", 1000, 1, 5000)
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		src := linesSQL(q, f)
		where := ""
		if by == "category" {
			if id := f.Q("category_id"); id != "" {
				where = " WHERE pr.category_id = " + q.Arg(id) + "::uuid"
			}
		}
		sql := `SELECT * FROM (SELECT ` + b.key + ` AS id, ` + b.label + `,` + saleAgg + `,
			CASE WHEN SUM(l.net) = 0 THEN NULL ELSE ROUND((SUM(l.net) - SUM(l.cost)) / SUM(l.net) * 100, 2) END AS margin
			FROM ` + src + ` ` + b.join + where + ` GROUP BY ` + b.key + `, ` + groupCols(by) + `) x
			ORDER BY ` + order + ` NULLS LAST LIMIT ` + q.Arg(limit)
		var err error
		list, err = rows(ctx, tx, sql, q.Args...)
		return err
	})
	keys := append(append([]string{}, b.extra...), "sales_count", "qty", "discounts", "returns", "tax", "net_sales", "cost", "gross_profit", "margin")
	keys = stripCost(p, keys)
	if !canCost(p) {
		dropKeys(list, "cost", "gross_profit", "margin")
	}
	return export.Table{Title: b.title, Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", "sales_count", "qty", "discounts", "returns", "tax", "net_sales")}, nil, err
}

func groupCols(by string) string {
	switch by {
	case "product":
		return "pr.name, pr.sku"
	case "category":
		return "c.name"
	case "cashier":
		return "u.name"
	default:
		return "cu.name"
	}
}

// ByPaymentMethod is FR-REP-03 for payment methods: sale receipts less refunds.
func (s *Service) ByPaymentMethod(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("pay.status = 'posted'")
		q.Add("pay.kind IN ('sale','customer_payment','sale_refund')")
		f.dates(q, "pay.payment_date")
		f.branch(q, "pay.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT pay.method,
			       COUNT(*) FILTER (WHERE pay.direction = 'in') AS payments,
			       COALESCE(SUM(pay.amount) FILTER (WHERE pay.direction = 'in'), 0) AS amount_in,
			       COALESCE(SUM(pay.amount) FILTER (WHERE pay.direction = 'out'), 0) AS refunds,
			       COALESCE(SUM(CASE WHEN pay.direction = 'in' THEN pay.amount ELSE -pay.amount END), 0) AS net
			FROM payments pay`+q.Where()+` GROUP BY pay.method ORDER BY net DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Sales by payment method", Subtitle: f.subtitle(), Columns: cols("method", "payments", "amount_in", "refunds", "net"),
		Rows: list, Totals: totals(list, "Total", "payments", "amount_in", "refunds", "net")}, nil, err
}

// Tills is FR-REP-04: one row per till session.
func (s *Service) Tills(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		var hasPayout bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='money_transfers' AND column_name='till_session_id')`).Scan(&hasPayout); err != nil {
			return err
		}
		payouts := "0::numeric"
		if hasPayout {
			payouts = "COALESCE((SELECT SUM(mt.amount + mt.fee) FROM money_transfers mt WHERE mt.till_session_id = t.id), 0)"
		}
		q := sqlq.New()
		if f.From != "" || f.To != "" {
			tz := q.Arg(f.tz)
			f.dates(q, "(t.opened_at AT TIME ZONE "+tz+")::date")
		}
		f.branch(q, "t.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT t.id, u.name AS cashier, b.name AS branch, t.status, t.opened_at, t.closed_at, t.opening_float,
			       COALESCE(c.cash_sales, 0) AS cash_sales, COALESCE(c.cash_in_other, 0) AS cash_in_other,
			       COALESCE(c.refunds, 0) AS refunds, `+payouts+` AS pay_outs,
			       COALESCE(t.expected_cash, t.opening_float + COALESCE(c.cash_sales, 0) + COALESCE(c.cash_in_other, 0) - COALESCE(c.refunds, 0) - COALESCE(c.cash_out_other, 0) - `+payouts+`) AS expected_cash,
			       t.counted_cash, t.difference
			FROM till_sessions t
			JOIN users u ON u.id = t.cashier_id
			JOIN branches b ON b.id = t.branch_id
			LEFT JOIN LATERAL (
				SELECT SUM(amount) FILTER (WHERE direction = 'in' AND kind = 'sale') AS cash_sales,
				       SUM(amount) FILTER (WHERE direction = 'in' AND kind <> 'sale') AS cash_in_other,
				       SUM(amount) FILTER (WHERE direction = 'out' AND kind = 'sale_refund') AS refunds,
				       SUM(amount) FILTER (WHERE direction = 'out' AND kind <> 'sale_refund') AS cash_out_other
				FROM payments WHERE till_session_id = t.id AND method = 'cash' AND status = 'posted'
			) c ON true`+q.Where()+` ORDER BY t.opened_at DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Till sessions", Subtitle: f.subtitle(),
		Columns: cols("cashier", "branch", "status", "opened_at", "closed_at", "opening_float", "cash_sales", "refunds", "pay_outs", "expected_cash", "counted_cash", "difference"),
		Rows:    list, Totals: totals(list, "Total", "opening_float", "cash_sales", "refunds", "pay_outs", "difference")}, nil, err
}
