package inbox

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/kernel/deps"
)

// Mount builds the module from the shared services and registers its routes.
func Mount(r fiber.Router, d deps.Deps) *Service {
	svc := New(d.DB, d.Approval, d.Audit, d.Clock)
	Register(r, &Handler{Svc: svc})
	return svc
}
