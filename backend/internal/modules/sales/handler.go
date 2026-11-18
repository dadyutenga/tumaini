package sales

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Preview(c fiber.Ctx) error {
	var body CreateIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.Preview(c.Context(), middleware.Principal(c), body)
	return httpx.OK(c, out, err)
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body CreateIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) List(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	cust, err := httpx.QueryUUID(c, "customer_id")
	if err != nil {
		return err
	}
	cashier, err := httpx.QueryUUID(c, "cashier_id")
	if err != nil {
		return err
	}
	f := ListFilter{From: from, To: to, Status: c.Query("status"), CustomerID: cust, CashierID: cashier, Method: c.Query("method"), Q: c.Query("q"), Sort: c.Query("sort")}
	list, total, err := h.Svc.List(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	sale, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, sale, err)
}

func (h *Handler) Receipt(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Receipt(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Invoice(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Invoice(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	switch strings.ToLower(c.Query("format")) {
	case "", "json":
		return c.JSON(httpx.Data(out))
	case "pdf":
		return export.File(c, "invoice-"+str(out["number"])+".pdf", "application/pdf", InvoicePDF(out))
	default:
		return httpx.BadRequest("invalid_query", "format must be json or pdf")
	}
}

func (h *Handler) Void(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason" validate:"required"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Void(c.Context(), middleware.Principal(c), id, body.Reason)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body SalePayIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.ReceivePayment(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Return(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ReturnIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.CreateReturn(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) SaleReturns(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	p := middleware.Principal(c)
	if _, err := h.Svc.Get(c.Context(), p, id); err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.Returns(c.Context(), p, ReturnFilter{SaleID: &id}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) Returns(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	sale, err := httpx.QueryUUID(c, "sale_id")
	if err != nil {
		return err
	}
	list, total, err := h.Svc.Returns(c.Context(), middleware.Principal(c), ReturnFilter{SaleID: sale, Status: c.Query("status"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetReturn(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Return(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Hold(c fiber.Ctx) error {
	var body CreateIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.Hold(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) Held(c fiber.Ctx) error {
	out, err := h.Svc.Held(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) DiscardHeld(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DiscardHeld(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) Quotations(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	cust, err := httpx.QueryUUID(c, "customer_id")
	if err != nil {
		return err
	}
	list, total, err := h.Svc.Quotations(c.Context(), middleware.Principal(c), QuoteFilter{Status: c.Query("status"), CustomerID: cust, Q: c.Query("q"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) CreateQuotation(c fiber.Ctx) error {
	var body QuoteIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreateQuotation(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) GetQuotation(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Quotation(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) UpdateQuotation(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body QuoteIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.UpdateQuotation(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, out, err)
}

func (h *Handler) ConvertQuotation(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ConvertIn
	if err := httpx.BindOptional(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.ConvertQuotation(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) OpenTill(c fiber.Ctx) error {
	var body struct {
		MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
		OpeningFloat   decimal.Decimal `json:"opening_float" validate:"dgte0"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.OpenTill(c.Context(), middleware.Principal(c), body.MoneyAccountID, body.OpeningFloat)
	return httpx.Created(c, out, err)
}

func (h *Handler) CurrentTill(c fiber.Ctx) error {
	out, err := h.Svc.CurrentTill(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) CloseTill(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Counted decimal.Decimal `json:"counted_cash" validate:"dgte0"`
		Note    string          `json:"note"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CloseTill(c.Context(), middleware.Principal(c), id, body.Counted, body.Note)
	return httpx.OK(c, out, err)
}

func (h *Handler) Tills(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	cashier, err := httpx.QueryUUID(c, "cashier_id")
	if err != nil {
		return err
	}
	list, total, err := h.Svc.Tills(c.Context(), middleware.Principal(c), TillFilter{Status: c.Query("status"), CashierID: cashier, From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) Till(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Till(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) PayOut(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body PayOutIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.PayOut(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/tills/current", middleware.Require("tills.open_close"), h.CurrentTill)
	r.Post("/tills/open", middleware.Require("tills.open_close"), h.OpenTill)
	r.Get("/tills", middleware.Require("tills.view"), h.Tills)
	r.Get("/tills/:id", middleware.Require("tills.view"), h.Till)
	r.Post("/tills/:id/close", middleware.Require("tills.open_close"), h.CloseTill)
	r.Post("/tills/:id/pay-out", middleware.Require("tills.open_close"), middleware.Idempotent(), h.PayOut)

	r.Post("/sales/preview", middleware.Require("sales.create"), h.Preview)
	r.Post("/sales/hold", middleware.Require("sales.create"), h.Hold)
	r.Get("/sales/held", middleware.Require("sales.create"), h.Held)
	r.Delete("/sales/held/:id", middleware.Require("sales.create"), h.DiscardHeld)
	r.Post("/sales", middleware.Require("sales.create"), middleware.Idempotent(), h.Create)
	r.Get("/sales", middleware.Require("sales.view"), h.List)
	r.Get("/sales/:id", middleware.Require("sales.view"), h.Get)
	r.Get("/sales/:id/receipt", middleware.Require("sales.view"), h.Receipt)
	r.Get("/sales/:id/invoice", middleware.Require("sales.view"), h.Invoice)
	r.Post("/sales/:id/void", middleware.Require("sales.void"), middleware.Idempotent(), h.Void)
	r.Post("/sales/:id/payments", middleware.Require("payments.receive"), middleware.Idempotent(), h.Pay)
	r.Get("/sales/:id/returns", middleware.Require("sales.view"), h.SaleReturns)
	r.Post("/sales/:id/returns", middleware.Require("sales.refund_request"), middleware.Idempotent(), h.Return)
	r.Get("/sale-returns", middleware.Require("sales.view"), h.Returns)
	r.Get("/sale-returns/:id", middleware.Require("sales.view"), h.GetReturn)

	r.Get("/quotations", middleware.Require("quotations.manage"), h.Quotations)
	r.Post("/quotations", middleware.Require("quotations.manage"), h.CreateQuotation)
	r.Get("/quotations/:id", middleware.Require("quotations.manage"), h.GetQuotation)
	r.Put("/quotations/:id", middleware.Require("quotations.manage"), h.UpdateQuotation)
	r.Post("/quotations/:id/convert", middleware.Require("sales.create"), middleware.Idempotent(), h.ConvertQuotation)
}
