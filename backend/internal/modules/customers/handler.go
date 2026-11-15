package customers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) List(c fiber.Ctx) error {
	list, err := h.Svc.List(c.Context(), middleware.Principal(c), c.Query("q"))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body Input
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	cust, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(cust))
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	cust, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(cust))
}

func (h *Handler) Update(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body Input
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	cust, err := h.Svc.Update(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(cust))
}

func (h *Handler) Credit(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		CreditLimit decimal.Decimal `json:"credit_limit"`
		Days        int             `json:"payment_terms_days"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.CreditLimit(c.Context(), middleware.Principal(c), id, body.CreditLimit, body.Days); err != nil {
		return err
	}
	return h.Get(c)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body PayIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Receive(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Statement(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	data, err := h.Svc.Statement(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) Debts(c fiber.Ctx) error {
	list, err := h.Svc.Debts(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/customers", middleware.Require("customers.view"), h.List)
	r.Post("/customers", middleware.Require("customers.manage"), h.Create)
	r.Get("/customers/debts", middleware.Require("customers.view"), h.Debts)
	r.Get("/customers/:id", middleware.Require("customers.view"), h.Get)
	r.Put("/customers/:id", middleware.Require("customers.manage"), h.Update)
	r.Put("/customers/:id/credit-limit", middleware.Require("customers.credit_limit"), h.Credit)
	r.Post("/customers/:id/payments", middleware.Require("payments.receive"), middleware.Idempotent(), h.Pay)
	r.Get("/customers/:id/statement", middleware.Require("customers.statement"), h.Statement)
	r.Get("/reports/customer-debt", middleware.Require("reports.debts"), h.Debts)
}
