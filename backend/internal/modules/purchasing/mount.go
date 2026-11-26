package purchasing

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes.
// Purchase orders do not use app.approval_requests in the approved model; shop.purchase.manage gates them.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Audit, d.Approval, d.Notify, d.Clock)
	Register(r, &Handler{Svc: svc})
	return svc
}
