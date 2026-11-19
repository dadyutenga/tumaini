package inventory

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
	"bms/internal/platform/jobs"
)

// Mount builds the module from the shared services and registers its routes,
// approval hooks and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Stock, d.Ledger, d.Audit, d.Notify, d.Approval, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Jobs.Register(jobs.CheckLowStock, svc.CheckLowStock)
	return svc
}
