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

// OnHand lists current stock per product and location. Cost columns require cost permission.
func (s *Service) OnHand(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("sl.tenant_id = $1")
		f.branch(q, "sl.location_id")
		if c := optUUID(f, "category_id"); c != nil {
			q.Add("pr.category_id = ?", *c)
		}
		q.If(f.Q("q") != "", "(pr.name ILIKE '%' || ? || '%' OR pr.sku ILIKE '%' || ? || '%')", f.Q("q"), f.Q("q"))
		var err error
		list, err = rows(ctx, tx, `
			SELECT pr.id AS product_id, pr.name::text AS product, pr.sku, COALESCE(c.name::text, 'Uncategorised') AS category,
			       b.name::text AS branch, sl.quantity_on_hand AS qty_on_hand,
			       COALESCE(pr.min_stock_level, ts.low_stock_default) AS reorder_level,
			       ROUND(pr.average_cost, 4) AS avg_cost, ROUND(sl.quantity_on_hand * pr.average_cost, 2) AS value
			FROM app.stock_balances sl
			JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
			JOIN app.locations b ON b.tenant_id = sl.tenant_id AND b.id = sl.location_id
			JOIN app.tenant_settings ts ON ts.tenant_id = sl.tenant_id
			LEFT JOIN app.product_categories c ON c.tenant_id = pr.tenant_id AND c.id = pr.category_id`+q.Where()+`
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
	"product":  {"pr.name::text AS product, pr.sku, COALESCE(c.name::text, 'Uncategorised') AS category, SUM(v.qty) AS qty_on_hand, CASE WHEN SUM(v.qty) = 0 THEN 0 ELSE ROUND(SUM(v.qty * pr.average_cost) / SUM(v.qty), 4) END AS avg_cost", "pr.id, pr.name, pr.sku, c.name"},
	"category": {"COALESCE(c.name::text, 'Uncategorised') AS category, SUM(v.qty) AS qty_on_hand", "c.name"},
	"branch":   {"b.name::text AS branch, SUM(v.qty) AS qty_on_hand", "b.id, b.name"},
}

// Valuation computes stock quantity, average cost, and value today or as of a past date.
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
		q := sqlq.New(p.TenantID)
		var src string
		if asOf == "" {
			src = `(SELECT sl.location_id AS branch_id, sl.product_id, sl.quantity_on_hand AS qty FROM app.stock_balances sl WHERE sl.tenant_id = $1` + branchAnd(q, f, "sl.location_id") + `)`
		} else {
			day := q.Arg(asOf)
			src = `(SELECT m.location_id AS branch_id, m.product_id, SUM(m.quantity_delta) AS qty
				FROM app.stock_movements m WHERE m.tenant_id = $1 AND m.business_date <= ` + day + `::date` + branchAnd(q, f, "m.location_id") + `
				GROUP BY m.location_id, m.product_id)`
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT `+g.sel+`, ROUND(SUM(v.qty * pr.average_cost), 2) AS value
			FROM `+src+` v
			JOIN app.products pr ON pr.tenant_id = $1 AND pr.id = v.product_id
			JOIN app.locations b ON b.tenant_id = $1 AND b.id = v.branch_id
			LEFT JOIN app.product_categories c ON c.tenant_id = pr.tenant_id AND c.id = pr.category_id
			GROUP BY `+g.group+`
			HAVING SUM(v.qty) <> 0 OR SUM(v.qty * pr.average_cost) <> 0
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

func branchAnd(q *sqlq.Q, f Filter, col string) string {
	if f.Branches == nil {
		return ""
	}
	return " AND " + col + " = ANY(" + q.Arg(f.Branches) + ")"
}

var movementTypeMap = map[string]string{
	"purchase_receipt": "purchase_receipt",
	"purchase":         "purchase_receipt",
	"sale":             "sale",
	"sale_void":        "sale_void",
	"sale_return":      "sale_return",
	"adjustment":       "adjustment",
	"opening":          "adjustment",
	"stock_in":         "adjustment",
	"stock_out":        "adjustment",
	"transfer_out":     "transfer_out",
	"transfer_in":      "transfer_in",
}

// Movements returns paginated stock movement history from app.stock_movements.
func (s *Service) Movements(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	typ := f.Q("type")
	mappedTyp := ""
	if typ != "" {
		var ok bool
		if mappedTyp, ok = movementTypeMap[typ]; !ok {
			return export.Table{}, nil, badQuery("type is not a known movement type")
		}
	}
	page := f.Int("page", 1, 1, 1000000)
	per := f.Int("per_page", 100, 1, 1000)
	var list []map[string]any
	var total int64
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("m.tenant_id = $1")
		f.dates(q, "m.business_date")
		f.branch(q, "m.location_id")
		q.If(mappedTyp != "", "m.movement_type = ?::app.stock_movement_type", mappedTyp)
		if id := optUUID(f, "product_id"); id != nil {
			q.Add("m.product_id = ?", *id)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_movements m`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		list, err = rows(ctx, tx, `
			SELECT m.business_date AS date, m.occurred_at AS created_at, pr.name::text AS product, pr.sku, b.name::text AS branch,
			       m.movement_type::text AS type, m.quantity_delta AS qty,
			       ROUND(COALESCE(m.unit_cost, pr.average_cost), 4) AS unit_cost,
			       ROUND(m.quantity_delta * COALESCE(m.unit_cost, pr.average_cost), 2) AS value,
			       COALESCE(sb.quantity_on_hand, 0) AS qty_after,
			       CASE WHEN m.sale_line_id IS NOT NULL THEN 'sale'
			            WHEN m.sale_return_line_id IS NOT NULL THEN 'sale_return'
			            WHEN m.goods_receipt_line_id IS NOT NULL THEN 'goods_receipt'
			            WHEN m.stock_adjustment_line_id IS NOT NULL THEN 'stock_adjustment'
			            ELSE 'stock_transfer' END AS source,
			       COALESCE(m.sale_line_id, m.sale_return_line_id, m.goods_receipt_line_id, m.stock_adjustment_line_id, m.stock_transfer_line_id) AS source_id
			FROM app.stock_movements m
			JOIN app.products pr ON pr.tenant_id = m.tenant_id AND pr.id = m.product_id
			JOIN app.locations b ON b.tenant_id = m.tenant_id AND b.id = m.location_id
			LEFT JOIN app.stock_balances sb ON sb.tenant_id = m.tenant_id AND sb.location_id = m.location_id AND sb.product_id = m.product_id`+q.Where()+`
			ORDER BY m.occurred_at DESC, m.id`+q.Page(per, (page-1)*per), q.Args...)
		return err
	})
	keys := stripCost(p, []string{"date", "product", "sku", "branch", "type", "qty", "unit_cost", "value", "qty_after", "source"})
	if !canCost(p) {
		dropKeys(list, "unit_cost", "value")
	}
	return export.Table{Title: "Stock movements", Subtitle: f.subtitle(), Columns: cols(keys...), Rows: list},
		map[string]any{"meta": map[string]any{"page": page, "per_page": per, "total": total}}, err
}

// LowStock lists active products at or below their minimum stock level.
func (s *Service) LowStock(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("sl.tenant_id = $1 AND pr.status = 'active' AND COALESCE(pr.min_stock_level, ts.low_stock_default) > 0 AND sl.quantity_on_hand <= COALESCE(pr.min_stock_level, ts.low_stock_default)")
		f.branch(q, "sl.location_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT pr.id AS product_id, pr.name::text AS product, pr.sku, b.name::text AS branch,
			       sl.quantity_on_hand AS qty_on_hand, COALESCE(pr.min_stock_level, ts.low_stock_default) AS reorder_level,
			       GREATEST(COALESCE(pr.min_stock_level, ts.low_stock_default) * 2 - sl.quantity_on_hand, 0) AS suggested_qty
			FROM app.stock_balances sl
			JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
			JOIN app.locations b ON b.tenant_id = sl.tenant_id AND b.id = sl.location_id
			JOIN app.tenant_settings ts ON ts.tenant_id = sl.tenant_id`+q.Where()+`
			ORDER BY sl.quantity_on_hand - COALESCE(pr.min_stock_level, ts.low_stock_default), pr.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Low stock", Columns: cols("product", "sku", "branch", "qty_on_hand", "reorder_level", "suggested_qty"), Rows: list}, nil, err
}

// DeadStock lists products with stock on hand and no sale for at least N days (default 90).
func (s *Service) DeadStock(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	days := f.Int("days", 90, 1, 3650)
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		ref := f.To
		if ref == "" {
			ref = f.today
		}
		q := sqlq.New(p.TenantID)
		r := q.Arg(ref)
		d := q.Arg(days)
		q.Add("sl.tenant_id = $1 AND sl.quantity_on_hand > 0")
		f.branch(q, "sl.location_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT * FROM (
				SELECT pr.name::text AS product, pr.sku, b.name::text AS branch,
				       sl.quantity_on_hand AS qty_on_hand, ROUND(sl.quantity_on_hand * pr.average_cost, 2) AS value,
				       ls.last_sold, (`+r+`::date - COALESCE(ls.last_sold, pr.created_at::date)) AS days_idle
				FROM app.stock_balances sl
				JOIN app.products pr ON pr.tenant_id = sl.tenant_id AND pr.id = sl.product_id
				JOIN app.locations b ON b.tenant_id = sl.tenant_id AND b.id = sl.location_id
				LEFT JOIN LATERAL (
					SELECT max(s.business_date) AS last_sold
					FROM app.sale_lines si
					JOIN app.sales s ON s.tenant_id = si.tenant_id AND s.id = si.sale_id
					WHERE si.tenant_id = sl.tenant_id AND si.product_id = sl.product_id AND s.location_id = sl.location_id AND s.status = 'completed'
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

// Expiring returns an empty report because batch/expiry tracking is not part of the approved BMS schema.
func (s *Service) Expiring(_ context.Context, _ *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	days := f.Int("days", 30, 0, 3650)
	return export.Table{
		Title: "Expiring stock", Subtitle: "Batch expiry tracking is not enabled in this schema",
		Columns: cols("source", "number", "product", "sku", "branch", "batch_no", "expiry_date", "days_left", "qty_received", "qty_on_hand"),
		Rows:    []map[string]any{},
	}, map[string]any{"days": days}, nil
}

// Losses lists downward stock adjustments with their reason and cost value.
func (s *Service) Losses(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("m.tenant_id = $1 AND m.movement_type = 'adjustment' AND m.quantity_delta < 0")
		f.dates(q, "m.business_date")
		f.branch(q, "m.location_id")
		var err error
		list, err = rows(ctx, tx, `
			SELECT m.business_date AS date, COALESCE(sa.reason::text, 'adjustment') AS reason,
			       pr.name::text AS product, pr.sku, b.name::text AS branch,
			       -m.quantity_delta AS qty, ROUND(-m.quantity_delta * COALESCE(m.unit_cost, pr.average_cost), 2) AS value
			FROM app.stock_movements m
			JOIN app.products pr ON pr.tenant_id = m.tenant_id AND pr.id = m.product_id
			JOIN app.locations b ON b.tenant_id = m.tenant_id AND b.id = m.location_id
			LEFT JOIN app.stock_adjustment_lines sal ON sal.tenant_id = m.tenant_id AND sal.id = m.stock_adjustment_line_id
			LEFT JOIN app.stock_adjustments sa ON sa.tenant_id = sal.tenant_id AND sa.id = sal.stock_adjustment_id`+q.Where()+`
			ORDER BY m.business_date DESC, pr.name`, q.Args...)
		return err
	})
	return export.Table{Title: "Stock losses", Subtitle: f.subtitle(), Columns: cols("date", "reason", "product", "sku", "branch", "qty", "value"),
		Rows: list, Totals: totals(list, "Total", "qty", "value")}, nil, err
}

// Purchases compares ordered against received quantities and values by supplier, product, or month.
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
		q := sqlq.New(p.TenantID)
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
		src := `
			SELECT po.supplier_id, pl.product_id, date_trunc('month', po.order_date)::date AS month,
			       pl.quantity_ordered AS oq, pl.line_total AS ov, 0::numeric AS rq, 0::numeric AS rv, 0::numeric AS iv
			FROM app.purchase_lines pl
			JOIN app.purchases po ON po.tenant_id = pl.tenant_id AND po.id = pl.purchase_id
			WHERE po.tenant_id = $1 AND po.status NOT IN ('draft','cancelled')` + cond("po.order_date", "po.location_id") + `
			UNION ALL
			SELECT po.supplier_id, pl.product_id, date_trunc('month', gr.business_date)::date, 0, 0, grl.quantity, grl.quantity * grl.unit_cost, 0
			FROM app.goods_receipt_lines grl
			JOIN app.goods_receipts gr ON gr.tenant_id = grl.tenant_id AND gr.id = grl.goods_receipt_id
			JOIN app.purchase_lines pl ON pl.tenant_id = grl.tenant_id AND pl.id = grl.purchase_line_id
			JOIN app.purchases po ON po.tenant_id = gr.tenant_id AND po.id = gr.purchase_id
			WHERE gr.tenant_id = $1` + cond("gr.business_date", "gr.location_id")
		if group != "product" {
			src += `
			UNION ALL
			SELECT po.supplier_id, NULL::uuid, date_trunc('month', po.order_date)::date, 0, 0, 0, 0, po.total
			FROM app.purchases po
			WHERE po.tenant_id = $1 AND po.status IN ('ordered','partially_received','received')` + cond("po.order_date", "po.location_id")
		}
		sel := map[string]string{
			"supplier": "sp.name::text AS supplier",
			"product":  "pr.name::text AS product, pr.sku",
			"month":    "x.month",
		}[group]
		join := map[string]string{
			"supplier": "JOIN app.suppliers sp ON sp.tenant_id = $1 AND sp.id = x.supplier_id",
			"product":  "JOIN app.products pr ON pr.tenant_id = $1 AND pr.id = x.product_id",
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

// CustomerDebt ages unpaid completed sales and issued invoices per customer.
func (s *Service) CustomerDebt(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	asOf := ""
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		asOf = f.To
		if asOf == "" {
			asOf = f.today
		}
		q := sqlq.New(p.TenantID)
		d := q.Arg(asOf)
		sc := "s.tenant_id = $1 AND s.balance_due > 0 AND s.status = 'completed'"
		if f.Branches != nil {
			sc += " AND s.location_id = ANY(" + q.Arg(f.Branches) + ")"
		}
		var err error
		list, err = rows(ctx, tx, aged(`
			SELECT c.id, c.full_name::text AS customer, c.phone::text AS phone, COALESCE(c.credit_limit, 0) AS credit_limit,
			       `+d+`::date - COALESCE(s.due_date, s.business_date) AS age,
			       `+d+`::date > COALESCE(s.due_date, s.business_date) AS is_overdue, s.balance_due AS amt
			FROM app.sales s JOIN app.customers c ON c.tenant_id = s.tenant_id AND c.id = s.customer_id
			WHERE `+sc+`
			UNION ALL
			SELECT c.id, c.full_name::text, c.phone::text, COALESCE(c.credit_limit, 0),
			       `+d+`::date - i.due_date, `+d+`::date > i.due_date, i.balance_due
			FROM app.invoices i JOIN app.customers c ON c.tenant_id = i.tenant_id AND c.id = i.customer_id
			WHERE i.tenant_id = $1 AND i.balance_due > 0 AND i.status IN ('issued','partially_paid')`,
			"id, customer, phone, credit_limit", "customer"), q.Args...)
		return err
	})
	return export.Table{Title: "Customer debt", Subtitle: "As of " + asOf, Columns: cols("customer", "phone", "credit_limit", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance"),
		Rows: list, Totals: totals(list, "Total", "invoices", "current", "d31_60", "d61_90", "over_90", "overdue", "balance")}, map[string]any{"as_of": asOf}, err
}

// SupplierDebt ages unpaid purchase balances per supplier.
func (s *Service) SupplierDebt(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
	var list []map[string]any
	asOf := ""
	err := s.tx(ctx, p, &f, func(ctx context.Context, tx pgx.Tx) error {
		asOf = f.To
		if asOf == "" {
			asOf = f.today
		}
		q := sqlq.New(p.TenantID)
		d := q.Arg(asOf)
		q.Add("pu.tenant_id = $1 AND pu.balance_due > 0 AND pu.status <> 'cancelled'")
		f.branch(q, "pu.location_id")
		var err error
		list, err = rows(ctx, tx, aged(`
			SELECT sp.id, sp.name::text AS supplier, sp.phone::text AS phone,
			       `+d+`::date - pu.order_date AS age,
			       `+d+`::date > COALESCE(pu.due_date, pu.order_date) AS is_overdue, pu.balance_due AS amt
			FROM app.purchases pu JOIN app.suppliers sp ON sp.tenant_id = pu.tenant_id AND sp.id = pu.supplier_id`+q.Where(), "id, supplier, phone", "supplier"), q.Args...)
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
