package payroll

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Create(c fiber.Ctx) error {
	var body struct {
		Start string `json:"period_start"`
		End   string `json:"period_end"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CreateRun(c.Context(), middleware.Principal(c), body.Start, body.End)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) Calculate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Calculate(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Approve(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.Approve(c.Context(), middleware.Principal(c), id); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "approved"}))
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
	if err := h.Svc.Pay(c.Context(), middleware.Principal(c), id, body.MoneyAccountID); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "locked"}))
}

func (h *Handler) Component(c fiber.Ctx) error {
	var body struct {
		Name  string          `json:"name"`
		Kind  string          `json:"kind"`
		Value decimal.Decimal `json:"value"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	id, err := h.Svc.Component(c.Context(), middleware.Principal(c), body.Name, body.Kind, body.Value)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(fiber.Map{"id": id}))
}

func Register(r fiber.Router, h *Handler) {
	r.Post("/salary-components", middleware.Require("components.manage"), h.Component)
	r.Post("/payroll-runs", middleware.Require("payroll.prepare"), h.Create)
	r.Post("/payroll-runs/:id/calculate", middleware.Require("payroll.prepare"), h.Calculate)
	r.Post("/payroll-runs/:id/approve", middleware.Require("payroll.approve"), h.Approve)
	r.Post("/payroll-runs/:id/pay", middleware.Require("payroll.pay"), h.Pay)
}
