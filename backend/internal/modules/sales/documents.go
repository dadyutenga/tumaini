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

func businessHeader(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (map[string]any, error) {
	var name, currency string
	var taxNo, regNo, phone, email, address *string
	if err := tx.QueryRow(ctx, `
		SELECT business_name::text, tin, registration_no, phone::text, email::text, address, currency::text
		FROM app.tenant_settings WHERE tenant_id = $1`, tenantID).
		Scan(&name, &taxNo, &regNo, &phone, &email, &address, &currency); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "legal_name": name, "tax_number": taxNo, "reg_number": regNo, "phone": phone, "email": email, "address": address, "currency": currency}, nil
}

// Receipt returns print data. The first print is the original; later prints are marked COPY.
func (s *Service) Receipt(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale.receipt_printed", "sale", id, nil, nil); err != nil {
			return err
		}
		var prints int
		_ = tx.QueryRow(ctx, `
			SELECT count(*) FROM audit.audit_log
			WHERE tenant_id = $1 AND entity_table = 'sale' AND entity_id = $2 AND action = 'sale.receipt_printed'`,
			p.TenantID, id).Scan(&prints)
		if prints < 1 {
			prints = 1
		}
		m["receipt_prints"] = prints
		m["copy"] = prints > 1
		if m["payments"], err = salePayments(ctx, tx, p.TenantID, id); err != nil {
			return err
		}
		if m["business"], err = businessHeader(ctx, tx, p.TenantID); err != nil {
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

// Invoice returns A4 invoice data for a sale, with customer details, due date and balance.
func (s *Service) Invoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, id)
		if err != nil {
			return err
		}
		// Shop sales carry a single immutable receipt_no; services invoices live in app.invoices.
		m["invoice_number"] = m["number"]
		if m["business"], err = businessHeader(ctx, tx, p.TenantID); err != nil {
			return err
		}
		if m["payments"], err = salePayments(ctx, tx, p.TenantID, id); err != nil {
			return err
		}
		var addr, email *string
		if cid, ok := m["customer_id"].(*uuid.UUID); ok && cid != nil {
			if err := tx.QueryRow(ctx, `SELECT address, email::text FROM app.customers WHERE tenant_id = $1 AND id = $2`, p.TenantID, *cid).Scan(&addr, &email); err != nil && err != pgx.ErrNoRows {
				return err
			}
		}
		m["customer"] = map[string]any{"id": m["customer_id"], "name": m["customer_name"], "phone": m["customer_phone"], "address": addr, "email": email}
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

// ReceiptPDF renders a narrow thermal shop receipt (58/80mm, warranty, COPY watermark).
func ReceiptPDF(m map[string]any) []byte {
	paper := 80
	if cfg, ok := m["settings"].(map[string]any); ok {
		if pw, ok := cfg["paper_width"].(int); ok && pw > 0 {
			paper = pw
		}
	}
	d := pdf.NewThermal(paper)
	biz, _ := m["business"].(map[string]any)
	d.Heading(str(biz["name"]), 11)
	if v := str(biz["address"]); v != "" {
		d.Text(v)
	}
	info := str(biz["phone"])
	if tin := str(biz["tax_number"]); tin != "" {
		if info != "" {
			info += " | "
		}
		info += "TIN: " + tin
	}
	if info != "" {
		d.Text(info)
	}
	if copyFlag, _ := m["copy"].(bool); copyFlag {
		d.Heading("*** COPY ***", 10)
	}
	d.Rule()
	d.KV("Receipt No", str(m["number"]))
	d.KV("Date", str(m["business_date"]))
	if v := str(m["cashier_name"]); v != "" {
		d.KV("Cashier", v)
	}
	if v := str(m["customer_name"]); v != "" {
		d.KV("Customer", v)
	} else {
		d.KV("Customer", "Walk-in")
	}
	d.Rule()
	var rows [][]string
	var warranties []string
	items, _ := m["items"].([]map[string]any)
	for _, it := range items {
		name := str(it["product_name"])
		if sn := str(it["serial_number"]); sn != "" {
			name += " [SN:" + sn + "]"
		}
		rows = append(rows, []string{name, str(it["qty"]), amountText(it["line_total"])})
		if w, ok := it["warranty_months"].(int); ok && w > 0 {
			warranties = append(warranties, fmt.Sprintf("%d months (%s)", w, str(it["product_name"])))
		}
	}
	d.Table([]string{"Item", "Qty", "Total"}, []float64{4, 1, 2}, rows)
	d.Rule()
	d.KV("Subtotal", amountText(m["subtotal"]))
	if disc, ok := m["discount_total"].(decimal.Decimal); ok && disc.IsPositive() {
		d.KV("Discount", amountText(disc))
	}
	d.KV("TOTAL", amountText(m["total"]))
	d.KV("Paid", amountText(m["paid_total"]))
	if chg, ok := m["change_due"].(decimal.Decimal); ok && chg.IsPositive() {
		d.KV("Change", amountText(chg))
	}
	if bal, ok := m["balance"].(decimal.Decimal); ok && bal.IsPositive() {
		d.KV("Balance", amountText(bal))
		if due := str(m["due_date"]); due != "" {
			d.KV("Due date", due)
		}
	}
	if len(warranties) > 0 {
		d.Rule()
		for _, w := range warranties {
			d.Text("Warranty: " + w)
		}
	}
	if f := str(m["receipt_footer"]); f != "" {
		d.Space(4)
		d.Text(f)
	} else {
		d.Space(4)
		d.Text("Thank you for your business")
	}
	return d.Bytes()
}

// QuotationPDF renders a quotation as an A4 PDF.
func QuotationPDF(m map[string]any) []byte {
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
	d.Heading("QUOTATION "+str(m["number"]), 13)
	d.KV("Date", str(m["business_date"]))
	if v := str(m["valid_until"]); v != "" {
		d.KV("Valid until", v)
	}
	d.KV("Customer", str(m["customer_name"]))
	d.Space(8)
	var rows [][]string
	items, _ := m["items"].([]map[string]any)
	for _, it := range items {
		rows = append(rows, []string{str(it["product_name"]), str(it["qty"]), amountText(it["unit_price"]), amountText(it["discount"]), amountText(it["line_total"])})
	}
	d.Table([]string{"Item", "Qty", "Unit price", "Discount", "Total"}, []float64{4.5, 1, 1.5, 1.5, 1.5}, rows)
	d.Rule()
	for _, kv := range [][2]string{{"Subtotal", "subtotal"}, {"Discount", "discount_total"}, {"Tax", "tax_total"}, {"Total", "total"}} {
		d.KV(kv[0], amountText(m[kv[1]]))
	}
	if n := str(m["note"]); n != "" {
		d.Space(8)
		d.Text(n)
	}
	d.Space(12)
	d.Text("Accepted by: ____________________________   Date: ______________")
	return d.Bytes()
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
	d.Heading("INVOICE "+str(m["invoice_number"]), 13)
	d.KV("Sale", str(m["number"]))
	d.KV("Date", str(m["business_date"]))
	if v := str(m["due_date"]); v != "" {
		d.KV("Due date", v)
	}
	cust, _ := m["customer"].(map[string]any)
	d.KV("Customer", str(cust["name"]))
	for _, k := range []string{"phone", "address"} {
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
