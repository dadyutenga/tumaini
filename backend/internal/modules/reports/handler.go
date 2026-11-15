package reports

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) ProfitLoss(c fiber.Ctx) error {
	data, err := h.Svc.ProfitLoss(c.Context(), middleware.Principal(c), c.Query("from"), c.Query("to"))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(data))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/reports/profit-loss", middleware.Require("reports.financial"), h.ProfitLoss)
}
