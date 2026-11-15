package inbox

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) List(c fiber.Ctx) error {
	list, err := h.Svc.List(c.Context(), middleware.Principal(c), c.Query("status"))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Approve(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.Bind().Body(&body)
	if err := h.Svc.Decide(c.Context(), middleware.Principal(c), id, true, body.Note); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "approved"}))
}

func (h *Handler) Reject(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.Bind().Body(&body)
	if err := h.Svc.Decide(c.Context(), middleware.Principal(c), id, false, body.Note); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "rejected"}))
}

func (h *Handler) Notes(c fiber.Ctx) error {
	list, err := h.Svc.Notifications(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func (h *Handler) Audit(c fiber.Ctx) error {
	list, err := h.Svc.Audit(c.Context(), middleware.Principal(c), c.Query("action"))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/approvals", middleware.Require("approvals.view"), h.List)
	r.Post("/approvals/:id/approve", middleware.Require("approvals.decide"), h.Approve)
	r.Post("/approvals/:id/reject", middleware.Require("approvals.decide"), h.Reject)
	r.Get("/notifications", h.Notes)
	r.Get("/audit-logs", middleware.Require("audit.view"), h.Audit)
}
