package expenses

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
)

// Mount builds the module from the shared services and registers its routes and background jobs.
// Expense approvals live directly on app.expenses (submitted -> approved/rejected -> paid), not in
// app.approval_requests.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Ledger, d.Audit, d.Approval, d.Notify, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Jobs.Register(jobs.RecurringExpenses, svc.RunRecurring)
	d.Jobs.Daily(jobs.RecurringExpenses, "00:30")
	return svc
}
