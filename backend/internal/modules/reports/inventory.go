package reports

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/sqlq"
)

func optUUID(f Filter, key string) *uuid.UUID {
	id, err := uuid.Parse(f.Q(key))
	if err != nil {
		return nil
	}
	return &id
}

// OnHand is FR-REP-07 stock on hand. Cost columns need the cost permission.
func (s *Service) OnHand(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("pr.kind = 'stock'")
		f.branch(q, "sl.branch_id")
		if c := optUUID(f, "category_id"); c != nil {
			q.Add("pr.category_id = ?", *c)
		}
		q.If(f.Q("q") != "", "(pr.name ILIKE '%' || ? || '%' OR pr.sku ILIKE '%' || ? || '%')", f.Q("q"), f.Q("q"))
		var err error
		list, err = rows(ctx, tx, `
			SELECT pr.id AS product_id, pr.name AS product, pr.sku, COALESCE(c.name, 'Uncategorised') AS category, b.name AS branch,
			       sl.qty_on_hand, pr.reorder_level, ROUND(sl.avg_cost, 4) AS avg_cost, ROUND(sl.qty_on_hand * sl.avg_cost, 2) AS value
			FROM stock_levels sl
			JOIN products pr ON pr.id = sl.product_id
			JOIN branches b ON b.id = sl.branch_id
			LEFT JOIN categories c ON c.id = pr.category_id`+q.Where()+`
			ORDER BY pr.name, b.name`, q.Args...)
		return err
	})
	keys := stripCost(p, []string{"product", "sku", "category", "branch", "qty_on_hand", "reorder_level", "avg_cost", "value"})
	if !canCost(p) {
		dropKeys(list, "avg_cost", "value")
	}
	return export.Table{Title: "Stock on hand", Columns: cols(keys...), Rows: list, Totals: totals(list, "Total", stripCost(p, []string{"qty_on_hand", "value"})...)}, nil, err
}

var valuationGroups = map[string]struct{ sel, group string }{
	"product":  {"pr.name AS product, pr.sku, COALESCE(c.name, 'Uncategorised') AS category, SUM(v.qty) AS qty_on_hand, CASE WHEN SUM(v.qty) = 0 THEN 0 ELSE ROUND(SUM(v.qty * v.avg) / SUM(v.qty), 4) END AS avg_cost", "pr.id, pr.name, pr.sku, c.name"},
	"category": {"COALESCE(c.name, 'Uncategorised') AS category, SUM(v.qty) AS qty_on_hand", "c.name"},
	"branch":   {"b.name AS branch, SUM(v.qty) AS qty_on_hand", "b.id, b.name"},
}

// Valuation is FR-INV-28: quantity, average cost and value now or at a past
// date. For a past date the last movement on or before that day gives the
// quantity and average cost after it.
func (s *Service) Valuation(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	group := f.Q("group_by")
	if group == "" {
		group = "product"
	}
	g, ok := valuationGroups[group]
	if !ok {
		return export.Table{}, nil, badQuery("group_by must be product, category or branch")
	}
	asOf := f.Q("as_of")
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		var src string
		if asOf == "" {
			src = `(SELECT sl.branch_id, sl.product_id, sl.qty_on_hand AS qty, sl.avg_cost AS avg FROM stock_levels sl` + branchWhere(q, f, "sl.branch_id") + `)`
		} else {
			tz := q.Arg(f.tz)
			day := q.Arg(asOf)
			src = `(SELECT DISTINCT ON (m.branch_id, m.product_id) m.branch_id, m.product_id, m.qty_after AS qty, m.avg_cost_after AS avg
				FROM stock_movements m WHERE (m.created_at AT TIME ZONE ` + tz + `)::date <= ` + day + `::date` + branchAnd(q, f, "m.branch_id") + `
				ORDER BY m.branch_id, m.product_id, m.created_at DESC, m.id DESC)`
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT `+g.sel+`, ROUND(SUM(v.qty * v.avg), 2) AS value
			FROM `+src+` v
			JOIN products pr ON pr.id = v.product_id AND pr.kind = 'stock'
			JOIN branches b ON b.id = v.branch_id
			LEFT JOIN categories c ON c.id = pr.category_id
			GROUP BY `+g.group+`
			HAVING SUM(v.qty) <> 0 OR SUM(v.qty * v.avg) <> 0
			ORDER BY value DESC`, q.Args...)
		return err
	})
	keys := map[string][]string{
		"product":  {"product", "sku", "category", "qty_on_hand", "avg_cost", "value"},
		"category": {"category", "qty_on_hand", "value"},
		"branch":   {"branch", "qty_on_hand", "value"},
	}[group]
	sub := "Today"
	if asOf != "" {
		sub = "As of " + asOf
	}
	return export.Table{Title: "Inventory valuation by " + group, Subtitle: sub, Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", "qty_on_hand", "value")}, map[string]any{"as_of": asOf, "group_by": group}, err
}

func branchWhere(q *sqlq.Q, f Filter, col string) string {
	if f.Branches == nil {
		return ""
	}
	return " WHERE " + col + " = ANY(" + q.Arg(f.Branches) + ")"
}

func branchAnd(q *sqlq.Q, f Filter, col string) string {
	if f.Branches == nil {
		return ""
	}
	return " AND " + col + " = ANY(" + q.Arg(f.Branches) + ")"
}

var movementTypes = map[string]bool{"opening": true, "stock_in": true, "stock_out": true, "purchase": true, "purchase_return": true,
	"sale": true, "sale_return": true, "transfer_out": true, "transfer_in": true, "adjustment": true}

// Movements is FR-REP-07 movement history, paginated with page and per_page.
func (s *Service) Movements(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	typ := f.Q("type")
	if typ != "" && !movementTypes[typ] {
		return export.Table{}, nil, badQuery("type is not a known movement type")
	}
	page := f.Int("page", 1, 1, 1000000)
	per := f.Int("per_page", 100, 1, 1000)
	var list []map[string]any
	var total int64
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		f.dates(q, "m.created_at::date")
		f.branch(q, "m.branch_id")
		q.If(typ != "", "m.type = ?", typ)
		if id := optUUID(f, "product_id"); id != nil {
			q.Add("m.product_id = ?", *id)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_movements m`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT m.created_at::date AS date, m.created_at, pr.name AS product, pr.sku, b.name AS branch, m.type,
			       m.qty, ROUND(m.unit_cost, 4) AS unit_cost, ROUND(m.qty * m.unit_cost, 2) AS value, m.qty_after,
			       m.source_type AS source, m.source_id
			FROM stock_movements m
			JOIN products pr ON pr.id = m.product_id
			JOIN branches b ON b.id = m.branch_id`+q.Where()+`
			ORDER BY m.created_at DESC, m.id`+q.Page(per, (page-1)*per), q.Args...)
		return err
	})
	keys := stripCost(p, []string{"date", "product", "sku", "branch", "type", "qty", "unit_cost", "value", "qty_after", "source"})
	if !canCost(p) {
		dropKeys(list, "unit_cost", "value")
	}
	return export.Table{Title: "Stock movements", Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list},
		map[string]any{"meta": map[string]any{"page": page, "per_page": per, "total": total}}, err
}

// LowStock is FR-INV-26: products at or below reorder level with a suggested order.
func (s *Service) LowStock(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("pr.kind = 'stock' AND pr.is_active AND pr.reorder_level > 0 AND sl.qty_on_hand <= pr.reorder_level")
		f.branch(q, "sl.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT pr.id AS product_id, pr.name AS product, pr.sku, b.name AS branch, sl.qty_on_hand, pr.reorder_level,
			       GREATEST(pr.reorder_level * 2 - sl.qty_on_hand, 0) AS suggested_qty
			FROM stock_levels sl
			JOIN products pr ON pr.id = sl.product_id
			JOIN branches b ON b.id = sl.branch_id`+q.Where()+`
			ORDER BY sl.qty_on_hand - pr.reorder_level, pr.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Low stock", Columns: cols("product", "sku", "branch", "qty_on_hand", "reorder_level", "suggested_qty"), Rows: list}, nil, err
}

// DeadStock lists products with stock and no sale in the last N days (days, default 90).
func (s *Service) DeadStock(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	days := f.Int("days", 90, 1, 3650)
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		ref := f.To
		if ref == "" {
			ref = f.today
		}
		q := sqlq.New()
		r := q.Arg(ref)
		d := q.Arg(days)
		q.Add("pr.kind = 'stock' AND sl.qty_on_hand > 0")
		f.branch(q, "sl.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT * FROM (
				SELECT pr.name AS product, pr.sku, b.name AS branch, sl.qty_on_hand, ROUND(sl.qty_on_hand * sl.avg_cost, 2) AS value,
				       ls.last_sold, (`+r+`::date - COALESCE(ls.last_sold, pr.created_at::date)) AS days_idle
				FROM stock_levels sl
				JOIN products pr ON pr.id = sl.product_id
				JOIN branches b ON b.id = sl.branch_id
				LEFT JOIN LATERAL (
					SELECT max(s.business_date) AS last_sold FROM sale_items si JOIN sales s ON s.id = si.sale_id
					WHERE si.product_id = sl.product_id AND s.branch_id = sl.branch_id AND s.kind = 'sale'
					  AND s.status IN ('completed','partially_refunded','refunded')
				) ls ON true`+q.Where()+`
			) x WHERE x.days_idle >= `+d+` ORDER BY x.days_idle DESC, x.product`, q.Args...)
		return err
	})
	keys := stripCost(p, []string{"product", "sku", "branch", "qty_on_hand", "value", "last_sold", "days_idle"})
	if !canCost(p) {
		dropKeys(list, "value")
	}
	return export.Table{Title: "Dead stock", Subtitle: "No sale for at least the chosen number of days", Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", stripCost(p, []string{"qty_on_hand", "value"})...)}, map[string]any{"days": days}, err
}

// Expiring lists receipt lines whose expiry date falls within the next N days, or has passed (FR-INV-32).
// Expiry is recorded per receipt, not per batch of stock on hand, so qty_received is what arrived with
// that receipt, and qty_on_hand is the product's total in that branch today.
func (s *Service) Expiring(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	days := f.Int("days", 30, 0, 3650)
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		ref := f.To
		if ref == "" {
			ref = f.today
		}
		q := sqlq.New()
		r := q.Arg(ref)
		d := q.Arg(days)
		q.Add("x.expiry_date <= " + r + "::date + " + d + "::int")
		f.branch(q, "x.branch_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT x.source, x.number, pr.name AS product, pr.sku, b.name AS branch, x.batch_no,
			       x.expiry_date::text AS expiry_date, (x.expiry_date - `+r+`::date) AS days_left,
			       x.qty_received, COALESCE(sl.qty_on_hand, 0) AS qty_on_hand
			FROM (
				SELECT 'stock_entry' AS source, e.number, e.branch_id, i.product_id, i.batch_no, i.expiry_date, i.qty AS qty_received
				FROM stock_entry_items i JOIN stock_entries e ON e.id = i.entry_id
				WHERE e.direction = 'in' AND i.expiry_date IS NOT NULL
				UNION ALL
				SELECT 'goods_receipt', g.number, g.branch_id, gi.product_id, gi.batch_no, gi.expiry_date, gi.qty_received
				FROM goods_receipt_items gi JOIN goods_receipts g ON g.id = gi.receipt_id
				WHERE gi.expiry_date IS NOT NULL
			) x
			JOIN products pr ON pr.id = x.product_id
			JOIN branches b ON b.id = x.branch_id
			LEFT JOIN stock_levels sl ON sl.product_id = x.product_id AND sl.branch_id = x.branch_id`+q.Where()+`
			ORDER BY x.expiry_date, pr.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Expiring stock", Subtitle: "Receipt lines expiring within the chosen number of days, or already expired",
		Columns: cols("source", "number", "product", "sku", "branch", "batch_no", "expiry_date", "days_left", "qty_received", "qty_on_hand"),
		Rows:    list}, map[string]any{"days": days}, err
}

// Losses lists stock out, downward adjustments and transfer shortfalls with their cost.
func (s *Service) Losses(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		tz := q.Arg(f.tz)
		mq := "(m.type = 'stock_out' OR (m.type = 'adjustment' AND m.qty < 0))"
		if f.From != "" {
			mq += " AND (m.created_at AT TIME ZONE " + tz + ")::date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			mq += " AND (m.created_at AT TIME ZONE " + tz + ")::date <= " + q.Arg(f.To) + "::date"
		}
		tq := "t.status = 'received_with_difference' AND ti.qty_received < ti.qty_sent"
		if f.From != "" {
			tq += " AND (t.received_at AT TIME ZONE " + tz + ")::date >= " + q.Arg(f.From) + "::date"
		}
		if f.To != "" {
			tq += " AND (t.received_at AT TIME ZONE " + tz + ")::date <= " + q.Arg(f.To) + "::date"
		}
		if f.Branches != nil {
			b := q.Arg(f.Branches)
			mq += " AND m.branch_id = ANY(" + b + ")"
			tq += " AND t.to_branch_id = ANY(" + b + ")"
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT x.date, x.reason, pr.name AS product, pr.sku, b.name AS branch, x.qty, ROUND(x.value, 2) AS value
			FROM (
				SELECT (m.created_at AT TIME ZONE `+tz+`)::date AS date,
				       CASE WHEN m.type = 'stock_out' THEN COALESCE(se.reason, 'stock_out') ELSE 'adjustment' END AS reason,
				       m.product_id, m.branch_id, -m.qty AS qty, -m.qty * m.unit_cost AS value
				FROM stock_movements m
				LEFT JOIN stock_entries se ON se.id = m.source_id AND m.source_type = 'stock_entry'
				WHERE `+mq+`
				UNION ALL
				SELECT (t.received_at AT TIME ZONE `+tz+`)::date, 'transfer_loss', ti.product_id, t.to_branch_id,
				       ti.qty_sent - ti.qty_received, (ti.qty_sent - ti.qty_received) * ti.unit_cost
				FROM stock_transfer_items ti JOIN stock_transfers t ON t.id = ti.transfer_id
				WHERE `+tq+`
			) x
			JOIN products pr ON pr.id = x.product_id
			JOIN branches b ON b.id = x.branch_id
			ORDER BY x.date DESC, pr.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Stock losses", Subtitle: f.subtitle(), Columns: cols("date", "reason", "product", "sku", "branch", "qty", "value"),
		Rows: list, Totals: totals(list, "Total", "qty", "value")}, nil, err
}

// Purchases is FR-REP-08: ordered against received and invoiced.
func (s *Service) Purchases(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	group := f.Q("group_by")
	if group == "" {
		group = "supplier"
	}
	if group != "supplier" && group != "product" && group != "month" {
		return export.Table{}, nil, badQuery("group_by must be supplier, product or month")
	}
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		cond := func(dateCol, branchCol string) string {
			c := ""
			if f.From != "" {
				c += " AND " + dateCol + " >= " + q.Arg(f.From) + "::date"
			}
			if f.To != "" {
				c += " AND " + dateCol + " <= " + q.Arg(f.To) + "::date"
			}
			if f.Branches != nil {
				c += " AND " + branchCol + " = ANY(" + q.Arg(f.Branches) + ")"
			}
			return c
		}
		// One row per source line: ordered (PO items), received (GRN items), invoiced (invoices).
		src := `
			SELECT po.supplier_id, pi.product_id, date_trunc('month', po.order_date)::date AS month,
			       pi.qty_ordered AS oq, pi.line_total AS ov, 0::numeric AS rq, 0::numeric AS rv, 0::numeric AS iv
			FROM purchase_order_items pi JOIN purchase_orders po ON po.id = pi.po_id
			WHERE po.status NOT IN ('draft','cancelled','pending_approval')` + cond("po.order_date", "po.branch_id") + `
			UNION ALL
			SELECT g.supplier_id, gi.product_id, date_trunc('month', g.received_date)::date, 0, 0, gi.qty_received, gi.qty_received * gi.unit_cost, 0
			FROM goods_receipt_items gi JOIN goods_receipts g ON g.id = gi.receipt_id
			WHERE true` + cond("g.received_date", "g.branch_id")
		if group != "product" {
			src += `
			UNION ALL
			SELECT i.supplier_id, NULL, date_trunc('month', i.invoice_date)::date, 0, 0, 0, 0, i.total
			FROM purchase_invoices i WHERE i.status = 'posted'` + cond("i.invoice_date", "i.branch_id")
		}
		sel := map[string]string{
			"supplier": "sp.name AS supplier",
			"product":  "pr.name AS product, pr.sku",
			"month":    "x.month",
		}[group]
		join := map[string]string{
			"supplier": "JOIN suppliers sp ON sp.id = x.supplier_id",
			"product":  "JOIN products pr ON pr.id = x.product_id",
			"month":    "",
		}[group]
		gb := map[string]string{"supplier": "sp.id, sp.name", "product": "pr.id, pr.name, pr.sku", "month": "x.month"}[group]
		var err error
		list, err = rows(ctx, tx, `
			SELECT `+sel+`, ROUND(SUM(x.oq), 3) AS ordered_qty, ROUND(SUM(x.ov), 2) AS ordered_value,
			       ROUND(SUM(x.rq), 3) AS received_qty, ROUND(SUM(x.rv), 2) AS received_value, ROUND(SUM(x.iv), 2) AS invoiced,
			       ROUND(GREATEST(SUM(x.oq) - SUM(x.rq), 0), 3) AS outstanding_qty
			FROM (`+src+`) x `+join+` GROUP BY `+gb+` ORDER BY 1`, q.Args...)
		return err
	})
	first := map[string][]string{"supplier": {"supplier"}, "product": {"product", "sku"}, "month": {"month"}}[group]
	keys := append(first, "ordered_qty", "ordered_value", "received_qty", "received_value", "invoiced", "outstanding_qty")
	if group == "product" {
		keys = append(first, "ordered_qty", "ordered_value", "received_qty", "received_value", "outstanding_qty")
	}
	return export.Table{Title: "Purchases by " + group, Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list,
		Totals: totals(list, "Total", "ordered_value", "received_value", "invoiced")}, map[string]any{"group_by": group}, err
}

// CustomerDebt is FR-REP-09: unpaid sale balances per customer in age buckets
// counted from the due date (or sale date) to the as-of date.
func (s *Service) CustomerDebt(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	asOf := ""
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		asOf = f.To
		if asOf == "" {
			asOf = f.today
		}
		q := sqlq.New()
		d := q.Arg(asOf)
		q.Add("s.balance > 0 AND s.kind = 'sale' AND s.status IN ('completed','partially_refunded')")
		f.branch(q, "s.branch_id")
		var err error
		list, err = rows(ctx, tx, aged(`
			SELECT c.id, c.name AS customer, c.phone, c.credit_limit,
			       `+d+`::date - COALESCE(s.due_date, s.business_date) AS age,
			       `+d+`::date > COALESCE(s.due_date, s.business_date) AS is_overdue, s.balance AS amt
			FROM sales s JOIN customers c ON c.id = s.customer_id`+q.Where(), "id, customer, phone, credit_limit", "customer"), q.Args...)
		return err
	})
	return export.Table{Title: "Customer debt", Subtitle: "As of " + asOf, Columns: cols("customer", "phone", "credit_limit", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance"),
		Rows: list, Totals: totals(list, "Total", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance")}, map[string]any{"as_of": asOf}, err
}

// SupplierDebt is FR-REP-10: unpaid purchase invoices per supplier by age.
func (s *Service) SupplierDebt(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	asOf := ""
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		asOf = f.To
		if asOf == "" {
			asOf = f.today
		}
		q := sqlq.New()
		d := q.Arg(asOf)
		q.Add("i.balance > 0 AND i.status = 'posted'")
		f.branch(q, "i.branch_id")
		var err error
		list, err = rows(ctx, tx, aged(`
			SELECT sp.id, sp.name AS supplier, sp.phone,
			       `+d+`::date - i.invoice_date AS age,
			       `+d+`::date > COALESCE(i.due_date, i.invoice_date) AS is_overdue, i.balance AS amt
			FROM purchase_invoices i JOIN suppliers sp ON sp.id = i.supplier_id`+q.Where(), "id, supplier, phone", "supplier"), q.Args...)
		return err
	})
	return export.Table{Title: "Supplier debt", Subtitle: "As of " + asOf, Columns: cols("supplier", "phone", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance"),
		Rows: list, Totals: totals(list, "Total", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance")}, map[string]any{"as_of": asOf}, err
}

// aged buckets a per-document query with columns age, is_overdue and amt.
func aged(inner, keys, order string) string {
	return `SELECT ` + keys + `, COUNT(*) AS invoices,
		COALESCE(SUM(amt) FILTER (WHERE age <= 30), 0) AS current,
		COALESCE(SUM(amt) FILTER (WHERE age BETWEEN 31 AND 60), 0) AS d31_60,
		COALESCE(SUM(amt) FILTER (WHERE age BETWEEN 61 AND 90), 0) AS d61_90,
		COALESCE(SUM(amt) FILTER (WHERE age > 90), 0) AS over_90,
		COALESCE(SUM(amt) FILTER (WHERE is_overdue), 0) AS overdue,
		SUM(amt) AS balance
		FROM (` + inner + `) d GROUP BY ` + keys + ` ORDER BY balance DESC, ` + order
}
