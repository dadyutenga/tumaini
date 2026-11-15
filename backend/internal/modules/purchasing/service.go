package purchasing

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Service struct {
	DB       *db.DB
	Stock    *stock.Service
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, st *stock.Service, led *ledger.Service, aud *audit.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Stock: st, Ledger: led, Audit: aud, Approval: ap, Clock: clk}
}

type SupplierIn struct {
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
	Address string `json:"address"`
	Terms   int    `json:"payment_terms_days"`
}

func (s *Service) CreateSupplier(ctx context.Context, p *auth.Principal, in SupplierIn) (map[string]any, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Supplier name is required")
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO suppliers (id, business_id, name, phone, email, address, payment_terms_days, created_by)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::citext,NULLIF($6,''),$7,$8)
		`, id, p.BusinessID, strings.TrimSpace(in.Name), in.Phone, in.Email, in.Address, in.Terms, p.UserID)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "That supplier already exists")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": in.Name}, nil
}

type POItem struct {
	ProductID uuid.UUID       `json:"product_id"`
	Qty       decimal.Decimal `json:"qty"`
	UnitCost  decimal.Decimal `json:"unit_cost"`
}

type POIn struct {
	SupplierID uuid.UUID `json:"supplier_id"`
	Items      []POItem  `json:"items"`
	Note       string    `json:"note"`
}

func (s *Service) CreatePO(ctx context.Context, p *auth.Principal, in POIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		number, err := sequence.Next(ctx, tx, *p.BranchID, "po")
		if err != nil {
			return err
		}
		id := ids.New()
		total := money.Zero
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_orders (id, business_id, branch_id, number, supplier_id, order_date, note, created_by)
			VALUES ($1,$2,$3,$4,$5,current_date,$6,$7)
		`, id, p.BusinessID, *p.BranchID, number, in.SupplierID, in.Note, p.UserID); err != nil {
			return err
		}
		for _, it := range in.Items {
			line := money.Money(it.Qty.Mul(it.UnitCost))
			total = total.Add(line)
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_order_items (id, business_id, po_id, product_id, qty_ordered, unit_cost, line_total)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, ids.New(), p.BusinessID, id, it.ProductID, it.Qty, money.Cost(it.UnitCost), line); err != nil {
				return err
			}
		}
		_, _ = tx.Exec(ctx, `UPDATE purchase_orders SET subtotal=$2, total=$2 WHERE id=$1`, id, total)
		out = map[string]any{"id": id, "number": number, "status": "draft", "total": total}
		return nil
	})
	return out, err
}

func (s *Service) Submit(ctx context.Context, p *auth.Principal, id uuid.UUID) (int, map[string]any, error) {
	var status int
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var total decimal.Decimal
		var st, number string
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT total, status, number, branch_id FROM purchase_orders WHERE id=$1`, id).Scan(&total, &st, &number, &branch); err != nil {
			return httpx.NotFound("not_found", "Purchase order not found")
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft order can be submitted")
		}
		g, ok := p.Can("po.create")
		if auth.WithinLimit(g, ok, total) {
			_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, id, p.UserID)
			status = 200
			out = map[string]any{"id": id, "status": "approved"}
			return err
		}
		_, _ = tx.Exec(ctx, `UPDATE purchase_orders SET status='pending_approval' WHERE id=$1`, id)
		reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: &branch, Kind: "purchase_order", SubjectType: "purchase_order", SubjectID: id,
			Amount: decimal.NullDecimal{Decimal: total, Valid: true}, Summary: "Purchase order " + number,
		})
		status = 202
		out = map[string]any{"id": id, "status": "pending_approval", "approval_request_id": reqID}
		return err
	})
	return status, out, err
}

func (s *Service) OnPOApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	_, err := tx.Exec(ctx, `UPDATE purchase_orders SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, req.SubjectID, p.UserID)
	return err
}

type ReceiptItem struct {
	ProductID uuid.UUID       `json:"product_id"`
	Qty       decimal.Decimal `json:"qty"`
	UnitCost  decimal.Decimal `json:"unit_cost"`
}

func (s *Service) Receive(ctx context.Context, p *auth.Principal, poID uuid.UUID, items []ReceiptItem) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var branch, supplier uuid.UUID
		var st, poNumber string
		if err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id, status, number FROM purchase_orders WHERE id=$1`, poID).Scan(&branch, &supplier, &st, &poNumber); err != nil {
			return httpx.NotFound("not_found", "Purchase order not found")
		}
		if st != "approved" && st != "partially_received" {
			return httpx.Conflict("invalid_status", "The order must be approved before it can be received")
		}
		number, err := sequence.Next(ctx, tx, branch, "grn")
		if err != nil {
			return err
		}
		id := ids.New()
		total := money.Zero
		var lines []stock.Line
		for _, it := range items {
			var poItem uuid.UUID
			var ordered, received decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT id, qty_ordered, qty_received FROM purchase_order_items WHERE po_id=$1 AND product_id=$2`, poID, it.ProductID).Scan(&poItem, &ordered, &received); err != nil {
				return httpx.BadRequest("invalid_body", "That product is not on the order")
			}
			if received.Add(it.Qty).GreaterThan(ordered) {
				return httpx.Conflict("invalid_status", "You cannot receive more than was ordered")
			}
			cost := money.Cost(it.UnitCost)
			total = total.Add(money.Money(it.Qty.Mul(cost)))
			lines = append(lines, stock.Line{ProductID: it.ProductID, Qty: it.Qty, UnitCost: cost})
			if _, err := tx.Exec(ctx, `UPDATE purchase_order_items SET qty_received=qty_received+$2 WHERE id=$1`, poItem, it.Qty); err != nil {
				return err
			}
		}
		date := s.Clock.Now().Format("2006-01-02")
		if _, err := tx.Exec(ctx, `
			INSERT INTO goods_receipts (id, business_id, branch_id, number, po_id, supplier_id, received_date, total_value, received_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		`, id, p.BusinessID, branch, number, poID, supplier, date, total, p.UserID); err != nil {
			return err
		}
		for _, it := range items {
			var poItem uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT id FROM purchase_order_items WHERE po_id=$1 AND product_id=$2`, poID, it.ProductID).Scan(&poItem)
			if _, err := tx.Exec(ctx, `
				INSERT INTO goods_receipt_items (id, business_id, receipt_id, po_item_id, product_id, qty_received, unit_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, ids.New(), p.BusinessID, id, poItem, it.ProductID, it.Qty, money.Cost(it.UnitCost)); err != nil {
				return err
			}
		}
		user := p.UserID
		if err := s.Stock.Receive(ctx, tx, p.BusinessID, branch, &user, "purchase", "grn", id, lines); err != nil {
			return err
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "grn", id, "Goods received "+number, &user, []ledger.Line{
			{Code: "1200", BranchID: &branch, Debit: total},
			{Code: "2050", BranchID: &branch, Credit: total},
		}); err != nil {
			return err
		}
		var open int
		_ = tx.QueryRow(ctx, `SELECT count(*) FROM purchase_order_items WHERE po_id=$1 AND qty_received < qty_ordered`, poID).Scan(&open)
		next := "received"
		if open > 0 {
			next = "partially_received"
		}
		_, _ = tx.Exec(ctx, `UPDATE purchase_orders SET status=$2 WHERE id=$1`, poID, next)
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "po_status": next, "total_value": total}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

type InvoiceIn struct {
	SupplierID  uuid.UUID       `json:"supplier_id"`
	ReceiptIDs  []uuid.UUID     `json:"receipt_ids"`
	SupplierRef string          `json:"supplier_ref"`
	Tax         decimal.Decimal `json:"tax_total"`
	Date        string          `json:"date"`
}

func (s *Service) Invoice(ctx context.Context, p *auth.Principal, in InvoiceIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
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
		sub := money.Zero
		for _, rid := range in.ReceiptIDs {
			var v decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT total_value FROM goods_receipts WHERE id=$1 AND invoice_id IS NULL`, rid).Scan(&v); err != nil {
				return httpx.Conflict("invalid_status", "A receipt is missing or already invoiced")
			}
			sub = sub.Add(v)
		}
		tax := money.Money(in.Tax)
		total := sub.Add(tax)
		number, err := sequence.Next(ctx, tx, *p.BranchID, "pinv")
		if err != nil {
			return err
		}
		id := ids.New()
		date := in.Date
		if date == "" {
			date = s.Clock.Now().Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_invoices (id, business_id, branch_id, number, supplier_ref, supplier_id, invoice_date, subtotal, tax_total, total, balance, created_by)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$10,$11)
		`, id, p.BusinessID, *p.BranchID, number, in.SupplierRef, in.SupplierID, date, sub, tax, total, p.UserID); err != nil {
			return err
		}
		for _, rid := range in.ReceiptIDs {
			_, _ = tx.Exec(ctx, `UPDATE goods_receipts SET invoice_id=$2 WHERE id=$1`, rid, id)
		}
		user := p.UserID
		lines := []ledger.Line{{Code: "2050", BranchID: p.BranchID, Debit: sub}, {Code: "2000", BranchID: p.BranchID, PartyType: "supplier", PartyID: &in.SupplierID, Credit: total}}
		if tax.IsPositive() {
			lines = append(lines, ledger.Line{Code: "1500", BranchID: p.BranchID, Debit: tax})
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "purchase_invoice", id, "Purchase invoice "+number, &user, lines); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "total": total, "balance": total}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) Pay(ctx context.Context, p *auth.Principal, supplierID uuid.UUID, amount decimal.Decimal, account uuid.UUID, method, ref string) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
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
		amt := money.Money(amount)
		if err := s.Ledger.EnsureFunds(ctx, tx, account, amt); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, balance FROM purchase_invoices WHERE supplier_id=$1 AND balance>0 AND status='posted' ORDER BY invoice_date`, supplierID)
		if err != nil {
			return err
		}
		type inv struct {
			id  uuid.UUID
			bal decimal.Decimal
		}
		var invs []inv
		for rows.Next() {
			var i inv
			if err := rows.Scan(&i.id, &i.bal); err != nil {
				rows.Close()
				return err
			}
			invs = append(invs, i)
		}
		rows.Close()
		date := s.Clock.Now().Format("2006-01-02")
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: *p.BranchID, MoneyAccountID: account, UserID: p.UserID,
			Direction: "out", Kind: "supplier_payment", Method: method, PartyType: "supplier", PartyID: &supplierID,
			Amount: amt, Reference: ref, Date: date,
		})
		if err != nil {
			return err
		}
		left := amt
		applied := money.Zero
		for _, i := range invs {
			if !left.IsPositive() {
				break
			}
			use := left
			if use.GreaterThan(i.bal) {
				use = i.bal
			}
			_, _ = tx.Exec(ctx, `UPDATE purchase_invoices SET paid_total=paid_total+$2, balance=balance-$2 WHERE id=$1`, i.id, use)
			_, _ = tx.Exec(ctx, `INSERT INTO payment_allocations (id, business_id, payment_id, document_type, document_id, amount) VALUES ($1,$2,$3,'purchase_invoice',$4,$5)`, ids.New(), p.BusinessID, res.ID, i.id, use)
			applied = applied.Add(use)
			left = left.Sub(use)
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, account)
		if err != nil {
			return err
		}
		user := p.UserID
		lines := []ledger.Line{{Code: code, BranchID: p.BranchID, MoneyAccountID: &account, Credit: amt}}
		if applied.IsPositive() {
			lines = append(lines, ledger.Line{Code: "2000", BranchID: p.BranchID, PartyType: "supplier", PartyID: &supplierID, Debit: applied})
		}
		if left.IsPositive() {
			lines = append(lines, ledger.Line{Code: "1400", BranchID: p.BranchID, PartyType: "supplier", PartyID: &supplierID, Debit: left})
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "supplier_payment", res.ID, "Supplier payment "+res.Number, &user, lines); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": res.ID, "number": res.Number, "amount": amt, "allocated": applied}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) GetPO(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var number, status string
		var total decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT number, status, total FROM purchase_orders WHERE id=$1`, id).Scan(&number, &status, &total); err != nil {
			return httpx.NotFound("not_found", "Purchase order not found")
		}
		rows, err := tx.Query(ctx, `SELECT product_id, qty_ordered, qty_received, unit_cost FROM purchase_order_items WHERE po_id=$1`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		var items []map[string]any
		for rows.Next() {
			var pid uuid.UUID
			var ordered, received, cost decimal.Decimal
			if err := rows.Scan(&pid, &ordered, &received, &cost); err != nil {
				return err
			}
			items = append(items, map[string]any{"product_id": pid, "qty_ordered": ordered, "qty_received": received, "qty_outstanding": ordered.Sub(received), "unit_cost": cost})
		}
		out = map[string]any{"id": id, "number": number, "status": status, "total": total, "items": items}
		return nil
	})
	return out, err
}
