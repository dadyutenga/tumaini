package customers

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/shopspring/decimal"

	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
	"bms/internal/platform/pdf"
)

type Handler struct{ Svc *Service }

func (h *Handler) List(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	f := Filter{Q: c.Query("q"), Active: httpx.QueryBool(c, "is_active")}
	if b := httpx.QueryBool(c, "has_balance"); b != nil {
		f.HasBalance = *b
	}
	list, total, err := h.Svc.List(c.Context(), middleware.Principal(c), f, page)
	return httpx.Paged(c, list, page, total, err)
}

func (h *Handler) Create(c fiber.Ctx) error {
	var body Input
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	cust, err := h.Svc.Create(c.Context(), middleware.Principal(c), body)
	return httpx.Created(c, cust, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	cust, err := h.Svc.Get(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, cust, err)
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
	cust, err := h.Svc.Update(c.Context(), middleware.Principal(c), id, body)
	return httpx.OK(c, cust, err)
}

func (h *Handler) Credit(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body struct {
		CreditLimit decimal.Decimal `json:"credit_limit" validate:"dgte0"`
		Days        int             `json:"payment_terms_days" validate:"gte=0,lte=365"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	if err := h.Svc.CreditLimit(c.Context(), middleware.Principal(c), id, body.CreditLimit, body.Days); err != nil {
		return err
	}
	return h.Get(c)
}

func (h *Handler) Deactivate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	cust, err := h.Svc.Deactivate(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, cust, err)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body PayIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.Receive(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) WriteOff(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var body WriteOffIn
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	status, raw, err := h.Svc.WriteOff(c.Context(), middleware.Principal(c), id, body)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Unpaid(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Unpaid(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
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
	data, err := h.Svc.Statement(c.Context(), middleware.Principal(c), id, from, to)
	if err != nil {
		return err
	}
	switch strings.ToLower(c.Query("format")) {
	case "", "json":
		return c.JSON(httpx.Data(data))
	case "pdf":
		return export.File(c, "statement.pdf", "application/pdf", statementPDF(data))
	default:
		return httpx.BadRequest("invalid_query", "format must be json or pdf")
	}
}

func statementPDF(m map[string]any) []byte {
	cust := m["customer"].(Customer)
	d := pdf.New()
	d.Heading("Customer statement", 15)
	d.KV("Customer", cust.Name)
	if cust.Phone != nil {
		d.KV("Phone", *cust.Phone)
	}
	d.KV("Period", export.Cell(m["from"])+" to "+export.Cell(m["to"]))
	d.KV("Opening balance", export.Cell(m["opening_balance"]))
	d.Space(6)
	var rows [][]string
	for _, ln := range m["lines"].([]map[string]any) {
		rows = append(rows, []string{export.Cell(ln["date"]), export.Cell(ln["number"]), export.Cell(ln["description"]), export.Cell(ln["debit"]), export.Cell(ln["credit"]), export.Cell(ln["balance"])})
	}
	d.Table([]string{"Date", "Number", "Description", "Debit", "Credit", "Balance"}, []float64{1.3, 2, 3.5, 1.4, 1.4, 1.4}, rows)
	d.Rule()
	d.KV("Closing balance", export.Cell(m["closing_balance"]))
	return d.Bytes()
}

func (h *Handler) Debts(c fiber.Ctx) error {
	list, err := h.Svc.Debts(c.Context(), middleware.Principal(c), "customers.view")
	return httpx.OK(c, list, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/customers", middleware.RequireAny("shop.customer.view_all", "shop.customer.manage"), h.List)
	r.Post("/customers", middleware.Require("shop.customer.manage"), h.Create)
	r.Get("/customers/debts", middleware.Require("finance.debt.view"), h.Debts)
	r.Get("/customers/:id", middleware.RequireAny("shop.customer.view_all", "shop.customer.manage"), h.Get)
	r.Put("/customers/:id", middleware.Require("shop.customer.manage"), h.Update)
	r.Put("/customers/:id/credit-limit", middleware.Require("finance.debt.view"), h.Credit)
	r.Post("/customers/:id/deactivate", middleware.Require("shop.customer.manage"), h.Deactivate)
	r.Get("/customers/:id/unpaid", middleware.RequireAny("shop.customer.view_all", "shop.customer.manage"), h.Unpaid)
	r.Post("/customers/:id/payments", middleware.Require("finance.payment.receive"), middleware.Idempotent(), h.Pay)
	r.Post("/customers/:id/write-off", middleware.Require("finance.transaction.correct"), middleware.Idempotent(), h.WriteOff)
	r.Get("/customers/:id/statement", middleware.Require("finance.debt.view"), h.Statement)
}
