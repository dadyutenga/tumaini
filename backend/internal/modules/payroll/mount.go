package payroll

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
)

// Mount builds the module from the shared services and registers its routes,
// approval hooks and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Ledger, d.Audit, d.Approval, d.Notify, d.Jobs, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Approval.Register("payroll_run", approval.Hooks{OnApproved: svc.onApproved, OnRejected: svc.onRejected})
	d.Jobs.Register(jobs.PayrollCalculate, svc.CalculateJob)
	d.Jobs.Register(jobs.SalaryDue, svc.SalaryDue)
	d.Jobs.Daily(jobs.SalaryDue, "08:00")
	return svc
}
