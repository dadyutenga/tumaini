package attendance

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
)

// Mount builds the module from the shared services and registers its routes and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Audit, d.Approval, d.Notify, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Jobs.Register(jobs.AttendanceClose, svc.CloseDay)
	d.Jobs.Daily(jobs.AttendanceClose, "end")
	return svc
}
