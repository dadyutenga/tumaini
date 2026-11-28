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
	"attendance_rate": "Attendance %", "business": "Business", "commission": "Commission", "leave_days": "Leave", "overtime_hours": "Overtime hours",
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

// SalesSummary is totals per day, week or month.
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
	"product":  {"Sales by product", "l.product_id", "pr.name::text AS product, pr.sku", "JOIN app.products pr ON pr.id = l.product_id", []string{"product", "sku"}},
	"category": {"Sales by category", "pr.category_id", "COALESCE(c.name::text, 'Uncategorised') AS category", "JOIN app.products pr ON pr.id = l.product_id LEFT JOIN app.product_categories c ON c.id = pr.category_id", []string{"category"}},
	"cashier":  {"Sales by cashier", "m.user_id", "u.full_name::text AS cashier", "JOIN app.memberships m ON m.id = l.sold_by JOIN iam.users u ON u.id = m.user_id", []string{"cashier"}},
	"customer": {"Sales by customer", "l.customer_id", "COALESCE(cu.full_name::text, 'Walk-in') AS customer", "LEFT JOIN app.customers cu ON cu.id = l.customer_id", []string{"customer"}},
}

var breakdownSort = map[string]string{
	"revenue": "net_sales", "net_sales": "net_sales", "qty": "qty", "profit": "gross_profit", "gross_profit": "gross_profit",
	"margin": "margin", "count": "sales_count", "sales_count": "sales_count", "total": "total",
}

// Breakdown lists sales grouped by product, category, cashier, or customer.
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
		return "u.full_name"
	default:
		return "cu.full_name"
	}
}

// ByPaymentMethod reports customer receipts less refunds by payment method.
func (s *Service) ByPaymentMethod(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		rc := "pr.status = 'posted'"
		mc := "pm.status = 'posted' AND pm.customer_id IS NOT NULL"
		if f.From != "" {
			a := q.Arg(f.From)
			rc += " AND pr.business_date >= " + a + "::date"
			mc += " AND pm.business_date >= " + a + "::date"
		}
		if f.To != "" {
			a := q.Arg(f.To)
			rc += " AND pr.business_date <= " + a + "::date"
			mc += " AND pm.business_date <= " + a + "::date"
		}
		if f.Branches != nil {
			a := q.Arg(f.Branches)
			rc += " AND pr.location_id = ANY(" + a + ")"
			mc += " AND pm.location_id = ANY(" + a + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT method,
			       COUNT(*) FILTER (WHERE dir = 'in') AS payments,
			       COALESCE(SUM(amount) FILTER (WHERE dir = 'in'), 0) AS amount_in,
			       COALESCE(SUM(amount) FILTER (WHERE dir = 'out'), 0) AS refunds,
			       COALESCE(SUM(CASE WHEN dir = 'in' THEN amount ELSE -amount END), 0) AS net
			FROM (
				SELECT CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method, 'in'::text AS dir, pr.amount
				FROM app.payments_received pr WHERE `+rc+`
				UNION ALL
				SELECT CASE WHEN pm.method = 'bank_transfer' THEN 'bank' ELSE pm.method::text END AS method, 'out'::text AS dir, pm.amount
				FROM app.payments_made pm WHERE `+mc+`
			) x
			GROUP BY method ORDER BY net DESC`, q.Args...)
		return err
	})
	return export.Table{Title: "Sales by payment method", Subtitle: f.subtitle(), Columns: cols("method", "payments", "amount_in", "refunds", "net"),
		Rows: list, Totals: totals(list, "Total", "payments", "amount_in", "refunds", "net")}, nil, err
}

// Tills returns an empty report because till sessions are replaced by day closings in the approved BMS schema.
func (s *Service) Tills(_ context.Context, _ *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	return export.Table{
		Title: "Till sessions", Subtitle: f.subtitle(),
		Columns: cols("cashier", "branch", "status", "opened_at", "closed_at", "opening_float", "cash_sales", "refunds", "pay_outs", "expected_cash", "counted_cash", "difference"),
		Rows:    []map[string]any{},
		Totals:  map[string]any{"label": "Total"},
	}, nil, nil
}
