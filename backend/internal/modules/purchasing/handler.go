package purchasing

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
	"bms/internal/platform/pdf"
)

type Handler struct{ Svc *Service }

func docFilter(c fiber.Ctx) (DocFilter, error) {
	from, to, err := httpx.Range(c)
	if err != nil {
		return DocFilter{}, err
	}
	sup, err := httpx.QueryUUID(c, "supplier_id")
	if err != nil {
		return DocFilter{}, err
	}
	po, err := httpx.QueryUUID(c, "po_id")
	if err != nil {
		return DocFilter{}, err
	}
	unpaid := httpx.QueryBool(c, "unpaid")
	return DocFilter{Status: c.Query("status"), SupplierID: sup, POID: po, From: from, To: to, Unpaid: unpaid != nil && *unpaid}, nil
}

// ---- Suppliers ----

func (h *Handler) ListSuppliers(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.ListSuppliers(c.Context(), middleware.Principal(c), c.Query("q"), httpx.QueryBool(c, "is_active"), page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) CreateSupplier(c fiber.Ctx) error {
	var body SupplierIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreateSupplier(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) GetSupplier(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetSupplier(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) UpdateSupplier(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body SupplierIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.UpdateSupplier(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, out, err)
}

func (h *Handler) DeactivateSupplier(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.DeactivateSupplier(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) SupplierPurchases(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.SupplierPurchases(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Unpaid(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Unpaid(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Statement(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	out, err := h.Svc.Statement(c.Context(), middleware.Principal(c), id, from, to)
	if err != nil {
		return err
	}
	if c.Query("format") != "pdf" {
		return c.JSON(httpx.Data(out))
	}
	sup := out["supplier"].(map[string]any)
	d := pdf.New()
	d.Heading("Supplier statement", 14)
	d.KV("Supplier", sup["name"].(string))
	d.KV("Period", from+" to "+to)
	d.KV("Opening balance", export.Cell(out["opening_balance"]))
	d.Space(6)
	var rows [][]string
	for _, ln := range out["lines"].([]map[string]any) {
		rows = append(rows, []string{export.Cell(ln["date"]), export.Cell(ln["description"]), export.Cell(ln["debit"]), export.Cell(ln["credit"]), export.Cell(ln["balance"])})
	}
	d.Table([]string{"Date", "Description", "Paid", "Purchased", "Balance"}, []float64{1.5, 4, 1.5, 1.5, 1.5}, rows)
	d.Rule()
	d.KV("Closing balance", export.Cell(out["closing_balance"]))
	return export.File(c, "supplier-statement.pdf", "application/pdf", d.Bytes())
}

func (h *Handler) Debts(c fiber.Ctx) error {
	out, err := h.Svc.Debts(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body PaymentIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Pay(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

// ---- Purchase orders ----

func (h *Handler) ListPOs(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	sup, err := httpx.QueryUUID(c, "supplier_id")
	if err != nil {
		return err
	}
	prod, err := httpx.QueryUUID(c, "product_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.ListPOs(c.Context(), middleware.Principal(c), POFilter{Status: c.Query("status"), SupplierID: sup, ProductID: prod, From: from, To: to, Q: c.Query("q")}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) CreatePO(c fiber.Ctx) error {
	var body POIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreatePO(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) GetPO(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetPO(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) UpdatePO(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body POIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.UpdatePO(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, out, err)
}

func (h *Handler) Submit(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, out, err := h.Svc.Submit(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(httpx.Data(out))
}

func (h *Handler) CancelPO(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.CancelPO(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) ClosePO(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.ClosePO(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) POPDF(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	name, raw, err := h.Svc.POPDF(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return export.File(c, name, "application/pdf", raw)
}

func (h *Handler) Suggest(c fiber.Ctx) error {
	sup, err := httpx.QueryUUID(c, "supplier_id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Suggest(c.Context(), middleware.Principal(c), sup)
	return httpx.OK(c, out, err)
}

// ---- Receipts, invoices, returns ----

func (h *Handler) ListGRNs(c fiber.Ctx) error {
	f, err := docFilter(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.ListGRNs(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetGRN(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetGRN(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Receive(c fiber.Ctx) error {
	var body ReceiptIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Receive(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) ListInvoices(c fiber.Ctx) error {
	f, err := docFilter(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.ListInvoices(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetInvoice(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetInvoice(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Invoice(c fiber.Ctx) error {
	var body InvoiceIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Invoice(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) VoidInvoice(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason" validate:"max=500"`
	}
	if err := httpx.BindOptional(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.VoidInvoice(c.Context(), middleware.Principal(c), id, body.Reason)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) ListReturns(c fiber.Ctx) error {
	f, err := docFilter(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.ListReturns(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetReturn(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetReturn(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateReturn(c fiber.Ctx) error {
	var body ReturnIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.CreateReturn(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func Register(r fiber.Router, h *Handler) {
	canViewSup := middleware.RequireAny("shop.supplier.manage", "shop.purchase.manage", "finance.debt.view", "finance.payment.make")
	r.Get("/suppliers", canViewSup, h.ListSuppliers)
	r.Post("/suppliers", middleware.Require("shop.supplier.manage"), h.CreateSupplier)
	r.Get("/suppliers/debts", middleware.RequireAny("finance.debt.view", "shop.supplier.manage", "shop.purchase.manage"), h.Debts)
	r.Get("/suppliers/:id", canViewSup, h.GetSupplier)
	r.Put("/suppliers/:id", middleware.Require("shop.supplier.manage"), h.UpdateSupplier)
	r.Post("/suppliers/:id/deactivate", middleware.Require("shop.supplier.manage"), h.DeactivateSupplier)
	r.Get("/suppliers/:id/purchases", middleware.RequireAny("shop.purchase.manage", "shop.supplier.manage"), h.SupplierPurchases)
	r.Get("/suppliers/:id/unpaid", canViewSup, h.Unpaid)
	r.Get("/suppliers/:id/statement", canViewSup, h.Statement)
	r.Post("/suppliers/:id/payments", middleware.Require("finance.payment.make"), middleware.Idempotent(), h.Pay)

	r.Get("/purchase-orders", middleware.Require("shop.purchase.manage"), h.ListPOs)
	r.Post("/purchase-orders", middleware.Require("shop.purchase.manage"), h.CreatePO)
	r.Get("/purchase-orders/suggest", middleware.Require("shop.purchase.manage"), h.Suggest)
	r.Get("/purchase-orders/:id", middleware.Require("shop.purchase.manage"), h.GetPO)
	r.Put("/purchase-orders/:id", middleware.Require("shop.purchase.manage"), h.UpdatePO)
	r.Post("/purchase-orders/:id/submit", middleware.Require("shop.purchase.manage"), h.Submit)
	r.Post("/purchase-orders/:id/cancel", middleware.Require("shop.purchase.manage"), h.CancelPO)
	r.Post("/purchase-orders/:id/close", middleware.Require("shop.purchase.manage"), h.ClosePO)
	r.Get("/purchase-orders/:id/pdf", middleware.Require("shop.purchase.manage"), h.POPDF)

	r.Get("/goods-receipts", middleware.Require("shop.purchase.manage"), h.ListGRNs)
	r.Post("/goods-receipts", middleware.Require("shop.purchase.manage"), middleware.Idempotent(), h.Receive)
	r.Get("/goods-receipts/:id", middleware.Require("shop.purchase.manage"), h.GetGRN)

	r.Get("/purchase-invoices", middleware.Require("shop.purchase.manage"), h.ListInvoices)
	r.Post("/purchase-invoices", middleware.Require("shop.purchase.manage"), h.Invoice)
	r.Get("/purchase-invoices/:id", middleware.Require("shop.purchase.manage"), h.GetInvoice)
	r.Post("/purchase-invoices/:id/void", middleware.Require("shop.purchase.manage"), h.VoidInvoice)

	r.Get("/purchase-returns", middleware.Require("shop.purchase.manage"), h.ListReturns)
	r.Post("/purchase-returns", middleware.Require("shop.purchase.manage"), h.CreateReturn)
	r.Get("/purchase-returns/:id", middleware.Require("shop.purchase.manage"), h.GetReturn)
}
