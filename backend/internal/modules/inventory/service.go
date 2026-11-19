package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
	"bms/internal/platform/setting"
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

// bizDate is today's business date in the business timezone (BR-15).
func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) string {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return s.Clock.Now().Format("2006-01-02")
	}
	return d.Format("2006-01-02")
}

type Item struct {
	ProductID uuid.UUID       `json:"product_id" validate:"required"`
	Qty       decimal.Decimal `json:"qty" validate:"dpos"`
	UnitCost  decimal.Decimal `json:"unit_cost" validate:"dgte0"`
}

type EntryInput struct {
	Direction string `json:"direction" validate:"oneof=in out"`
	Reason    string `json:"reason" validate:"oneof=opening found gift damaged expired internal_use theft other"`
	Note      string `json:"note" validate:"max=500"`
	Items     []Item `json:"items" validate:"min=1,max=500,dive"`
}

var inReasons = map[string]bool{"opening": true, "found": true, "gift": true, "other": true}
var outReasons = map[string]bool{"damaged": true, "expired": true, "internal_use": true, "theft": true, "other": true}

// outAccount maps a stock-out reason to the expense account it is posted to (FR-INV-15).
func outAccount(reason string) string {
	if reason == "internal_use" {
		return "6090"
	}
	return "5100"
}

// inAccount maps a stock-in reason to the credit side: opening equity or stock gain.
func inAccount(reason string) string {
	if reason == "opening" {
		return "3100"
	}
	return "4200"
}

// stockProducts checks every product exists, is active and is a stock item.
func stockProducts(ctx context.Context, tx pgx.Tx, pids []uuid.UUID) error {
	for _, id := range pids {
		var kind string
		var active bool
		if err := tx.QueryRow(ctx, `SELECT kind, is_active FROM products WHERE id=$1`, id).Scan(&kind, &active); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		if kind != "stock" {
			return httpx.BadRequest("invalid_body", "Services do not carry stock")
		}
		if !active {
			return httpx.Conflict("invalid_status", "That product is inactive")
		}
	}
	return nil
}

func (s *Service) PostEntry(ctx context.Context, p *auth.Principal, in EntryInput) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	if in.Direction == "in" && !inReasons[in.Reason] {
		return 0, nil, httpx.BadRequest("invalid_body", "That reason is not valid for stock in")
	}
	if in.Direction == "out" && !outReasons[in.Reason] {
		return 0, nil, httpx.BadRequest("invalid_body", "That reason is not valid for stock out")
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
		if err := stockProducts(ctx, tx, productIDs(in.Items)); err != nil {
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
			cost := money.Cost(it.UnitCost)
			total = total.Add(money.Money(it.Qty.Mul(cost)))
			lines = append(lines, stock.Line{ProductID: it.ProductID, Qty: it.Qty, UnitCost: cost})
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_entries (id, business_id, branch_id, number, direction, reason, note, total_value, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9)
		`, id, p.BusinessID, *p.BranchID, number, in.Direction, in.Reason, in.Note, total, p.UserID); err != nil {
			return err
		}
		user := p.UserID
		date := s.bizDate(ctx, tx)
		if in.Direction == "in" {
			move := "stock_in"
			if in.Reason == "opening" {
				move = "opening"
			}
			for _, it := range in.Items {
				if _, err := tx.Exec(ctx, `
					INSERT INTO stock_entry_items (id, business_id, entry_id, product_id, qty, unit_cost) VALUES ($1,$2,$3,$4,$5,$6)
				`, ids.New(), p.BusinessID, id, it.ProductID, it.Qty, money.Cost(it.UnitCost)); err != nil {
					return err
				}
			}
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, *p.BranchID, &user, move, "stock_entry", id, lines); err != nil {
				return err
			}
			if total.IsPositive() {
				if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_entry", id, "Stock in "+number, &user, []ledger.Line{
					{Code: "1200", BranchID: p.BranchID, Debit: total},
					{Code: inAccount(in.Reason), BranchID: p.BranchID, Credit: total},
				}); err != nil {
					return err
				}
			}
		} else {
			costs, err := s.Stock.Deduct(ctx, tx, p.BusinessID, *p.BranchID, &user, "stock_out", "stock_entry", id, lines)
			if err != nil {
				return err
			}
			value := money.Zero
			for _, it := range in.Items {
				value = value.Add(money.Money(it.Qty.Mul(costs[it.ProductID])))
				if _, err := tx.Exec(ctx, `
					INSERT INTO stock_entry_items (id, business_id, entry_id, product_id, qty, unit_cost) VALUES ($1,$2,$3,$4,$5,$6)
				`, ids.New(), p.BusinessID, id, it.ProductID, it.Qty, costs[it.ProductID]); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_entries SET total_value=$2 WHERE id=$1`, id, value); err != nil {
				return err
			}
			if value.IsPositive() {
				if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "stock_entry", id, "Stock out "+number, &user, []ledger.Line{
					{Code: outAccount(in.Reason), BranchID: p.BranchID, Debit: value},
					{Code: "1200", BranchID: p.BranchID, Credit: value},
				}); err != nil {
					return err
				}
			}
			total = value
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": p.BranchID, "product_ids": productIDs(in.Items)}); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "stock_entry.posted", "stock_entry", id, nil, map[string]any{"number": number, "direction": in.Direction, "reason": in.Reason, "total": total}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "total_value": total, "direction": in.Direction, "reason": in.Reason}))
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

// ---- Adjustments ----

type CountLine struct {
	ProductID  uuid.UUID       `json:"product_id" validate:"required"`
	QtyCounted decimal.Decimal `json:"qty_counted" validate:"dgte0"`
}

type AdjustmentIn struct {
	Reason string      `json:"reason" validate:"required,max=500"`
	Items  []CountLine `json:"items" validate:"min=1,max=2000,dive"`
}

func uniqueCounts(lines []CountLine) error {
	seen := map[uuid.UUID]bool{}
	for _, ln := range lines {
		if seen[ln.ProductID] {
			return httpx.BadRequest("invalid_body", "Each product may appear only once")
		}
		seen[ln.ProductID] = true
	}
	return nil
}

func (s *Service) CreateAdjustment(ctx context.Context, p *auth.Principal, in AdjustmentIn, stockTake bool) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	if err := uniqueCounts(in.Items); err != nil {
		return nil, err
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pids := make([]uuid.UUID, len(in.Items))
		for i, ln := range in.Items {
			pids[i] = ln.ProductID
		}
		if err := stockProducts(ctx, tx, pids); err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "adjustment")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_adjustments (id, business_id, branch_id, number, reason, is_stock_take, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, *p.BranchID, number, in.Reason, stockTake, p.UserID); err != nil {
			return err
		}
		value, err := s.writeCounts(ctx, tx, p.BusinessID, id, *p.BranchID, in.Items)
		if err != nil {
			return err
		}
		result = map[string]any{"id": id, "number": number, "status": "draft", "total_value": value, "items": len(in.Items)}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.created", "stock_adjustment", id, nil, result)
	})
	return result, err
}

// writeCounts stores counted lines with the current system quantity and cost
// and returns the signed value of the difference.
func (s *Service) writeCounts(ctx context.Context, tx pgx.Tx, businessID, id, branch uuid.UUID, lines []CountLine) (decimal.Decimal, error) {
	value := money.Zero
	for _, ln := range lines {
		qty, cost, err := s.Stock.Level(ctx, tx, branch, ln.ProductID)
		if err != nil {
			return value, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_adjustment_items (id, business_id, adjustment_id, product_id, qty_system, qty_counted, unit_cost)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, ids.New(), businessID, id, ln.ProductID, qty, money.Qty(ln.QtyCounted), cost); err != nil {
			return value, err
		}
		value = value.Add(money.Money(money.Qty(ln.QtyCounted).Sub(qty).Mul(cost)))
	}
	if _, err := tx.Exec(ctx, `UPDATE stock_adjustments SET total_value=$2 WHERE id=$1`, id, value); err != nil {
		return value, err
	}
	return value, nil
}

type CountSheetIn struct {
	CategoryID *uuid.UUID `json:"category_id"`
	Reason     string     `json:"reason" validate:"max=500"`
}

// CountSheet creates a stock-take draft prefilled with system quantities (FR-INV-19).
func (s *Service) CountSheet(ctx context.Context, p *auth.Principal, in CountSheetIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = "Stock take"
	}
	var lines []CountLine
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, COALESCE(sl.qty_on_hand, 0)
			FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$1
			WHERE p.kind='stock' AND p.is_active AND ($2::uuid IS NULL OR p.category_id=$2)
			ORDER BY p.name
		`, *p.BranchID, in.CategoryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ln CountLine
			if err := rows.Scan(&ln.ProductID, &ln.QtyCounted); err != nil {
				return err
			}
			lines = append(lines, ln)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, httpx.Conflict("invalid_status", "There are no stock products to count")
	}
	out, err := s.CreateAdjustment(ctx, p, AdjustmentIn{Reason: in.Reason, Items: lines}, true)
	if err != nil {
		return nil, err
	}
	return s.GetAdjustment(ctx, p, out["id"].(uuid.UUID))
}

// loadAdjustment locks the adjustment and checks it is inside the caller's branch scope.
func (s *Service) loadAdjustment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (string, uuid.UUID, string, error) {
	var st, number string
	var branch uuid.UUID
	err := tx.QueryRow(ctx, `SELECT status, branch_id, number FROM stock_adjustments WHERE id=$1 FOR UPDATE`, id).Scan(&st, &branch, &number)
	if err != nil || !p.SeesBranch("stock.adjust", branch) {
		return "", uuid.Nil, "", httpx.NotFound("not_found", "Adjustment not found")
	}
	return st, branch, number, nil
}

func (s *Service) UpdateAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID, in AdjustmentIn) (map[string]any, error) {
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	if err := uniqueCounts(in.Items); err != nil {
		return nil, err
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		st, branch, _, err := s.loadAdjustment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be edited")
		}
		pids := make([]uuid.UUID, len(in.Items))
		for i, ln := range in.Items {
			pids[i] = ln.ProductID
		}
		if err := stockProducts(ctx, tx, pids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM stock_adjustment_items WHERE adjustment_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_adjustments SET reason=$2 WHERE id=$1`, id, in.Reason); err != nil {
			return err
		}
		value, err := s.writeCounts(ctx, tx, p.BusinessID, id, branch, in.Items)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.updated", "stock_adjustment", id, nil, map[string]any{"reason": in.Reason, "total_value": value})
	})
	if err != nil {
		return nil, err
	}
	return s.GetAdjustment(ctx, p, id)
}

func (s *Service) CancelAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		st, _, _, err := s.loadAdjustment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be cancelled")
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_adjustments SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.cancelled", "stock_adjustment", id, map[string]any{"status": st}, map[string]any{"status": "cancelled"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "cancelled"}, nil
}

// refreshCounts re-reads the system quantity and cost of every line under lock,
// because stock may have moved since the draft was written.
func (s *Service) refreshCounts(ctx context.Context, tx pgx.Tx, id, branch uuid.UUID) (decimal.Decimal, error) {
	if _, err := tx.Exec(ctx, `
		UPDATE stock_adjustment_items ai
		SET qty_system = COALESCE(sl.qty_on_hand, 0), unit_cost = COALESCE(sl.avg_cost, ai.unit_cost)
		FROM stock_adjustment_items x LEFT JOIN stock_levels sl ON sl.product_id = x.product_id AND sl.branch_id = $2
		WHERE ai.id = x.id AND ai.adjustment_id = $1
	`, id, branch); err != nil {
		return money.Zero, err
	}
	var value decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(round((qty_counted - qty_system) * unit_cost, 2)), 0) FROM stock_adjustment_items WHERE adjustment_id=$1
	`, id).Scan(&value); err != nil {
		return money.Zero, err
	}
	_, err := tx.Exec(ctx, `UPDATE stock_adjustments SET total_value=$2 WHERE id=$1`, id, value)
	return value, err
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
		st, branch, number, err := s.loadAdjustment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if st == "pending_approval" {
			return httpx.Conflict("invalid_status", "This adjustment is already waiting for approval")
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be posted")
		}
		value, err := s.refreshCounts(ctx, tx, id, branch)
		if err != nil {
			return err
		}
		abs := value.Abs()
		g, ok := p.Can("stock.adjust")
		if !p.IsOwner && !auth.WithinLimit(g, ok, abs) {
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
			if err := s.Audit.Record(ctx, tx, p, "stock_adjustment.submitted", "stock_adjustment", id, nil, map[string]any{"value": value, "approval_request_id": reqID}); err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"id": id, "approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		if err := s.applyAdjustment(ctx, tx, p, id, branch); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "stock_adjustment.posted", "stock_adjustment", id, nil, map[string]any{"number": number, "value": value}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": number, "status": "posted", "total_value": value}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) applyAdjustment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id, branch uuid.UUID) error {
	if _, err := s.refreshCounts(ctx, tx, id, branch); err != nil {
		return err
	}
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
	if err := rows.Err(); err != nil {
		return err
	}
	user := p.UserID
	gain, loss := money.Zero, money.Zero
	var pids []uuid.UUID
	for _, it := range items {
		diff := it.counted.Sub(it.system)
		if diff.IsZero() {
			continue
		}
		pids = append(pids, it.id)
		if diff.IsPositive() {
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, branch, &user, "adjustment", "stock_adjustment", id, []stock.Line{{ProductID: it.id, Qty: diff, UnitCost: it.cost}}); err != nil {
				return err
			}
			gain = gain.Add(money.Money(diff.Mul(it.cost)))
		} else {
			costs, err := s.Stock.Deduct(ctx, tx, p.BusinessID, branch, &user, "adjustment", "stock_adjustment", id, []stock.Line{{ProductID: it.id, Qty: diff.Abs()}})
			if err != nil {
				return err
			}
			loss = loss.Add(money.Money(diff.Abs().Mul(costs[it.id])))
		}
	}
	date := s.bizDate(ctx, tx)
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
	if len(pids) > 0 {
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE stock_adjustments SET status='posted', posted_by=$2, posted_at=now() WHERE id=$1`, id, p.UserID)
	return err
}

func (s *Service) OnAdjustmentApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var branch uuid.UUID
	var st string
	if err := tx.QueryRow(ctx, `SELECT branch_id, status FROM stock_adjustments WHERE id=$1 FOR UPDATE`, req.SubjectID).Scan(&branch, &st); err != nil {
		return err
	}
	if st != "pending_approval" {
		return httpx.Conflict("invalid_status", "This adjustment is no longer waiting for approval")
	}
	if err := s.applyAdjustment(ctx, tx, p, req.SubjectID, branch); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "stock_adjustment.approved", "stock_adjustment", req.SubjectID, nil, map[string]any{"approval_request_id": req.ID})
}

// OnAdjustmentRejected returns the adjustment to draft so it can be corrected
// or cancelled (rejecting returns the record to its previous state).
func (s *Service) OnAdjustmentRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	if _, err := tx.Exec(ctx, `UPDATE stock_adjustments SET status='draft' WHERE id=$1 AND status='pending_approval'`, req.SubjectID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "stock_adjustment."+req.Status, "stock_adjustment", req.SubjectID, nil, map[string]any{"status": "draft", "note": req.Note})
}

// ---- Transfers ----

type TransferInput struct {
	ToBranchID uuid.UUID `json:"to_branch_id" validate:"required"`
	Note       string    `json:"note" validate:"max=500"`
	Items      []Item    `json:"items" validate:"min=1,max=500,dive"`
}

func (s *Service) checkTransferInput(ctx context.Context, tx pgx.Tx, from uuid.UUID, in TransferInput) error {
	if in.ToBranchID == from {
		return httpx.BadRequest("invalid_body", "Choose a different receiving branch")
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM branches WHERE id=$1`, in.ToBranchID).Scan(&active); err != nil || !active {
		return httpx.NotFound("not_found", "Receiving branch not found")
	}
	seen := map[uuid.UUID]bool{}
	for _, it := range in.Items {
		if seen[it.ProductID] {
			return httpx.BadRequest("invalid_body", "Each product may appear only once")
		}
		seen[it.ProductID] = true
	}
	return stockProducts(ctx, tx, productIDs(in.Items))
}

func (s *Service) CreateTransfer(ctx context.Context, p *auth.Principal, in TransferInput) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose the sending branch first")
	}
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkTransferInput(ctx, tx, *p.BranchID, in); err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "transfer")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_transfers (id, business_id, number, from_branch_id, to_branch_id, note, created_by)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7)
		`, id, p.BusinessID, number, *p.BranchID, in.ToBranchID, in.Note, p.UserID); err != nil {
			return err
		}
		if err := insertTransferItems(ctx, tx, p.BusinessID, id, in.Items); err != nil {
			return err
		}
		result = map[string]any{"id": id, "number": number, "status": "draft", "from_branch_id": *p.BranchID, "to_branch_id": in.ToBranchID}
		return s.Audit.Record(ctx, tx, p, "transfer.created", "stock_transfer", id, nil, result)
	})
	return result, err
}

func insertTransferItems(ctx context.Context, tx pgx.Tx, businessID, id uuid.UUID, items []Item) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO stock_transfer_items (id, business_id, transfer_id, product_id, qty_sent) VALUES ($1,$2,$3,$4,$5)
		`, ids.New(), businessID, id, it.ProductID, money.Qty(it.Qty)); err != nil {
			return err
		}
	}
	return nil
}

type transferRow struct {
	from, to uuid.UUID
	status   string
	number   string
}

// lockTransfer loads a transfer under lock. side is "from" or "to": the branch
// the caller must be able to act in for the permission key.
func lockTransfer(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, key, side string) (transferRow, error) {
	var t transferRow
	err := tx.QueryRow(ctx, `SELECT from_branch_id, to_branch_id, status, number FROM stock_transfers WHERE id=$1 FOR UPDATE`, id).Scan(&t.from, &t.to, &t.status, &t.number)
	if err != nil {
		return t, httpx.NotFound("not_found", "Transfer not found")
	}
	branch := t.from
	if side == "to" {
		branch = t.to
	}
	if !p.SeesBranch(key, branch) {
		return t, httpx.NotFound("not_found", "Transfer not found")
	}
	return t, nil
}

func (s *Service) UpdateTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID, in TransferInput) (map[string]any, error) {
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		t, err := lockTransfer(ctx, tx, p, id, "transfers.create", "from")
		if err != nil {
			return err
		}
		if t.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft transfer can be edited")
		}
		if err := s.checkTransferInput(ctx, tx, t.from, in); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM stock_transfer_items WHERE transfer_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_transfers SET to_branch_id=$2, note=NULLIF($3,'') WHERE id=$1`, id, in.ToBranchID, in.Note); err != nil {
			return err
		}
		if err := insertTransferItems(ctx, tx, p.BusinessID, id, in.Items); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "transfer.updated", "stock_transfer", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(ctx, p, id)
}

func (s *Service) CancelTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		t, err := lockTransfer(ctx, tx, p, id, "transfers.create", "from")
		if err != nil {
			return err
		}
		if t.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft transfer can be cancelled. A dispatched transfer must be received")
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_transfers SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "transfer.cancelled", "stock_transfer", id, map[string]any{"status": "draft"}, map[string]any{"status": "cancelled"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "cancelled"}, nil
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
		t, err := lockTransfer(ctx, tx, p, id, "transfers.dispatch", "from")
		if err != nil {
			return err
		}
		if t.status != "draft" {
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
		if err := rows.Err(); err != nil {
			return err
		}
		if len(lines) == 0 {
			return httpx.Conflict("invalid_status", "The transfer has no items")
		}
		user := p.UserID
		costs, err := s.Stock.Deduct(ctx, tx, p.BusinessID, t.from, &user, "transfer_out", "stock_transfer", id, lines)
		if err != nil {
			return err
		}
		value := money.Zero
		for _, ln := range lines {
			value = value.Add(money.Money(ln.Qty.Mul(costs[ln.ProductID])))
			if _, err := tx.Exec(ctx, `UPDATE stock_transfer_items SET unit_cost=$3 WHERE transfer_id=$1 AND product_id=$2`, id, ln.ProductID, costs[ln.ProductID]); err != nil {
				return err
			}
		}
		if value.IsPositive() {
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, s.bizDate(ctx, tx), "stock_transfer", id, "Transfer dispatch "+t.number, &user, []ledger.Line{
				{Code: "1210", BranchID: &t.from, Debit: value},
				{Code: "1200", BranchID: &t.from, Credit: value},
			}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_transfers SET status='dispatched', dispatched_by=$2, dispatched_at=now() WHERE id=$1`, id, p.UserID); err != nil {
			return err
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": t.from, "product_ids": pids}); err != nil {
			return err
		}
		_ = s.Notify.ToPermission(ctx, tx, p.BusinessID, &t.to, "transfers.receive", notify.Message{
			Type: "transfer_dispatched", Title: "Stock on the way", Body: t.number + " was dispatched to your branch",
			Link: "/stock-transfers/" + id.String(), EntityType: "stock_transfer", EntityID: &id,
		})
		if err := s.Audit.Record(ctx, tx, p, "transfer.dispatched", "stock_transfer", id, nil, map[string]any{"number": t.number, "value": value}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": t.number, "status": "dispatched", "value": value}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// ReturnTransfer sends a dispatched transfer back to the sender in full
// (FR-INV-25). Every item re-enters the sending branch at the cost it left,
// and the dispatch journal entry is mirrored (1200 Dr / 1210 Cr).
func (s *Service) ReturnTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		t, err := lockTransfer(ctx, tx, p, id, "transfers.dispatch", "from")
		if err != nil {
			return err
		}
		if t.status != "dispatched" {
			return httpx.Conflict("invalid_status", "Only a dispatched transfer can be returned")
		}
		rows, err := tx.Query(ctx, `SELECT product_id, qty_sent, unit_cost FROM stock_transfer_items WHERE transfer_id=$1`, id)
		if err != nil {
			return err
		}
		var lines []stock.Line
		var pids []uuid.UUID
		value := money.Zero
		for rows.Next() {
			var ln stock.Line
			if err := rows.Scan(&ln.ProductID, &ln.Qty, &ln.UnitCost); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, ln)
			pids = append(pids, ln.ProductID)
			value = value.Add(money.Money(ln.Qty.Mul(ln.UnitCost)))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		user := p.UserID
		if err := s.Stock.Receive(ctx, tx, p.BusinessID, t.from, &user, "transfer_in", "stock_transfer_return", id, lines); err != nil {
			return err
		}
		if err := s.Ledger.Reverse(ctx, tx, p.BusinessID, "stock_transfer", id, s.bizDate(ctx, tx), "Transfer returned "+t.number, &user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_transfers SET status='returned', note=COALESCE(NULLIF($2,''), note) WHERE id=$1`, id, reason); err != nil {
			return err
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": t.from, "product_ids": pids}); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "transfer.returned", "stock_transfer", id, map[string]any{"status": "dispatched"}, map[string]any{"status": "returned", "value": value, "reason": reason}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": t.number, "status": "returned", "value": value}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type ReceiveLine struct {
	ProductID   uuid.UUID       `json:"product_id" validate:"required"`
	QtyReceived decimal.Decimal `json:"qty_received" validate:"dgte0"`
}

type ReceiveIn struct {
	Items []ReceiveLine `json:"items" validate:"dive"`
	Note  string        `json:"note" validate:"max=500"`
}

func (s *Service) ReceiveTransfer(ctx context.Context, p *auth.Principal, id uuid.UUID, in ReceiveIn) (int, []byte, error) {
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
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
		t, err := lockTransfer(ctx, tx, p, id, "transfers.receive", "to")
		if err != nil {
			return err
		}
		if t.status != "dispatched" {
			return httpx.Conflict("invalid_status", "This transfer is not in transit")
		}
		rows, err := tx.Query(ctx, `
			SELECT ti.product_id, ti.qty_sent, ti.unit_cost, p.name
			FROM stock_transfer_items ti JOIN products p ON p.id=ti.product_id WHERE ti.transfer_id=$1
		`, id)
		if err != nil {
			return err
		}
		type item struct {
			id         uuid.UUID
			sent, cost decimal.Decimal
			name       string
		}
		var items []item
		onTransfer := map[uuid.UUID]bool{}
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.sent, &it.cost, &it.name); err != nil {
				rows.Close()
				return err
			}
			items = append(items, it)
			onTransfer[it.id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		got := map[uuid.UUID]decimal.Decimal{}
		for _, ln := range in.Items {
			if !onTransfer[ln.ProductID] {
				return httpx.BadRequest("invalid_body", "A received product is not on this transfer")
			}
			got[ln.ProductID] = money.Qty(ln.QtyReceived)
		}
		user := p.UserID
		receivedValue, lossValue := money.Zero, money.Zero
		var recvLines []stock.Line
		var shortages []string
		var pids []uuid.UUID
		for _, it := range items {
			qty, ok := got[it.id]
			if !ok {
				qty = it.sent
			}
			if qty.GreaterThan(it.sent) {
				return httpx.BadRequest("invalid_body", "Received quantity cannot exceed the quantity sent")
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_transfer_items SET qty_received=$3 WHERE transfer_id=$1 AND product_id=$2`, id, it.id, qty); err != nil {
				return err
			}
			if qty.IsPositive() {
				recvLines = append(recvLines, stock.Line{ProductID: it.id, Qty: qty, UnitCost: it.cost})
				receivedValue = receivedValue.Add(money.Money(qty.Mul(it.cost)))
				pids = append(pids, it.id)
			}
			if short := it.sent.Sub(qty); short.IsPositive() {
				lossValue = lossValue.Add(money.Money(short.Mul(it.cost)))
				shortages = append(shortages, it.name+" short by "+short.String())
			}
		}
		if len(recvLines) > 0 {
			if err := s.Stock.Receive(ctx, tx, p.BusinessID, t.to, &user, "transfer_in", "stock_transfer", id, recvLines); err != nil {
				return err
			}
		}
		// The dispatch moved exactly sent x cost into transit; clear it fully.
		var transit decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(round(qty_sent*unit_cost, 2)),0) FROM stock_transfer_items WHERE transfer_id=$1`, id).Scan(&transit); err != nil {
			return err
		}
		lossValue = transit.Sub(receivedValue)
		if lossValue.IsNegative() {
			receivedValue, lossValue = transit, money.Zero
		}
		var jlines []ledger.Line
		if receivedValue.IsPositive() {
			jlines = append(jlines, ledger.Line{Code: "1200", BranchID: &t.to, Debit: receivedValue})
		}
		if lossValue.IsPositive() {
			jlines = append(jlines, ledger.Line{Code: "5110", BranchID: &t.from, Debit: lossValue})
		}
		if transit.IsPositive() {
			jlines = append(jlines, ledger.Line{Code: "1210", BranchID: &t.from, Credit: transit})
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, s.bizDate(ctx, tx), "stock_transfer_receive", id, "Transfer receive "+t.number, &user, jlines); err != nil {
				return err
			}
		}
		final := "received"
		if len(shortages) > 0 {
			final = "received_with_difference"
			// FR-INV-24: the Owner is told about every transfer difference.
			if err := s.notifyOwners(ctx, tx, p.BusinessID, notify.Message{
				Type: "transfer_difference", Title: "Transfer difference", Body: t.number + ": " + strings.Join(shortages, ", "),
				Link: "/stock-transfers/" + id.String(), EntityType: "stock_transfer", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE stock_transfers SET status=$2, received_by=$3, received_at=now(), note=COALESCE(NULLIF($4,''), note) WHERE id=$1`, id, final, p.UserID, in.Note); err != nil {
			return err
		}
		if len(pids) > 0 {
			if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": t.to, "product_ids": pids}); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "transfer.received", "stock_transfer", id, nil, map[string]any{"status": final, "received_value": receivedValue, "loss_value": lossValue}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": t.number, "status": final, "received_value": receivedValue, "loss_value": lossValue}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) notifyOwners(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, m notify.Message) error {
	rows, err := tx.Query(ctx, `SELECT u.id FROM users u JOIN roles r ON r.id=u.role_id WHERE r.slug='owner' AND u.is_active`)
	if err != nil {
		return err
	}
	var owners []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range owners {
		if err := s.Notify.ToUser(ctx, tx, businessID, id, m); err != nil {
			return err
		}
	}
	return nil
}

// CheckLowStock raises one notification per product when it reaches its
// reorder level, and re-arms once it is restocked above it (FR-INV-27).
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
			var notified, kindStock bool
			err := tx.QueryRow(ctx, `
				SELECT p.name, COALESCE(sl.qty_on_hand,0), p.reorder_level, COALESCE(sl.low_notified,false), p.kind='stock'
				FROM products p LEFT JOIN stock_levels sl ON sl.product_id=p.id AND sl.branch_id=$2
				WHERE p.id=$1
			`, pid, body.BranchID).Scan(&name, &qty, &reorder, &notified, &kindStock)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if !kindStock {
				continue
			}
			if qty.GreaterThan(reorder) {
				if notified {
					if _, err := tx.Exec(ctx, `UPDATE stock_levels SET low_notified=false WHERE branch_id=$1 AND product_id=$2`, body.BranchID, pid); err != nil {
						return err
					}
				}
				continue
			}
			// A reorder level of zero means the product is not tracked for low stock.
			if notified || !reorder.IsPositive() {
				continue
			}
			if err := s.Notify.ToPermission(ctx, tx, businessID, &body.BranchID, "stock.view", notify.Message{
				Type: "low_stock", Title: "Low stock", Body: name + " is at or below its reorder level",
				EntityType: "product", EntityID: &pid, Link: "/inventory/low-stock",
			}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_levels SET low_notified=true WHERE branch_id=$1 AND product_id=$2`, body.BranchID, pid); err != nil {
				return err
			}
		}
		return nil
	})
}
