package hr

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes.
// Leave requests and advances live on their own tables and do not use app.approval_requests.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Ledger, d.Audit, d.Approval, d.Notify, d.Clock, d.Hash, d.Store)
	Register(r, &Handler{Svc: svc})
	return svc
}
