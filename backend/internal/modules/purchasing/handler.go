package purchasing

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) CreateSupplier(c fiber.Ctx) error {
	var body SupplierIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CreateSupplier(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) CreatePO(c fiber.Ctx) error {
	var body POIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CreatePO(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
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

func (h *Handler) GetPO(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetPO(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Receive(c fiber.Ctx) error {
	var body struct {
		POID  uuid.UUID     `json:"po_id"`
		Items []ReceiptItem `json:"items"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Receive(c.Context(), middleware.Principal(c), body.POID, body.Items)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Invoice(c fiber.Ctx) error {
	var body InvoiceIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Invoice(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Amount         decimal.Decimal `json:"amount"`
		Method         string          `json:"method"`
		MoneyAccountID uuid.UUID       `json:"money_account_id"`
		Reference      string          `json:"reference"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Pay(c.Context(), middleware.Principal(c), id, body.Amount, body.MoneyAccountID, body.Method, body.Reference)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func Register(r fiber.Router, h *Handler) {
	registerReads(r, h)
	r.Post("/suppliers", middleware.Require("suppliers.manage"), h.CreateSupplier)
	r.Post("/purchase-orders", middleware.Require("po.create"), h.CreatePO)
	r.Post("/purchase-orders/:id/submit", middleware.Require("po.create"), h.Submit)
	r.Post("/goods-receipts", middleware.Require("grn.create"), middleware.Idempotent(), h.Receive)
	r.Post("/purchase-invoices", middleware.Require("purchase_invoices.manage"), middleware.Idempotent(), h.Invoice)
	r.Post("/suppliers/:id/payments", middleware.Require("payments.pay"), middleware.Idempotent(), h.Pay)
}
