package attendance

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) ClockIn(c fiber.Ctx) error {
	out, err := h.Svc.ClockIn(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Schedule(c fiber.Ctx) error {
	var body struct {
		BranchID   *uuid.UUID `json:"branch_id"`
		EmployeeID *uuid.UUID `json:"employee_id"`
		Weekday    int        `json:"weekday"`
		Start      string     `json:"start_time"`
		End        string     `json:"end_time"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.PutSchedule(c.Context(), middleware.Principal(c), body.BranchID, body.EmployeeID, body.Weekday, body.Start, body.End); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) Mark(c fiber.Ctx) error {
	var body struct {
		EmployeeID uuid.UUID `json:"employee_id"`
		Day        string    `json:"day"`
		Status     string    `json:"status"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	if err := h.Svc.Mark(c.Context(), middleware.Principal(c), body.EmployeeID, body.Day, body.Status); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"ok": true}))
}

func (h *Handler) Days(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Query("employee_id"))
	if err != nil {
		return httpx.BadRequest("invalid_id", "employee_id is required")
	}
	list, err := h.Svc.Days(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(list))
}

func Register(r fiber.Router, h *Handler) {
	r.Post("/attendance/clock-in", middleware.Require("attendance.clock"), h.ClockIn)
	r.Put("/schedules", middleware.Require("schedules.manage"), h.Schedule)
	r.Post("/attendance/days", middleware.Require("attendance.edit"), h.Mark)
	r.Get("/attendance/days", middleware.Require("attendance.view"), h.Days)
}
