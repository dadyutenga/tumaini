package finance

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) List(c fiber.Ctx) error {
	p := middleware.Principal(c)
	_, bal := p.Can("accounts.view_balance")
	list, err := h.Svc.List(c.Context(), p, c.Query("type"), bal)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body AccountIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	a, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(a))
}

func (h *Handler) Transfer(c fiber.Ctx) error {
	var body TransferIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Transfer(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Trial(c fiber.Ctx) error {
	data, err := h.Svc.TrialBalance(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) Transactions(c fiber.Ctx) error {
	var account *uuid.UUID
	if raw := c.Query("account_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("invalid_id", "account_id must be a UUID")
		}
		account = &id
	}
	list, err := h.Svc.Transactions(c.Context(), middleware.Principal(c), account)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/money-accounts", middleware.Require("accounts.view"), h.List)
	r.Post("/money-accounts", middleware.Require("accounts.manage"), h.Create)
	r.Post("/money-transfers", middleware.Require("transfers_money.create"), middleware.Idempotent(), h.Transfer)
	r.Get("/ledger/accounts", middleware.Require("reports.financial"), h.Trial)
	r.Get("/transactions", middleware.Require("transactions.view"), h.Transactions)
	r.Get("/reports/trial-balance", middleware.Require("reports.financial"), h.Trial)
}
