package purchasing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/business"
	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/pdf"
	"bms/internal/platform/sqlq"
)

type POFilter struct {
	Status     string
	SupplierID *uuid.UUID
	ProductID  *uuid.UUID
	From, To   string
	Q          string
}

type DocFilter struct {
	Status     string
	SupplierID *uuid.UUID
	POID       *uuid.UUID
	From, To   string
	Unpaid     bool
}

func poWhere(f POFilter, branch *uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	q.If(branch != nil, "po.location_id = ?", branch)
	q.If(f.Status != "", "po.status = ?::app.purchase_status", f.Status)
	q.If(f.SupplierID != nil, "po.supplier_id = ?", f.SupplierID)
	q.If(f.ProductID != nil, "EXISTS (SELECT 1 FROM app.purchase_lines i WHERE i.purchase_id = po.id AND i.product_id = ?)", f.ProductID)
	q.If(f.From != "", "po.order_date >= ?::date", f.From)
	q.If(f.To != "", "po.order_date <= ?::date", f.To)
	q.If(f.Q != "", "(po.purchase_no ILIKE '%' || ? || '%' OR po.supplier_invoice_ref ILIKE '%' || ? || '%')", f.Q, f.Q)
	return q
}

func (s *Service) poRows(ctx context.Context, tx pgx.Tx, f POFilter, branch *uuid.UUID, limit, offset int) ([]map[string]any, error) {
	q := poWhere(f, branch)
	rows, err := tx.Query(ctx, `
		SELECT po.id, po.purchase_no, po.location_id, po.supplier_id, s.name::text, po.status::text,
		       po.order_date::text, po.expected_date::text, po.due_date::text, po.supplier_invoice_ref,
		       po.total, po.amount_paid, po.balance_due, po.created_at,
		       COALESCE((SELECT SUM(quantity_ordered) FROM app.purchase_lines i WHERE i.purchase_id = po.id), 0),
		       COALESCE((SELECT SUM(quantity_received) FROM app.purchase_lines i WHERE i.purchase_id = po.id), 0)
		FROM app.purchases po JOIN app.suppliers s ON s.id = po.supplier_id`+q.Where()+
		` ORDER BY po.order_date DESC, po.created_at DESC`+q.Page(limit, offset), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, loc, sup uuid.UUID
		var number, sname, st, date string
		var expected, due, invRef *string
		var total, paid, bal, ordered, received decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &loc, &sup, &sname, &st, &date, &expected, &due, &invRef, &total, &paid, &bal, &at, &ordered, &received); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "branch_id": loc, "supplier_id": sup, "supplier": sname, "status": st,
			"order_date": date, "expected_date": expected, "due_date": due, "supplier_invoice_ref": invRef,
			"total": total, "paid_total": paid, "balance": bal, "created_at": at, "qty_ordered": ordered, "qty_received": received})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListPOs(ctx context.Context, p *auth.Principal, f POFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := poWhere(f, p.BranchID)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.purchases po`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = s.poRows(ctx, tx, f, p.BranchID, page.PerPage, page.Offset)
		return err
	})
	return orEmpty(out), total, err
}

// GetPO shows ordered, received and outstanding quantity per line, receipts and payments.
func (s *Service) GetPO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch, sup uuid.UUID
		var by *uuid.UUID
		var number, status, sname, date string
		var expected, due, invRef, note *string
		var sub, disc, tax, total, paid, bal decimal.Decimal
		var at time.Time
		err := tx.QueryRow(ctx, "\n\t\t\tSELECT po.purchase_no, po.status::text, po.location_id, po.supplier_id, s.name::text,\n\t\t\t       po.order_date::text, po.expected_date::text, po.due_date::text, po.supplier_invoice_ref, po.notes,\n\t\t\t       po.subtotal, po.discount_amount, po.tax_amount, po.total, po.amount_paid, po.balance_due, po.created_by, po.created_at\n\t\t\tFROM app.purchases po JOIN app.suppliers s ON s.id = po.supplier_id\n\t\t\tWHERE po.id = $1", id).
			Scan(&number, &status, &branch, &sup, &sname, &date, &expected, &due, &invRef, &note, &sub, &disc, &tax, &total, &paid, &bal, &by, &at)
		if err != nil {
			return httpx.NotFound("not_found", "Purchase order not found")
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT i.id, i.product_id, p.name::text, COALESCE(p.sku, ''), i.quantity_ordered, i.quantity_received, i.unit_cost, i.line_total\n\t\t\tFROM app.purchase_lines i JOIN app.products p ON p.id = i.product_id\n\t\t\tWHERE i.purchase_id = $1 ORDER BY i.line_no", id)
		if err != nil {
			return err
		}
		var items []map[string]any
		for rows.Next() {
			var iid, pid uuid.UUID
			var name, sku string
			var ordered, received, cost, line decimal.Decimal
			if err := rows.Scan(&iid, &pid, &name, &sku, &ordered, &received, &cost, &line); err != nil {
				rows.Close()
				return err
			}
			outstanding := ordered.Sub(received)
			if outstanding.IsNegative() {
				outstanding = money.Zero
			}
			items = append(items, map[string]any{"id": iid, "product_id": pid, "product_name": name, "sku": sku,
				"qty_ordered": ordered, "qty_received": received, "qty_outstanding": outstanding, "unit_cost": cost, "line_total": line})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		receipts, err := s.grnRows(ctx, tx, DocFilter{POID: &id}, nil, 100, 0)
		if err != nil {
			return err
		}
		pays := []map[string]any{}
		pr, err := tx.Query(ctx, "\n\t\t\tSELECT pm.id, pm.payment_no, pm.business_date::text, pm.method::text, pm.status::text, a.amount, pm.reference\n\t\t\tFROM app.payment_made_allocations a\n\t\t\tJOIN app.payments_made pm ON pm.id = a.payment_made_id\n\t\t\tWHERE a.purchase_id = $1 ORDER BY pm.business_date, pm.created_at", id)
		if err != nil {
			return err
		}
		defer pr.Close()
		for pr.Next() {
			var pid uuid.UUID
			var pno, pdate, method, pst string
			var amt decimal.Decimal
			var ref *string
			if err := pr.Scan(&pid, &pno, &pdate, &method, &pst, &amt, &ref); err != nil {
				return err
			}
			pays = append(pays, map[string]any{"payment_id": pid, "number": pno, "date": pdate, "method": payments.MethodOut(method),
				"status": pst, "amount": amt, "reference": ref})
		}
		out = map[string]any{"id": id, "number": number, "status": status, "branch_id": branch, "supplier_id": sup, "supplier": sname,
			"order_date": date, "expected_date": expected, "due_date": due, "supplier_invoice_ref": invRef, "note": note,
			"subtotal": sub, "discount_amount": disc, "tax_total": tax, "total": total, "paid_total": paid, "balance": bal,
			"created_by": by, "created_at": at, "items": orEmpty(items), "receipts": receipts, "payments": pays}
		return pr.Err()
	})
	return out, err
}

// POPDF renders a printable purchase order.
func (s *Service) POPDF(ctx context.Context, p *auth.Principal, id uuid.UUID) (string, []byte, error) {
	po, err := s.GetPO(ctx, p, id)
	if err != nil {
		return "", nil, err
	}
	var bizName string
	_ = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		head, err := business.HeaderFor(ctx, tx, auth.Shop)
		bizName = head.Name
		return err
	})
	d := pdf.New()
	d.Heading(bizName, 16)
	d.Heading("Purchase order "+po["number"].(string), 13)
	d.KV("Supplier", po["supplier"].(string))
	d.KV("Order date", po["order_date"].(string))
	if e, ok := po["expected_date"].(*string); ok && e != nil {
		d.KV("Expected", *e)
	}
	d.KV("Status", po["status"].(string))
	d.Space(8)
	var rows [][]string
	for _, it := range po["items"].([]map[string]any) {
		rows = append(rows, []string{it["product_name"].(string), it["sku"].(string), it["qty_ordered"].(decimal.Decimal).String(),
			it["unit_cost"].(decimal.Decimal).StringFixed(2), it["line_total"].(decimal.Decimal).StringFixed(2)})
	}
	d.Table([]string{"Product", "SKU", "Qty", "Unit cost", "Total"}, []float64{4, 2, 1, 1.5, 1.5}, rows)
	d.Rule()
	d.KV("Subtotal", po["subtotal"].(decimal.Decimal).StringFixed(2))
	d.KV("Tax", po["tax_total"].(decimal.Decimal).StringFixed(2))
	d.KV("Total", po["total"].(decimal.Decimal).StringFixed(2))
	if n, ok := po["note"].(*string); ok && n != nil {
		d.Space(6)
		d.Text(*n)
	}
	return po["number"].(string) + ".pdf", d.Bytes(), nil
}

// Suggest proposes order lines from products at or below min_stock_level in the active location.
func (s *Service) Suggest(ctx context.Context, p *auth.Principal, supplier *uuid.UUID) ([]map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if supplier != nil {
			if _, err := supplierRow(ctx, tx, *supplier); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT p.id, p.name::text, COALESCE(p.sku, ''), COALESCE(sb.quantity_on_hand, 0),\n\t\t\t       COALESCE(p.min_stock_level, ts.low_stock_default), p.average_cost,\n\t\t\t       (SELECT grl.unit_cost\n\t\t\t        FROM app.goods_receipt_lines grl\n\t\t\t        JOIN app.goods_receipts gr ON gr.id = grl.goods_receipt_id\n\t\t\t        JOIN app.purchase_lines pl ON pl.id = grl.purchase_line_id\n\t\t\t        JOIN app.purchases po ON po.id = pl.purchase_id\n\t\t\t        WHERE pl.product_id = p.id AND ($2::uuid IS NULL OR po.supplier_id = $2)\n\t\t\t        ORDER BY gr.received_at DESC LIMIT 1)\n\t\t\tFROM app.products p\n\t\t\t		CROSS JOIN app.business_settings ts\n\t\t\tLEFT JOIN app.stock_balances sb ON sb.product_id = p.id AND sb.location_id = $1\n\t\t\tWHERE p.status = 'active'\n\t\t\t  AND COALESCE(p.min_stock_level, ts.low_stock_default) > 0\n\t\t\t  AND COALESCE(sb.quantity_on_hand, 0) <= COALESCE(p.min_stock_level, ts.low_stock_default)\n\t\t\tORDER BY p.name", *p.BranchID, supplier)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name, sku string
			var qty, reorder, avg decimal.Decimal
			var last decimal.NullDecimal
			if err := rows.Scan(&id, &name, &sku, &qty, &reorder, &avg, &last); err != nil {
				return err
			}
			suggest := reorder.Mul(decimal.NewFromInt(2)).Sub(qty)
			if !suggest.IsPositive() {
				continue
			}
			cost := avg
			if last.Valid {
				cost = last.Decimal
			}
			out = append(out, map[string]any{"product_id": id, "product_name": name, "sku": sku, "qty_on_hand": qty, "reorder_level": reorder, "qty": suggest, "unit_cost": cost})
		}
		return rows.Err()
	})
	return orEmpty(out), err
}

func grnWhere(f DocFilter, branch *uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	q.If(branch != nil, "g.location_id = ?", branch)
	q.If(f.SupplierID != nil, "po.supplier_id = ?", f.SupplierID)
	q.If(f.POID != nil, "g.purchase_id = ?", f.POID)
	q.If(f.From != "", "g.business_date >= ?::date", f.From)
	q.If(f.To != "", "g.business_date <= ?::date", f.To)
	return q
}

func (s *Service) grnRows(ctx context.Context, tx pgx.Tx, f DocFilter, branch *uuid.UUID, limit, offset int) ([]map[string]any, error) {
	q := grnWhere(f, branch)
	rows, err := tx.Query(ctx, `
		SELECT g.id, g.receipt_no, g.location_id, g.purchase_id, po.purchase_no, po.supplier_id, s.name::text,
		       g.business_date::text, g.notes,
		       COALESCE((SELECT SUM(round(l.quantity * l.unit_cost, 2)) FROM app.goods_receipt_lines l WHERE l.goods_receipt_id = g.id), 0),
		       g.created_by, g.created_at
		FROM app.goods_receipts g
		JOIN app.purchases po ON po.id = g.purchase_id
		JOIN app.suppliers s ON s.id = po.supplier_id`+q.Where()+
		` ORDER BY g.business_date DESC, g.created_at DESC`+q.Page(limit, offset), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, loc, poID, sup uuid.UUID
		var by *uuid.UUID
		var number, poNum, sname, date string
		var note *string
		var value decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &loc, &poID, &poNum, &sup, &sname, &date, &note, &value, &by, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "branch_id": loc, "po_id": poID, "po_number": poNum,
			"supplier_id": sup, "supplier": sname, "received_date": date, "delivery_note": note, "total_value": value,
			"received_by": by, "created_at": at})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListGRNs(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := grnWhere(f, p.BranchID)
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.goods_receipts g
			JOIN app.purchases po ON po.id = g.purchase_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = s.grnRows(ctx, tx, f, p.BranchID, page.PerPage, page.Offset)
		return err
	})
	return orEmpty(out), total, err
}

func (s *Service) GetGRN(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var loc, poID, sup uuid.UUID
		var by *uuid.UUID
		var number, poNum, sname, date string
		var note *string
		var value decimal.Decimal
		var at time.Time
		err := tx.QueryRow(ctx, "\n\t\t\tSELECT g.receipt_no, g.location_id, g.purchase_id, po.purchase_no, po.supplier_id, s.name::text,\n\t\t\t       g.business_date::text, g.notes,\n\t\t\t       COALESCE((SELECT SUM(round(l.quantity * l.unit_cost, 2)) FROM app.goods_receipt_lines l WHERE l.goods_receipt_id = g.id), 0),\n\t\t\t       g.created_by, g.created_at\n\t\t\tFROM app.goods_receipts g\n\t\t\tJOIN app.purchases po ON po.id = g.purchase_id\n\t\t\tJOIN app.suppliers s ON s.id = po.supplier_id\n\t\t\tWHERE g.id = $1", id).
			Scan(&number, &loc, &poID, &poNum, &sup, &sname, &date, &note, &value, &by, &at)
		if err != nil {
			return httpx.NotFound("not_found", "Goods receipt not found")
		}
		items, err := tx.Query(ctx, "\n\t\t\tSELECT gl.id, pl.product_id, pr.name::text, COALESCE(pr.sku, ''), gl.quantity, gl.unit_cost, round(gl.quantity * gl.unit_cost, 2)\n\t\t\tFROM app.goods_receipt_lines gl\n\t\t\tJOIN app.purchase_lines pl ON pl.id = gl.purchase_line_id\n\t\t\tJOIN app.products pr ON pr.id = pl.product_id\n\t\t\tWHERE gl.goods_receipt_id = $1 ORDER BY pl.line_no", id)
		if err != nil {
			return err
		}
		defer items.Close()
		var lines []map[string]any
		for items.Next() {
			var iid, pid uuid.UUID
			var name, sku string
			var qty, cost, val decimal.Decimal
			if err := items.Scan(&iid, &pid, &name, &sku, &qty, &cost, &val); err != nil {
				return err
			}
			lines = append(lines, map[string]any{"id": iid, "product_id": pid, "product_name": name, "sku": sku,
				"qty_received": qty, "unit_cost": cost, "value": val})
		}
		if err := items.Err(); err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": number, "branch_id": loc, "po_id": poID, "po_number": poNum,
			"supplier_id": sup, "supplier": sname, "received_date": date, "delivery_note": note, "total_value": value,
			"received_by": by, "created_at": at, "items": orEmpty(lines)}
		return nil
	})
	return out, err
}

// Separate purchase invoices are not part of the approved schema.
func (s *Service) ListInvoices(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	return []map[string]any{}, 0, nil
}

func (s *Service) GetInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Separate purchase invoices")
}
