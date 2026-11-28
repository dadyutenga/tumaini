package inventory

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
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
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT p.id, p.name::text, coalesce(p.sku, ''), coalesce(sum(b.quantity_on_hand), 0), p.average_cost,\n\t\t\t       coalesce(p.min_stock_level, ts.low_stock_default)\n\t\t\t		FROM app.products p CROSS JOIN app.business_settings ts\n\t\t\tLEFT JOIN app.stock_balances b ON b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)\n\t\t\tWHERE p.status = 'active'\n\t\t\tGROUP BY p.id, ts.low_stock_default ORDER BY p.name", locationFilter(p))
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
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT p.id, p.name::text, coalesce(p.sku, ''), coalesce(sum(b.quantity_on_hand), 0), coalesce(p.min_stock_level, ts.low_stock_default)\n\t\t\t		FROM app.products p CROSS JOIN app.business_settings ts\n\t\t\tLEFT JOIN app.stock_balances b ON b.product_id = p.id AND ($1::uuid IS NULL OR b.location_id = $1)\n\t\t\tWHERE p.status = 'active'\n\t\t\tGROUP BY p.id, ts.low_stock_default, p.min_stock_level\n\t\t\tHAVING coalesce(sum(b.quantity_on_hand), 0) <= coalesce(p.min_stock_level, ts.low_stock_default)\n\t\t\tORDER BY p.name", locationFilter(p))
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
	LEFT JOIN app.sale_lines sl ON sl.id = m.sale_line_id
	LEFT JOIN app.sale_return_lines srl ON srl.id = m.sale_return_line_id
	LEFT JOIN app.goods_receipt_lines grl ON grl.id = m.goods_receipt_line_id
	LEFT JOIN app.stock_adjustment_lines sal ON sal.id = m.stock_adjustment_line_id
	LEFT JOIN app.stock_transfer_lines stl ON stl.id = m.stock_transfer_line_id`

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
		if err := tx.QueryRow(ctx, "SELECT name::text FROM app.products WHERE id = $1", productID).Scan(&name); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		if from != "" {
			if err := tx.QueryRow(ctx, "\n\t\t\t\tSELECT coalesce(sum(quantity_delta), 0) FROM app.stock_movements\n\t\t\t\tWHERE product_id = $1 AND ($2::uuid IS NULL OR location_id = $2)\n\t\t\t\t  AND occurred_at < $3::date", productID, locationFilter(p), from).Scan(&opening); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.movement_type::text, m.quantity_delta, coalesce(m.unit_cost, 0), `+sourceType+`, `+sourceID+`, m.occurred_at
			FROM app.stock_movements m`+sourceJoins+`
			WHERE m.product_id = $1 AND ($2::uuid IS NULL OR m.location_id = $2)
			  AND ($3 = '' OR m.occurred_at >= $3::date) AND ($4 = '' OR m.occurred_at < $4::date + 1)
			ORDER BY m.occurred_at, m.id`, productID, locationFilter(p), from, to)
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
			      WHERE ($1::uuid IS NULL OR m.location_id = $1) AND ($2 = '' OR m.occurred_at < $2::date + 1)
			      GROUP BY m.location_id, m.product_id) x
			JOIN app.products pr ON pr.id = x.product_id
			JOIN app.locations l ON l.id = x.location_id
			LEFT JOIN app.product_categories c ON c.id = pr.category_id
			WHERE x.qty <> 0 ORDER BY l.name, pr.name`, locationFilter(p), asOf)
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
		const where = ` WHERE ($1::uuid IS NULL OR m.location_id = $1) AND ($2::uuid IS NULL OR m.product_id = $2)
			AND ($3 = '' OR m.movement_type = $3::app.stock_movement_type)
			AND ($4 = '' OR m.occurred_at >= $4::date) AND ($5 = '' OR m.occurred_at < $5::date + 1)`
		args := []any{locationFilter(p), f.ProductID, typ, f.From, f.To}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_movements m`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.location_id, m.product_id, p.name::text, m.movement_type::text, m.quantity_delta, coalesce(m.unit_cost, 0),
			       `+sourceType+`, `+sourceID+`, mb.user_id, m.occurred_at
			FROM app.stock_movements m JOIN app.products p ON p.id = m.product_id`+sourceJoins+`
			LEFT JOIN app.memberships mb ON mb.id = m.created_by`+where+`
			ORDER BY m.occurred_at DESC, m.id DESC LIMIT $6 OFFSET $7`, append(args, page.PerPage, page.Offset)...)
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

// Entries lists posted stock entries: adjustments created from a stated reason, as opposed to counts.
func (s *Service) Entries(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		const where = ` WHERE a.reason <> 'count_correction' AND a.status = 'posted'
			AND ($1::uuid IS NULL OR a.location_id = $1)
			AND ($2 = '' OR (CASE WHEN v.net >= 0 THEN 'in' ELSE 'out' END) = $2)
			AND ($3 = '' OR a.reason::text = $3)
			AND ($4 = '' OR a.created_at >= $4::date) AND ($5 = '' OR a.created_at < $5::date + 1)`
		const from = ` FROM app.stock_adjustments a
			JOIN LATERAL (SELECT coalesce(sum(l.quantity_delta), 0) AS net, count(*) AS n,
			                     coalesce(sum(abs(l.quantity_delta) * coalesce(l.unit_cost, pr.average_cost)), 0) AS value
			                FROM app.stock_adjustment_lines l JOIN app.products pr ON pr.id = l.product_id
			               WHERE l.stock_adjustment_id = a.id) v ON true
			LEFT JOIN app.memberships m ON m.id = a.created_by`
		args := []any{locationFilter(p), f.Direction, f.Reason, f.From, f.To}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.adjustment_no, a.location_id, CASE WHEN v.net >= 0 THEN 'in' ELSE 'out' END, a.reason::text, a.note, v.value, m.user_id, a.created_at, v.n`+from+where+`
			ORDER BY a.created_at DESC LIMIT $6 OFFSET $7`, append(args, page.PerPage, page.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch uuid.UUID
			var by *uuid.UUID
			var number, dir, reason string
			var note *string
			var value decimal.Decimal
			var at time.Time
			var n int
			if err := rows.Scan(&id, &number, &branch, &dir, &reason, &note, &value, &by, &at, &n); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "direction": dir, "reason": reason, "note": note,
				"total_value": value, "created_by": by, "created_at": at, "item_count": n})
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) GetEntry(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		var by *uuid.UUID
		var number, reason string
		var note *string
		var at time.Time
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT a.adjustment_no, a.location_id, a.reason::text, a.note, m.user_id, a.created_at\n\t\t\tFROM app.stock_adjustments a LEFT JOIN app.memberships m ON m.id = a.created_by\n\t\t\tWHERE a.id = $1 AND a.reason <> 'count_correction'", id).Scan(&number, &branch, &reason, &note, &by, &at); err != nil {
			return httpx.NotFound("not_found", "Stock entry not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT l.product_id, pr.name::text, coalesce(pr.sku, ''), abs(l.quantity_delta), coalesce(l.unit_cost, pr.average_cost),
			       round(abs(l.quantity_delta) * coalesce(l.unit_cost, pr.average_cost), 2)
			FROM app.stock_adjustment_lines l JOIN app.products pr ON pr.id = l.product_id
			WHERE l.stock_adjustment_id = $1 ORDER BY pr.name`, id, []string{"product_id", "product_name", "sku", "qty", "unit_cost", "value"})
		if err != nil {
			return err
		}
		dir, value := "in", money.Zero
		var net decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(quantity_delta), 0) FROM app.stock_adjustment_lines WHERE stock_adjustment_id = $1`, id).Scan(&net); err != nil {
			return err
		}
		if net.IsNegative() {
			dir = "out"
		}
		for _, it := range items {
			if v, ok := it["value"].(decimal.Decimal); ok {
				value = value.Add(v)
			}
			it["batch_no"], it["expiry_date"] = nil, nil
		}
		out = map[string]any{"id": id, "number": number, "branch_id": branch, "direction": dir, "reason": reason, "note": note,
			"total_value": value, "created_by": by, "created_at": at, "items": items}
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
	out := []map[string]any{}
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
	return out, rows.Err()
}

// adjustmentNote splits the stock-take marker off a note.
func adjustmentNote(note *string) (string, bool) {
	if note == nil {
		return "", false
	}
	if strings.HasPrefix(*note, stockTakeMark) {
		return strings.TrimPrefix(*note, stockTakeMark), true
	}
	return *note, false
}

// Adjustments lists counted adjustments (stock counts and stock takes).
func (s *Service) Adjustments(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		const where = ` WHERE a.reason = 'count_correction' AND ($1::uuid IS NULL OR a.location_id = $1)
			AND ($2 = '' OR a.status::text = $2) AND ($3 = '' OR a.created_at >= $3::date) AND ($4 = '' OR a.created_at < $4::date + 1)`
		args := []any{locationFilter(p), f.Status, f.From, f.To}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_adjustments a`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.adjustment_no, a.location_id, coalesce(a.note, ''), a.status::text, m.user_id, a.created_at, a.posted_at,
			       (SELECT coalesce(sum(round(cl.counted_qty * pr.average_cost - coalesce(cl.system_qty, 0) * pr.average_cost, 2)), 0)
			          FROM app.stock_counts c JOIN app.stock_count_lines cl ON cl.stock_count_id = c.id
			          JOIN app.products pr ON pr.id = cl.product_id
			         WHERE c.stock_adjustment_id = a.id)
			FROM app.stock_adjustments a LEFT JOIN app.memberships m ON m.id = a.created_by`+where+`
			ORDER BY a.created_at DESC LIMIT $5 OFFSET $6`, append(args, page.PerPage, page.Offset)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch uuid.UUID
			var by *uuid.UUID
			var number, note, st string
			var value decimal.Decimal
			var at time.Time
			var posted *time.Time
			if err := rows.Scan(&id, &number, &branch, &note, &st, &by, &at, &posted, &value); err != nil {
				return err
			}
			reason, take := adjustmentNote(&note)
			out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "reason": reason, "status": st, "is_stock_take": take,
				"total_value": value, "created_by": by, "created_at": at, "posted_at": posted})
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) GetAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch, count uuid.UUID
		var by *uuid.UUID
		var number, st string
		var note *string
		var at time.Time
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT a.adjustment_no, a.location_id, a.note, a.status::text, m.user_id, a.created_at, coalesce(c.id, '00000000-0000-0000-0000-000000000000'::uuid)\n\t\t\tFROM app.stock_adjustments a\n\t\t\tLEFT JOIN app.memberships m ON m.id = a.created_by\n\t\t\tLEFT JOIN app.stock_counts c ON c.stock_adjustment_id = a.id\n\t\t\tWHERE a.id = $1 AND a.reason = 'count_correction'", id).Scan(&number, &branch, &note, &st, &by, &at, &count); err != nil {
			return httpx.NotFound("not_found", "Adjustment not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT cl.product_id, pr.name::text, coalesce(pr.sku, ''), cl.system_qty, cl.counted_qty, cl.counted_qty - cl.system_qty, pr.average_cost,
			       round((cl.counted_qty - cl.system_qty) * pr.average_cost, 2)
			FROM app.stock_count_lines cl JOIN app.products pr ON pr.id = cl.product_id
			WHERE cl.stock_count_id = $1 ORDER BY pr.name`, count,
			[]string{"product_id", "product_name", "sku", "qty_system", "qty_counted", "difference", "unit_cost", "value"})
		if err != nil {
			return err
		}
		value := money.Zero
		for _, it := range items {
			if v, ok := it["value"].(decimal.Decimal); ok {
				value = value.Add(v)
			}
		}
		reason, take := adjustmentNote(note)
		out = map[string]any{"id": id, "number": number, "branch_id": branch, "reason": reason, "status": st, "is_stock_take": take,
			"total_value": value, "created_by": by, "created_at": at, "items": items}
		return nil
	})
	return out, err
}

func (s *Service) Transfers(ctx context.Context, p *auth.Principal, direction string, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	var total int
	if direction != "" && direction != "in" && direction != "out" {
		return out, 0, httpx.BadRequest("invalid_query", "direction must be in or out")
	}
	if direction != "" && p.BranchID == nil {
		return out, 0, httpx.BadRequest("branch_required", "Choose a branch to see transfers "+direction)
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		const where = ` WHERE (CASE $1 WHEN 'in' THEN t.to_location_id = $2 WHEN 'out' THEN t.from_location_id = $2
			             ELSE ($2::uuid IS NULL OR t.from_location_id = $2 OR t.to_location_id = $2) END)
			AND ($3 = '' OR t.status::text = $3) AND ($4 = '' OR t.created_at >= $4::date) AND ($5 = '' OR t.created_at < $5::date + 1)`
		args := []any{direction, p.BranchID, f.Status, f.From, f.To}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_transfers t`+where, args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.transfer_no, t.from_location_id, fb.name::text, t.to_location_id, tb.name::text, t.status::text, t.note, t.created_at, t.dispatched_at, t.received_at,
			       (SELECT coalesce(sum(round(l.quantity * pr.average_cost, 2)), 0) FROM app.stock_transfer_lines l
			         JOIN app.products pr ON pr.id = l.product_id WHERE l.stock_transfer_id = t.id)
			FROM app.stock_transfers t
			JOIN app.locations fb ON fb.id = t.from_location_id
			JOIN app.locations tb ON tb.id = t.to_location_id`+where+`
			ORDER BY t.created_at DESC LIMIT $6 OFFSET $7`, append(args, page.PerPage, page.Offset)...)
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
			out = append(out, map[string]any{"id": id, "number": number, "from_branch_id": from, "from_branch": fname, "to_branch_id": to, "to_branch": tname,
				"status": st, "note": note, "created_at": at, "dispatched_at": disp, "received_at": recv, "value": value})
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) GetTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var from, to uuid.UUID
		var by *uuid.UUID
		var number, fname, tname, st string
		var note *string
		var at time.Time
		var disp, recv *time.Time
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT t.transfer_no, t.from_location_id, fb.name::text, t.to_location_id, tb.name::text, t.status::text, t.note, m.user_id, t.created_at, t.dispatched_at, t.received_at\n\t\t\tFROM app.stock_transfers t\n\t\t\tJOIN app.locations fb ON fb.id = t.from_location_id\n\t\t\tJOIN app.locations tb ON tb.id = t.to_location_id\n\t\t\tLEFT JOIN app.memberships m ON m.id = t.created_by\n\t\t\tWHERE t.id = $1", id).Scan(&number, &from, &fname, &to, &tname, &st, &note, &by, &at, &disp, &recv); err != nil {
			return httpx.NotFound("not_found", "Transfer not found")
		}
		items, err := itemRows(ctx, tx, `
			SELECT l.product_id, pr.name::text, coalesce(pr.sku, ''), l.quantity,
			       CASE WHEN (SELECT t.status FROM app.stock_transfers t WHERE t.id = l.stock_transfer_id) = 'received' THEN l.quantity END, 0::numeric, pr.average_cost
			FROM app.stock_transfer_lines l JOIN app.products pr ON pr.id = l.product_id
			WHERE l.stock_transfer_id = $1 ORDER BY pr.name`, id,
			[]string{"product_id", "product_name", "sku", "qty_sent", "qty_received", "qty_difference", "unit_cost"})
		if err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": number, "from_branch_id": from, "from_branch": fname, "to_branch_id": to, "to_branch": tname, "status": st,
			"note": note, "created_by": by, "created_at": at, "dispatched_at": disp, "received_at": recv, "items": items}
		return nil
	})
	return out, err
}
