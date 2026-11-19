package purchasing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

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

func branchCond(q *sqlq.Q, col string, branches []uuid.UUID) {
	if branches != nil {
		q.Add(col+" = ANY(?)", branches)
	}
}

func poWhere(f POFilter, branches []uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	branchCond(q, "po.branch_id", branches)
	q.If(f.Status != "", "po.status = ?", f.Status)
	q.If(f.SupplierID != nil, "po.supplier_id = ?", f.SupplierID)
	q.If(f.ProductID != nil, "EXISTS (SELECT 1 FROM purchase_order_items i WHERE i.po_id=po.id AND i.product_id = ?)", f.ProductID)
	q.If(f.From != "", "po.order_date >= ?::date", f.From)
	q.If(f.To != "", "po.order_date <= ?::date", f.To)
	q.If(f.Q != "", "po.number ILIKE '%' || ? || '%'", f.Q)
	return q
}

func (s *Service) poRows(ctx context.Context, tx pgx.Tx, f POFilter, branches []uuid.UUID, limit, offset int) ([]map[string]any, error) {
	q := poWhere(f, branches)
	rows, err := tx.Query(ctx, `
		SELECT po.id, po.number, po.branch_id, po.supplier_id, s.name, po.status, po.order_date::text, po.expected_date::text, po.total, po.created_at,
		       COALESCE((SELECT SUM(qty_ordered) FROM purchase_order_items i WHERE i.po_id=po.id),0),
		       COALESCE((SELECT SUM(qty_received) FROM purchase_order_items i WHERE i.po_id=po.id),0)
		FROM purchase_orders po JOIN suppliers s ON s.id=po.supplier_id`+q.Where()+` ORDER BY po.order_date DESC, po.created_at DESC`+q.Page(limit, offset), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, branch, sup uuid.UUID
		var number, sname, st, date string
		var expected *string
		var total, ordered, received decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &branch, &sup, &sname, &st, &date, &expected, &total, &at, &ordered, &received); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "supplier_id": sup, "supplier": sname, "status": st,
			"order_date": date, "expected_date": expected, "total": total, "created_at": at, "qty_ordered": ordered, "qty_received": received})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListPOs(ctx context.Context, p *auth.Principal, f POFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		branches := p.ScopeBranches("po.view")
		q := poWhere(f, branches)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM purchase_orders po`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = s.poRows(ctx, tx, f, branches, page.PerPage, page.Offset)
		return err
	})
	return orEmpty(out), total, err
}

// GetPO shows ordered, received and outstanding quantity per line (FR-PUR-13).
func (s *Service) GetPO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch, sup, by uuid.UUID
		var approver *uuid.UUID
		var number, status, sname, date string
		var expected, note *string
		var sub, tax, total decimal.Decimal
		var at time.Time
		err := tx.QueryRow(ctx, `
			SELECT po.number, po.status, po.branch_id, po.supplier_id, s.name, po.order_date::text, po.expected_date::text, po.note,
			       po.subtotal, po.tax_total, po.total, po.created_by, po.approved_by, po.created_at
			FROM purchase_orders po JOIN suppliers s ON s.id=po.supplier_id WHERE po.id=$1
		`, id).Scan(&number, &status, &branch, &sup, &sname, &date, &expected, &note, &sub, &tax, &total, &by, &approver, &at)
		if err != nil || !p.SeesBranch("po.view", branch) {
			return httpx.NotFound("not_found", "Purchase order not found")
		}
		rows, err := tx.Query(ctx, `
			SELECT i.id, i.product_id, p.name, p.sku, i.qty_ordered, i.qty_received, i.unit_cost, i.tax_rate, i.line_total
			FROM purchase_order_items i JOIN products p ON p.id=i.product_id WHERE i.po_id=$1 ORDER BY p.name
		`, id)
		if err != nil {
			return err
		}
		var items []map[string]any
		for rows.Next() {
			var iid, pid uuid.UUID
			var name, sku string
			var ordered, received, cost, rate, line decimal.Decimal
			if err := rows.Scan(&iid, &pid, &name, &sku, &ordered, &received, &cost, &rate, &line); err != nil {
				rows.Close()
				return err
			}
			outstanding := ordered.Sub(received)
			if outstanding.IsNegative() {
				outstanding = money.Zero
			}
			items = append(items, map[string]any{"id": iid, "product_id": pid, "product_name": name, "sku": sku, "qty_ordered": ordered,
				"qty_received": received, "qty_outstanding": outstanding, "unit_cost": cost, "tax_rate": rate, "line_total": line})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		receipts, err := s.grnRows(ctx, tx, DocFilter{POID: &id}, nil, 100, 0)
		if err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": number, "status": status, "branch_id": branch, "supplier_id": sup, "supplier": sname,
			"order_date": date, "expected_date": expected, "note": note, "subtotal": sub, "tax_total": tax, "total": total,
			"created_by": by, "approved_by": approver, "created_at": at, "items": orEmpty(items), "receipts": receipts}
		return nil
	})
	return out, err
}

// POPDF renders a printable order (FR-PUR-03).
func (s *Service) POPDF(ctx context.Context, p *auth.Principal, id uuid.UUID) (string, []byte, error) {
	po, err := s.GetPO(ctx, p, id)
	if err != nil {
		return "", nil, err
	}
	var bizName string
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name FROM businesses WHERE id=$1`, p.BusinessID).Scan(&bizName)
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

// Suggest proposes order lines from products at or below reorder level in the
// active branch (FR-PUR-14), costed at the last price from this supplier.
func (s *Service) Suggest(ctx context.Context, p *auth.Principal, supplier *uuid.UUID) ([]map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if supplier != nil {
			if _, err := supplierRow(ctx, tx, *supplier); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, p.sku, COALESCE(sl.qty_on_hand,0), p.reorder_level, COALESCE(sl.avg_cost,0),
			       (SELECT gi.unit_cost FROM goods_receipt_items gi JOIN goods_receipts g ON g.id=gi.receipt_id
			        WHERE gi.product_id=p.id AND ($2::uuid IS NULL OR g.supplier_id=$2) ORDER BY g.created_at DESC LIMIT 1)
			FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$1
			WHERE p.kind='stock' AND p.is_active AND p.reorder_level > 0 AND COALESCE(sl.qty_on_hand,0) <= p.reorder_level
			ORDER BY p.name
		`, *p.BranchID, supplier)
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

func grnWhere(f DocFilter, branches []uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	branchCond(q, "g.branch_id", branches)
	q.If(f.SupplierID != nil, "g.supplier_id = ?", f.SupplierID)
	q.If(f.POID != nil, "g.po_id = ?", f.POID)
	q.If(f.From != "", "g.received_date >= ?::date", f.From)
	q.If(f.To != "", "g.received_date <= ?::date", f.To)
	if f.Status == "invoiced" {
		q.Add("g.invoice_id IS NOT NULL")
	} else if f.Status == "uninvoiced" {
		q.Add("g.invoice_id IS NULL")
	}
	return q
}

func (s *Service) grnRows(ctx context.Context, tx pgx.Tx, f DocFilter, branches []uuid.UUID, limit, offset int) ([]map[string]any, error) {
	q := grnWhere(f, branches)
	rows, err := tx.Query(ctx, `
		SELECT g.id, g.number, g.branch_id, g.po_id, po.number, g.supplier_id, s.name, g.received_date::text, g.delivery_note, g.extra_costs, g.total_value, g.invoice_id, g.received_by, g.created_at
		FROM goods_receipts g JOIN suppliers s ON s.id=g.supplier_id LEFT JOIN purchase_orders po ON po.id=g.po_id`+q.Where()+
		` ORDER BY g.received_date DESC, g.created_at DESC`+q.Page(limit, offset), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, branch, sup, by uuid.UUID
		var po, inv *uuid.UUID
		var number, sname, date string
		var poNum, note *string
		var extra, value decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &branch, &po, &poNum, &sup, &sname, &date, &note, &extra, &value, &inv, &by, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "po_id": po, "po_number": poNum, "supplier_id": sup, "supplier": sname,
			"received_date": date, "delivery_note": note, "extra_costs": extra, "total_value": value, "invoice_id": inv, "received_by": by, "created_at": at})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListGRNs(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		branches := p.ScopeBranches("po.view")
		q := grnWhere(f, branches)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM goods_receipts g`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = s.grnRows(ctx, tx, f, branches, page.PerPage, page.Offset)
		return err
	})
	return orEmpty(out), total, err
}

func (s *Service) GetGRN(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT branch_id FROM goods_receipts WHERE id=$1`, id).Scan(&branch); err != nil || !p.SeesBranch("po.view", branch) {
			return httpx.NotFound("not_found", "Goods receipt not found")
		}
		one, err := s.grnByID(ctx, tx, id)
		if err != nil {
			return err
		}
		items, err := tx.Query(ctx, `
			SELECT gi.id, gi.product_id, p.name, p.sku, gi.qty_received, gi.qty_returned, gi.unit_cost, round(gi.qty_received*gi.unit_cost, 2),
			       gi.batch_no, gi.expiry_date::text
			FROM goods_receipt_items gi JOIN products p ON p.id=gi.product_id WHERE gi.receipt_id=$1 ORDER BY p.name
		`, id)
		if err != nil {
			return err
		}
		defer items.Close()
		var lines []map[string]any
		for items.Next() {
			var iid, pid uuid.UUID
			var name, sku string
			var qty, ret, cost, value decimal.Decimal
			var batch, expiry *string
			if err := items.Scan(&iid, &pid, &name, &sku, &qty, &ret, &cost, &value, &batch, &expiry); err != nil {
				return err
			}
			lines = append(lines, map[string]any{"id": iid, "product_id": pid, "product_name": name, "sku": sku, "qty_received": qty, "qty_returned": ret, "unit_cost": cost, "value": value, "batch_no": batch, "expiry_date": expiry})
		}
		if err := items.Err(); err != nil {
			return err
		}
		one["items"] = orEmpty(lines)
		out = one
		return nil
	})
	return out, err
}

func (s *Service) grnByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	var branch, sup, by uuid.UUID
	var po, inv *uuid.UUID
	var number, sname, date string
	var note *string
	var extra, value decimal.Decimal
	var at time.Time
	err := tx.QueryRow(ctx, `
		SELECT g.number, g.branch_id, g.po_id, g.supplier_id, s.name, g.received_date::text, g.delivery_note, g.extra_costs, g.total_value, g.invoice_id, g.received_by, g.created_at
		FROM goods_receipts g JOIN suppliers s ON s.id=g.supplier_id WHERE g.id=$1
	`, id).Scan(&number, &branch, &po, &sup, &sname, &date, &note, &extra, &value, &inv, &by, &at)
	if err != nil {
		return nil, httpx.NotFound("not_found", "Goods receipt not found")
	}
	return map[string]any{"id": id, "number": number, "branch_id": branch, "po_id": po, "supplier_id": sup, "supplier": sname, "received_date": date,
		"delivery_note": note, "extra_costs": extra, "total_value": value, "invoice_id": inv, "received_by": by, "created_at": at}, nil
}

func invoiceWhere(f DocFilter, branches []uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	branchCond(q, "i.branch_id", branches)
	q.If(f.Status != "", "i.status = ?", f.Status)
	q.If(f.SupplierID != nil, "i.supplier_id = ?", f.SupplierID)
	q.If(f.From != "", "i.invoice_date >= ?::date", f.From)
	q.If(f.To != "", "i.invoice_date <= ?::date", f.To)
	if f.Unpaid {
		q.Add("i.balance > 0 AND i.status = 'posted'")
	}
	return q
}

func (s *Service) invoiceRows(ctx context.Context, tx pgx.Tx, f DocFilter, branches []uuid.UUID, limit, offset int) ([]map[string]any, error) {
	q := invoiceWhere(f, branches)
	rows, err := tx.Query(ctx, `
		SELECT i.id, i.number, i.supplier_ref, i.branch_id, i.supplier_id, s.name, i.status, i.invoice_date::text, i.due_date::text,
		       i.subtotal, i.tax_total, i.total, i.paid_total, i.balance, i.created_at
		FROM purchase_invoices i JOIN suppliers s ON s.id=i.supplier_id`+q.Where()+` ORDER BY i.invoice_date DESC, i.created_at DESC`+q.Page(limit, offset), q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, branch, sup uuid.UUID
		var number, sname, st, date string
		var ref, due *string
		var sub, tax, total, paid, bal decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &ref, &branch, &sup, &sname, &st, &date, &due, &sub, &tax, &total, &paid, &bal, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "supplier_ref": ref, "branch_id": branch, "supplier_id": sup, "supplier": sname, "status": st,
			"invoice_date": date, "due_date": due, "subtotal": sub, "tax_total": tax, "total": total, "paid_total": paid, "balance": bal, "created_at": at})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListInvoices(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		branches := p.ScopeBranches("po.view")
		q := invoiceWhere(f, branches)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM purchase_invoices i`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = s.invoiceRows(ctx, tx, f, branches, page.PerPage, page.Offset)
		return err
	})
	return orEmpty(out), total, err
}

func (s *Service) GetInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT branch_id FROM purchase_invoices WHERE id=$1`, id).Scan(&branch); err != nil || !p.SeesBranch("po.view", branch) {
			return httpx.NotFound("not_found", "Purchase invoice not found")
		}
		q := invoiceWhere(DocFilter{}, nil)
		q.Add("i.id = ?", id)
		rows, err := tx.Query(ctx, `
			SELECT i.id, i.number, i.supplier_ref, i.branch_id, i.supplier_id, s.name, i.status, i.invoice_date::text, i.due_date::text,
			       i.subtotal, i.tax_total, i.total, i.paid_total, i.balance, i.created_at
			FROM purchase_invoices i JOIN suppliers s ON s.id=i.supplier_id`+q.Where(), q.Args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var iid, br, sup uuid.UUID
			var number, sname, st, date string
			var ref, due *string
			var sub, tax, total, paid, bal decimal.Decimal
			var at time.Time
			if err := rows.Scan(&iid, &number, &ref, &br, &sup, &sname, &st, &date, &due, &sub, &tax, &total, &paid, &bal, &at); err != nil {
				rows.Close()
				return err
			}
			out = map[string]any{"id": iid, "number": number, "supplier_ref": ref, "branch_id": br, "supplier_id": sup, "supplier": sname, "status": st,
				"invoice_date": date, "due_date": due, "subtotal": sub, "tax_total": tax, "total": total, "paid_total": paid, "balance": bal, "created_at": at}
		}
		rows.Close()
		if out == nil {
			return httpx.NotFound("not_found", "Purchase invoice not found")
		}
		receipts := []map[string]any{}
		rr, err := tx.Query(ctx, `SELECT id FROM goods_receipts WHERE invoice_id=$1 ORDER BY received_date`, id)
		if err != nil {
			return err
		}
		var rids []uuid.UUID
		for rr.Next() {
			var rid uuid.UUID
			if err := rr.Scan(&rid); err != nil {
				rr.Close()
				return err
			}
			rids = append(rids, rid)
		}
		rr.Close()
		for _, rid := range rids {
			g, err := s.grnByID(ctx, tx, rid)
			if err != nil {
				return err
			}
			receipts = append(receipts, g)
		}
		pays := []map[string]any{}
		pr, err := tx.Query(ctx, `
			SELECT pm.id, pm.number, pm.payment_date::text, pm.method, pm.status, a.amount, pm.reference
			FROM payment_allocations a JOIN payments pm ON pm.id=a.payment_id
			WHERE a.document_type='purchase_invoice' AND a.document_id=$1 ORDER BY pm.payment_date, pm.created_at
		`, id)
		if err != nil {
			return err
		}
		defer pr.Close()
		for pr.Next() {
			var pid uuid.UUID
			var number, date, method, st string
			var amt decimal.Decimal
			var ref *string
			if err := pr.Scan(&pid, &number, &date, &method, &st, &amt, &ref); err != nil {
				return err
			}
			pays = append(pays, map[string]any{"payment_id": pid, "number": number, "date": date, "method": method, "status": st, "amount": amt, "reference": ref})
		}
		out["receipts"] = receipts
		out["payments"] = pays
		return pr.Err()
	})
	return out, err
}

func returnWhere(f DocFilter, branches []uuid.UUID) *sqlq.Q {
	q := sqlq.New()
	branchCond(q, "r.branch_id", branches)
	q.If(f.SupplierID != nil, "r.supplier_id = ?", f.SupplierID)
	q.If(f.From != "", "r.created_at >= ?::date", f.From)
	q.If(f.To != "", "r.created_at < ?::date + 1", f.To)
	q.If(f.Status != "", "r.settlement = ?", f.Status)
	return q
}

const returnCols = `SELECT r.id, r.number, r.branch_id, r.supplier_id, s.name, r.receipt_id, r.invoice_id, r.reason, r.total, r.settlement, r.money_account_id, r.created_by, r.created_at
	FROM purchase_returns r JOIN suppliers s ON s.id=r.supplier_id`

func scanReturns(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, branch, sup, by uuid.UUID
		var receipt, inv, acc *uuid.UUID
		var number, sname, reason, settlement string
		var total decimal.Decimal
		var at time.Time
		if err := rows.Scan(&id, &number, &branch, &sup, &sname, &receipt, &inv, &reason, &total, &settlement, &acc, &by, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "supplier_id": sup, "supplier": sname, "receipt_id": receipt,
			"invoice_id": inv, "reason": reason, "total": total, "settlement": settlement, "money_account_id": acc, "created_by": by, "created_at": at})
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) ListReturns(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := returnWhere(f, p.ScopeBranches("po.view"))
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM purchase_returns r`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, returnCols+q.Where()+` ORDER BY r.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		out, err = scanReturns(rows)
		return err
	})
	return orEmpty(out), total, err
}

func (s *Service) GetReturn(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, returnCols+` WHERE r.id=$1`, id)
		if err != nil {
			return err
		}
		list, err := scanReturns(rows)
		if err != nil {
			return err
		}
		if len(list) == 0 || !p.SeesBranch("po.view", list[0]["branch_id"].(uuid.UUID)) {
			return httpx.NotFound("not_found", "Purchase return not found")
		}
		out = list[0]
		items, err := tx.Query(ctx, `
			SELECT ri.receipt_item_id, ri.product_id, p.name, p.sku, ri.qty, ri.unit_cost, round(ri.qty*ri.unit_cost,2)
			FROM purchase_return_items ri JOIN products p ON p.id=ri.product_id WHERE ri.return_id=$1 ORDER BY p.name
		`, id)
		if err != nil {
			return err
		}
		defer items.Close()
		var lines []map[string]any
		for items.Next() {
			var rid *uuid.UUID
			var pid uuid.UUID
			var name, sku string
			var qty, cost, value decimal.Decimal
			if err := items.Scan(&rid, &pid, &name, &sku, &qty, &cost, &value); err != nil {
				return err
			}
			lines = append(lines, map[string]any{"receipt_item_id": rid, "product_id": pid, "product_name": name, "sku": sku, "qty": qty, "unit_cost": cost, "value": value})
		}
		out["items"] = orEmpty(lines)
		return items.Err()
	})
	return out, err
}
