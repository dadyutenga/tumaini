package purchasing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/payments"
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
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, st *stock.Service, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Stock: st, Ledger: led, Audit: aud, Approval: ap, Notify: n, Clock: clk}
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) string {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return s.Clock.Now().Format("2006-01-02")
	}
	return d.Format("2006-01-02")
}

func orEmpty(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
}

// idemRun runs fn inside a transaction guarded by the request's idempotency key.
func (s *Service) idemRun(ctx context.Context, p *auth.Principal, fn func(ctx context.Context, tx pgx.Tx) (int, any, error)) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		st, out, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, st, httpx.Data(out))
		status, body = st, raw
		return err
	})
	return status, body, err
}

func activeSupplier(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM suppliers WHERE id=$1`, id).Scan(&active); err != nil {
		return httpx.NotFound("not_found", "Supplier not found")
	}
	if !active {
		return httpx.Conflict("invalid_status", "That supplier is inactive")
	}
	return nil
}

func stockProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var kind string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT kind, is_active FROM products WHERE id=$1`, id).Scan(&kind, &active); err != nil {
		return httpx.NotFound("not_found", "Product not found")
	}
	if kind != "stock" {
		return httpx.BadRequest("invalid_body", "Only stock products can be purchased into stock")
	}
	if !active {
		return httpx.Conflict("invalid_status", "That product is inactive")
	}
	return nil
}

// ---- Purchase orders ----

type POItem struct {
	ProductID uuid.UUID       `json:"product_id" validate:"required"`
	Qty       decimal.Decimal `json:"qty" validate:"dpos"`
	UnitCost  decimal.Decimal `json:"unit_cost" validate:"dgte0"`
	TaxRate   decimal.Decimal `json:"tax_rate" validate:"dgte0"`
}

type POIn struct {
	SupplierID   uuid.UUID `json:"supplier_id" validate:"required"`
	ExpectedDate string    `json:"expected_date" validate:"date"`
	Note         string    `json:"note" validate:"max=1000"`
	Items        []POItem  `json:"items" validate:"min=1,max=500,dive"`
}

// writePOItems replaces the order lines and returns subtotal, tax and total.
func writePOItems(ctx context.Context, tx pgx.Tx, businessID, poID uuid.UUID, items []POItem) (decimal.Decimal, decimal.Decimal, error) {
	seen := map[uuid.UUID]bool{}
	sub, tax := money.Zero, money.Zero
	for _, it := range items {
		if seen[it.ProductID] {
			return sub, tax, httpx.BadRequest("invalid_body", "Each product may appear only once")
		}
		seen[it.ProductID] = true
		if err := stockProduct(ctx, tx, it.ProductID); err != nil {
			return sub, tax, err
		}
		line := money.Money(it.Qty.Mul(it.UnitCost))
		lineTax := money.Money(line.Mul(it.TaxRate).Div(decimal.NewFromInt(100)))
		sub = sub.Add(line)
		tax = tax.Add(lineTax)
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_order_items (id, business_id, po_id, product_id, qty_ordered, unit_cost, tax_rate, line_total)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, ids.New(), businessID, poID, it.ProductID, money.Qty(it.Qty), money.Cost(it.UnitCost), it.TaxRate, line); err != nil {
			return sub, tax, err
		}
	}
	return sub, tax, nil
}

func (s *Service) CreatePO(ctx context.Context, p *auth.Principal, in POIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := activeSupplier(ctx, tx, in.SupplierID); err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "po")
		if err != nil {
			return err
		}
		id = ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_orders (id, business_id, branch_id, number, supplier_id, order_date, expected_date, note, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::date,NULLIF($8,''),$9)
		`, id, p.BusinessID, *p.BranchID, number, in.SupplierID, s.bizDate(ctx, tx), in.ExpectedDate, in.Note, p.UserID); err != nil {
			return err
		}
		sub, tax, err := writePOItems(ctx, tx, p.BusinessID, id, in.Items)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET subtotal=$2, tax_total=$3, total=$4 WHERE id=$1`, id, sub, tax, sub.Add(tax)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase_order.created", "purchase_order", id, nil, map[string]any{"number": number, "total": sub.Add(tax)})
	})
	if err != nil {
		return nil, err
	}
	return s.GetPO(ctx, p, id)
}

type poRow struct {
	branch, supplier uuid.UUID
	status, number   string
	total            decimal.Decimal
}

func lockPO(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, key string) (poRow, error) {
	var r poRow
	err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id, status, number, total FROM purchase_orders WHERE id=$1 FOR UPDATE`, id).
		Scan(&r.branch, &r.supplier, &r.status, &r.number, &r.total)
	if err != nil || !p.SeesBranch(key, r.branch) {
		return r, httpx.NotFound("not_found", "Purchase order not found")
	}
	return r, nil
}

func (s *Service) UpdatePO(ctx context.Context, p *auth.Principal, id uuid.UUID, in POIn) (map[string]any, error) {
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, p, id, "po.create")
		if err != nil {
			return err
		}
		if po.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft order can be edited")
		}
		if err := activeSupplier(ctx, tx, in.SupplierID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM purchase_order_items WHERE po_id=$1`, id); err != nil {
			return err
		}
		sub, tax, err := writePOItems(ctx, tx, p.BusinessID, id, in.Items)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE purchase_orders SET supplier_id=$2, expected_date=NULLIF($3,'')::date, note=NULLIF($4,''), subtotal=$5, tax_total=$6, total=$7 WHERE id=$1
		`, id, in.SupplierID, in.ExpectedDate, in.Note, sub, tax, sub.Add(tax)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase_order.updated", "purchase_order", id, map[string]any{"total": po.total}, map[string]any{"total": sub.Add(tax)})
	})
	if err != nil {
		return nil, err
	}
	return s.GetPO(ctx, p, id)
}

// Submit approves the order at once when it is within the caller's po.create
// limit, otherwise it waits for approval (202).
func (s *Service) Submit(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, map[string]any, error) {
	var status int
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, p, id, "po.create")
		if err != nil {
			return err
		}
		if po.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft order can be submitted")
		}
		g, ok := p.Can("po.create")
		if p.IsOwner || auth.WithinLimit(g, ok, po.total) {
			if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, id, p.UserID); err != nil {
				return err
			}
			status = 200
			out = map[string]any{"id": id, "number": po.number, "status": "approved"}
			return s.Audit.Record(ctx, tx, p, "purchase_order.approved", "purchase_order", id, map[string]any{"status": "draft"}, out)
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='pending_approval' WHERE id=$1`, id); err != nil {
			return err
		}
		reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: &po.branch, Kind: "purchase_order", SubjectType: "purchase_order", SubjectID: id,
			Amount: decimal.NullDecimal{Decimal: po.total, Valid: true}, Summary: "Purchase order " + po.number,
		})
		if err != nil {
			return err
		}
		status = 202
		out = map[string]any{"id": id, "number": po.number, "status": "pending_approval", "approval_request_id": reqID}
		return s.Audit.Record(ctx, tx, p, "purchase_order.submitted", "purchase_order", id, map[string]any{"status": "draft"}, out)
	})
	return status, out, err
}

func (s *Service) OnPOApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	tag, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1 AND status='pending_approval'`, req.SubjectID, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.Conflict("invalid_status", "This order is no longer waiting for approval")
	}
	return s.Audit.Record(ctx, tx, p, "purchase_order.approved", "purchase_order", req.SubjectID, map[string]any{"status": "pending_approval"}, map[string]any{"status": "approved"})
}

// OnPORejected returns the order to draft (rejection or requester cancel).
func (s *Service) OnPORejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='draft' WHERE id=$1 AND status='pending_approval'`, req.SubjectID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "purchase_order."+req.Status, "purchase_order", req.SubjectID, map[string]any{"status": "pending_approval"}, map[string]any{"status": "draft", "note": req.Note})
}

// CancelPO cancels a draft, pending or approved order on which nothing was received (FR-PUR-04).
func (s *Service) CancelPO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, p, id, "po.create")
		if err != nil {
			return err
		}
		if po.status != "draft" && po.status != "pending_approval" && po.status != "approved" {
			return httpx.Conflict("invalid_status", "This order can no longer be cancelled")
		}
		var received decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(qty_received),0) FROM purchase_order_items WHERE po_id=$1`, id).Scan(&received); err != nil {
			return err
		}
		if received.IsPositive() {
			return httpx.Conflict("invalid_status", "Goods were already received on this order. Close it instead")
		}
		if po.status == "pending_approval" {
			if _, err := tx.Exec(ctx, `
				UPDATE approval_requests SET status='cancelled', decided_by=$2, decided_at=now(), decision_note='Order cancelled'
				WHERE subject_type='purchase_order' AND subject_id=$1 AND status='pending'
			`, id, p.UserID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='cancelled' WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase_order.cancelled", "purchase_order", id, map[string]any{"status": po.status}, map[string]any{"status": "cancelled"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "cancelled"}, nil
}

// ClosePO closes a partially received order so the rest is no longer expected.
func (s *Service) ClosePO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, p, id, "po.approve")
		if err != nil {
			return err
		}
		if po.status != "partially_received" {
			return httpx.Conflict("invalid_status", "Only a partially received order can be closed")
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='closed' WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase_order.closed", "purchase_order", id, map[string]any{"status": po.status}, map[string]any{"status": "closed"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "closed"}, nil
}

// ---- Goods receipts ----

type ReceiptItem struct {
	ProductID uuid.UUID       `json:"product_id" validate:"required"`
	Qty       decimal.Decimal `json:"qty" validate:"dpos"`
	UnitCost  decimal.Decimal `json:"unit_cost" validate:"dgte0"`
	// BatchNo and ExpiryDate are recorded with the receipt line (FR-INV-32).
	BatchNo    string `json:"batch_no" validate:"max=60"`
	ExpiryDate string `json:"expiry_date" validate:"omitempty,date"`
}

type ReceiptIn struct {
	POID         *uuid.UUID      `json:"po_id"`
	SupplierID   *uuid.UUID      `json:"supplier_id"`
	DeliveryNote string          `json:"delivery_note" validate:"max=200"`
	ExtraCosts   decimal.Decimal `json:"extra_costs" validate:"dgte0"`
	Items        []ReceiptItem   `json:"items" validate:"min=1,max=500,dive"`
}

// Receive posts a goods receipt against an approved order, or a direct
// receipt that creates the order and receipt together (FR-PUR-05, 06, 07, 08).
func (s *Service) Receive(ctx context.Context, p *auth.Principal, in ReceiptIn) (int, []byte, error) {
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	if in.POID == nil && in.SupplierID == nil {
		return 0, nil, httpx.Validation(errors.New("po_id or supplier_id"))
	}
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		seen := map[uuid.UUID]bool{}
		for _, it := range in.Items {
			if seen[it.ProductID] {
				return 0, nil, httpx.BadRequest("invalid_body", "Each product may appear only once")
			}
			seen[it.ProductID] = true
		}
		var poID, branch, supplier uuid.UUID
		date := s.bizDate(ctx, tx)
		if in.POID != nil {
			po, err := lockPO(ctx, tx, p, *in.POID, "grn.create")
			if err != nil {
				return 0, nil, err
			}
			if po.status != "approved" && po.status != "partially_received" {
				return 0, nil, httpx.Conflict("invalid_status", "The order must be approved before it can be received")
			}
			poID, branch, supplier = *in.POID, po.branch, po.supplier
		} else {
			if p.BranchID == nil {
				return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
			}
			if err := activeSupplier(ctx, tx, *in.SupplierID); err != nil {
				return 0, nil, err
			}
			branch, supplier = *p.BranchID, *in.SupplierID
			number, err := sequence.Next(ctx, tx, branch, "po")
			if err != nil {
				return 0, nil, err
			}
			poID = ids.New()
			items := make([]POItem, len(in.Items))
			for i, it := range in.Items {
				items[i] = POItem{ProductID: it.ProductID, Qty: it.Qty, UnitCost: it.UnitCost}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_orders (id, business_id, branch_id, number, supplier_id, status, order_date, note, created_by, approved_by, approved_at)
				VALUES ($1,$2,$3,$4,$5,'approved',$6,'Direct receipt',$7,$7,now())
			`, poID, p.BusinessID, branch, number, supplier, date, p.UserID); err != nil {
				return 0, nil, err
			}
			sub, _, err := writePOItems(ctx, tx, p.BusinessID, poID, items)
			if err != nil {
				return 0, nil, err
			}
			if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET subtotal=$2, total=$2 WHERE id=$1`, poID, sub); err != nil {
				return 0, nil, err
			}
		}
		type line struct {
			poItem    uuid.UUID
			item      ReceiptItem
			cost      decimal.Decimal
			goodValue decimal.Decimal
		}
		lines := make([]line, 0, len(in.Items))
		goods := money.Zero
		for _, it := range in.Items {
			var poItem uuid.UUID
			var ordered, received decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT id, qty_ordered, qty_received FROM purchase_order_items WHERE po_id=$1 AND product_id=$2 FOR UPDATE`, poID, it.ProductID).Scan(&poItem, &ordered, &received); err != nil {
				return 0, nil, httpx.BadRequest("invalid_body", "That product is not on the order")
			}
			qty := money.Qty(it.Qty)
			if received.Add(qty).GreaterThan(ordered) {
				return 0, nil, httpx.Conflict("invalid_status", "You cannot receive more than was ordered")
			}
			cost := money.Cost(it.UnitCost)
			value := money.Money(qty.Mul(cost))
			goods = goods.Add(value)
			lines = append(lines, line{poItem: poItem, item: ReceiptItem{ProductID: it.ProductID, Qty: qty, UnitCost: cost, BatchNo: it.BatchNo, ExpiryDate: it.ExpiryDate}, cost: cost, goodValue: value})
		}
		// Extra costs are spread across lines by value and raise each unit cost (FR-PUR-08).
		extra := money.Money(in.ExtraCosts)
		if extra.IsPositive() && goods.IsZero() {
			return 0, nil, httpx.BadRequest("invalid_body", "Extra costs need lines with a value to spread over")
		}
		total := goods.Add(extra)
		number, err := sequence.Next(ctx, tx, branch, "grn")
		if err != nil {
			return 0, nil, err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO goods_receipts (id, business_id, branch_id, number, po_id, supplier_id, received_date, delivery_note, extra_costs, total_value, received_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11)
		`, id, p.BusinessID, branch, number, poID, supplier, date, in.DeliveryNote, extra, total, p.UserID); err != nil {
			return 0, nil, err
		}
		stockLines := make([]stock.Line, 0, len(lines))
		pids := make([]uuid.UUID, 0, len(lines))
		for _, ln := range lines {
			cost := ln.cost
			if extra.IsPositive() {
				share := extra.Mul(ln.goodValue).Div(goods)
				cost = money.Cost(cost.Add(share.Div(ln.item.Qty)))
			}
			if _, err := tx.Exec(ctx, `UPDATE purchase_order_items SET qty_received=qty_received+$2 WHERE id=$1`, ln.poItem, ln.item.Qty); err != nil {
				return 0, nil, err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO goods_receipt_items (id, business_id, receipt_id, po_item_id, product_id, qty_received, unit_cost, batch_no, expiry_date)
				VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::date)
			`, ids.New(), p.BusinessID, id, ln.poItem, ln.item.ProductID, ln.item.Qty, cost, strings.TrimSpace(ln.item.BatchNo), ln.item.ExpiryDate); err != nil {
				return 0, nil, err
			}
			stockLines = append(stockLines, stock.Line{ProductID: ln.item.ProductID, Qty: ln.item.Qty, UnitCost: cost})
			pids = append(pids, ln.item.ProductID)
		}
		user := p.UserID
		if err := s.Stock.Receive(ctx, tx, p.BusinessID, branch, &user, "purchase", "grn", id, stockLines); err != nil {
			return 0, nil, err
		}
		if total.IsPositive() {
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "grn", id, "Goods received "+number, &user, []ledger.Line{
				{Code: "1200", BranchID: &branch, Debit: total},
				{Code: "2050", BranchID: &branch, PartyType: "supplier", PartyID: &supplier, Credit: total},
			}); err != nil {
				return 0, nil, err
			}
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM purchase_order_items WHERE po_id=$1 AND qty_received < qty_ordered`, poID).Scan(&open); err != nil {
			return 0, nil, err
		}
		next := "received"
		if open > 0 {
			next = "partially_received"
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET status=$2 WHERE id=$1`, poID, next); err != nil {
			return 0, nil, err
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
			return 0, nil, err
		}
		out := map[string]any{"id": id, "number": number, "po_id": poID, "po_status": next, "supplier_id": supplier, "extra_costs": extra, "total_value": total}
		if err := s.Audit.Record(ctx, tx, p, "goods_receipt.posted", "goods_receipt", id, nil, out); err != nil {
			return 0, nil, err
		}
		return 201, out, nil
	})
}

// ---- Purchase invoices ----

type PayNow struct {
	MoneyAccountID uuid.UUID `json:"money_account_id" validate:"required"`
	Method         string    `json:"method" validate:"omitempty,oneof=cash bank mobile_money"`
	Reference      string    `json:"reference" validate:"max=100"`
}

type InvoiceIn struct {
	SupplierID  uuid.UUID       `json:"supplier_id" validate:"required"`
	ReceiptIDs  []uuid.UUID     `json:"receipt_ids" validate:"min=1,max=100"`
	SupplierRef string          `json:"supplier_ref" validate:"max=100"`
	Tax         decimal.Decimal `json:"tax_total" validate:"dgte0"`
	Date        string          `json:"date" validate:"date"`
	DueDate     string          `json:"due_date" validate:"date"`
	PayNow      *PayNow         `json:"pay_now"`
}

func (s *Service) Invoice(ctx context.Context, p *auth.Principal, in InvoiceIn) (int, []byte, error) {
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	if in.PayNow != nil {
		if err := httpx.Check(*in.PayNow); err != nil {
			return 0, nil, err
		}
	}
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var terms int
		if err := tx.QueryRow(ctx, `SELECT payment_terms_days FROM suppliers WHERE id=$1`, in.SupplierID).Scan(&terms); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Supplier not found")
		}
		sub := money.Zero
		var branch *uuid.UUID
		seen := map[uuid.UUID]bool{}
		for _, rid := range in.ReceiptIDs {
			if seen[rid] {
				return 0, nil, httpx.BadRequest("invalid_body", "A receipt is listed twice")
			}
			seen[rid] = true
			var v, returned decimal.Decimal
			var sup, b uuid.UUID
			var inv *uuid.UUID
			err := tx.QueryRow(ctx, `
				SELECT r.total_value, r.supplier_id, r.branch_id, r.invoice_id,
				       COALESCE((SELECT SUM(pr.total) FROM purchase_returns pr WHERE pr.receipt_id=r.id AND pr.invoice_id IS NULL),0)
				FROM goods_receipts r WHERE r.id=$1 FOR UPDATE
			`, rid).Scan(&v, &sup, &b, &inv, &returned)
			if err != nil || !p.SeesBranch("purchase_invoices.manage", b) {
				return 0, nil, httpx.NotFound("not_found", "Goods receipt not found")
			}
			if sup != in.SupplierID {
				return 0, nil, httpx.Conflict("invalid_status", "A receipt belongs to a different supplier")
			}
			if inv != nil {
				return 0, nil, httpx.Conflict("invalid_status", "A receipt is already invoiced")
			}
			if branch != nil && *branch != b {
				return 0, nil, httpx.Conflict("invalid_status", "All receipts on one invoice must be from the same branch")
			}
			bb := b
			branch = &bb
			sub = sub.Add(v.Sub(returned))
		}
		tax := money.Money(in.Tax)
		total := sub.Add(tax)
		number, err := sequence.Next(ctx, tx, *branch, "pinv")
		if err != nil {
			return 0, nil, err
		}
		id := ids.New()
		date := in.Date
		if date == "" {
			date = s.bizDate(ctx, tx)
		}
		due := in.DueDate
		if due == "" {
			d, _ := time.Parse("2006-01-02", date)
			due = d.AddDate(0, 0, terms).Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_invoices (id, business_id, branch_id, number, supplier_ref, supplier_id, invoice_date, due_date, subtotal, tax_total, total, balance, created_by)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11,$11,$12)
		`, id, p.BusinessID, *branch, number, in.SupplierRef, in.SupplierID, date, due, sub, tax, total, p.UserID); err != nil {
			if db.IsUnique(err) {
				return 0, nil, httpx.Conflict("duplicate", "That supplier invoice number is already recorded")
			}
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE goods_receipts SET invoice_id=$2 WHERE id = ANY($1)`, in.ReceiptIDs, id); err != nil {
			return 0, nil, err
		}
		user := p.UserID
		lines := []ledger.Line{{Code: "2000", BranchID: branch, PartyType: "supplier", PartyID: &in.SupplierID, Credit: total}}
		if sub.IsPositive() {
			lines = append(lines, ledger.Line{Code: "2050", BranchID: branch, PartyType: "supplier", PartyID: &in.SupplierID, Debit: sub})
		}
		if tax.IsPositive() {
			lines = append(lines, ledger.Line{Code: "1500", BranchID: branch, Debit: tax})
		}
		if total.IsPositive() {
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "purchase_invoice", id, "Purchase invoice "+number, &user, lines); err != nil {
				return 0, nil, err
			}
		}
		out := map[string]any{"id": id, "number": number, "supplier_id": in.SupplierID, "subtotal": sub, "tax_total": tax, "total": total, "balance": total, "due_date": due, "status": "posted"}
		if err := s.Audit.Record(ctx, tx, p, "purchase_invoice.posted", "purchase_invoice", id, nil, out); err != nil {
			return 0, nil, err
		}
		if in.PayNow != nil && total.IsPositive() {
			if _, ok := p.Can("payments.pay"); !ok && !p.IsOwner {
				return 0, nil, httpx.Forbidden("permission_denied", "You cannot pay suppliers")
			}
			method := in.PayNow.Method
			if method == "" {
				method = "cash"
			}
			pay, err := s.pay(ctx, tx, p, *branch, in.SupplierID, PaymentIn{
				Amount: total, Method: method, MoneyAccountID: in.PayNow.MoneyAccountID, Reference: in.PayNow.Reference, Date: date,
				Allocations: []Allocation{{InvoiceID: id, Amount: total}},
			})
			if err != nil {
				return 0, nil, err
			}
			out["payment"] = pay
			out["balance"] = money.Zero
			out["paid_total"] = total
		}
		return 201, out, nil
	})
}

// VoidInvoice voids an unpaid invoice, reverses its journal entry and frees
// its receipts to be invoiced again.
func (s *Service) VoidInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var st, number string
		var branch uuid.UUID
		var paid decimal.Decimal
		err := tx.QueryRow(ctx, `SELECT status, number, branch_id, paid_total FROM purchase_invoices WHERE id=$1 FOR UPDATE`, id).Scan(&st, &number, &branch, &paid)
		if err != nil || !p.SeesBranch("purchase_invoices.manage", branch) {
			return 0, nil, httpx.NotFound("not_found", "Purchase invoice not found")
		}
		if st != "posted" {
			return 0, nil, httpx.Conflict("invalid_status", "This invoice is already void")
		}
		var allocs, returns int
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM payment_allocations a JOIN payments pm ON pm.id=a.payment_id
			        WHERE a.document_type='purchase_invoice' AND a.document_id=$1 AND pm.status='posted'),
			       (SELECT count(*) FROM purchase_returns WHERE invoice_id=$1)
		`, id).Scan(&allocs, &returns); err != nil {
			return 0, nil, err
		}
		if paid.IsPositive() || allocs > 0 {
			return 0, nil, httpx.Conflict("invalid_status", "Only an unpaid invoice can be voided. Void its payments first")
		}
		if returns > 0 {
			return 0, nil, httpx.Conflict("invalid_status", "Goods were returned against this invoice, so it cannot be voided")
		}
		user := p.UserID
		if err := s.Ledger.Reverse(ctx, tx, p.BusinessID, "purchase_invoice", id, s.bizDate(ctx, tx), "Void purchase invoice "+number, &user); err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE purchase_invoices SET status='voided', balance=0 WHERE id=$1`, id); err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE goods_receipts SET invoice_id=NULL WHERE invoice_id=$1`, id); err != nil {
			return 0, nil, err
		}
		out := map[string]any{"id": id, "number": number, "status": "voided"}
		if err := s.Audit.Record(ctx, tx, p, "purchase_invoice.voided", "purchase_invoice", id, map[string]any{"status": "posted"}, map[string]any{"status": "voided", "reason": reason}); err != nil {
			return 0, nil, err
		}
		return 200, out, nil
	})
}

// ---- Supplier payments ----

type Allocation struct {
	InvoiceID uuid.UUID       `json:"invoice_id" validate:"required"`
	Amount    decimal.Decimal `json:"amount" validate:"dpos"`
}

type PaymentIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"omitempty,oneof=cash bank mobile_money"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=100"`
	Date           string          `json:"date" validate:"date"`
	Note           string          `json:"note" validate:"max=500"`
	Allocations    []Allocation    `json:"allocations" validate:"max=200,dive"`
}

// Pay records a supplier payment. Without allocations it pays unpaid invoices
// oldest first (BR-09); anything left is an advance to the supplier.
func (s *Service) Pay(ctx context.Context, p *auth.Principal, supplierID uuid.UUID, in PaymentIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	g, ok := p.Can("payments.pay")
	if !p.IsOwner && !auth.WithinLimit(g, ok, money.Money(in.Amount)) {
		return 0, nil, httpx.Forbidden("over_limit", "This payment is above your limit. Ask someone with a higher limit to pay it")
	}
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		if _, err := supplierRow(ctx, tx, supplierID); err != nil {
			return 0, nil, err
		}
		if in.Method == "" {
			in.Method = "cash"
		}
		out, err := s.pay(ctx, tx, p, *p.BranchID, supplierID, in)
		if err != nil {
			return 0, nil, err
		}
		return 201, out, nil
	})
}

func (s *Service) pay(ctx context.Context, tx pgx.Tx, p *auth.Principal, branch, supplierID uuid.UUID, in PaymentIn) (map[string]any, error) {
	amt := money.Money(in.Amount)
	code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.MoneyAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("not_found", "Money account not found")
	}
	if err != nil {
		return nil, err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM money_accounts WHERE id=$1`, in.MoneyAccountID).Scan(&active); err != nil || !active {
		return nil, httpx.Conflict("invalid_status", "That money account is inactive")
	}
	if err := s.Ledger.EnsureFunds(ctx, tx, in.MoneyAccountID, amt); err != nil {
		return nil, err
	}
	type use struct {
		id  uuid.UUID
		amt decimal.Decimal
	}
	var uses []use
	left := amt
	if len(in.Allocations) > 0 {
		seen := map[uuid.UUID]bool{}
		for _, a := range in.Allocations {
			if seen[a.InvoiceID] {
				return nil, httpx.BadRequest("invalid_body", "An invoice is allocated twice")
			}
			seen[a.InvoiceID] = true
			var bal decimal.Decimal
			var sup uuid.UUID
			var st string
			if err := tx.QueryRow(ctx, `SELECT balance, supplier_id, status FROM purchase_invoices WHERE id=$1 FOR UPDATE`, a.InvoiceID).Scan(&bal, &sup, &st); err != nil || sup != supplierID {
				return nil, httpx.NotFound("not_found", "Purchase invoice not found for this supplier")
			}
			x := money.Money(a.Amount)
			if st != "posted" || x.GreaterThan(bal) {
				return nil, httpx.Conflict("invalid_status", "An allocation is more than the invoice balance")
			}
			if x.GreaterThan(left) {
				return nil, httpx.BadRequest("invalid_body", "Allocations add up to more than the payment")
			}
			uses = append(uses, use{a.InvoiceID, x})
			left = left.Sub(x)
		}
	} else {
		rows, err := tx.Query(ctx, `
			SELECT id, balance FROM purchase_invoices WHERE supplier_id=$1 AND balance>0 AND status='posted'
			ORDER BY invoice_date, created_at FOR UPDATE
		`, supplierID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id uuid.UUID
			var bal decimal.Decimal
			if err := rows.Scan(&id, &bal); err != nil {
				rows.Close()
				return nil, err
			}
			if !left.IsPositive() {
				continue
			}
			x := decimal.Min(left, bal)
			uses = append(uses, use{id, x})
			left = left.Sub(x)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	date := in.Date
	if date == "" {
		date = s.bizDate(ctx, tx)
	}
	res, err := payments.Insert(ctx, tx, payments.Input{
		BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
		Direction: "out", Kind: "supplier_payment", Method: in.Method, PartyType: "supplier", PartyID: &supplierID,
		Amount: amt, Reference: in.Reference, Note: in.Note, Date: date,
	})
	if err != nil {
		return nil, err
	}
	applied := money.Zero
	allocs := []map[string]any{}
	for _, u := range uses {
		if _, err := tx.Exec(ctx, `UPDATE purchase_invoices SET paid_total=paid_total+$2, balance=balance-$2 WHERE id=$1`, u.id, u.amt); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_allocations (id, business_id, payment_id, document_type, document_id, amount) VALUES ($1,$2,$3,'purchase_invoice',$4,$5)
		`, ids.New(), p.BusinessID, res.ID, u.id, u.amt); err != nil {
			return nil, err
		}
		applied = applied.Add(u.amt)
		allocs = append(allocs, map[string]any{"invoice_id": u.id, "amount": u.amt})
	}
	user := p.UserID
	lines := []ledger.Line{{Code: code, BranchID: &branch, MoneyAccountID: &in.MoneyAccountID, PartyType: "supplier", PartyID: &supplierID, Credit: amt}}
	if applied.IsPositive() {
		lines = append(lines, ledger.Line{Code: "2000", BranchID: &branch, PartyType: "supplier", PartyID: &supplierID, Debit: applied})
	}
	if left.IsPositive() {
		lines = append(lines, ledger.Line{Code: "1400", BranchID: &branch, PartyType: "supplier", PartyID: &supplierID, Debit: left})
	}
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "supplier_payment", res.ID, "Supplier payment "+res.Number, &user, lines); err != nil {
		return nil, err
	}
	out := map[string]any{"id": res.ID, "number": res.Number, "amount": amt, "allocated": applied, "advance": left, "allocations": allocs, "voucher_url": "/api/v1/payments/" + res.ID.String() + "/voucher"}
	if err := s.Audit.Record(ctx, tx, p, "supplier_payment.posted", "payment", res.ID, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}
