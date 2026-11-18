package purchasing

import (
	"bms/internal/platform/readapi"
	"github.com/gofiber/fiber/v3"
)

func registerReads(r fiber.Router, h *Handler) {
	readapi.Register(r, h.Svc.DB,
		readapi.Resource{Path: "/suppliers", Permission: "suppliers.view", Select: "t.id,t.name,t.contact_person,t.phone,t.email::text,t.address,t.tax_number,t.payment_terms_days,t.is_active,t.created_at", From: "suppliers t", Order: "t.name,t.id", Search: "t.name || ' ' || COALESCE(t.phone,'') || ' ' || COALESCE(t.email::text,'')", Filters: map[string]readapi.Filter{"is_active": {Column: "t.is_active", Kind: "bool"}}, Detail: true},
		readapi.Resource{Path: "/purchase-orders", Permission: "po.view", Select: "t.*,s.name AS supplier_name", From: "purchase_orders t JOIN suppliers s ON s.id=t.supplier_id", Order: "t.order_date DESC,t.id", Search: "t.number || ' ' || s.name", Branch: "t.branch_id", Date: "t.order_date", Filters: map[string]readapi.Filter{"supplier_id": {Column: "t.supplier_id", Kind: "uuid"}, "status": {Column: "t.status"}}, Detail: true, Children: []readapi.Child{{Name: "items", SQL: "SELECT i.*,p.name AS product_name FROM purchase_order_items i JOIN products p ON p.id=i.product_id WHERE i.po_id=$1 ORDER BY i.id"}}},
		readapi.Resource{Path: "/goods-receipts", Permission: "po.view", Select: "t.*,s.name AS supplier_name", From: "goods_receipts t JOIN suppliers s ON s.id=t.supplier_id", Order: "t.received_date DESC,t.id", Search: "t.number || ' ' || s.name", Branch: "t.branch_id", Date: "t.received_date", Filters: map[string]readapi.Filter{"supplier_id": {Column: "t.supplier_id", Kind: "uuid"}, "po_id": {Column: "t.po_id", Kind: "uuid"}}, Detail: true, Children: []readapi.Child{{Name: "items", SQL: "SELECT i.*,p.name AS product_name FROM goods_receipt_items i JOIN products p ON p.id=i.product_id WHERE i.receipt_id=$1 ORDER BY i.id"}}},
		readapi.Resource{Path: "/purchase-invoices", Permission: "po.view", Select: "t.*,s.name AS supplier_name", From: "purchase_invoices t JOIN suppliers s ON s.id=t.supplier_id", Order: "t.invoice_date DESC,t.id", Search: "t.number || ' ' || s.name", Branch: "t.branch_id", Date: "t.invoice_date", Filters: map[string]readapi.Filter{"supplier_id": {Column: "t.supplier_id", Kind: "uuid"}, "status": {Column: "t.status"}}, Detail: true, Children: []readapi.Child{{Name: "receipts", SQL: "SELECT id,number,total_value FROM goods_receipts WHERE invoice_id=$1 ORDER BY id"}}},
		readapi.Resource{Path: "/purchase-returns", Permission: "po.view", Select: "t.*", From: "purchase_returns t", Order: "t.created_at DESC,t.id", Search: "t.number", Branch: "t.branch_id", Date: "t.created_at", Filters: map[string]readapi.Filter{"supplier_id": {Column: "t.supplier_id", Kind: "uuid"}}, Detail: true, Children: []readapi.Child{{Name: "items", SQL: "SELECT * FROM purchase_return_items WHERE return_id=$1 ORDER BY id"}}},
	)
}
