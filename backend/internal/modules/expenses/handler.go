package expenses

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) Create(c fiber.Ctx) error {
	var body Input
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) List(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	cat, err := httpx.QueryUUID(c, "category_id")
	if err != nil {
		return err
	}
	by, err := httpx.QueryUUID(c, "submitted_by")
	if err != nil {
		return err
	}
	proj, err := httpx.QueryUUID(c, "project_id")
	if err != nil {
		return err
	}
	loc, err := httpx.QueryUUID(c, "location_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	sort := httpx.SortFrom(c, map[string]string{"date": "e.expense_date", "amount": "e.amount", "created_at": "e.created_at", "number": "e.expense_no"}, "")
	rows, total, err := h.Svc.List(c.Context(), middleware.Principal(c), ListFilter{
		Status: c.Query("status"), CategoryID: cat, SubmittedBy: by, ProjectID: proj, LocationID: loc, From: from, To: to, Q: c.Query("q"), Sort: sort,
	}, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	data, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, data, err)
}

func (h *Handler) Update(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body Input
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	data, err := h.Svc.Update(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, data, err)
}

func (h *Handler) decide(approve bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var body struct {
			Note string `json:"note" validate:"max=1000"`
		}
		if err := httpx.BindOptional(c, &body); err != nil {
			return err
		}
		data, err := h.Svc.Decide(c.Context(), middleware.Principal(c), id, approve, body.Note)
		return httpx.OK(c, data, err)
	}
}

func (h *Handler) Cancel(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	data, err := h.Svc.Cancel(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, data, err)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		MoneyAccountID uuid.UUID `json:"money_account_id" validate:"required"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Pay(c.Context(), middleware.Principal(c), id, body.MoneyAccountID)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Reverse(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason" validate:"required,max=500"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Reverse(c.Context(), middleware.Principal(c), id, body.Reason)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Categories(c fiber.Ctx) error {
	list, err := h.Svc.Categories(c.Context(), middleware.Principal(c), c.Query("all") == "true")
	return httpx.OK(c, list, err)
}

func (h *Handler) CreateCategory(c fiber.Ctx) error {
	var body CategoryIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	data, err := h.Svc.CreateCategory(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, data, err)
}

func (h *Handler) UpdateCategory(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body CategoryIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	data, err := h.Svc.UpdateCategory(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, data, err)
}

func (h *Handler) Recurring(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Recurring(c.Context(), middleware.Principal(c), page, httpx.QueryBool(c, "is_active"))
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) CreateRecurring(c fiber.Ctx) error {
	var body RecurringIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	data, err := h.Svc.CreateRecurring(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, data, err)
}

func (h *Handler) UpdateRecurring(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body RecurringIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	data, err := h.Svc.UpdateRecurring(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, data, err)
}

func (h *Handler) setRecurring(active bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		data, err := h.Svc.SetRecurringActive(c.Context(), middleware.Principal(c), id, active)
		return httpx.OK(c, data, err)
	}
}

func (h *Handler) PettyCash(c fiber.Ctx) error {
	list, err := h.Svc.PettyCash(c.Context(), middleware.Principal(c))
	return httpx.OK(c, list, err)
}

func (h *Handler) TopUp(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body TopUpIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.TopUp(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Reconcile(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body ReconcileIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Reconcile(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func Register(r fiber.Router, h *Handler) {
	canView := middleware.RequireAny("finance.expense.submit", "finance.expense.enter", "finance.expense.approve", "finance.expense.pay", "finance.transaction.view")
	canCreate := middleware.RequireAny("finance.expense.submit", "finance.expense.enter")

	r.Get("/expense-categories", middleware.RequireAny("finance.expense.submit", "finance.expense.enter", "finance.expense.approve", "finance.expense.pay", "settings.manage"), h.Categories)
	r.Post("/expense-categories", middleware.RequireAny("finance.expense.enter", "settings.manage"), h.CreateCategory)
	r.Put("/expense-categories/:id", middleware.RequireAny("finance.expense.enter", "settings.manage"), h.UpdateCategory)

	r.Get("/expenses", canView, h.List)
	r.Post("/expenses", canCreate, middleware.Idempotent(), h.Create)
	r.Get("/expenses/:id", canView, h.Get)
	r.Put("/expenses/:id", canCreate, h.Update)
	r.Post("/expenses/:id/approve", middleware.Require("finance.expense.approve"), h.decide(true))
	r.Post("/expenses/:id/reject", middleware.Require("finance.expense.approve"), h.decide(false))
	r.Post("/expenses/:id/cancel", middleware.RequireAny("finance.expense.submit", "finance.expense.enter", "finance.expense.approve"), h.Cancel)
	r.Post("/expenses/:id/pay", middleware.Require("finance.expense.pay"), middleware.Idempotent(), h.Pay)
	r.Post("/expenses/:id/reverse", middleware.Require("finance.transaction.correct"), h.Reverse)

	r.Get("/recurring-expenses", middleware.RequireAny("finance.expense.enter", "finance.expense.approve", "finance.expense.pay"), h.Recurring)
	r.Post("/recurring-expenses", middleware.RequireAny("finance.expense.enter", "finance.expense.approve"), h.CreateRecurring)
	r.Put("/recurring-expenses/:id", middleware.RequireAny("finance.expense.enter", "finance.expense.approve"), h.UpdateRecurring)
	r.Post("/recurring-expenses/:id/pause", middleware.RequireAny("finance.expense.enter", "finance.expense.approve"), h.setRecurring(false))
	r.Post("/recurring-expenses/:id/resume", middleware.RequireAny("finance.expense.enter", "finance.expense.approve"), h.setRecurring(true))

	r.Get("/petty-cash", middleware.RequireAny("finance.account.view", "finance.account.manage", "finance.expense.pay"), h.PettyCash)
	r.Post("/petty-cash/:id/top-up", middleware.Require("finance.account.manage"), middleware.Idempotent(), h.TopUp)
	r.Post("/petty-cash/:id/reconcile", middleware.RequireAny("finance.account.manage", "finance.day.close"), middleware.Idempotent(), h.Reconcile)
}
