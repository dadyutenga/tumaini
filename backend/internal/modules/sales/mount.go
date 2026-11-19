package sales

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes and approval hooks.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Stock, d.Ledger, d.Audit, d.Approval, d.Notify, d.Clock)
	Register(r, &Handler{Svc: svc})
	d.Approval.Register("sale_discount", approval.Hooks{OnApproved: svc.OnApproved, OnRejected: svc.OnRejected})
	d.Approval.Register("sale_void", approval.Hooks{OnApproved: svc.OnVoidApproved, OnRejected: svc.OnVoidRejected})
	return svc
}
