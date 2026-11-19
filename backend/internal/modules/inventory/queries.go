package inventory

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

func orEmpty(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
}

// locationFilter is the location a stock read is limited to: the active branch, or every location
// when none is selected (X-Branch-Id: all).
func locationFilter(p *auth.Principal) *uuid.UUID { return p.BranchID }

func (s *Service) Levels(ctx context.Context, p *auth.Principal, showCost bool) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name::text, coalesce(p.sku, ''), coalesce(sum(b.quantity_on_hand), 0), p.average_cost,
			       coalesce(p.min_stock_level, ts.low_stock_default)
			FROM app.products p JOIN app.tenant_settings ts ON ts.tenant_id = p.tenant_id
			LEFT JOIN app.stock_balances b ON b.tenant_id = p.tenant_id AND b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)
			WHERE p.tenant_id = $2 AND p.status = 'active'
			GROUP BY p.id, ts.low_stock_default ORDER BY p.name`, locationFilter(p), p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name, sku string
			var qty, cost, reorder decimal.Decimal
			if err := rows.Scan(&id, &name, &sku, &qty, &cost, &reorder); err != nil {
				return err
			}
			row := map[string]any{"product_id": id, "name": name, "sku": sku, "qty_on_hand": qty, "reorder_level": reorder}
			if showCost {
				row["avg_cost"] = cost
				row["value"] = money.Money(qty.Mul(cost))
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) Low(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name::text, coalesce(p.sku, ''), coalesce(sum(b.quantity_on_hand), 0), coalesce(p.min_stock_level, ts.low_stock_default)
			FROM app.products p JOIN app.tenant_settings ts ON ts.tenant_id = p.tenant_id
			LEFT JOIN app.stock_balances b ON b.tenant_id = p.tenant_id AND b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)
			WHERE p.tenant_id = $2 AND p.status = 'active'
			GROUP BY p.id, ts.low_stock_default, p.min_stock_level
			HAVING coalesce(sum(b.quantity_on_hand), 0) <= coalesce(p.min_stock_level, ts.low_stock_default)
			ORDER BY p.name`, locationFilter(p), p.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name, sku string
			var qty, reorder decimal.Decimal
			if err := rows.Scan(&id, &name, &sku, &qty, &reorder); err != nil {
				return err
			}
			suggest := reorder.Mul(decimal.NewFromInt(2)).Sub(qty)
			if suggest.IsNegative() {
				suggest = decimal.Zero
			}
			out = append(out, map[string]any{"product_id": id, "name": name, "sku": sku, "qty_on_hand": qty, "reorder_level": reorder, "suggested_qty": suggest})
		}
		return rows.Err()
	})
	return out, err
}

// sourceJoins resolves which document a stock movement came from. Each movement row points at exactly
// one line (the table has a one-of constraint), so exactly one of these joins matches.
const sourceJoins = `
	LEFT JOIN app.sale_lines sl ON sl.tenant_id = m.tenant_id AND sl.id = m.sale_line_id
	LEFT JOIN app.sale_return_lines srl ON srl.tenant_id = m.tenant_id AND srl.id = m.sale_return_line_id
	LEFT JOIN app.goods_receipt_lines grl ON grl.tenant_id = m.tenant_id AND grl.id = m.goods_receipt_line_id
	LEFT JOIN app.stock_adjustment_lines sal ON sal.tenant_id = m.tenant_id AND sal.id = m.stock_adjustment_line_id
	LEFT JOIN app.stock_transfer_lines stl ON stl.tenant_id = m.tenant_id AND stl.id = m.stock_transfer_line_id`

const sourceID = `coalesce(sl.sale_id, srl.sale_return_id, grl.goods_receipt_id, sal.stock_adjustment_id, stl.stock_transfer_id)`
const sourceType = `CASE WHEN sl.id IS NOT NULL THEN 'sale' WHEN srl.id IS NOT NULL THEN 'sale_return' WHEN grl.id IS NOT NULL THEN 'goods_receipt'
	WHEN sal.id IS NOT NULL THEN 'stock_adjustment' ELSE 'stock_transfer' END`

// StockCard lists movements with a running balance for one product. With from, opening_qty on each row
// is the balance before it. The balance is the running sum of signed quantity changes.
func (s *Service) StockCard(ctx context.Context, p *auth.Principal, productID uuid.UUID, from, to string) ([]map[string]any, error) {
	out := []map[string]any{}
	var name string
	opening := money.Zero
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name::text FROM app.products WHERE tenant_id = $1 AND id = $2`, p.TenantID, productID).Scan(&name); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		if err := tx.QueryRow(ctx, `
			SELECT coalesce(sum(quantity_delta), 0) FROM app.stock_movements
			WHERE tenant_id = $1 AND product_id = $2 AND ($3::uuid IS NULL OR location_id = $3)
			  AND ($4 = '' OR occurred_at < $4::date)`, p.TenantID, productID, locationFilter(p), from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.movement_type::text, m.quantity_delta, coalesce(m.unit_cost, 0), `+sourceType+`, `+sourceID+`, m.occurred_at
			FROM app.stock_movements m`+sourceJoins+`
			WHERE m.tenant_id = $1 AND m.product_id = $2 AND ($3::uuid IS NULL OR m.location_id = $3)
			  AND ($4 = '' OR m.occurred_at >= $4::date) AND ($5 = '' OR m.occurred_at < $5::date + 1)
			ORDER BY m.occurred_at, m.id`, p.TenantID, productID, locationFilter(p), from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		balance := opening
		for rows.Next() {
			var id, source uuid.UUID
			var typ, st string
			var qty, cost decimal.Decimal
			var at time.Time
			if err := rows.Scan(&id, &typ, &qty, &cost, &st, &source, &at); err != nil {
				return err
			}
			before := balance
			balance = balance.Add(qty)
			out = append(out, map[string]any{"id": id, "type": typ, "qty": qty, "unit_cost": cost, "qty_after": balance, "balance": balance,
				"opening_qty": before, "product_name": name, "source_type": st, "source_id": source, "created_at": at})
		}
		return rows.Err()
	})
	return out, err
}

// Valuation values stock per product, category and location, today or as of a past date. Quantities
// are exact for any date (the sum of movements up to it); the cost is each product's current moving
// average, because the approved model keeps one running average per product, not a history of it.
func (s *Service) Valuation(ctx context.Context, p *auth.Principal, asOf string) (map[string]any, error) {
	lines := []map[string]any{}
	total := money.Zero
	byCat := map[string]decimal.Decimal{}
	byBranch := map[string]decimal.Decimal{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT x.location_id, l.name::text, pr.id, pr.name::text, c.name::text, x.qty, pr.average_cost
			FROM (SELECT m.location_id, m.product_id, sum(m.quantity_delta) AS qty FROM app.stock_movements m
			      WHERE m.tenant_id = $1 AND ($2::uuid IS NULL OR m.location_id = $2) AND ($3 = '' OR m.occurred_at < $3::date + 1)
			      GROUP BY m.location_id, m.product_id) x
			JOIN app.products pr ON pr.tenant_id = $1 AND pr.id = x.product_id
			JOIN app.locations l ON l.tenant_id = $1 AND l.id = x.location_id
			LEFT JOIN app.product_categories c ON c.tenant_id = $1 AND c.id = pr.category_id
			WHERE x.qty <> 0 ORDER BY l.name, pr.name`, p.TenantID, locationFilter(p), asOf)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var branch, id uuid.UUID
			var bname, name string
			var cat *string
			var qty, cost decimal.Decimal
			if err := rows.Scan(&branch, &bname, &id, &name, &cat, &qty, &cost); err != nil {
				return err
			}
			value := money.Money(qty.Mul(cost))
			total = total.Add(value)
			cname := "Uncategorised"
			if cat != nil {
				cname = *cat
			}
			byCat[cname] = byCat[cname].Add(value)
			byBranch[bname] = byBranch[bname].Add(value)
			lines = append(lines, map[string]any{"branch_id": branch, "branch": bname, "product_id": id, "name": name, "category": cat, "qty": qty, "avg_cost": cost, "value": value})
		}
		return rows.Err()
	})
	cats := []map[string]any{}
	for k, v := range byCat {
		cats = append(cats, map[string]any{"category": k, "value": v})
	}
	brs := []map[string]any{}
	for k, v := range byBranch {
		brs = append(brs, map[string]any{"branch": k, "value": v})
	}
	return map[string]any{"as_of": asOf, "total": total, "lines": lines, "by_category": cats, "by_branch": brs}, err
}

type MovementFilter struct {
	ProductID *uuid.UUID
	Type      string
	From, To  string
}

// movementTypes maps the legacy movement names a client may still send to the enum values.
var movementTypes = map[string]string{
	"purchase": "purchase_receipt", "purchase_receipt": "purchase_receipt", "sale": "sale", "sale_void": "sale_void",
	"sale_return": "sale_return", "adjustment": "adjustment", "transfer_out": "transfer_out", "transfer_in": "transfer_in",
}

func (s *Service) Movements(ctx context.Context, p *auth.Principal, f MovementFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	var total int
	typ := movementTypes[f.Type]
	if f.Type != "" && typ == "" {
		return out, 0, nil
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		const where = ` WHERE m.tenant_id = $1 AND ($2::uuid IS NULL OR m.location_id = $2) AND ($3::uuid IS NULL OR m.product_id = $3)
			AND ($4 = '' OR m.movement_type = $4::app.stock_movement_type)
			AND ($5 = '' OR m.occurred_at >= $5::date) AND ($6 = '' OR m.occurred_at < $6::date + 1)`
		args := []any{p.TenantID, locationFilter(p), f.ProductID, typ, f.From, f.To}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_movements m`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.location_id, m.product_id, p.name::text, m.movement_type::text, m.quantity_delta, coalesce(m.unit_cost, 0),
			       `+sourceType+`, `+sourceID+`, mb.user_id, m.occurred_at
			FROM app.stock_movements m JOIN app.products p ON p.tenant_id = m.tenant_id AND p.id = m.product_id`+sourceJoins+`
			LEFT JOIN app.memberships mb ON mb.tenant_id = m.tenant_id AND mb.id = m.created_by`+where+`
			ORDER BY m.occurred_at DESC, m.id DESC LIMIT $7 OFFSET $8`, append(args, page.PerPage, page.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, pid, source uuid.UUID
			var by *uuid.UUID
			var name, mt, st string
			var qty, cost decimal.Decimal
			var at time.Time
			if err := rows.Scan(&id, &branch, &pid, &name, &mt, &qty, &cost, &st, &source, &by, &at); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "branch_id": branch, "product_id": pid, "product_name": name, "type": mt, "qty": qty,
				"unit_cost": cost, "source_type": st, "source_id": source, "created_by": by, "created_at": at})
		}
		return rows.Err()
	})
	return out, total, err
}

type DocFilter struct {
	Status, Direction, Reason string
	From, To                  string
}

func (s *Service) Entries(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("stock.view"); b != nil {
			q.Add("e.branch_id = ANY(?)", b)
		}
		q.If(f.Direction != "", "e.direction = ?", f.Direction)
		q.If(f.Reason != "", "e.reason = ?", f.Reason)
		q.If(f.From != "", "e.created_at >= ?::date", f.From)
		q.If(f.To != "", "e.created_at < ?::date + 1", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_entries e`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.number, e.branch_id, e.direction, e.reason, e.note, e.total_value, e.created_by, e.created_at,
			       (SELECT count(*) FROM stock_entry_items i WHERE i.entry_id=e.id)
			FROM stock_entries e`+q.Where()+` ORDER BY e.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, by uuid.UUID
			var number, dir, reason string
			var note *string
			var value decimal.Decimal
			var at time.Time
			var n int
			if err := rows.Scan(&id, &number, &branch, &dir, &reason, &note, &value, &by, &at, &n); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "direction": dir, "reason": reason, "note": note, "total_value": value, "created_by": by, "created_at": at, "item_count": n})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

func (s *Service) GetEntry(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch, by uuid.UUID
		var number, dir, reason string
		var note *string
		var value decimal.Decimal
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT number, branch_id, direction, reason, note, total_value, created_by, created_at FROM stock_entries WHERE id=$1`, id).
			Scan(&number, &branch, &dir, &reason, &note, &value, &by, &at)
		if err != nil || !p.SeesBranch("stock.view", branch) {
			return httpx.NotFound("not_found", "Stock entry not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT i.product_id, p.name, p.sku, i.qty, i.unit_cost, round(i.qty*i.unit_cost, 2)
			FROM stock_entry_items i JOIN products p ON p.id=i.product_id WHERE i.entry_id=$1 ORDER BY p.name`, id,
			[]string{"product_id", "product_name", "sku", "qty", "unit_cost", "value"})
		if err != nil {
			return err
		}
		// Batch and expiry (FR-INV-32) are strings, so they are merged in beside the decimal columns.
		batches, err := tx.Query(ctx, `SELECT product_id, batch_no, expiry_date::text FROM stock_entry_items WHERE entry_id=$1 AND (batch_no IS NOT NULL OR expiry_date IS NOT NULL)`, id)
		if err != nil {
			return err
		}
		byProduct := map[uuid.UUID][2]*string{}
		for batches.Next() {
			var pid uuid.UUID
			var batch, expiry *string
			if err := batches.Scan(&pid, &batch, &expiry); err != nil {
				batches.Close()
				return err
			}
			byProduct[pid] = [2]*string{batch, expiry}
		}
		batches.Close()
		if err := batches.Err(); err != nil {
			return err
		}
		for _, it := range items {
			b := byProduct[it["product_id"].(uuid.UUID)]
			it["batch_no"], it["expiry_date"] = b[0], b[1]
		}
		out = map[string]any{"id": id, "number": number, "branch_id": branch, "direction": dir, "reason": reason, "note": note, "total_value": value, "created_by": by, "created_at": at, "items": items}
		return nil
	})
	return out, err
}

// itemRows scans uuid, string, string, then decimals into maps keyed by cols.
func itemRows(ctx context.Context, tx pgx.Tx, sql string, id uuid.UUID, cols []string) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, sql, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		var pid uuid.UUID
		var name, sku string
		vals[0], vals[1], vals[2] = &pid, &name, &sku
		decs := make([]decimal.NullDecimal, len(cols)-3)
		for i := range decs {
			vals[3+i] = &decs[i]
		}
		if err := rows.Scan(vals...); err != nil {
			return nil, err
		}
		m := map[string]any{cols[0]: pid, cols[1]: name, cols[2]: sku}
		for i, d := range decs {
			if d.Valid {
				m[cols[3+i]] = d.Decimal
			} else {
				m[cols[3+i]] = nil
			}
		}
		out = append(out, m)
	}
	return orEmpty(out), rows.Err()
}

func (s *Service) Adjustments(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("stock.view"); b != nil {
			q.Add("a.branch_id = ANY(?)", b)
		}
		q.If(f.Status != "", "a.status = ?", f.Status)
		q.If(f.From != "", "a.created_at >= ?::date", f.From)
		q.If(f.To != "", "a.created_at < ?::date + 1", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_adjustments a`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.number, a.branch_id, a.reason, a.status, a.is_stock_take, a.total_value, a.created_by, a.created_at, a.posted_at
			FROM stock_adjustments a`+q.Where()+` ORDER BY a.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, by uuid.UUID
			var number, reason, st string
			var take bool
			var value decimal.Decimal
			var at time.Time
			var posted *time.Time
			if err := rows.Scan(&id, &number, &branch, &reason, &st, &take, &value, &by, &at, &posted); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "reason": reason, "status": st, "is_stock_take": take, "total_value": value, "created_by": by, "created_at": at, "posted_at": posted})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

func (s *Service) GetAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch, by uuid.UUID
		var number, reason, st string
		var take bool
		var value decimal.Decimal
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT number, branch_id, reason, status, is_stock_take, total_value, created_by, created_at FROM stock_adjustments WHERE id=$1`, id).
			Scan(&number, &branch, &reason, &st, &take, &value, &by, &at)
		if err != nil || !p.SeesBranch("stock.view", branch) {
			return httpx.NotFound("not_found", "Adjustment not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT i.product_id, p.name, p.sku, i.qty_system, i.qty_counted, i.qty_counted - i.qty_system, i.unit_cost, round((i.qty_counted - i.qty_system)*i.unit_cost, 2)
			FROM stock_adjustment_items i JOIN products p ON p.id=i.product_id WHERE i.adjustment_id=$1 ORDER BY p.name`, id,
			[]string{"product_id", "product_name", "sku", "qty_system", "qty_counted", "difference", "unit_cost", "value"})
		if err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": number, "branch_id": branch, "reason": reason, "status": st, "is_stock_take": take, "total_value": value, "created_by": by, "created_at": at, "items": items}
		return nil
	})
	return out, err
}

func (s *Service) Transfers(ctx context.Context, p *auth.Principal, direction string, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		branches := p.ScopeBranches("transfers.view")
		switch direction {
		case "in":
			if p.BranchID == nil {
				return httpx.BadRequest("branch_required", "Choose a branch to see transfers in")
			}
			q.Add("t.to_branch_id = ?", *p.BranchID)
		case "out":
			if p.BranchID == nil {
				return httpx.BadRequest("branch_required", "Choose a branch to see transfers out")
			}
			q.Add("t.from_branch_id = ?", *p.BranchID)
		case "":
			if branches != nil {
				q.Add("(t.from_branch_id = ANY(?) OR t.to_branch_id = ANY(?))", branches, branches)
			}
		default:
			return httpx.BadRequest("invalid_query", "direction must be in or out")
		}
		q.If(f.Status != "", "t.status = ?", f.Status)
		q.If(f.From != "", "t.created_at >= ?::date", f.From)
		q.If(f.To != "", "t.created_at < ?::date + 1", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_transfers t`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.number, t.from_branch_id, fb.name, t.to_branch_id, tb.name, t.status, t.note, t.created_at, t.dispatched_at, t.received_at,
			       (SELECT COALESCE(SUM(round(i.qty_sent*i.unit_cost,2)),0) FROM stock_transfer_items i WHERE i.transfer_id=t.id)
			FROM stock_transfers t JOIN branches fb ON fb.id=t.from_branch_id JOIN branches tb ON tb.id=t.to_branch_id`+q.Where()+
			` ORDER BY t.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, from, to uuid.UUID
			var number, fname, tname, st string
			var note *string
			var at time.Time
			var disp, recv *time.Time
			var value decimal.Decimal
			if err := rows.Scan(&id, &number, &from, &fname, &to, &tname, &st, &note, &at, &disp, &recv, &value); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "from_branch_id": from, "from_branch": fname, "to_branch_id": to, "to_branch": tname, "status": st, "note": note, "created_at": at, "dispatched_at": disp, "received_at": recv, "value": value})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

func (s *Service) GetTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var from, to, by uuid.UUID
		var number, fname, tname, st string
		var note *string
		var at time.Time
		var disp, recv *time.Time
		err := tx.QueryRow(ctx, `
			SELECT t.number, t.from_branch_id, fb.name, t.to_branch_id, tb.name, t.status, t.note, t.created_by, t.created_at, t.dispatched_at, t.received_at
			FROM stock_transfers t JOIN branches fb ON fb.id=t.from_branch_id JOIN branches tb ON tb.id=t.to_branch_id WHERE t.id=$1
		`, id).Scan(&number, &from, &fname, &to, &tname, &st, &note, &by, &at, &disp, &recv)
		if err != nil || !(p.SeesBranch("transfers.view", from) || p.SeesBranch("transfers.view", to)) {
			return httpx.NotFound("not_found", "Transfer not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT i.product_id, p.name, p.sku, i.qty_sent, i.qty_received, i.qty_sent - COALESCE(i.qty_received, i.qty_sent), i.unit_cost
			FROM stock_transfer_items i JOIN products p ON p.id=i.product_id WHERE i.transfer_id=$1 ORDER BY p.name`, id,
			[]string{"product_id", "product_name", "sku", "qty_sent", "qty_received", "qty_difference", "unit_cost"})
		if err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": number, "from_branch_id": from, "from_branch": fname, "to_branch_id": to, "to_branch": tname, "status": st, "note": note, "created_by": by, "created_at": at, "dispatched_at": disp, "received_at": recv, "items": items}
		return nil
	})
	return out, err
}
