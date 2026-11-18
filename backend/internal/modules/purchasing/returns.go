package purchasing

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
)

type ReturnItem struct {
	ReceiptItemID uuid.UUID       `json:"receipt_item_id" validate:"required"`
	Qty           decimal.Decimal `json:"qty" validate:"dpos"`
}

type ReturnIn struct {
	ReceiptID      uuid.UUID    `json:"receipt_id" validate:"required"`
	Reason         string       `json:"reason" validate:"required,max=500"`
	Settlement     string       `json:"settlement" validate:"oneof=reduce_debt refund"`
	MoneyAccountID *uuid.UUID   `json:"money_account_id"`
	Method         string       `json:"method" validate:"omitempty,oneof=cash bank mobile_money"`
	Reference      string       `json:"reference" validate:"max=100"`
	Items          []ReturnItem `json:"items" validate:"min=1,max=500,dive"`
}

// CreateReturn returns received goods to the supplier (FR-PUR-11). Stock
// leaves at the cost it was received at. For an invoiced receipt the supplier
// balance is reduced (2000) or a refund is received into a money account, and
// the matching share of input tax is reversed. For a receipt not yet invoiced
// the goods-received-not-invoiced liability (2050) is reduced instead.
func (s *Service) CreateReturn(ctx context.Context, p *auth.Principal, in ReturnIn) (int, []byte, error) {
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	if in.Settlement == "refund" && in.MoneyAccountID == nil {
		return 0, nil, httpx.Validation(errors.New("money_account_id"))
	}
	return s.idemRun(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var branch, supplier uuid.UUID
		var invoice *uuid.UUID
		var grnNumber string
		err := tx.QueryRow(ctx, `SELECT branch_id, supplier_id, invoice_id, number FROM goods_receipts WHERE id=$1 FOR UPDATE`, in.ReceiptID).Scan(&branch, &supplier, &invoice, &grnNumber)
		if err != nil || !p.SeesBranch("purchase_returns.create", branch) {
			return 0, nil, httpx.NotFound("not_found", "Goods receipt not found")
		}
		if invoice == nil && in.Settlement == "refund" {
			return 0, nil, httpx.Conflict("invalid_status", "A refund needs an invoiced receipt. Use reduce_debt for goods not yet invoiced")
		}
		type line struct {
			item, product uuid.UUID
			qty, cost     decimal.Decimal
		}
		var lines []line
		value := money.Zero
		seen := map[uuid.UUID]bool{}
		for _, it := range in.Items {
			if seen[it.ReceiptItemID] {
				return 0, nil, httpx.BadRequest("invalid_body", "Each receipt line may appear only once")
			}
			seen[it.ReceiptItemID] = true
			var product uuid.UUID
			var received, returned, cost decimal.Decimal
			if err := tx.QueryRow(ctx, `
				SELECT product_id, qty_received, qty_returned, unit_cost FROM goods_receipt_items WHERE id=$1 AND receipt_id=$2 FOR UPDATE
			`, it.ReceiptItemID, in.ReceiptID).Scan(&product, &received, &returned, &cost); err != nil {
				return 0, nil, httpx.NotFound("not_found", "Receipt line not found")
			}
			qty := money.Qty(it.Qty)
			if returned.Add(qty).GreaterThan(received) {
				return 0, nil, httpx.Conflict("invalid_status", "You cannot return more than was received")
			}
			lines = append(lines, line{it.ReceiptItemID, product, qty, cost})
			value = value.Add(money.Money(qty.Mul(cost)))
		}
		tax := money.Zero
		if invoice != nil {
			var sub, invTax decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT subtotal, tax_total FROM purchase_invoices WHERE id=$1 FOR UPDATE`, *invoice).Scan(&sub, &invTax); err != nil {
				return 0, nil, err
			}
			if sub.IsPositive() && invTax.IsPositive() {
				tax = money.Money(invTax.Mul(value).Div(sub))
			}
		}
		total := value.Add(tax)
		number, err := sequence.Next(ctx, tx, branch, "preturn")
		if err != nil {
			return 0, nil, err
		}
		id := ids.New()
		var account any
		if in.Settlement == "refund" {
			account = *in.MoneyAccountID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_returns (id, business_id, branch_id, number, supplier_id, receipt_id, invoice_id, reason, total, settlement, money_account_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		`, id, p.BusinessID, branch, number, supplier, in.ReceiptID, invoice, in.Reason, total, in.Settlement, account, p.UserID); err != nil {
			return 0, nil, err
		}
		stockLines := make([]stock.Line, 0, len(lines))
		pids := make([]uuid.UUID, 0, len(lines))
		for _, ln := range lines {
			if _, err := tx.Exec(ctx, `UPDATE goods_receipt_items SET qty_returned=qty_returned+$2 WHERE id=$1`, ln.item, ln.qty); err != nil {
				return 0, nil, err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO purchase_return_items (id, business_id, return_id, receipt_item_id, product_id, qty, unit_cost) VALUES ($1,$2,$3,$4,$5,$6,$7)
			`, ids.New(), p.BusinessID, id, ln.item, ln.product, ln.qty, ln.cost); err != nil {
				return 0, nil, err
			}
			stockLines = append(stockLines, stock.Line{ProductID: ln.product, Qty: ln.qty, UnitCost: ln.cost})
			pids = append(pids, ln.product)
		}
		user := p.UserID
		if err := s.Stock.DeductAtCost(ctx, tx, p.BusinessID, branch, &user, "purchase_return", "purchase_return", id, stockLines); err != nil {
			return 0, nil, err
		}
		date := s.bizDate(ctx, tx)
		jl := []ledger.Line{{Code: "1200", BranchID: &branch, Credit: value}}
		if tax.IsPositive() {
			jl = append(jl, ledger.Line{Code: "1500", BranchID: &branch, Credit: tax})
		}
		out := map[string]any{"id": id, "number": number, "receipt_id": in.ReceiptID, "invoice_id": invoice, "supplier_id": supplier, "settlement": in.Settlement, "value": value, "tax": tax, "total": total}
		switch {
		case invoice == nil:
			jl = append(jl, ledger.Line{Code: "2050", BranchID: &branch, PartyType: "supplier", PartyID: &supplier, Debit: total})
		case in.Settlement == "reduce_debt":
			jl = append(jl, ledger.Line{Code: "2000", BranchID: &branch, PartyType: "supplier", PartyID: &supplier, Debit: total})
			// Reduce the invoice balance; anything beyond it stays as a debit the supplier owes back.
			if _, err := tx.Exec(ctx, `UPDATE purchase_invoices SET balance = GREATEST(balance - $2, 0) WHERE id=$1`, *invoice, total); err != nil {
				return 0, nil, err
			}
		default:
			code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, *in.MoneyAccountID)
			if err != nil {
				return 0, nil, httpx.NotFound("not_found", "Money account not found")
			}
			method := in.Method
			if method == "" {
				method = "cash"
			}
			res, err := payments.Insert(ctx, tx, payments.Input{
				BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: *in.MoneyAccountID, UserID: p.UserID,
				Direction: "in", Kind: "supplier_refund", Method: method, PartyType: "supplier", PartyID: &supplier,
				Amount: total, Reference: in.Reference, Date: date, Note: "Return " + number,
			})
			if err != nil {
				return 0, nil, err
			}
			jl = append(jl, ledger.Line{Code: code, BranchID: &branch, MoneyAccountID: in.MoneyAccountID, PartyType: "supplier", PartyID: &supplier, Debit: total})
			out["payment_id"] = res.ID
		}
		if total.IsPositive() {
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "purchase_return", id, "Purchase return "+number+" ("+grnNumber+")", &user, jl); err != nil {
				return 0, nil, err
			}
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
			return 0, nil, err
		}
		if err := s.Audit.Record(ctx, tx, p, "purchase_return.posted", "purchase_return", id, nil, out); err != nil {
			return 0, nil, err
		}
		return 201, out, nil
	})
}
