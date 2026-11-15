package inventory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
)

type Service struct {
	DB       *db.DB
	Stock    *stock.Service
	Ledger   *ledger.Service
	Audit    *audit.Service
	Notify   *notify.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, st *stock.Service, led *ledger.Service, aud *audit.Service, n *notify.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Stock: st, Ledger: led, Audit: aud, Notify: n, Approval: ap, Clock: clk}
}

type Item struct {
	ProductID uuid.UUID       `json:"product_id"`
	Qty       decimal.Decimal `json:"qty"`
	UnitCost  decimal.Decimal `json:"unit_cost"`
}

type EntryInput struct {
	Direction string `json:"direction"`
	Reason    string `json:"reason"`
	Note      string `json:"note"`
	Items     []Item `json:"items"`
}

func (s *Service) PostEntry(ctx context.Context, p *auth.Principal, in EntryInput) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "stock_entry")
		if err != nil {
			return err
		}
		id := ids.New()
		total := money.Zero
		lines := make([]stock.Line, 0, len(in.Items))
		for _, it := range in.Items {
			if it.Qty.LessThanOrEqual(money.Zero) {
				return httpx.BadRequest("invalid_body", "Quantity must be greater than zero")
			}
			cost := money.Cost(it.UnitCost)
			total = total.Add(money.Money(it.Qty.Mul(cost)))
			lines = append(lines, stock.Line{ProductID: it.ProductID, Qty: it.Qty, UnitCost: cost})
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_entries (id, business_id, branch_id, number, direction, reason, note, total_value, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, id, p.BusinessID, *p.BranchID, number, in.Direction, in.Reason, in.Note, total, p.UserID); err != nil {
			return err
		}
		for _, it := range in.Items {
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_entry_items (id, business_id, entry_id, product_id, qty, unit_cost) VALUES ($1,$2,$3,$4,$5,$6)
			`, ids.New(), p.BusinessID, id, it.ProductID, it.Qty, money.Cost(it.UnitCost)); err != nil {
				return err
			}
		}
		user := p.UserID
		date := s.Clock.Now().Format("2006-01-02")
		if in.Direction == "in" {
			move := "stock_in"
			credit := "4200"
			if in.Reason == "opening" {
				move = "opening"
				credit = "3100"
			}
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, *p.BranchID, &user, move, "stock_entry", id, lines); err != nil {
				return err
			}
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_entry", id, "Stock in "+number, &user, []ledger.Line{
				{Code: "1200", BranchID: p.BranchID, Debit: total},
				{Code: credit, BranchID: p.BranchID, Credit: total},
			}); err != nil {
				return err
			}
		} else {
			costs, err := s.Stock.Deduct(ctx, tx, p.BusinessID, *p.BranchID, &user, "stock_out", "stock_entry", id, lines)
			if err != nil {
				return err
			}
			value := money.Zero
			for _, it := range in.Items {
				value = value.Add(money.Money(it.Qty.Mul(costs[it.ProductID])))
			}
			_, _ = tx.Exec(ctx, `UPDATE stock_entries SET total_value=$2 WHERE id=$1`, id, value)
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_entry", id, "Stock out "+number, &user, []ledger.Line{
				{Code: "5100", BranchID: p.BranchID, Debit: value},
				{Code: "1200", BranchID: p.BranchID, Credit: value},
			}); err != nil {
				return err
			}
			total = value
		}
		_ = jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": p.BranchID, "product_ids": productIDs(in.Items)})
		if err := s.Audit.Record(ctx, tx, p, "stock_entry.posted", "stock_entry", id, nil, map[string]any{"number": number, "total": total}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "total_value": total, "direction": in.Direction}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func productIDs(items []Item) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = it.ProductID
	}
	return out
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
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
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
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) StockCard(ctx context.Context, p *auth.Principal, productID uuid.UUID) ([]map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, type, qty, unit_cost, qty_after, avg_cost_after, source_type, source_id, created_at
			FROM stock_movements WHERE branch_id=$1 AND product_id=$2 ORDER BY created_at, id
		`, *p.BranchID, productID)
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
			out = append(out, map[string]any{"id": id, "type": typ, "qty": qty, "unit_cost": cost, "qty_after": after, "avg_cost_after": avg, "source_type": sourceType, "source_id": source, "created_at": at})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) Valuation(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var rowsOut []map[string]any
	total := money.Zero
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, p.name, c.name, sl.qty_on_hand, sl.avg_cost
			FROM stock_levels sl JOIN products p ON p.id=sl.product_id
			LEFT JOIN categories c ON c.id=p.category_id
			WHERE sl.branch_id=$1 AND sl.qty_on_hand <> 0 ORDER BY p.name
		`, *p.BranchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var cat *string
			var qty, cost decimal.Decimal
			if err := rows.Scan(&id, &name, &cat, &qty, &cost); err != nil {
				return err
			}
			value := money.Money(qty.Mul(cost))
			total = total.Add(value)
			rowsOut = append(rowsOut, map[string]any{"product_id": id, "name": name, "category": cat, "qty": qty, "avg_cost": cost, "value": value})
		}
		return rows.Err()
	})
	if rowsOut == nil {
		rowsOut = []map[string]any{}
	}
	return map[string]any{"total": total, "lines": rowsOut}, err
}

type CountLine struct {
	ProductID  uuid.UUID       `json:"product_id"`
	QtyCounted decimal.Decimal `json:"qty_counted"`
}

func (s *Service) CreateAdjustment(ctx context.Context, p *auth.Principal, reason string, lines []CountLine, stockTake bool) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		number, err := sequence.Next(ctx, tx, *p.BranchID, "adjustment")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_adjustments (id, business_id, branch_id, number, reason, is_stock_take, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, *p.BranchID, number, reason, stockTake, p.UserID); err != nil {
			return err
		}
		value := money.Zero
		for _, ln := range lines {
			var qty, cost decimal.Decimal
			_ = tx.QueryRow(ctx, `SELECT qty_on_hand, avg_cost FROM stock_levels WHERE branch_id=$1 AND product_id=$2`, *p.BranchID, ln.ProductID).Scan(&qty, &cost)
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_adjustment_items (id, business_id, adjustment_id, product_id, qty_system, qty_counted, unit_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, ids.New(), p.BusinessID, id, ln.ProductID, qty, ln.QtyCounted, cost); err != nil {
				return err
			}
			value = value.Add(money.Money(ln.QtyCounted.Sub(qty).Mul(cost)))
		}
		_, _ = tx.Exec(ctx, `UPDATE stock_adjustments SET total_value=$2 WHERE id=$1`, id, value)
		result = map[string]any{"id": id, "number": number, "status": "draft", "total_value": value}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.created", "stock_adjustment", id, nil, result)
	})
	return result, err
}

func (s *Service) PostAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var st string
		var value decimal.Decimal
		var branch uuid.UUID
		var number string
		if err := tx.QueryRow(ctx, `SELECT status, total_value, branch_id, number FROM stock_adjustments WHERE id=$1`, id).Scan(&st, &value, &branch, &number); err != nil {
			return httpx.NotFound("not_found", "Adjustment not found")
		}
		if st != "draft" && st != "pending_approval" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be posted")
		}
		abs := value.Abs()
		g, ok := p.Can("stock.adjust")
		if !auth.WithinLimit(g, ok, abs) && st != "pending_approval" {
			if _, err := tx.Exec(ctx, `UPDATE stock_adjustments SET status='pending_approval' WHERE id=$1`, id); err != nil {
				return err
			}
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &branch, Kind: "stock_adjustment", SubjectType: "stock_adjustment", SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: abs, Valid: true}, Summary: "Stock adjustment " + number,
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		if err := s.applyAdjustment(ctx, tx, p, id, branch); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": "posted"}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) applyAdjustment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id, branch uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT product_id, qty_system, qty_counted, unit_cost FROM stock_adjustment_items WHERE adjustment_id=$1`, id)
	if err != nil {
		return err
	}
	type row struct {
		id                    uuid.UUID
		system, counted, cost decimal.Decimal
	}
	var items []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.system, &r.counted, &r.cost); err != nil {
			rows.Close()
			return err
		}
		items = append(items, r)
	}
	rows.Close()
	user := p.UserID
	gain, loss := money.Zero, money.Zero
	for _, it := range items {
		diff := it.counted.Sub(it.system)
		if diff.IsZero() {
			continue
		}
		if diff.IsPositive() {
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, branch, &user, "adjustment", "stock_adjustment", id, []stock.Line{{ProductID: it.id, Qty: diff, UnitCost: it.cost}}); err != nil {
				return err
			}
			gain = gain.Add(money.Money(diff.Mul(it.cost)))
		} else {
			if _, err := s.Stock.Deduct(ctx, tx, p.BusinessID, branch, &user, "adjustment", "stock_adjustment", id, []stock.Line{{ProductID: it.id, Qty: diff.Abs()}}); err != nil {
				return err
			}
			loss = loss.Add(money.Money(diff.Abs().Mul(it.cost)))
		}
	}
	date := s.Clock.Now().Format("2006-01-02")
	var lines []ledger.Line
	if gain.IsPositive() {
		lines = append(lines, ledger.Line{Code: "1200", BranchID: &branch, Debit: gain}, ledger.Line{Code: "4200", BranchID: &branch, Credit: gain})
	}
	if loss.IsPositive() {
		lines = append(lines, ledger.Line{Code: "5100", BranchID: &branch, Debit: loss}, ledger.Line{Code: "1200", BranchID: &branch, Credit: loss})
	}
	if len(lines) > 0 {
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_adjustment", id, "Stock adjustment", &user, lines); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE stock_adjustments SET status='posted', posted_by=$2, posted_at=now() WHERE id=$1`, id, p.UserID)
	return err
}

func (s *Service) OnAdjustmentApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var branch uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT branch_id FROM stock_adjustments WHERE id=$1`, req.SubjectID).Scan(&branch); err != nil {
		return err
	}
	return s.applyAdjustment(ctx, tx, p, req.SubjectID, branch)
}

func (s *Service) OnAdjustmentRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	_, err := tx.Exec(ctx, `UPDATE stock_adjustments SET status='rejected' WHERE id=$1`, req.SubjectID)
	return err
}

type TransferInput struct {
	ToBranchID uuid.UUID `json:"to_branch_id"`
	Note       string    `json:"note"`
	Items      []Item    `json:"items"`
}

func (s *Service) CreateTransfer(ctx context.Context, p *auth.Principal, in TransferInput) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose the sending branch first")
	}
	if in.ToBranchID == *p.BranchID {
		return nil, httpx.BadRequest("invalid_body", "Choose a different receiving branch")
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		number, err := sequence.Next(ctx, tx, *p.BranchID, "transfer")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_transfers (id, business_id, number, from_branch_id, to_branch_id, note, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, number, *p.BranchID, in.ToBranchID, in.Note, p.UserID); err != nil {
			return err
		}
		for _, it := range in.Items {
			if _, err := tx.Exec(ctx, `
				INSERT INTO stock_transfer_items (id, business_id, transfer_id, product_id, qty_sent) VALUES ($1,$2,$3,$4,$5)
			`, ids.New(), p.BusinessID, id, it.ProductID, it.Qty); err != nil {
				return err
			}
		}
		result = map[string]any{"id": id, "number": number, "status": "draft"}
		return s.Audit.Record(ctx, tx, p, "transfer.created", "stock_transfer", id, nil, result)
	})
	return result, err
}

func (s *Service) Dispatch(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var from, to uuid.UUID
		var st, number string
		if err := tx.QueryRow(ctx, `SELECT from_branch_id, to_branch_id, status, number FROM stock_transfers WHERE id=$1`, id).Scan(&from, &to, &st, &number); err != nil {
			return httpx.NotFound("not_found", "Transfer not found")
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft transfer can be dispatched")
		}
		rows, err := tx.Query(ctx, `SELECT product_id, qty_sent FROM stock_transfer_items WHERE transfer_id=$1`, id)
		if err != nil {
			return err
		}
		var lines []stock.Line
		var pids []uuid.UUID
		for rows.Next() {
			var pid uuid.UUID
			var qty decimal.Decimal
			if err := rows.Scan(&pid, &qty); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, stock.Line{ProductID: pid, Qty: qty})
			pids = append(pids, pid)
		}
		rows.Close()
		user := p.UserID
		costs, err := s.Stock.Deduct(ctx, tx, p.BusinessID, from, &user, "transfer_out", "stock_transfer", id, lines)
		if err != nil {
			return err
		}
		value := money.Zero
		for _, ln := range lines {
			value = value.Add(money.Money(ln.Qty.Mul(costs[ln.ProductID])))
			_, _ = tx.Exec(ctx, `UPDATE stock_transfer_items SET unit_cost=$3 WHERE transfer_id=$1 AND product_id=$2`, id, ln.ProductID, costs[ln.ProductID])
		}
		date := s.Clock.Now().Format("2006-01-02")
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_transfer", id, "Transfer dispatch "+number, &user, []ledger.Line{
			{Code: "1210", BranchID: &from, Debit: value},
			{Code: "1200", BranchID: &from, Credit: value},
		}); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE stock_transfers SET status='dispatched', dispatched_by=$2, dispatched_at=now() WHERE id=$1`, id, p.UserID)
		if err != nil {
			return err
		}
		_ = jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": from, "product_ids": pids})
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": "dispatched", "value": value}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type ReceiveLine struct {
	ProductID   uuid.UUID       `json:"product_id"`
	QtyReceived decimal.Decimal `json:"qty_received"`
}

func (s *Service) ReceiveTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID, lines []ReceiveLine) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var from, to uuid.UUID
		var st, number string
		if err := tx.QueryRow(ctx, `SELECT from_branch_id, to_branch_id, status, number FROM stock_transfers WHERE id=$1`, id).Scan(&from, &to, &st, &number); err != nil {
			return httpx.NotFound("not_found", "Transfer not found")
		}
		if st != "dispatched" {
			return httpx.Conflict("invalid_status", "This transfer is not in transit")
		}
		got := map[uuid.UUID]decimal.Decimal{}
		for _, ln := range lines {
			got[ln.ProductID] = ln.QtyReceived
		}
		rows, err := tx.Query(ctx, `SELECT product_id, qty_sent, unit_cost FROM stock_transfer_items WHERE transfer_id=$1`, id)
		if err != nil {
			return err
		}
		type item struct {
			id         uuid.UUID
			sent, cost decimal.Decimal
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.sent, &it.cost); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
		}
		rows.Close()
		user := p.UserID
		receivedValue, lossValue := money.Zero, money.Zero
		var recvLines []stock.Line
		for _, it := range items {
			qty := got[it.id]
			if _, ok := got[it.id]; !ok {
				qty = it.sent
			}
			if qty.GreaterThan(it.sent) {
				return httpx.BadRequest("invalid_body", "Received quantity cannot exceed the quantity sent")
			}
			_, _ = tx.Exec(ctx, `UPDATE stock_transfer_items SET qty_received=$3 WHERE transfer_id=$1 AND product_id=$2`, id, it.id, qty)
			if qty.IsPositive() {
				recvLines = append(recvLines, stock.Line{ProductID: it.id, Qty: qty, UnitCost: it.cost})
				receivedValue = receivedValue.Add(money.Money(qty.Mul(it.cost)))
			}
			short := it.sent.Sub(qty)
			if short.IsPositive() {
				lossValue = lossValue.Add(money.Money(short.Mul(it.cost)))
			}
		}
		if len(recvLines) > 0 {
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, to, &user, "transfer_in", "stock_transfer", id, recvLines); err != nil {
				return err
			}
		}
		date := s.Clock.Now().Format("2006-01-02")
		jlines := []ledger.Line{}
		if receivedValue.IsPositive() {
			jlines = append(jlines, ledger.Line{Code: "1200", BranchID: &to, Debit: receivedValue})
		}
		if lossValue.IsPositive() {
			jlines = append(jlines, ledger.Line{Code: "5110", BranchID: &from, Debit: lossValue})
		}
		jlines = append(jlines, ledger.Line{Code: "1210", BranchID: &from, Credit: receivedValue.Add(lossValue)})
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_transfer_receive", id, "Transfer receive "+number, &user, jlines); err != nil {
			return err
		}
		final := "received"
		if lossValue.IsPositive() {
			final = "received_with_difference"
			_ = s.Notify.ToPermission(ctx, tx, p.BusinessID, &from, "users.manage", notify.Message{
				Type: "transfer_difference", Title: "Transfer difference", Body: number + " arrived short",
				EntityType: "stock_transfer", EntityID: &id,
			})
		}
		_, err = tx.Exec(ctx, `UPDATE stock_transfers SET status=$2, received_by=$3, received_at=now() WHERE id=$1`, id, final, p.UserID)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": final, "received_value": receivedValue, "loss_value": lossValue}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) CheckLowStock(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error {
	var body struct {
		BranchID   uuid.UUID   `json:"branch_id"`
		ProductIDs []uuid.UUID `json:"product_ids"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return err
	}
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		for _, pid := range body.ProductIDs {
			var name string
			var qty, reorder decimal.Decimal
			var notified bool
			err := tx.QueryRow(ctx, `
				SELECT p.name, COALESCE(sl.qty_on_hand,0), p.reorder_level, COALESCE(sl.low_notified,false)
				FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$2
				WHERE p.id=$1
			`, pid, body.BranchID).Scan(&name, &qty, &reorder, &notified)
			if err != nil {
				continue
			}
			if qty.GreaterThan(reorder) {
				_, _ = tx.Exec(ctx, `UPDATE stock_levels SET low_notified=false WHERE branch_id=$1 AND product_id=$2`, body.BranchID, pid)
				continue
			}
			if notified || reorder.IsZero() && qty.IsPositive() {
				continue
			}
			_ = s.Notify.ToPermission(ctx, tx, businessID, &body.BranchID, "stock.view", notify.Message{
				Type: "low_stock", Title: "Low stock", Body: name + " is at or below its reorder level",
				EntityType: "product", EntityID: &pid, Link: "/inventory/low-stock",
			})
			_, _ = tx.Exec(ctx, `UPDATE stock_levels SET low_notified=true WHERE branch_id=$1 AND product_id=$2`, body.BranchID, pid)
		}
		return nil
	})
}
