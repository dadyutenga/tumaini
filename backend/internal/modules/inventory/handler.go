package inventory

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) PostEntry(c fiber.Ctx) error {
	var body EntryInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	p := middleware.Principal(c)
	if body.Direction == "in" {
		if _, ok := p.Can("stock.in"); !ok {
			return httpx.Forbidden("permission_denied", "You cannot receive stock")
		}
	} else if body.Direction == "out" {
		if _, ok := p.Can("stock.out"); !ok {
			return httpx.Forbidden("permission_denied", "You cannot remove stock")
		}
	} else {
		return httpx.BadRequest("invalid_body", "Direction must be in or out")
	}
	status, raw, err := h.Svc.PostEntry(c.Context(), p, body)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Levels(c fiber.Ctx) error {
	p := middleware.Principal(c)
	_, cost := p.Can("products.view_cost")
	list, err := h.Svc.Levels(c.Context(), p, cost)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Low(c fiber.Ctx) error {
	list, err := h.Svc.Low(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Card(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	list, err := h.Svc.StockCard(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Valuation(c fiber.Ctx) error {
	data, err := h.Svc.Valuation(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func (h *Handler) CreateAdj(c fiber.Ctx) error {
	var body struct {
		Reason string      `json:"reason"`
		Items  []CountLine `json:"items"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CreateAdjustment(c.Context(), middleware.Principal(c), body.Reason, body.Items, false)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) PostAdj(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.PostAdjustment(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) CreateTransfer(c fiber.Ctx) error {
	var body TransferInput
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CreateTransfer(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) Dispatch(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.Dispatch(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func (h *Handler) Receive(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Items []ReceiveLine `json:"items"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.ReceiveTransfer(c.Context(), middleware.Principal(c), id, body.Items)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(raw)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/stock/levels", middleware.Require("stock.view"), h.Levels)
	r.Get("/stock/low", middleware.Require("stock.view"), h.Low)
	r.Get("/stock/valuation", middleware.Require("valuation.view"), h.Valuation)
	r.Get("/products/:id/stock-card", middleware.Require("stock.view"), h.Card)
	r.Post("/stock-entries", middleware.RequireAny("stock.in", "stock.out"), middleware.Idempotent(), h.PostEntry)
	r.Post("/stock-adjustments", middleware.Require("stock.adjust"), h.CreateAdj)
	r.Post("/stock-adjustments/:id/post", middleware.Require("stock.adjust"), middleware.Idempotent(), h.PostAdj)
	r.Post("/stock-transfers", middleware.Require("transfers.create"), h.CreateTransfer)
	r.Post("/stock-transfers/:id/dispatch", middleware.Require("transfers.dispatch"), middleware.Idempotent(), h.Dispatch)
	r.Post("/stock-transfers/:id/receive", middleware.Require("transfers.receive"), middleware.Idempotent(), h.Receive)
}
