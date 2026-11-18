package hr

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes,
// approval hooks and background jobs.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Ledger, d.Audit, d.Approval, d.Notify, d.Clock, d.Hash)
	Register(r, &Handler{Svc: svc})
	d.Approval.Register("leave", approval.Hooks{OnApproved: svc.onLeaveApproved, OnRejected: svc.onLeaveRejected})
	d.Approval.Register("loan", approval.Hooks{OnApproved: svc.onLoanDecided(true), OnRejected: svc.onLoanDecided(false)})
	return svc
}
