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

func (s *Service) Levels(ctx context.Context, p *auth.Principal, showCost bool) ([]map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, p.sku, COALESCE(sl.qty_on_hand,0), COALESCE(sl.avg_cost,0), p.reorder_level
			FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$1
			WHERE p.kind='stock' AND p.is_active ORDER BY p.name
		`, *p.BranchID)
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
	return orEmpty(out), err
}

func (s *Service) Low(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, p.sku, COALESCE(sl.qty_on_hand,0), p.reorder_level
			FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$1
			WHERE p.kind='stock' AND p.is_active AND COALESCE(sl.qty_on_hand,0) <= p.reorder_level
			ORDER BY p.name
		`, *p.BranchID)
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
	return orEmpty(out), err
}

// StockCard lists movements with running balance for one product in the active branch (FR-INV-29).
func (s *Service) StockCard(ctx context.Context, p *auth.Principal, productID uuid.UUID, from, to string) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	var name string
	opening := money.Zero
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM products WHERE id=$1`, productID).Scan(&name); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		if from != "" {
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE((SELECT qty_after FROM stock_movements WHERE branch_id=$1 AND product_id=$2 AND created_at < $3::date
				ORDER BY created_at DESC, id DESC LIMIT 1), 0)
			`, *p.BranchID, productID, from).Scan(&opening); err != nil {
				return err
			}
		}
		q := sqlq.New(*p.BranchID, productID)
		q.If(from != "", "created_at >= ?::date", from)
		q.If(to != "", "created_at < ?::date + 1", to)
		rows, err := tx.Query(ctx, `
			SELECT id, type, qty, unit_cost, qty_after, avg_cost_after, source_type, source_id, created_at
			FROM stock_movements WHERE branch_id=$1 AND product_id=$2`+q.And()+` ORDER BY created_at, id`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, source uuid.UUID
			var typ, sourceType string
			var qty, cost, after, avg decimal.Decimal
			var at time.Time
			if err := rows.Scan(&id, &typ, &qty, &cost, &after, &avg, &sourceType, &source, &at); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "type": typ, "qty": qty, "unit_cost": cost, "qty_after": after, "balance": after, "avg_cost_after": avg, "source_type": sourceType, "source_id": source, "created_at": at})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	out = orEmpty(out)
	closing := opening
	if len(out) > 0 {
		closing = out[len(out)-1]["qty_after"].(decimal.Decimal)
	}
	return map[string]any{"product_id": productID, "product_name": name, "branch_id": *p.BranchID, "opening_qty": opening, "closing_qty": closing, "movements": out}, nil
}

// Valuation values stock per product, category and branch, today or as of a past date (FR-INV-28).
func (s *Service) Valuation(ctx context.Context, p *auth.Principal, asOf string) (map[string]any, error) {
	branches := p.ScopeBranches("valuation.view")
	var lines []map[string]any
	total := money.Zero
	byCat := map[string]decimal.Decimal{}
	byBranch := map[string]decimal.Decimal{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var sql string
		q := sqlq.New()
		if asOf == "" {
			q.If(branches != nil, "sl.branch_id = ANY(?)", branches)
			sql = `SELECT sl.branch_id, b.name, p.id, p.name, c.name, sl.qty_on_hand, sl.avg_cost
				FROM stock_levels sl JOIN products p ON p.id=sl.product_id JOIN branches b ON b.id=sl.branch_id
				LEFT JOIN categories c ON c.id=p.category_id
				WHERE sl.qty_on_hand <> 0` + q.And() + ` ORDER BY b.name, p.name`
		} else {
			cut := q.Arg(asOf)
			q.If(branches != nil, "m.branch_id = ANY(?)", branches)
			sql = `SELECT x.branch_id, b.name, p.id, p.name, c.name, x.qty_after, x.avg_cost_after
				FROM (SELECT DISTINCT ON (m.branch_id, m.product_id) m.branch_id, m.product_id, m.qty_after, m.avg_cost_after
				      FROM stock_movements m WHERE m.created_at < ` + cut + `::date + 1` + q.And() + `
				      ORDER BY m.branch_id, m.product_id, m.created_at DESC, m.id DESC) x
				JOIN products p ON p.id=x.product_id JOIN branches b ON b.id=x.branch_id
				LEFT JOIN categories c ON c.id=p.category_id
				WHERE x.qty_after <> 0 ORDER BY b.name, p.name`
		}
		rows, err := tx.Query(ctx, sql, q.Args...)
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
	return map[string]any{"as_of": asOf, "total": total, "lines": orEmpty(lines), "by_category": cats, "by_branch": brs}, err
}

type MovementFilter struct {
	ProductID *uuid.UUID
	Type      string
	From, To  string
}

func (s *Service) Movements(ctx context.Context, p *auth.Principal, f MovementFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("stock.view"); b != nil {
			q.Add("m.branch_id = ANY(?)", b)
		}
		q.If(f.ProductID != nil, "m.product_id = ?", f.ProductID)
		q.If(f.Type != "", "m.type = ?", f.Type)
		q.If(f.From != "", "m.created_at >= ?::date", f.From)
		q.If(f.To != "", "m.created_at < ?::date + 1", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_movements m`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT m.id, m.branch_id, m.product_id, p.name, m.type, m.qty, m.unit_cost, m.qty_after, m.source_type, m.source_id, m.created_by, m.created_at
			FROM stock_movements m JOIN products p ON p.id=m.product_id`+q.Where()+` ORDER BY m.created_at DESC, m.id DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, pid, source uuid.UUID
			var by *uuid.UUID
			var name, typ, st string
			var qty, cost, after decimal.Decimal
			var at time.Time
			if err := rows.Scan(&id, &branch, &pid, &name, &typ, &qty, &cost, &after, &st, &source, &by, &at); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "branch_id": branch, "product_id": pid, "product_name": name, "type": typ, "qty": qty, "unit_cost": cost, "qty_after": after, "source_type": st, "source_id": source, "created_by": by, "created_at": at})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

type DocFilter struct {
	Status, Direction, Reason string
	From, To                  string
}

func (s *Service) Entries(ctx context.Context, p *auth.Principal, f DocFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
