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
	d.Table([]string{"Date", "Description", "Paid / returned", "Invoiced", "Balance"}, []float64{1.5, 4, 1.5, 1.5, 1.5}, rows)
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
	// Static paths are registered before /:id so they are not taken as ids.
	r.Get("/suppliers", middleware.Require("suppliers.view"), h.ListSuppliers)
	r.Post("/suppliers", middleware.Require("suppliers.manage"), h.CreateSupplier)
	r.Get("/suppliers/debts", middleware.Require("suppliers.view"), h.Debts)
	r.Get("/suppliers/:id", middleware.Require("suppliers.view"), h.GetSupplier)
	r.Put("/suppliers/:id", middleware.Require("suppliers.manage"), h.UpdateSupplier)
	r.Post("/suppliers/:id/deactivate", middleware.Require("suppliers.manage"), h.DeactivateSupplier)
	r.Get("/suppliers/:id/purchases", middleware.Require("po.view"), h.SupplierPurchases)
	r.Get("/suppliers/:id/unpaid", middleware.Require("suppliers.view"), h.Unpaid)
	r.Get("/suppliers/:id/statement", middleware.Require("suppliers.view"), h.Statement)
	r.Post("/suppliers/:id/payments", middleware.Require("payments.pay"), middleware.Idempotent(), h.Pay)

	r.Get("/purchase-orders", middleware.Require("po.view"), h.ListPOs)
	r.Post("/purchase-orders", middleware.Require("po.create"), h.CreatePO)
	r.Get("/purchase-orders/suggest", middleware.Require("po.create"), h.Suggest)
	r.Get("/purchase-orders/:id", middleware.Require("po.view"), h.GetPO)
	r.Put("/purchase-orders/:id", middleware.Require("po.create"), h.UpdatePO)
	r.Post("/purchase-orders/:id/submit", middleware.Require("po.create"), h.Submit)
	r.Post("/purchase-orders/:id/cancel", middleware.Require("po.create"), h.CancelPO)
	r.Post("/purchase-orders/:id/close", middleware.Require("po.approve"), h.ClosePO)
	r.Get("/purchase-orders/:id/pdf", middleware.Require("po.view"), h.POPDF)

	r.Get("/goods-receipts", middleware.Require("po.view"), h.ListGRNs)
	r.Post("/goods-receipts", middleware.Require("grn.create"), middleware.Idempotent(), h.Receive)
	r.Get("/goods-receipts/:id", middleware.Require("po.view"), h.GetGRN)

	r.Get("/purchase-invoices", middleware.Require("po.view"), h.ListInvoices)
	r.Post("/purchase-invoices", middleware.Require("purchase_invoices.manage"), middleware.Idempotent(), h.Invoice)
	r.Get("/purchase-invoices/:id", middleware.Require("po.view"), h.GetInvoice)
	r.Post("/purchase-invoices/:id/void", middleware.Require("purchase_invoices.manage"), middleware.Idempotent(), h.VoidInvoice)

	r.Get("/purchase-returns", middleware.Require("po.view"), h.ListReturns)
	r.Post("/purchase-returns", middleware.Require("purchase_returns.create"), middleware.Idempotent(), h.CreateReturn)
	r.Get("/purchase-returns/:id", middleware.Require("po.view"), h.GetReturn)
}
