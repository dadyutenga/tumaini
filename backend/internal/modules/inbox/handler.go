package inbox

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

var approvalKinds = map[string]bool{
	"sale_discount": true, "job_price_change": true, "sale_void": true,
	"transaction_correction": true, "payment_void": true, "day_reopen": true,
	"product_price_change": true,
	// Legacy aliases accepted and normalized in service.go
	"discount": true, "price_change": true,
}

var approvalPerms = []string{
	"shop.discount.approve",
	"services.price.approve",
	"shop.sale.void",
	"finance.transaction.correct",
	"finance.payment.void",
	"finance.day.reopen",
	"shop.product.price_approve",
}

func (h *Handler) List(c fiber.Ctx) error {
	f := ApprovalFilter{Status: c.Query("status"), Kind: c.Query("kind"), Mine: c.Query("mine") == "true"}
	switch f.Status {
	case "", "pending", "approved", "rejected", "cancelled", "expired":
	default:
		return httpx.BadRequest("invalid_query", "status must be pending, approved, rejected, cancelled or expired")
	}
	if f.Kind != "" && !approvalKinds[f.Kind] {
		return httpx.BadRequest("invalid_query", "Unknown approval kind")
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.List(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) Count(c fiber.Ctx) error {
	n, err := h.Svc.Count(c.Context(), middleware.Principal(c))
	return httpx.OK(c, fiber.Map{"pending": n}, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

type decideIn struct {
	Note string `json:"note" validate:"max=1000"`
}

func (h *Handler) decide(approve bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body decideIn
		if err := httpx.BindOptional(c, &body); err != nil {
			return err
		}
		status, raw, err := h.Svc.Decide(c.Context(), middleware.Principal(c), id, approve, body.Note)
		return httpx.Send(c, status, raw, err)
	}
}

func (h *Handler) Cancel(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	err = h.Svc.Cancel(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, fiber.Map{"id": id, "status": "cancelled"}, err)
}

func (h *Handler) ApproveWithPIN(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.ApproveWithPIN(c.Context(), middleware.Principal(c), id, "", "", "")
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Notifications(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Notifications(c.Context(), middleware.Principal(c), c.Query("unread") == "true", page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) UnreadCount(c fiber.Ctx) error {
	n, err := h.Svc.UnreadCount(c.Context(), middleware.Principal(c))
	return httpx.OK(c, fiber.Map{"unread": n}, err)
}

func (h *Handler) MarkRead(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	err = h.Svc.MarkRead(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, fiber.Map{"id": id, "read": true}, err)
}

func (h *Handler) ReadAll(c fiber.Ctx) error {
	n, err := h.Svc.ReadAll(c.Context(), middleware.Principal(c))
	return httpx.OK(c, fiber.Map{"marked": n}, err)
}

func (h *Handler) TestNotification(c fiber.Ctx) error {
	var body struct {
		Channel   string `json:"channel"`
		Recipient string `json:"recipient"`
		Message   string `json:"message"`
	}
	if err := httpx.BindOptional(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.SendTestNotification(c.Context(), middleware.Principal(c), body.Channel, body.Recipient, body.Message)
	return httpx.OK(c, out, err)
}

func (h *Handler) Audit(c fiber.Ctx) error {
	f := AuditFilter{Action: c.Query("action"), EntityType: c.Query("entity_type")}
	var err error
	if f.UserID, err = httpx.QueryUUID(c, "user_id"); err != nil {
		return err
	}
	if f.EntityID, err = httpx.QueryUUID(c, "entity_id"); err != nil {
		return err
	}
	if f.BranchID, err = httpx.QueryUUID(c, "branch_id"); err != nil {
		return err
	}
	if f.From, f.To, err = httpx.Range(c); err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.AuditLogs(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) AuditEntity(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	typ := c.Params("type")
	if typ == "" || len(typ) > 64 {
		return httpx.BadRequest("invalid_id", "type is required")
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.AuditLogs(c.Context(), middleware.Principal(c), AuditFilter{EntityType: typ, EntityID: &id}, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) StartExport(c fiber.Ctx) error {
	out, err := h.Svc.StartExport(c.Context(), middleware.Principal(c))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(httpx.Data(out))
}

func (h *Handler) Backups(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	out, total, err := h.Svc.Backups(c.Context(), middleware.Principal(c), page)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"data": out, "meta": httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}})
}

func (h *Handler) Export(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Export(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Search(c fiber.Ctx) error {
	p, err := searchPlace(c, *middleware.Principal(c))
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	result, err := h.Svc.Search(c.Context(), &p, SearchRequest{Text: c.Query("q"), Scope: c.Query("scope"), Limit: limit})
	return httpx.OK(c, result, err)
}

// searchPlace applies the location parameter, which overrides the X-Branch-Id header under the same
// access rules.
func searchPlace(c fiber.Ctx, p auth.Principal) (auth.Principal, error) {
	switch raw := c.Query("location"); raw {
	case "":
	case "all":
		if !p.CanAccessAllBranches() {
			return p, httpx.Forbidden("branch_forbidden", "You do not have access to all branches")
		}
		p.BranchID = nil
	default:
		id, err := uuid.Parse(raw)
		if err != nil {
			return p, httpx.BadRequest("invalid_branch", "location must be a location id")
		}
		if !p.AllowsBranch(id) {
			return p, httpx.Forbidden("branch_forbidden", "You do not have access to this branch")
		}
		p.BranchID = &id
	}
	return p, nil
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/search", h.Search)
	r.Get("/approvals", middleware.RequireAny(approvalPerms...), h.List)
	r.Get("/approvals/count", middleware.RequireAny(approvalPerms...), h.Count)
	r.Get("/approvals/:id", middleware.RequireAny(approvalPerms...), h.Get)
	r.Post("/approvals/:id/approve", middleware.RequireAny(approvalPerms...), middleware.Idempotent(), h.decide(true))
	r.Post("/approvals/:id/reject", middleware.RequireAny(approvalPerms...), h.decide(false))
	// Any requester may cancel their own request, so this route checks ownership in the service.
	r.Post("/approvals/:id/cancel", h.Cancel)
	r.Post("/approvals/:id/approve-with-pin", h.ApproveWithPIN)
	r.Get("/notifications", h.Notifications)
	r.Get("/notifications/unread-count", h.UnreadCount)
	r.Post("/notifications/read-all", h.ReadAll)
	r.Post("/notifications/test", middleware.Require("settings.manage"), h.TestNotification)
	r.Post("/notifications/:id/read", h.MarkRead)
	r.Get("/audit-logs", middleware.Require("security.audit.view"), h.Audit)
	r.Get("/audit-logs/entity/:type/:id", middleware.Require("security.audit.view"), h.AuditEntity)
	r.Post("/backups/export", middleware.Require("security.backup.manage"), h.StartExport)
	r.Get("/backups", middleware.Require("security.backup.manage"), h.Backups)
	r.Get("/exports/:id", h.Export)
}
