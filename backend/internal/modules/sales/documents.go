package sales

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/pdf"
	"bms/internal/platform/setting"
)

func businessHeader(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
	var name string
	var legal, taxNo, regNo, phone, email, address *string
	var currency string
	if err := tx.QueryRow(ctx, `SELECT name, legal_name, tax_number, reg_number, phone, email::text, address, currency FROM businesses LIMIT 1`).
		Scan(&name, &legal, &taxNo, &regNo, &phone, &email, &address, &currency); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "legal_name": legal, "tax_number": taxNo, "reg_number": regNo, "phone": phone, "email": email, "address": address, "currency": currency}, nil
}

// Receipt returns print data. The first print is the original; later prints are marked COPY (FR-SAL-11).
func (s *Service) Receipt(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, "sales.view", id, "sale")
		if err != nil {
			return err
		}
		var prints int
		if err := tx.QueryRow(ctx, `UPDATE sales SET receipt_prints = receipt_prints + 1 WHERE id=$1 RETURNING receipt_prints`, id).Scan(&prints); err != nil {
			return err
		}
		m["receipt_prints"] = prints
		m["copy"] = prints > 1
		if m["payments"], err = salePayments(ctx, tx, id); err != nil {
			return err
		}
		if m["business"], err = businessHeader(ctx, tx); err != nil {
			return err
		}
		m["settings"] = map[string]any{
			"header":       setting.String(ctx, tx, "receipt.header", ""),
			"show_logo":    setting.Bool(ctx, tx, "receipt.show_logo", true),
			"paper_width":  setting.Int(ctx, tx, "receipt.paper_width", 80),
			"show_cashier": setting.Bool(ctx, tx, "receipt.show_cashier", true),
			"show_tax":     setting.Bool(ctx, tx, "receipt.show_tax", true),
		}
		out = m
		return nil
	})
	return out, err
}

// Invoice returns A4 invoice data for a sale, with customer details, due date and balance (FR-SAL-12).
func (s *Service) Invoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, "sales.view", id, "sale")
		if err != nil {
			return err
		}
		if m["business"], err = businessHeader(ctx, tx); err != nil {
			return err
		}
		if m["payments"], err = salePayments(ctx, tx, id); err != nil {
			return err
		}
		var addr, email, taxNo *string
		if err := tx.QueryRow(ctx, `SELECT address, email::text, tax_number FROM customers WHERE id=$1`, m["customer_id"]).Scan(&addr, &email, &taxNo); err != nil && err != pgx.ErrNoRows {
			return err
		}
		m["customer"] = map[string]any{"id": m["customer_id"], "name": m["customer_name"], "phone": m["customer_phone"], "address": addr, "email": email, "tax_number": taxNo}
		m["terms"] = setting.String(ctx, tx, "invoice.terms", "")
		m["footer"] = setting.String(ctx, tx, "invoice.footer", "")
		out = m
		return nil
	})
	return out, err
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case *string:
		if t == nil {
			return ""
		}
		return *t
	default:
		return export.Cell(v)
	}
}

// InvoicePDF renders the invoice data as an A4 PDF.
func InvoicePDF(m map[string]any) []byte {
	d := pdf.New()
	biz, _ := m["business"].(map[string]any)
	d.Heading(str(biz["name"]), 16)
	for _, k := range []string{"address", "phone", "email"} {
		if v := str(biz[k]); v != "" {
			d.Text(v)
		}
	}
	if v := str(biz["tax_number"]); v != "" {
		d.Text("TIN: " + v)
	}
	d.Space(8)
	d.Heading("INVOICE "+str(m["number"]), 13)
	d.KV("Date", str(m["business_date"]))
	if v := str(m["due_date"]); v != "" {
		d.KV("Due date", v)
	}
	cust, _ := m["customer"].(map[string]any)
	d.KV("Customer", str(cust["name"]))
	for _, k := range []string{"phone", "address", "tax_number"} {
		if v := str(cust[k]); v != "" {
			d.KV("", v)
		}
	}
	d.Space(8)
	var rows [][]string
	items, _ := m["items"].([]map[string]any)
	for _, it := range items {
		rows = append(rows, []string{str(it["product_name"]), str(it["qty"]), str(it["unit_price"]), str(it["discount"]), str(it["tax_amount"]), str(it["line_total"])})
	}
	d.Table([]string{"Item", "Qty", "Unit price", "Discount", "Tax", "Total"}, []float64{4, 1, 1.5, 1.5, 1.5, 1.5}, rows)
	d.Rule()
	for _, kv := range [][2]string{{"Subtotal", "subtotal"}, {"Discount", "discount_total"}, {"Tax", "tax_total"}, {"Total", "total"}, {"Paid", "paid_total"}, {"Balance due", "balance"}} {
		d.KV(kv[0], amountText(m[kv[1]]))
	}
	if t := str(m["terms"]); t != "" {
		d.Space(8)
		d.Text(t)
	}
	if f := str(m["footer"]); f != "" {
		d.Text(f)
	}
	return d.Bytes()
}

func amountText(v any) string {
	if d, ok := v.(decimal.Decimal); ok {
		return d.StringFixed(2)
	}
	return fmt.Sprint(v)
}
