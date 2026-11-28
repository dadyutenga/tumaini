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
	"bms/internal/kernel/notify"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Approval: ap, Notify: n, Clock: clk}
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

func (s *Service) idemRun(ctx context.Context, p *auth.Principal, fn func(ctx context.Context, tx pgx.Tx) (int, any, error)) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		st, out, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, st, httpx.Data(out))
		status, body = st, raw
		return err
	})
	return status, body, err
}

func activeSupplier(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	var active bool
	var terms *int
	if err := tx.QueryRow(ctx, "SELECT status = 'active', payment_terms_days FROM app.suppliers WHERE id = $1", id).Scan(&active, &terms); err != nil {
		return 0, httpx.NotFound("not_found", "Supplier not found")
	}
	if !active {
		return 0, httpx.Conflict("invalid_status", "That supplier is inactive")
	}
	if terms == nil {
		return 0, nil
	}
	return *terms, nil
}

func activeProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var active bool
	if err := tx.QueryRow(ctx, "SELECT status = 'active' FROM app.products WHERE id = $1", id).Scan(&active); err != nil {
		return httpx.NotFound("not_found", "Product not found")
	}
	if !active {
		return httpx.Conflict("invalid_status", "That product is inactive")
	}
	return nil
}

// ---- Purchases (orders & payables) ----

type POItem struct {
	ProductID uuid.UUID       `json:"product_id" validate:"required"`
	Qty       decimal.Decimal `json:"qty" validate:"dpos"`
	UnitCost  decimal.Decimal `json:"unit_cost" validate:"dgte0"`
	TaxRate   decimal.Decimal `json:"tax_rate" validate:"dgte0"`
}

type POIn struct {
	SupplierID         uuid.UUID       `json:"supplier_id" validate:"required"`
	ExpectedDate       string          `json:"expected_date" validate:"date"`
	DueDate            string          `json:"due_date" validate:"date"`
	SupplierInvoiceRef string          `json:"supplier_invoice_ref" validate:"max=60"`
	DiscountAmount     decimal.Decimal `json:"discount_amount" validate:"dgte0"`
	Note               string          `json:"note" validate:"max=2000"`
	Items              []POItem        `json:"items" validate:"min=1,max=500,dive"`
}

// writePurchaseLines replaces the purchase lines and returns subtotal and tax.
func writePurchaseLines(ctx context.Context, tx pgx.Tx, purchaseID uuid.UUID, items []POItem) (decimal.Decimal, decimal.Decimal, error) {
	seen := map[uuid.UUID]bool{}
	sub, tax := money.Zero, money.Zero
	for i, it := range items {
		if seen[it.ProductID] {
			return sub, tax, httpx.BadRequest("invalid_body", "Each product may appear only once")
		}
		seen[it.ProductID] = true
		if err := activeProduct(ctx, tx, it.ProductID); err != nil {
			return sub, tax, err
		}
		qty := money.Qty(it.Qty)
		cost := money.Money(it.UnitCost)
		line := money.Money(qty.Mul(cost))
		lineTax := money.Money(line.Mul(it.TaxRate).Div(decimal.NewFromInt(100)))
		sub = sub.Add(line)
		tax = tax.Add(lineTax)
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.purchase_lines (purchase_id, line_no, product_id, quantity_ordered, unit_cost, line_total)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6)", purchaseID, i+1, it.ProductID, qty, cost, line); err != nil {
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		terms, err := activeSupplier(ctx, tx, in.SupplierID)
		if err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "purchase")
		if err != nil {
			return err
		}
		date := s.bizDate(ctx, tx)
		due := strings.TrimSpace(in.DueDate)
		if due == "" && terms > 0 {
			d, _ := time.Parse("2006-01-02", date)
			due = d.AddDate(0, 0, terms).Format("2006-01-02")
		}
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.purchases ( purchase_no, supplier_id, business_unit_id, location_id, status,\n\t\t\t                           order_date, expected_date, due_date, supplier_invoice_ref, notes)\n\t\t\tVALUES ($1,$2,(SELECT id FROM app.business_units WHERE code = 'A'),$3,'draft',\n\t\t\t        $4::date,NULLIF($5,'')::date,NULLIF($6,'')::date,NULLIF($7,''),NULLIF($8,''))\n\t\t\tRETURNING id", number, in.SupplierID, *p.BranchID, date,
			strings.TrimSpace(in.ExpectedDate), due, strings.TrimSpace(in.SupplierInvoiceRef), strings.TrimSpace(in.Note)).Scan(&id); err != nil {
			return err
		}
		sub, tax, err := writePurchaseLines(ctx, tx, id, in.Items)
		if err != nil {
			return err
		}
		disc := money.Money(in.DiscountAmount)
		if disc.GreaterThan(sub) {
			return httpx.BadRequest("invalid_body", "Discount cannot exceed subtotal")
		}
		total := sub.Sub(disc).Add(tax)
		if _, err := tx.Exec(ctx, "UPDATE app.purchases SET subtotal = $2, discount_amount = $3, tax_amount = $4, total = $5 WHERE id = $1", id, sub, disc, tax, total); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase.created", "purchase", id, nil, map[string]any{"number": number, "total": total})
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

func lockPO(ctx context.Context, tx pgx.Tx, id uuid.UUID) (poRow, error) {
	var r poRow
	err := tx.QueryRow(ctx, "\n\t\tSELECT location_id, supplier_id, status::text, purchase_no, total\n\t\tFROM app.purchases WHERE id = $1 FOR UPDATE", id).
		Scan(&r.branch, &r.supplier, &r.status, &r.number, &r.total)
	if err != nil {
		return r, httpx.NotFound("not_found", "Purchase order not found")
	}
	return r, nil
}

func (s *Service) UpdatePO(ctx context.Context, p *auth.Principal, id uuid.UUID, in POIn) (map[string]any, error) {
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft purchase can be edited")
		}
		if _, err := activeSupplier(ctx, tx, in.SupplierID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM app.purchase_lines WHERE purchase_id = $1", id); err != nil {
			return err
		}
		sub, tax, err := writePurchaseLines(ctx, tx, id, in.Items)
		if err != nil {
			return err
		}
		disc := money.Money(in.DiscountAmount)
		if disc.GreaterThan(sub) {
			return httpx.BadRequest("invalid_body", "Discount cannot exceed subtotal")
		}
		total := sub.Sub(disc).Add(tax)
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.purchases\n\t\t\tSET supplier_id = $2, expected_date = NULLIF($3,'')::date, due_date = NULLIF($4,'')::date,\n\t\t\t    supplier_invoice_ref = NULLIF($5,''), notes = NULLIF($6,''),\n\t\t\t    subtotal = $7, discount_amount = $8, tax_amount = $9, total = $10\n\t\t\tWHERE id = $1", id, in.SupplierID, strings.TrimSpace(in.ExpectedDate), strings.TrimSpace(in.DueDate),
			strings.TrimSpace(in.SupplierInvoiceRef), strings.TrimSpace(in.Note), sub, disc, tax, total); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase.updated", "purchase", id, map[string]any{"total": po.total}, map[string]any{"total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetPO(ctx, p, id)
}

// Submit moves a draft purchase to 'ordered' so goods can be received against it.
func (s *Service) Submit(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft purchase can be placed as ordered")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.purchases SET status = 'ordered' WHERE id = $1", id); err != nil {
			return err
		}
		out = map[string]any{"id": id, "number": po.number, "status": "ordered"}
		return s.Audit.Record(ctx, tx, p, "purchase.ordered", "purchase", id, map[string]any{"status": "draft"}, out)
	})
	return 200, out, err
}

// CancelPO cancels a draft or ordered purchase on which nothing has been received or paid.
func (s *Service) CancelPO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		po, err := lockPO(ctx, tx, id)
		if err != nil {
			return err
		}
		if po.status != "draft" && po.status != "ordered" {
			return httpx.Conflict("invalid_status", "This purchase can no longer be cancelled")
		}
		var received, paid decimal.Decimal
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT COALESCE((SELECT SUM(quantity_received) FROM app.purchase_lines WHERE purchase_id = $1), 0),\n\t\t\t       amount_paid\n\t\t\tFROM app.purchases WHERE id = $1", id).Scan(&received, &paid); err != nil {
			return err
		}
		if received.IsPositive() {
			return httpx.Conflict("invalid_status", "Goods were already received on this purchase")
		}
		if paid.IsPositive() {
			return httpx.Conflict("invalid_status", "Void the payments on this purchase before cancelling it")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.purchases SET status = 'cancelled' WHERE id = $1", id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "purchase.cancelled", "purchase", id, map[string]any{"status": po.status}, map[string]any{"status": "cancelled"})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "cancelled"}, nil
}

func (s *Service) ClosePO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Manual purchase closing (purchases move to received when all ordered lines are received)")
}

// ---- Goods receipts ----

type UnitIn struct {
	SerialNo string `json:"serial_no"`
	IMEI     string `json:"imei"`
}

type ReceiptItem struct {
	ProductID     uuid.UUID       `json:"product_id" validate:"required"`
	Qty           decimal.Decimal `json:"qty" validate:"dpos"`
	UnitCost      decimal.Decimal `json:"unit_cost" validate:"dgte0"`
	BatchNo       string          `json:"batch_no" validate:"max=60"`
	ExpiryDate    string          `json:"expiry_date" validate:"omitempty,date"`
	SerialNumbers []string        `json:"serial_numbers,omitempty"`
	Units         []UnitIn        `json:"units,omitempty"`
}

type ReceiptIn struct {
	POID               *uuid.UUID      `json:"po_id"`
	SupplierID         *uuid.UUID      `json:"supplier_id"`
	SupplierInvoiceRef string          `json:"supplier_invoice_ref" validate:"max=60"`
	DueDate            string          `json:"due_date" validate:"date"`
	DeliveryNote       string          `json:"delivery_note" validate:"max=1000"`
	ExtraCosts         decimal.Decimal `json:"extra_costs" validate:"dgte0"`
	Items              []ReceiptItem   `json:"items" validate:"min=1,max=500,dive"`
}

// Receive posts a goods receipt against an ordered purchase, or creates a direct ordered purchase
// and receives it in one transaction. The database trigger tg_goods_receipt_line_post increments
// quantity_received, posts stock_movements (updating moving weighted average cost and buying_price),
// and advances purchase.status to partially_received or received.
func (s *Service) Receive(ctx context.Context, p *auth.Principal, in ReceiptIn) (int, []byte, error) {
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	if in.POID == nil && in.SupplierID == nil {
		return 0, nil, httpx.Validation(errors.New("po_id or supplier_id"))
	}
	for _, it := range in.Items {
		if strings.TrimSpace(it.BatchNo) != "" || strings.TrimSpace(it.ExpiryDate) != "" {
			return 0, nil, httpx.BadRequest("batch_not_supported", "Batch numbers and expiry dates are not part of the approved model")
		}
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
			po, err := lockPO(ctx, tx, *in.POID)
			if err != nil {
				return 0, nil, err
			}
			if po.status != "ordered" && po.status != "partially_received" {
				return 0, nil, httpx.Conflict("invalid_status", "The purchase must be ordered before goods can be received")
			}
			poID, branch, supplier = *in.POID, po.branch, po.supplier
		} else {
			if p.BranchID == nil {
				return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
			}
			terms, err := activeSupplier(ctx, tx, *in.SupplierID)
			if err != nil {
				return 0, nil, err
			}
			branch, supplier = *p.BranchID, *in.SupplierID
			number, err := sequence.Next(ctx, tx, uuid.Nil, "purchase")
			if err != nil {
				return 0, nil, err
			}
			due := strings.TrimSpace(in.DueDate)
			if due == "" && terms > 0 {
				d, _ := time.Parse("2006-01-02", date)
				due = d.AddDate(0, 0, terms).Format("2006-01-02")
			}
			if err := tx.QueryRow(ctx, "\n\t\t\t\tINSERT INTO app.purchases ( purchase_no, supplier_id, business_unit_id, location_id, status,\n\t\t\t\t                           order_date, due_date, supplier_invoice_ref, notes)\n\t\t\t\tVALUES ($1,$2,(SELECT id FROM app.business_units WHERE code = 'A'),$3,'ordered',\n\t\t\t\t        $4::date,NULLIF($5,'')::date,NULLIF($6,''),'Direct receipt')\n\t\t\t\tRETURNING id", number, supplier, branch, date, due, strings.TrimSpace(in.SupplierInvoiceRef)).Scan(&poID); err != nil {
				return 0, nil, err
			}
			poItems := make([]POItem, len(in.Items))
			for i, it := range in.Items {
				poItems[i] = POItem{ProductID: it.ProductID, Qty: it.Qty, UnitCost: it.UnitCost}
			}
			sub, _, err := writePurchaseLines(ctx, tx, poID, poItems)
			if err != nil {
				return 0, nil, err
			}
			if _, err := tx.Exec(ctx, "UPDATE app.purchases SET subtotal = $2, total = $2 WHERE id = $1", poID, sub); err != nil {
				return 0, nil, err
			}
		}
		type line struct {
			poLineID  uuid.UUID
			item      ReceiptItem
			cost      decimal.Decimal
			goodValue decimal.Decimal
			units     []UnitIn
		}
		lines := make([]line, 0, len(in.Items))
		goods := money.Zero
		for _, it := range in.Items {
			var poLineID uuid.UUID
			var ordered, received decimal.Decimal
			if err := tx.QueryRow(ctx, "\n\t\t\t\tSELECT id, quantity_ordered, quantity_received\n\t\t\t\tFROM app.purchase_lines WHERE purchase_id = $1 AND product_id = $2 FOR UPDATE", poID, it.ProductID).Scan(&poLineID, &ordered, &received); err != nil {
				return 0, nil, httpx.BadRequest("invalid_body", "That product is not on the purchase order")
			}
			qty := money.Qty(it.Qty)
			if received.Add(qty).GreaterThan(ordered) {
				return 0, nil, httpx.Conflict("invalid_status", "You cannot receive more than was ordered")
			}
			cost := money.Money(it.UnitCost)
			value := money.Money(qty.Mul(cost))
			goods = goods.Add(value)
			var units []UnitIn
			for _, u := range it.Units {
				if strings.TrimSpace(u.SerialNo) != "" {
					units = append(units, UnitIn{SerialNo: strings.TrimSpace(u.SerialNo), IMEI: strings.TrimSpace(u.IMEI)})
				}
			}
			for _, sn := range it.SerialNumbers {
				if s := strings.TrimSpace(sn); s != "" {
					units = append(units, UnitIn{SerialNo: s})
				}
			}
			lines = append(lines, line{poLineID: poLineID, item: ReceiptItem{ProductID: it.ProductID, Qty: qty, UnitCost: cost}, cost: cost, goodValue: value, units: units})
		}
		extra := money.Money(in.ExtraCosts)
		if extra.IsPositive() && goods.IsZero() {
			return 0, nil, httpx.BadRequest("invalid_body", "Extra costs need lines with a value to spread over")
		}
		total := goods.Add(extra)
		number, err := sequence.Next(ctx, tx, uuid.Nil, "goods_receipt")
		if err != nil {
			return 0, nil, err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.goods_receipts ( receipt_no, purchase_id, location_id, business_date, notes)
			VALUES ($1,$2,$3,$4::date,NULLIF($5,''))
			RETURNING id`, number, poID, branch, date, strings.TrimSpace(in.DeliveryNote)).Scan(&id); err != nil {
			return 0, nil, err
		}
		pids := make([]uuid.UUID, 0, len(lines))
		for _, ln := range lines {
			cost := ln.cost
			if extra.IsPositive() {
				share := extra.Mul(ln.goodValue).Div(goods)
				cost = money.Money(cost.Add(share.Div(ln.item.Qty)))
			}
			var grLineID uuid.UUID
			if err := tx.QueryRow(ctx, "\n\t\t\t\tINSERT INTO app.goods_receipt_lines (goods_receipt_id, purchase_line_id, quantity, unit_cost)\n\t\t\t\tVALUES ($1, $2, $3, $4)\n\t\t\t\tRETURNING id", id, ln.poLineID, ln.item.Qty, cost).Scan(&grLineID); err != nil {
				return 0, nil, err
			}
			for _, u := range ln.units {
				var unitID uuid.UUID
				if err := tx.QueryRow(ctx, `
					INSERT INTO app.product_units ( product_id, location_id, serial_no, imei, status)
					VALUES ($1,$2,$3,NULLIF($4,''),'in_stock')
					RETURNING id`, ln.item.ProductID, branch, u.SerialNo, u.IMEI).Scan(&unitID); err != nil {
					if db.IsUnique(err) {
						return 0, nil, httpx.Conflict("duplicate", "Serial number or IMEI "+u.SerialNo+" is already registered")
					}
					return 0, nil, err
				}
				if _, err := tx.Exec(ctx, "\n\t\t\t\t\tINSERT INTO app.goods_receipt_line_units (goods_receipt_line_id, product_unit_id)\n\t\t\t\t\tVALUES ($1, $2)", grLineID, unitID); err != nil {
					return 0, nil, err
				}
			}
			pids = append(pids, ln.item.ProductID)
		}
		var poStatus string
		_ = tx.QueryRow(ctx, "SELECT status::text FROM app.purchases WHERE id = $1", poID).Scan(&poStatus)
		if err := jobs.EnqueueTx(ctx, tx, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
			return 0, nil, err
		}
		out := map[string]any{"id": id, "number": number, "po_id": poID, "po_status": poStatus, "supplier_id": supplier, "extra_costs": extra, "total_value": total}
		if err := s.Audit.Record(ctx, tx, p, "goods_receipt.posted", "goods_receipt", id, nil, out); err != nil {
			return 0, nil, err
		}
		return 201, out, nil
	})
}

// ---- Purchase invoices (removed; payables are on app.purchases) ----

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
	return 0, nil, removed("Separate purchase invoices (payables are recorded directly on purchases)")
}

func (s *Service) VoidInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	return 0, nil, removed("Separate purchase invoices")
}

// ---- Supplier payments ----

type Allocation struct {
	PurchaseID *uuid.UUID      `json:"purchase_id"`
	InvoiceID  *uuid.UUID      `json:"invoice_id"` // legacy alias for purchase_id
	Amount     decimal.Decimal `json:"amount" validate:"dpos"`
}

func (a Allocation) targetID() uuid.UUID {
	if a.PurchaseID != nil && *a.PurchaseID != uuid.Nil {
		return *a.PurchaseID
	}
	if a.InvoiceID != nil {
		return *a.InvoiceID
	}
	return uuid.Nil
}

type PaymentIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"max=30"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=60"`
	Date           string          `json:"date" validate:"date"`
	Note           string          `json:"note" validate:"max=1000"`
	Allocations    []Allocation    `json:"allocations" validate:"max=200,dive"`
}

// Pay records a supplier payment in app.payments_made. Without allocations it settles unpaid
// non-cancelled purchases oldest first; any remainder stays as a supplier advance.
func (s *Service) Pay(ctx context.Context, p *auth.Principal, supplierID uuid.UUID, in PaymentIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var lockedSup uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM app.suppliers WHERE id = $1 FOR UPDATE", supplierID).Scan(&lockedSup); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Supplier not found")
		}
		amt := money.Money(in.Amount)
		method := ""
		if strings.TrimSpace(in.Method) != "" {
			method = payments.Method(in.Method)
			if method == "" {
				return 0, nil, httpx.BadRequest("invalid_body", "Payment method must be cash, mobile_money, bank, card or cheque")
			}
		}
		var allocs []payments.Alloc
		left := amt
		if len(in.Allocations) > 0 {
			seen := map[uuid.UUID]bool{}
			for _, a := range in.Allocations {
				tid := a.targetID()
				if tid == uuid.Nil {
					return 0, nil, httpx.BadRequest("invalid_body", "Each allocation must name a purchase_id")
				}
				if seen[tid] {
					return 0, nil, httpx.BadRequest("invalid_body", "A purchase is allocated twice")
				}
				seen[tid] = true
				var bal decimal.Decimal
				var sup uuid.UUID
				var st string
				if err := tx.QueryRow(ctx, "\n\t\t\t\t\tSELECT balance_due, supplier_id, status::text FROM app.purchases WHERE id = $1 FOR UPDATE", tid).Scan(&bal, &sup, &st); err != nil || sup != supplierID {
					return 0, nil, httpx.NotFound("not_found", "Purchase not found for this supplier")
				}
				if st == "cancelled" {
					return 0, nil, httpx.Conflict("invalid_status", "A cancelled purchase cannot be paid")
				}
				useAmt := money.Money(a.Amount)
				if useAmt.GreaterThan(bal) {
					return 0, nil, httpx.Conflict("invalid_status", "Allocation exceeds the purchase balance")
				}
				if useAmt.GreaterThan(left) {
					return 0, nil, httpx.BadRequest("invalid_body", "Allocations exceed the payment amount")
				}
				left = left.Sub(useAmt)
				pid := tid
				allocs = append(allocs, payments.Alloc{PurchaseID: &pid, Amount: useAmt})
			}
		} else {
			rows, err := tx.Query(ctx, "\n\t\t\t\tSELECT id, balance_due FROM app.purchases\n\t\t\t\tWHERE supplier_id = $1 AND status <> 'cancelled' AND balance_due > 0\n\t\t\t\tORDER BY order_date, purchase_no FOR UPDATE", supplierID)
			if err != nil {
				return 0, nil, err
			}
			for rows.Next() && left.IsPositive() {
				var pid uuid.UUID
				var bal decimal.Decimal
				if err := rows.Scan(&pid, &bal); err != nil {
					rows.Close()
					return 0, nil, err
				}
				useAmt := left
				if bal.LessThan(useAmt) {
					useAmt = bal
				}
				left = left.Sub(useAmt)
				idCopy := pid
				allocs = append(allocs, payments.Alloc{PurchaseID: &idCopy, Amount: useAmt})
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return 0, nil, err
			}
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			date = s.bizDate(ctx, tx)
		}
		res, err := payments.Pay(ctx, tx, payments.PayInput{
			Business:   auth.Shop,
			LocationID: *p.BranchID,
			AccountID:  in.MoneyAccountID, SupplierID: &supplierID,
			Method: method, Amount: amt, Reference: strings.TrimSpace(in.Reference), BusinessDate: date,
			Notes: strings.TrimSpace(in.Note), Allocations: allocs,
		})
		if err != nil {
			return 0, nil, err
		}
		allocated := amt.Sub(left)
		out := map[string]any{"id": res.ID, "number": res.Number, "supplier_id": supplierID, "amount": amt,
			"allocated": allocated, "advance": left, "date": date}
		if err := s.Audit.Record(ctx, tx, p, "supplier.payment", "payment_made", res.ID, nil, out); err != nil {
			return 0, nil, err
		}
		return 201, out, nil
	})
}
