package finance

import (
	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

func (h *Handler) List(c fiber.Ctx) error {
	list, err := h.Svc.List(c.Context(), middleware.Principal(c), c.Query("type"), httpx.QueryBool(c, "is_active"))
	return httpx.OK(c, list, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body AccountIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	a, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, a, err)
}

func (h *Handler) Update(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body AccountUpdate
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	a, err := h.Svc.Update(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, a, err)
}

func (h *Handler) Deactivate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	a, err := h.Svc.Deactivate(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, a, err)
}

func (h *Handler) Statement(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	out, err := h.Svc.Statement(c.Context(), middleware.Principal(c), id, from, to)
	if err != nil {
		return err
	}
	if c.Query("format") == "" || c.Query("format") == "json" {
		return c.JSON(httpx.Data(out))
	}
	acc := out["account"].(Account)
	t := export.Table{
		Title:    "Statement " + acc.Name,
		Subtitle: "From " + str(out["from"]) + " to " + str(out["to"]) + ". Opening " + str(out["opening_balance"]) + ", closing " + str(out["closing_balance"]),
		Columns: []export.Col{{Key: "date", Label: "Date"}, {Key: "description", Label: "Description"}, {Key: "in", Label: "In"},
			{Key: "out", Label: "Out"}, {Key: "balance", Label: "Balance"}},
		Rows: out["lines"].([]map[string]any),
	}
	return export.Respond(c, t, nil, nil)
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

func (h *Handler) Transfer(c fiber.Ctx) error {
	var body TransferIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Transfer(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Transfers(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	acc, err := httpx.QueryUUID(c, "account_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Transfers(c.Context(), middleware.Principal(c), acc, from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) payments(fixedParty string) fiber.Handler {
	return func(c fiber.Ctx) error {
		from, to, err := httpx.Range(c)
		if err != nil {
			return err
		}
		party, err := httpx.QueryUUID(c, "party_id")
		if err != nil {
			return err
		}
		acc, err := httpx.QueryUUID(c, "account_id")
		if err != nil {
			return err
		}
		f := PaymentFilter{Direction: c.Query("direction"), Kind: c.Query("kind"), PartyType: c.Query("party_type"), Status: c.Query("status"),
			PartyID: party, AccountID: acc, From: from, To: to, Q: c.Query("q")}
		if fixedParty != "" {
			f.PartyType = fixedParty
		}
		page := httpx.PageFrom(c)
		rows, total, err := h.Svc.Payments(c.Context(), middleware.Principal(c), f, page)
		return httpx.Paged(c, rows, page, total, err)
	}
}

func (h *Handler) Payment(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Payment(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Voucher(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Voucher(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	switch c.Query("format") {
	case "", "json":
		return c.JSON(httpx.Data(out))
	case "pdf":
		return export.File(c, "voucher-"+str(out["number"])+".pdf", "application/pdf", VoucherPDF(out))
	default:
		return httpx.BadRequest("invalid_query", "format must be json or pdf")
	}
}

func (h *Handler) Void(c fiber.Ctx) error {
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
	status, raw, err := h.Svc.Void(c.Context(), middleware.Principal(c), id, body.Reason)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Income(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	cat, err := httpx.QueryUUID(c, "category_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Income(c.Context(), middleware.Principal(c), c.Query("kind"), cat, from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) RecordIncome(c fiber.Ctx) error {
	var body IncomeIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.RecordIncome(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) IncomeCategories(c fiber.Ctx) error {
	list, err := h.Svc.IncomeCategories(c.Context(), middleware.Principal(c))
	return httpx.OK(c, list, err)
}

func (h *Handler) CreateIncomeCategory(c fiber.Ctx) error {
	var body IncomeCategoryIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.CreateIncomeCategory(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, out, err)
}

func (h *Handler) Drawing(c fiber.Ctx) error {
	var body OutflowIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Drawing(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Statutory(c fiber.Ctx) error {
	var body OutflowIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Statutory(c.Context(), middleware.Principal(c), body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Transactions(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	acc, err := httpx.QueryUUID(c, "account_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Transactions(c.Context(), middleware.Principal(c), TxFilter{AccountID: acc, Type: c.Query("type"), From: from, To: to, Q: c.Query("q")}, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) Entries(c fiber.Ctx) error {
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	src, err := httpx.QueryUUID(c, "source_id")
	if err != nil {
		return err
	}
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.Entries(c.Context(), middleware.Principal(c), EntryFilter{From: from, To: to, SourceType: c.Query("source_type"), SourceID: src, AccountCode: c.Query("account_code")}, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) LedgerAccounts(c fiber.Ctx) error {
	to, err := httpx.QueryDate(c, "as_of")
	if err != nil {
		return err
	}
	data, err := h.Svc.TrialBalance(c.Context(), middleware.Principal(c), to, true)
	return httpx.OK(c, data, err)
}

func (h *Handler) Trial(c fiber.Ctx) error {
	to, err := httpx.QueryDate(c, "to")
	if err != nil {
		return err
	}
	data, err := h.Svc.TrialBalance(c.Context(), middleware.Principal(c), to, false)
	return httpx.OK(c, data, err)
}

func (h *Handler) PeriodLock(c fiber.Ctx) error {
	out, err := h.Svc.PeriodLock(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) SetPeriodLock(c fiber.Ctx) error {
	var body struct {
		LockedUntil *string `json:"locked_until"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	out, err := h.Svc.SetPeriodLock(c.Context(), middleware.Principal(c), body.LockedUntil)
	return httpx.OK(c, out, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/money-accounts", middleware.Require("accounts.view"), h.List)
	r.Post("/money-accounts", middleware.Require("accounts.manage"), h.Create)
	r.Get("/money-accounts/:id", middleware.Require("accounts.view"), h.Get)
	r.Put("/money-accounts/:id", middleware.Require("accounts.manage"), h.Update)
	r.Post("/money-accounts/:id/deactivate", middleware.Require("accounts.manage"), h.Deactivate)
	r.Get("/money-accounts/:id/statement", middleware.Require("accounts.view_balance"), h.Statement)
	r.Post("/money-accounts/:id/reconcile", middleware.Require("reconcile.manage"), middleware.Idempotent(), h.Reconcile)

	r.Get("/payments", middleware.Require("payments.view"), h.payments(""))
	r.Get("/payments/customers", middleware.Require("payments.view"), h.payments("customer"))
	r.Get("/payments/suppliers", middleware.Require("payments.view"), h.payments("supplier"))
	r.Get("/payments/staff", middleware.Require("payments.view"), h.payments("employee"))
	r.Get("/payments/:id", middleware.Require("payments.view"), h.Payment)
	r.Get("/payments/:id/voucher", middleware.Require("payments.view"), h.Voucher)
	r.Post("/payments/:id/void", middleware.Require("payments.void"), middleware.Idempotent(), h.Void)

	r.Get("/money-transfers", middleware.Require("accounts.view"), h.Transfers)
	r.Post("/money-transfers", middleware.Require("transfers_money.create"), middleware.Idempotent(), h.Transfer)
	r.Get("/income", middleware.Require("payments.view"), h.Income)
	r.Post("/income", middleware.Require("income.create"), middleware.Idempotent(), h.RecordIncome)
	r.Get("/income-categories", middleware.Require("income.create"), h.IncomeCategories)
	r.Post("/income-categories", middleware.Require("income.create"), h.CreateIncomeCategory)
	r.Post("/drawings", middleware.Require("drawings.create"), middleware.Idempotent(), h.Drawing)
	r.Post("/statutory-payments", middleware.Require("payments.pay"), middleware.Idempotent(), h.Statutory)

	r.Get("/transactions", middleware.Require("transactions.view"), h.Transactions)
	r.Get("/ledger/accounts", middleware.Require("reports.financial"), h.LedgerAccounts)
	r.Get("/ledger/entries", middleware.Require("reports.financial"), h.Entries)
	r.Get("/reports/trial-balance", middleware.Require("reports.financial"), h.Trial)
	r.Get("/periods/lock", middleware.Require("periods.lock"), h.PeriodLock)
	r.Put("/periods/lock", middleware.Require("periods.lock"), h.SetPeriodLock)
}
