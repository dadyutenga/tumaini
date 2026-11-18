package hr

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Create(c fiber.Ctx) error {
	var body EmployeeIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) Loan(c fiber.Ctx) error {
	var body struct {
		EmployeeID       uuid.UUID       `json:"employee_id"`
		Principal        decimal.Decimal `json:"principal"`
		MonthlyDeduction decimal.Decimal `json:"monthly_deduction"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.Loan(c.Context(), middleware.Principal(c), body.EmployeeID, body.Principal, body.MonthlyDeduction)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) Disburse(c fiber.Ctx) error {
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
	if err := h.Svc.Disburse(c.Context(), middleware.Principal(c), id, body.MoneyAccountID); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "disbursed"}))
}

func Register(r fiber.Router, h *Handler) {
	registerReads(r, h)
	registerLeave(r, h)
	r.Post("/employees", middleware.Require("employees.manage"), h.Create)
	r.Post("/loans", middleware.Require("loans.create"), h.Loan)
	r.Post("/loans/:id/disburse", middleware.Require("loans.approve"), h.Disburse)
}
