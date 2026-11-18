package attendance

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

var pr = middleware.Principal

type clockIn struct {
	EmployeeID *uuid.UUID `json:"employee_id"`
}

func (h *Handler) ClockIn(c fiber.Ctx) error {
	var in clockIn
	if err := httpx.BindOptional(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.ClockIn(c.Context(), pr(c), in.EmployeeID)
	return httpx.OK(c, out, err)
}

func (h *Handler) ClockOut(c fiber.Ctx) error {
	var in clockIn
	if err := httpx.BindOptional(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.ClockOut(c.Context(), pr(c), in.EmployeeID)
	return httpx.OK(c, out, err)
}

func (h *Handler) Today(c fiber.Ctx) error {
	branch, err := httpx.QueryUUID(c, "branch_id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Today(c.Context(), pr(c), branch)
	return httpx.OK(c, out, err)
}

func dayFilter(c fiber.Ctx) (DayFilter, error) {
	f := DayFilter{Status: c.Query("status"), OTStatus: c.Query("overtime_status"), Review: httpx.QueryBool(c, "needs_review")}
	var err error
	if f.EmployeeID, err = httpx.QueryUUID(c, "employee_id"); err != nil {
		return f, err
	}
	if f.BranchID, err = httpx.QueryUUID(c, "branch_id"); err != nil {
		return f, err
	}
	f.From, f.To, err = httpx.Range(c)
	return f, err
}

func (h *Handler) list(mod func(*DayFilter)) fiber.Handler {
	return func(c fiber.Ctx) error {
		f, err := dayFilter(c)
		if err != nil {
			return err
		}
		if mod != nil {
			mod(&f)
		}
		page := httpx.PageFrom(c)
		rows, total, err := h.Svc.Days(c.Context(), pr(c), f, page)
		return httpx.Paged(c, rows, page, total, err)
	}
}

func (h *Handler) PutDay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in DayIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.PutDay(c.Context(), pr(c), id, in)
	return httpx.OK(c, out, err)
}

func (h *Handler) AddEvent(c fiber.Ctx) error {
	var in EventIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.AddEvent(c.Context(), pr(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) Mark(c fiber.Ctx) error {
	var in struct {
		EmployeeID uuid.UUID `json:"employee_id" validate:"required"`
		Day        string    `json:"day" validate:"required,date"`
		Status     string    `json:"status" validate:"required,oneof=present late absent half_day leave holiday day_off"`
		Note       string    `json:"note"`
	}
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.Mark(c.Context(), pr(c), in.EmployeeID, in.Day, in.Status, in.Note)
	return httpx.OK(c, out, err)
}

func (h *Handler) decideOvertime(approve bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "day_id")
		if err != nil {
			return err
		}
		var in struct {
			Note string `json:"note"`
		}
		if err := httpx.BindOptional(c, &in); err != nil {
			return err
		}
		out, err := h.Svc.DecideOvertime(c.Context(), pr(c), id, approve, in.Note)
		return httpx.OK(c, out, err)
	}
}

func (h *Handler) Summary(c fiber.Ctx) error {
	branch, err := httpx.QueryUUID(c, "branch_id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Summary(c.Context(), pr(c), c.Query("month"), branch)
	return httpx.OK(c, out, err)
}

func (h *Handler) Schedules(c fiber.Ctx) error {
	branch, err := httpx.QueryUUID(c, "branch_id")
	if err != nil {
		return err
	}
	emp, err := httpx.QueryUUID(c, "employee_id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Schedules(c.Context(), pr(c), branch, emp)
	return httpx.OK(c, out, err)
}

func (h *Handler) PutSchedule(c fiber.Ctx) error {
	var in ScheduleIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.PutSchedule(c.Context(), pr(c), in)
	return httpx.OK(c, out, err)
}

func (h *Handler) Holidays(c fiber.Ctx) error {
	year, _ := strconv.Atoi(c.Query("year"))
	out, err := h.Svc.Holidays(c.Context(), pr(c), year)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateHoliday(c fiber.Ctx) error {
	var in HolidayIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.CreateHoliday(c.Context(), pr(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) DeleteHoliday(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeleteHoliday(c.Context(), pr(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func Register(r fiber.Router, h *Handler) {
	req := middleware.Require
	r.Post("/attendance/clock-in", req("attendance.clock"), h.ClockIn)
	r.Post("/attendance/clock-out", req("attendance.clock"), h.ClockOut)
	r.Get("/attendance/today", req("attendance.view"), h.Today)
	r.Get("/attendance/days", req("attendance.view"), h.list(nil))
	r.Post("/attendance/days", req("attendance.edit"), h.Mark)
	r.Put("/attendance/days/:id", req("attendance.edit"), h.PutDay)
	r.Post("/attendance/events", req("attendance.edit"), h.AddEvent)
	r.Get("/attendance/late", req("attendance.view"), h.list(func(f *DayFilter) { f.LateOnly = true }))
	r.Get("/attendance/overtime", req("attendance.view"), h.list(func(f *DayFilter) { f.OTOnly = true }))
	r.Post("/attendance/overtime/:day_id/approve", req("overtime.approve"), h.decideOvertime(true))
	r.Post("/attendance/overtime/:day_id/reject", req("overtime.approve"), h.decideOvertime(false))
	r.Get("/attendance/summary", req("attendance.view"), h.Summary)
	r.Get("/schedules", req("schedules.manage"), h.Schedules)
	r.Put("/schedules", req("schedules.manage"), h.PutSchedule)
	r.Get("/holidays", req("schedules.manage"), h.Holidays)
	r.Post("/holidays", req("schedules.manage"), h.CreateHoliday)
	r.Delete("/holidays/:id", req("schedules.manage"), h.DeleteHoliday)
}
