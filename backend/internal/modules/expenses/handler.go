package expenses

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Create(c fiber.Ctx) error {
	var body Input
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Categories(c fiber.Ctx) error {
	list, err := h.Svc.Categories(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	data, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		MoneyAccountID uuid.UUID `json:"money_account_id"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Pay(c.Context(), middleware.Principal(c), id, body.MoneyAccountID)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func Register(r fiber.Router, h *Handler) {
	registerReads(r, h)
	r.Get("/expense-categories", middleware.Require("expenses.view"), h.Categories)
	r.Post("/expenses", middleware.Require("expenses.create"), middleware.Idempotent(), h.Create)
	r.Post("/expenses/:id/pay", middleware.Require("expenses.pay"), middleware.Idempotent(), h.Pay)
}
