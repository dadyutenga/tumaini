package attendance

import (
	"bms/internal/platform/readapi"
	"github.com/gofiber/fiber/v3"
)

func registerReads(r fiber.Router, h *Handler) {
	days := readapi.Resource{Path: "/attendance/days", Permission: "attendance.view", Select: "t.*,e.name AS employee_name,e.employee_no", From: "attendance_days t JOIN employees e ON e.id=t.employee_id", Order: "t.day DESC,t.id", Branch: "t.branch_id", Employee: "t.employee_id", Date: "t.day", Filters: map[string]readapi.Filter{"employee_id": {Column: "t.employee_id", Kind: "uuid"}, "status": {Column: "t.status"}}}
	readapi.Register(r, h.Svc.DB, days,
		readapi.Resource{Path: "/schedules", Permission: "schedules.manage", Select: "t.*", From: "work_schedules t LEFT JOIN employees e ON e.id=t.employee_id", Order: "t.weekday,t.id", Branch: "COALESCE(t.branch_id,e.branch_id)", Filters: map[string]readapi.Filter{"employee_id": {Column: "t.employee_id", Kind: "uuid"}}},
		readapi.Resource{Path: "/holidays", Permission: "schedules.manage", Select: "t.*", From: "holidays t", Order: "t.day,t.id", Date: "t.day"},
		readapi.Resource{Path: "/me/attendance", Self: true, Select: "t.*", From: "attendance_days t", Order: "t.day DESC,t.id", Branch: "t.branch_id", Employee: "t.employee_id", Date: "t.day"},
	)
}
