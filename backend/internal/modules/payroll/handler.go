package payroll

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
	"bms/internal/platform/pdf"
)

type Handler struct{ Svc *Service }

var pr = middleware.Principal

func (h *Handler) Create(c fiber.Ctx) error {
	var in RunIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.CreateRun(c.Context(), pr(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) List(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	rows, total, err := h.Svc.ListRuns(c.Context(), pr(c), c.Query("status"), from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) Get(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetRun(c.Context(), pr(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Calculate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, out, err := h.Svc.Calculate(c.Context(), pr(c), id)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(httpx.Data(out))
}

func (h *Handler) PutLines(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	pid, err := httpx.UUIDParam(c, "pid")
	if err != nil {
		return err
	}
	var in LinesIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.PutLines(c.Context(), pr(c), id, pid, in)
	return httpx.OK(c, out, err)
}

func (h *Handler) Submit(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, out, err := h.Svc.Submit(c.Context(), pr(c), id)
	if err != nil {
		return err
	}
	return c.Status(status).JSON(httpx.Data(out))
}

func (h *Handler) Approve(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	status, raw, err := h.Svc.Approve(c.Context(), pr(c), id)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Pay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in PayIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	status, raw, err := h.Svc.Pay(c.Context(), pr(c), id, in)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Reverse(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.BindOptional(c, &in); err != nil {
		return err
	}
	status, raw, err := h.Svc.Reverse(c.Context(), pr(c), id, in.Reason)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) PaymentList(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	run, rows, err := h.Svc.PaymentList(c.Context(), pr(c), id)
	if err != nil {
		return err
	}
	t := export.Table{
		Title:    "Payment list " + asStr(run["number"]),
		Subtitle: asStr(run["period_start"]) + " to " + asStr(run["period_end"]),
		Columns: []export.Col{
			{Key: "employee_no", Label: "Employee no"},
			{Key: "name", Label: "Name"},
			{Key: "pay_method", Label: "Method"},
			{Key: "pay_account", Label: "Account"},
			{Key: "payslip_number", Label: "Payslip"},
			{Key: "amount_due", Label: "Amount due"},
			{Key: "net", Label: "Net"},
		},
		Rows: rows,
	}
	return export.Respond(c, t, map[string]any{"run_id": id}, nil)
}

func (h *Handler) Payslip(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Payslip(c.Context(), pr(c), id)
	if err != nil {
		return err
	}
	if strings.EqualFold(c.Query("format"), "pdf") {
		return export.File(c, "payslip-"+asStr(out["number"])+".pdf", "application/pdf", payslipPDF(out))
	}
	return c.JSON(httpx.Data(out))
}

func payslipPDF(s map[string]any) []byte {
	d := pdf.New()
	d.Heading("Payslip "+asStr(s["number"]), 16)
	d.KV("Employee", asStr(s["employee_name"])+" ("+asStr(s["employee_no"])+")")
	d.KV("Period", asStr(s["period_start"])+" to "+asStr(s["period_end"]))
	d.KV("Run", asStr(s["run_number"]))
	d.Space(6)
	var rows [][]string
	if lines, ok := s["lines"].([]map[string]any); ok {
		for _, l := range lines {
			rows = append(rows, []string{asStr(l["kind"]), asStr(l["label"]), asStr(l["amount"]), asStr(l["loan_balance_after"])})
		}
	}
	d.Table([]string{"Type", "Item", "Amount", "Loan balance after"}, []float64{1.2, 3, 1.4, 1.6}, rows)
	d.Space(6)
	d.KV("Gross", asStr(s["gross"]))
	d.KV("Deductions", asStr(s["total_deductions"]))
	d.KV("Net pay", asStr(s["net"]))
	d.KV("Paid", asStr(s["paid_total"]))
	return d.Bytes()
}

func asStr(v any) string {
	if v == nil {
		return ""
	}
	return export.Cell(v)
}

func (h *Handler) SalaryPayments(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	emp, err := httpx.QueryUUID(c, "employee_id")
	if err != nil {
		return err
	}
	run, err := httpx.QueryUUID(c, "run_id")
	if err != nil {
		return err
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	rows, total, err := h.Svc.SalaryPayments(c.Context(), pr(c), emp, run, from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) Components(c fiber.Ctx) error {
	out, err := h.Svc.ComponentsList(c.Context(), pr(c))
	return httpx.OKList(c, out, err)
}

func (h *Handler) SaveComponent(c fiber.Ctx) error {
	var id *uuid.UUID
	if c.Params("id") != "" {
		v, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		id = &v
	}
	var in ComponentIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.SaveComponent(c.Context(), pr(c), id, in)
	if id == nil {
		return httpx.Created(c, out, err)
	}
	return httpx.OK(c, out, err)
}

func Register(r fiber.Router, h *Handler) {
	req := middleware.Require
	any := middleware.RequireAny

	r.Get("/salary-components", any("hr.compensation.view", "hr.compensation.manage", "hr.payroll.view", "hr.payroll.manage"), h.Components)
	r.Post("/salary-components", req("hr.compensation.manage"), h.SaveComponent)
	r.Put("/salary-components/:id", req("hr.compensation.manage"), h.SaveComponent)

	r.Get("/payroll-runs", any("hr.payroll.view", "hr.payroll.manage", "hr.payroll.approve"), h.List)
	r.Post("/payroll-runs", req("hr.payroll.manage"), h.Create)
	r.Get("/payroll-runs/:id", any("hr.payroll.view", "hr.payroll.manage", "hr.payroll.approve"), h.Get)
	r.Post("/payroll-runs/:id/calculate", req("hr.payroll.manage"), h.Calculate)
	r.Put("/payroll-runs/:id/payslips/:pid/lines", req("hr.payroll.manage"), h.PutLines)
	r.Post("/payroll-runs/:id/submit", req("hr.payroll.manage"), h.Submit)
	r.Post("/payroll-runs/:id/approve", req("hr.payroll.approve"), hrkit.OptionalIdempotent(), h.Approve)
	r.Post("/payroll-runs/:id/pay", req("hr.payroll.approve"), hrkit.OptionalIdempotent(), h.Pay)
	r.Post("/payroll-runs/:id/reverse", any("hr.payroll.approve", "hr.payroll.manage"), hrkit.OptionalIdempotent(), h.Reverse)
	r.Get("/payroll-runs/:id/payment-list", any("hr.payroll.approve", "hr.payroll.view"), h.PaymentList)

	// Payslip view allows hr.payroll.view OR self-service employee (enforced in service + RLS).
	r.Get("/payslips/:id", h.Payslip)
	r.Get("/salary-payments", any("hr.payroll.view", "hr.payroll.approve", "finance.transaction.view"), h.SalaryPayments)
}
