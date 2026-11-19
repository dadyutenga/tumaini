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
	// BatchNo and ExpiryDate are recorded on stock-in lines only (FR-INV-32).
	BatchNo    string `json:"batch_no" validate:"max=60"`
	ExpiryDate string `json:"expiry_date" validate:"omitempty,date"`
}

type EntryInput struct {
	Direction string `json:"direction" validate:"oneof=in out"`
	Reason    string `json:"reason" validate:"oneof=opening found gift damaged expired internal_use theft other"`
	Note      string `json:"note" validate:"max=500"`
	Items     []Item `json:"items" validate:"min=1,max=500,dive"`
}

// outAccount maps a stock-out reason to the expense account it is posted to (FR-INV-15).
var inReasons = map[string]bool{"opening": true, "found": true, "gift": true, "other": true}
var outReasons = map[string]bool{"damaged": true, "expired": true, "internal_use": true, "theft": true, "other": true}

// adjustmentReason maps a stock-entry reason onto app.adjustment_reason. Reasons the enum does not
// have become "other", which the schema requires a note for; the original reason is kept as the note.
func adjustmentReason(reason string) string {
	switch reason {
	case "opening", "found", "damaged", "expired", "theft":
		return reason
	}
	return "other"
}

// stockProducts checks every product exists and is active.
func stockProducts(ctx context.Context, tx pgx.Tx, pids []uuid.UUID) error {
	for _, id := range pids {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM app.products WHERE id = $1`, id).Scan(&active); err != nil {
			return httpx.NotFound("not_found", "Product not found")
		}
		if !active {
			return httpx.Conflict("invalid_status", "That product is inactive")
		}
	}
	return nil
}

// PostEntry records stock coming in or going out for a stated reason. In the approved schema this is a
// stock adjustment that is created and posted in one step: the database writes the stock movements
// and keeps the balances. Batch numbers and expiry dates are not part of the approved model.
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
	for _, it := range in.Items {
		if it.BatchNo != "" || it.ExpiryDate != "" {
			return 0, nil, httpx.BadRequest("batch_not_supported", "Batch numbers and expiry dates are not supported")
		}
	}
	if err := uniqueItems(in.Items); err != nil {
		return 0, nil, err
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if err := stockProducts(ctx, tx, productIDs(in.Items)); err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "stock_adjustment")
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		if note == "" {
			note = strings.ReplaceAll(in.Reason, "_", " ")
		}
		id := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.stock_adjustments (id, tenant_id, adjustment_no, location_id, reason, note, business_date)
			VALUES ($1,$2,$3,$4,$5::app.adjustment_reason,$6,$7::date)`,
			id, p.TenantID, number, *p.BranchID, adjustmentReason(in.Reason), note, s.bizDate(ctx, tx)); err != nil {
			return err
		}
		sign := decimal.NewFromInt(1)
		if in.Direction == "out" {
			sign = decimal.NewFromInt(-1)
		}
		total := money.Zero
		for _, it := range in.Items {
			qty := money.Qty(it.Qty).Mul(sign)
			var cost any
			if in.Direction == "in" {
				cost = it.UnitCost
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.stock_adjustment_lines (tenant_id, stock_adjustment_id, product_id, quantity_delta, unit_cost) VALUES ($1,$2,$3,$4,$5)`,
				p.TenantID, id, it.ProductID, qty, cost); err != nil {
				return err
			}
		}
		// Posting fires the database trigger that writes the movements and updates the balances.
		if _, err := tx.Exec(ctx, `UPDATE app.stock_adjustments SET status = 'posted', posted_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT coalesce(sum(abs(l.quantity_delta) * coalesce(l.unit_cost, pr.average_cost)), 0)
			FROM app.stock_adjustment_lines l JOIN app.products pr ON pr.tenant_id = l.tenant_id AND pr.id = l.product_id
			WHERE l.stock_adjustment_id = $1`, id).Scan(&total); err != nil {
			return err
		}
		total = money.Money(total)
		if err := jobs.EnqueueTx(ctx, tx, p.TenantID, jobs.CheckLowStock, map[string]any{"branch_id": p.BranchID, "product_ids": productIDs(in.Items)}); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "stock_entry.posted", "stock_adjustments", id, nil, map[string]any{"number": number, "direction": in.Direction, "reason": in.Reason, "total": total}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "total_value": total, "direction": in.Direction, "reason": in.Reason}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func uniqueItems(items []Item) error {
	seen := map[uuid.UUID]bool{}
	for _, it := range items {
		if seen[it.ProductID] {
			return httpx.BadRequest("invalid_body", "Each product may appear only once")
		}
		seen[it.ProductID] = true
	}
	return nil
}

func productIDs(items []Item) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = it.ProductID
	}
	return out
}

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

// stockTakeMark prefixes the note of an adjustment that came from a count sheet, so the list can tell
// a stock take from a manual count (the schema has no flag for it).
const stockTakeMark = "[stock take] "

// A counted adjustment is two linked records: a stock count (what was counted against what the
// system held) and a draft adjustment. Posting recomputes each difference against the stock at that
// moment, writes the adjustment lines, and the database posts the movements.

// CreateAdjustment records counted quantities as a draft. Only one count may be open per branch.
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		pids := make([]uuid.UUID, len(in.Items))
		for i, ln := range in.Items {
			pids[i] = ln.ProductID
		}
		if err := stockProducts(ctx, tx, pids); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.stock_counts WHERE tenant_id = $1 AND location_id = $2 AND status = 'in_progress'`, p.TenantID, *p.BranchID).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return httpx.Conflict("invalid_status", "This branch already has an open stock count. Post or cancel it first.")
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "stock_adjustment")
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Reason)
		if stockTake {
			note = stockTakeMark + note
		}
		id, countID := uuid.New(), uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.stock_adjustments (id, tenant_id, adjustment_no, location_id, reason, note, business_date)
			VALUES ($1,$2,$3,$4,'count_correction',$5,$6::date)`, id, p.TenantID, number, *p.BranchID, note, s.bizDate(ctx, tx)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.stock_counts (id, tenant_id, location_id, stock_adjustment_id) VALUES ($1,$2,$3,$4)`, countID, p.TenantID, *p.BranchID, id); err != nil {
			return err
		}
		value, err := s.writeCounts(ctx, tx, p, countID, in.Items)
		if err != nil {
			return err
		}
		result = map[string]any{"id": id, "number": number, "status": "draft", "total_value": value, "items": len(in.Items)}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.created", "stock_adjustments", id, nil, result)
	})
	return result, err
}

// writeCounts stores the counted lines with the system quantity at this moment and returns the signed
// value of the differences.
func (s *Service) writeCounts(ctx context.Context, tx pgx.Tx, p *auth.Principal, countID uuid.UUID, lines []CountLine) (decimal.Decimal, error) {
	value := money.Zero
	for _, ln := range lines {
		var sys, cost decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT coalesce((SELECT quantity_on_hand FROM app.stock_balances WHERE tenant_id = $1 AND location_id = $2 AND product_id = $3), 0),
			       (SELECT average_cost FROM app.products WHERE tenant_id = $1 AND id = $3)`, p.TenantID, *p.BranchID, ln.ProductID).Scan(&sys, &cost); err != nil {
			return value, err
		}
		counted := money.Qty(ln.QtyCounted)
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.stock_count_lines (tenant_id, stock_count_id, product_id, system_qty, counted_qty, counted_by, counted_at)
			VALUES ($1,$2,$3,$4,$5,$6,now())`, p.TenantID, countID, ln.ProductID, sys, counted, p.MembershipID); err != nil {
			return value, err
		}
		value = value.Add(money.Money(counted.Sub(sys).Mul(cost)))
	}
	return value, nil
}

type CountSheetIn struct {
	CategoryID *uuid.UUID `json:"category_id"`
	Reason     string     `json:"reason" validate:"max=500"`
}

// CountSheet creates a stock-take draft prefilled with the system quantities.
func (s *Service) CountSheet(ctx context.Context, p *auth.Principal, in CountSheetIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if strings.TrimSpace(in.Reason) == "" {
		in.Reason = "Stock take"
	}
	var lines []CountLine
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT p.id, coalesce(b.quantity_on_hand, 0)
			FROM app.products p LEFT JOIN app.stock_balances b ON b.tenant_id = p.tenant_id AND b.product_id = p.id AND b.location_id = $1
			WHERE p.tenant_id = $3 AND p.status = 'active' AND ($2::uuid IS NULL OR p.category_id = $2)
			ORDER BY p.name`, *p.BranchID, in.CategoryID, p.TenantID)
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

// loadAdjustment locks the adjustment and returns its status, location, number and open count.
func (s *Service) loadAdjustment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (string, uuid.UUID, string, uuid.UUID, error) {
	var st, number string
	var branch uuid.UUID
	var count *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT a.status::text, a.location_id, a.adjustment_no, (SELECT c.id FROM app.stock_counts c WHERE c.tenant_id = a.tenant_id AND c.stock_adjustment_id = a.id)
		FROM app.stock_adjustments a WHERE a.tenant_id = $1 AND a.id = $2 AND a.reason = 'count_correction' FOR UPDATE OF a`, p.TenantID, id).Scan(&st, &branch, &number, &count)
	if err != nil {
		return "", uuid.Nil, "", uuid.Nil, httpx.NotFound("not_found", "Adjustment not found")
	}
	cid := uuid.Nil
	if count != nil {
		cid = *count
	}
	return st, branch, number, cid, nil
}

func (s *Service) UpdateAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID, in AdjustmentIn) (map[string]any, error) {
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	if err := uniqueCounts(in.Items); err != nil {
		return nil, err
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		st, branch, _, count, err := s.loadAdjustment(ctx, tx, p, id)
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
		if _, err := tx.Exec(ctx, `DELETE FROM app.stock_count_lines WHERE tenant_id = $1 AND stock_count_id = $2`, p.TenantID, count); err != nil {
			return err
		}
		var mark string
		_ = tx.QueryRow(ctx, `SELECT CASE WHEN note LIKE $2 THEN $3 ELSE '' END FROM app.stock_adjustments WHERE id = $1`, id, stockTakeMark+"%", stockTakeMark).Scan(&mark)
		if _, err := tx.Exec(ctx, `UPDATE app.stock_adjustments SET note = $2 WHERE id = $1`, id, mark+strings.TrimSpace(in.Reason)); err != nil {
			return err
		}
		bp := *p
		bp.BranchID = &branch
		value, err := s.writeCounts(ctx, tx, &bp, count, in.Items)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.updated", "stock_adjustments", id, nil, map[string]any{"reason": in.Reason, "total_value": value})
	})
	if err != nil {
		return nil, err
	}
	return s.GetAdjustment(ctx, p, id)
}

func (s *Service) CancelAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		st, _, _, count, err := s.loadAdjustment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be cancelled")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.stock_adjustments SET status = 'cancelled' WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.stock_counts SET status = 'cancelled' WHERE id = $1`, count); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "stock_adjustment.cancelled", "stock_adjustments", id, map[string]any{"status": st}, map[string]any{"status": "cancelled"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "cancelled"}, nil
}

// PostAdjustment re-reads the stock under lock (it may have moved since the count), writes the
// adjustment lines for every difference, and posts. The database writes the movements and balances.
func (s *Service) PostAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		st, branch, number, count, err := s.loadAdjustment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft adjustment can be posted")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.stock_adjustment_lines WHERE tenant_id = $1 AND stock_adjustment_id = $2`, p.TenantID, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO app.stock_adjustment_lines (tenant_id, stock_adjustment_id, product_id, quantity_delta, unit_cost)
			SELECT cl.tenant_id, $2, cl.product_id, cl.counted_qty - coalesce(b.quantity_on_hand, 0), pr.average_cost
			FROM app.stock_count_lines cl
			JOIN app.products pr ON pr.tenant_id = cl.tenant_id AND pr.id = cl.product_id
			LEFT JOIN app.stock_balances b ON b.tenant_id = cl.tenant_id AND b.product_id = cl.product_id AND b.location_id = $3
			WHERE cl.tenant_id = $1 AND cl.stock_count_id = $4 AND cl.counted_qty IS NOT NULL AND cl.counted_qty <> coalesce(b.quantity_on_hand, 0)`,
			p.TenantID, id, branch, count)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("nothing_to_adjust", "Every counted quantity already matches the system, so there is nothing to post")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.stock_adjustments SET status = 'posted', posted_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.stock_counts SET status = 'completed', completed_at = now() WHERE id = $1`, count); err != nil {
			return err
		}
		var value decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(round(quantity_delta * unit_cost, 2)), 0) FROM app.stock_adjustment_lines WHERE stock_adjustment_id = $1`, id).Scan(&value); err != nil {
			return err
		}
		pids := []uuid.UUID{}
		rows, err := tx.Query(ctx, `SELECT product_id FROM app.stock_adjustment_lines WHERE stock_adjustment_id = $1`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid uuid.UUID
			if err := rows.Scan(&pid); err != nil {
				rows.Close()
				return err
			}
			pids = append(pids, pid)
		}
		rows.Close()
		if err := jobs.EnqueueTx(ctx, tx, p.TenantID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "stock_adjustment.posted", "stock_adjustments", id, nil, map[string]any{"number": number, "value": value}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": number, "status": "posted", "total_value": money.Money(value)}))
		status, body = 200, raw
		return err
	})
	return status, body, err
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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

// notifyOwners tells the business owners (everyone who sees the business dashboard).
func (s *Service) notifyOwners(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, m notify.Message) error {
	return s.Notify.ToPermission(ctx, tx, businessID, nil, "dashboard.business.view", m)
}

// CheckLowStock raises a notification for each product at or below its minimum. The notification
// service keeps it to one per person, product and day, which replaces the old "notified" flag.
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
			var qty, minimum decimal.Decimal
			err := tx.QueryRow(ctx, `
				SELECT p.name::text, coalesce((SELECT quantity_on_hand FROM app.stock_balances b WHERE b.tenant_id = p.tenant_id AND b.product_id = p.id AND b.location_id = $2), 0),
				       coalesce(p.min_stock_level, ts.low_stock_default)
				FROM app.products p JOIN app.tenant_settings ts ON ts.tenant_id = p.tenant_id WHERE p.id = $1 AND p.status = 'active'`, pid, body.BranchID).Scan(&name, &qty, &minimum)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			// A minimum of zero means the product is not tracked for low stock.
			if !minimum.IsPositive() || qty.GreaterThan(minimum) {
				continue
			}
			if err := s.Notify.ToPermission(ctx, tx, businessID, &body.BranchID, "shop.stock.view", notify.Message{
				Type: "low_stock", Title: "Low stock: " + name, Body: name + " is at or below its minimum level",
				EntityType: "product", EntityID: &pid, Link: "/inventory/low-stock",
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
