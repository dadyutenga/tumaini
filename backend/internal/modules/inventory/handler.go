package inventory

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) PostEntry(c fiber.Ctx) error {
	var body EntryInput
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	p := middleware.Principal(c)
	// stock.in and stock.out are separate keys; the route accepts either and
	// the direction decides which one is required.
	key := "stock.in"
	if body.Direction == "out" {
		key = "stock.out"
	}
	if _, ok := p.Can(key); !ok {
		return httpx.Forbidden("permission_denied", "You do not have permission to do that")
	}
	status, raw, err := h.Svc.PostEntry(c.Context(), p, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Entries(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.Entries(c.Context(), middleware.Principal(c), DocFilter{Direction: c.Query("direction"), Reason: c.Query("reason"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetEntry(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetEntry(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Levels(c fiber.Ctx) error {
	p := middleware.Principal(c)
	_, cost := p.Can("shop.product.view_cost")
	list, err := h.Svc.Levels(c.Context(), p, cost)
	return httpx.OK(c, list, err)
}

func (h *Handler) Low(c fiber.Ctx) error {
	list, err := h.Svc.Low(c.Context(), middleware.Principal(c))
	return httpx.OK(c, list, err)
}

func (h *Handler) Movements(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	pid, err := httpx.QueryUUID(c, "product_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.Movements(c.Context(), middleware.Principal(c), MovementFilter{ProductID: pid, Type: c.Query("type"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) Card(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	out, err := h.Svc.StockCard(c.Context(), middleware.Principal(c), id, from, to)
	return httpx.OK(c, out, err)
}

func (h *Handler) Valuation(c fiber.Ctx) error {
	asOf, err := httpx.QueryDate(c, "as_of")
	if err != nil {
		return err
	}
	data, err := h.Svc.Valuation(c.Context(), middleware.Principal(c), asOf)
	return httpx.OK(c, data, err)
}

func (h *Handler) Adjustments(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.Adjustments(c.Context(), middleware.Principal(c), DocFilter{Status: c.Query("status"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetAdj(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetAdjustment(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateAdj(c fiber.Ctx) error {
	var body AdjustmentIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreateAdjustment(c.Context(), middleware.Principal(c), body, false)
	return httpx.Created(c, out, err)
}

func (h *Handler) CountSheet(c fiber.Ctx) error {
	var body CountSheetIn
	if err := httpx.BindOptional(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CountSheet(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) UpdateAdj(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body AdjustmentIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.UpdateAdjustment(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, out, err)
}

func (h *Handler) CancelAdj(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.CancelAdjustment(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) PostAdj(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.PostAdjustment(c.Context(), middleware.Principal(c), id)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Transfers(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.Transfers(c.Context(), middleware.Principal(c), c.Query("direction"), DocFilter{Status: c.Query("status"), From: from, To: to}, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) GetTransfer(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetTransfer(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateTransfer(c fiber.Ctx) error {
	var body TransferInput
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreateTransfer(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) UpdateTransfer(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body TransferInput
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.UpdateTransfer(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, out, err)
}

func (h *Handler) CancelTransfer(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.CancelTransfer(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Dispatch(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.Dispatch(c.Context(), middleware.Principal(c), id)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Receive(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ReceiveIn
	if err := httpx.BindOptional(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.ReceiveTransfer(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) ReturnTransfer(c fiber.Ctx) error {
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
	status, raw, err := h.Svc.ReturnTransfer(c.Context(), middleware.Principal(c), id, body.Reason)
	return httpx.Send(c, status, raw, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/stock/levels", middleware.Require("shop.stock.view"), h.Levels)
	r.Get("/stock/low", middleware.Require("shop.stock.view"), h.Low)
	r.Get("/stock/movements", middleware.Require("shop.stock.view"), h.Movements)
	r.Get("/stock/valuation", middleware.Require("shop.product.view_cost"), h.Valuation)
	r.Get("/products/:id/stock-card", middleware.Require("shop.stock.view"), h.Card)

	r.Get("/stock-entries", middleware.Require("stock.view"), h.Entries)
	r.Post("/stock-entries", middleware.RequireAny("stock.in", "stock.out"), middleware.Idempotent(), h.PostEntry)
	r.Get("/stock-entries/:id", middleware.Require("stock.view"), h.GetEntry)

	r.Get("/stock-adjustments", middleware.Require("stock.view"), h.Adjustments)
	r.Post("/stock-adjustments", middleware.Require("stock.adjust"), h.CreateAdj)
	r.Post("/stock-adjustments/count-sheet", middleware.Require("stock.adjust"), h.CountSheet)
	r.Get("/stock-adjustments/:id", middleware.Require("stock.view"), h.GetAdj)
	r.Put("/stock-adjustments/:id", middleware.Require("stock.adjust"), h.UpdateAdj)
	r.Post("/stock-adjustments/:id/post", middleware.Require("stock.adjust"), middleware.Idempotent(), h.PostAdj)
	r.Post("/stock-adjustments/:id/cancel", middleware.Require("stock.adjust"), h.CancelAdj)

	r.Get("/stock-transfers", middleware.Require("transfers.view"), h.Transfers)
	r.Post("/stock-transfers", middleware.Require("transfers.create"), h.CreateTransfer)
	r.Get("/stock-transfers/:id", middleware.Require("transfers.view"), h.GetTransfer)
	r.Put("/stock-transfers/:id", middleware.Require("transfers.create"), h.UpdateTransfer)
	r.Post("/stock-transfers/:id/dispatch", middleware.Require("transfers.dispatch"), middleware.Idempotent(), h.Dispatch)
	r.Post("/stock-transfers/:id/receive", middleware.Require("transfers.receive"), middleware.Idempotent(), h.Receive)
	r.Post("/stock-transfers/:id/return", middleware.Require("transfers.dispatch"), middleware.Idempotent(), h.ReturnTransfer)
	r.Post("/stock-transfers/:id/cancel", middleware.Require("transfers.create"), h.CancelTransfer)
}
