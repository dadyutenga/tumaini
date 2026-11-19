package reports

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/kernel/audit"
	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct {
	Svc   *Service
	Audit *audit.Service
}

type reportFn func(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error)

// filter reads the common report inputs and limits branches to the caller's
// scope for perm. An explicit branch_id outside the scope is 404.
func filter(c fiber.Ctx, perm string) (Filter, error) {
	p := middleware.Principal(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return Filter{}, err
	}
	for _, k := range []string{"as_of"} {
		if _, err := httpx.QueryDate(c, k); err != nil {
			return Filter{}, err
		}
	}
	for _, k := range []string{"category_id", "product_id"} {
		if _, err := httpx.QueryUUID(c, k); err != nil {
			return Filter{}, err
		}
	}
	f := Filter{From: from, To: to, Query: c.Queries()}
	branch, err := httpx.QueryUUID(c, "branch_id")
	if err != nil {
		return Filter{}, err
	}
	if branch != nil {
		if !p.SeesBranch(perm, *branch) {
			return Filter{}, httpx.NotFound("not_found", "Branch not found")
		}
		f.Branches = []uuid.UUID{*branch}
	} else {
		f.Branches = p.ScopeBranches(perm)
	}
	return f, nil
}

func (h *Handler) run(perm string, fn reportFn) fiber.Handler {
	return func(c fiber.Ctx) error {
		f, err := filter(c, perm)
		if err != nil {
			return err
		}
		p := middleware.Principal(c)
		t, extra, err := fn(c.Context(), p, f)
		if err == nil && h.exporting(c) {
			// FR-SEC-01: a downloaded report is a recorded export.
			if aerr := h.Svc.DB.InTx(c.Context(), p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
				return h.Audit.Record(ctx, tx, p, "report.exported", "report", uuid.Nil, nil,
					map[string]any{"report": t.Title, "path": c.Path(), "format": strings.ToLower(c.Query("format")), "rows": len(t.Rows), "query": f.Query})
			}); aerr != nil {
				return aerr
			}
		}
		return export.Respond(c, t, extra, err)
	}
}

func (h *Handler) exporting(c fiber.Ctx) bool {
	switch strings.ToLower(c.Query("format")) {
	case "", "json":
		return false
	}
	return h.Audit != nil
}

func (h *Handler) breakdown(by string) reportFn {
	return func(ctx context.Context, p *auth.Principal, f Filter) (export.Table, map[string]any, error) {
		return h.Svc.Breakdown(ctx, p, f, by)
	}
}

func (h *Handler) Dashboard(c fiber.Ctx) error {
	f, err := filter(c, "dashboard.view")
	if err != nil {
		return err
	}
	out, err := h.Svc.Dashboard(c.Context(), middleware.Principal(c), f)
	return httpx.OK(c, out, err)
}

func (h *Handler) Me(c fiber.Ctx) error {
	out, err := h.Svc.Me(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/dashboard", middleware.Require("dashboard.view"), h.Dashboard)
	r.Get("/dashboard/me", h.Me)

	sales := func(path string, fn reportFn) {
		r.Get(path, middleware.Require("reports.sales"), h.run("reports.sales", fn))
	}
	sales("/reports/sales/summary", h.Svc.SalesSummary)
	sales("/reports/sales/by-product", h.breakdown("product"))
	sales("/reports/sales/by-category", h.breakdown("category"))
	sales("/reports/sales/by-cashier", h.breakdown("cashier"))
	sales("/reports/sales/by-customer", h.breakdown("customer"))
	sales("/reports/sales/by-payment-method", h.Svc.ByPaymentMethod)
	sales("/reports/tills", h.Svc.Tills)

	fin := func(path string, fn reportFn) {
		r.Get(path, middleware.Require("reports.financial"), h.run("reports.financial", fn))
	}
	fin("/reports/profit-loss", h.Svc.ProfitLoss)
	fin("/reports/cash-flow", h.Svc.CashFlow)
	fin("/reports/balance-sheet", h.Svc.BalanceSheet)
	fin("/reports/tax", h.Svc.Tax)

	r.Get("/reports/expenses", middleware.Require("reports.expenses"), h.run("reports.expenses", h.Svc.Expenses))

	inv := func(path string, fn reportFn) {
		r.Get(path, middleware.Require("reports.inventory"), h.run("reports.inventory", fn))
	}
	inv("/reports/inventory/on-hand", h.Svc.OnHand)
	inv("/reports/inventory/valuation", h.Svc.Valuation)
	inv("/reports/inventory/movements", h.Svc.Movements)
	inv("/reports/inventory/low-stock", h.Svc.LowStock)
	inv("/reports/inventory/dead-stock", h.Svc.DeadStock)
	inv("/reports/inventory/losses", h.Svc.Losses)
	inv("/reports/inventory/expiring", h.Svc.Expiring)

	r.Get("/reports/purchases", middleware.Require("reports.purchases"), h.run("reports.purchases", h.Svc.Purchases))
	r.Get("/reports/customer-debt", middleware.Require("reports.debts"), h.run("reports.debts", h.Svc.CustomerDebt))
	r.Get("/reports/supplier-debt", middleware.Require("reports.debts"), h.run("reports.debts", h.Svc.SupplierDebt))

	pay := func(path string, fn reportFn) {
		r.Get(path, middleware.Require("reports.payroll"), h.run("reports.payroll", fn))
	}
	pay("/reports/payroll/summary", h.Svc.PayrollSummary)
	pay("/reports/payroll/by-department", h.Svc.PayrollByDepartment)
	pay("/reports/payroll/deductions", h.Svc.PayrollDeductions)
	pay("/reports/payroll/loans", h.Svc.PayrollLoans)

	r.Get("/reports/staff-performance", middleware.Require("reports.staff"), h.run("reports.staff", h.Svc.StaffPerformance))
	r.Get("/reports/branches", middleware.Require("reports.branches"), h.run("reports.branches", h.Svc.Branches))
}
