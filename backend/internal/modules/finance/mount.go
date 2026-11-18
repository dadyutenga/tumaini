package finance

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes,
// approval hooks and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Ledger, d.Audit, d.Approval, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Approval.Register("payment_void", approval.Hooks{OnApproved: svc.OnVoidApproved, OnRejected: svc.OnVoidRejected})
	return svc
}
