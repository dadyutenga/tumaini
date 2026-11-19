package payroll

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
)

// Mount builds the module from the shared services and registers its routes
// and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Audit, d.Notify, d.Jobs, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Jobs.Register(jobs.PayrollCalculate, svc.CalculateJob)
	d.Jobs.Register(jobs.SalaryDue, svc.SalaryDue)
	d.Jobs.Daily(jobs.SalaryDue, "08:00")
	return svc
}
