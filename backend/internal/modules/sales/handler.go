package sales

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func send(c fiber.Ctx, status int, body []byte, err error) error {
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(body)
}

func (h *Handler) Preview(c fiber.Ctx) error {
	var body CreateIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.Preview(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body CreateIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	return send(c, status, raw, err)
}

func (h *Handler) List(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	list, total, err := h.Svc.List(c.Context(), middleware.Principal(c), page)
	if err != nil {
		return err
	}
	return c.JSON(httpx.List(list, httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	sale, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(sale))
}

func (h *Handler) Receipt(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	sale, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(sale))
}

func (h *Handler) Return(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ReturnIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	status, raw, err := h.Svc.CreateReturn(c.Context(), middleware.Principal(c), id, body)
	return send(c, status, raw, err)
}

func (h *Handler) OpenTill(c fiber.Ctx) error {
	var body struct {
		MoneyAccountID uuid.UUID       `json:"money_account_id"`
		OpeningFloat   decimal.Decimal `json:"opening_float"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.OpenTill(c.Context(), middleware.Principal(c), body.MoneyAccountID, body.OpeningFloat)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) CurrentTill(c fiber.Ctx) error {
	out, err := h.Svc.CurrentTill(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) CloseTill(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Counted decimal.Decimal `json:"counted_cash"`
		Note    string          `json:"note"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.CloseTill(c.Context(), middleware.Principal(c), id, body.Counted, body.Note)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/tills/current", middleware.Require("tills.open_close"), h.CurrentTill)
	r.Post("/tills/open", middleware.Require("tills.open_close"), h.OpenTill)
	r.Post("/tills/:id/close", middleware.Require("tills.open_close"), h.CloseTill)
	r.Post("/sales/preview", middleware.Require("sales.create"), h.Preview)
	r.Post("/sales", middleware.Require("sales.create"), middleware.Idempotent(), h.Create)
	r.Get("/sales", middleware.Require("sales.view"), h.List)
	r.Get("/sales/:id", middleware.Require("sales.view"), h.Get)
	r.Get("/sales/:id/receipt", middleware.Require("sales.view"), h.Receipt)
	r.Post("/sales/:id/returns", middleware.Require("sales.refund_request"), middleware.Idempotent(), h.Return)
}
