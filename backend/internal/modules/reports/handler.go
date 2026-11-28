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

// filter reads the common report inputs and optional branch_id (location_id).
func filter(c fiber.Ctx) (Filter, error) {
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
	p := middleware.Principal(c)
	if branch != nil {
		if p != nil && !p.AllowsBranch(*branch) {
			return Filter{}, httpx.Forbidden("branch_forbidden", "You do not have access to this branch")
		}
		f.Branches = []uuid.UUID{*branch}
	} else if p != nil && !p.CanAccessAllBranches() && p.BranchID != nil {
		f.Branches = []uuid.UUID{*p.BranchID}
	}
	return f, nil
}

func (h *Handler) run(fn reportFn) fiber.Handler {
	return func(c fiber.Ctx) error {
		f, err := filter(c)
		if err != nil {
			return err
		}
		p, err := forBusiness(middleware.Principal(c), f)
		if err != nil {
			return err
		}
		if h.exporting(c) && !p.Has("reports.export") {
			return httpx.Forbidden("permission_denied", "You do not have permission to export reports")
		}
		t, extra, err := fn(c.Context(), p, f)
		if err == nil && h.exporting(c) {
			if aerr := h.Svc.DB.InActorTx(c.Context(), p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
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
	f, err := filter(c)
	if err != nil {
		return err
	}
	p, err := forBusiness(middleware.Principal(c), f)
	if err != nil {
		return err
	}
	// Without a choice the dashboard shows what the request covers: "all" needs both businesses.
	if f.Q("business") == "" && len(p.Active) == 1 {
		f.Query["business"] = map[string]string{auth.Shop: "shop", auth.Services: "services"}[p.Active[0].Code]
	}
	out, err := h.Svc.Dashboard(c.Context(), p, f)
	return httpx.OK(c, out, err)
}

// forBusiness narrows a report to the business named by ?business=shop or services, which the
// caller must work in. Row security then keeps every figure to it.
func forBusiness(p *auth.Principal, f Filter) (*auth.Principal, error) {
	code := map[string]string{"shop": auth.Shop, "services": auth.Services}[f.Q("business")]
	if code == "" {
		return p, nil
	}
	for _, u := range p.Units {
		if u.Code == code {
			narrowed := *p
			narrowed.Active = []auth.Unit{u}
			return &narrowed, nil
		}
	}
	return nil, httpx.Forbidden("business_forbidden", "You do not work in that business")
}

func (h *Handler) Me(c fiber.Ctx) error {
	out, err := h.Svc.Me(c.Context(), middleware.Principal(c))
	return httpx.OK(c, out, err)
}

func Register(r fiber.Router, h *Handler) {
	r.Get("/dashboard", middleware.RequireAny("dashboard.business.view", "dashboard.operations.view"), h.Dashboard)
	r.Get("/dashboard/me", h.Me)

	ops := middleware.RequireAny("reports.operations.view", "reports.business.view")
	biz := middleware.Require("reports.business.view")
	pay := middleware.RequireAny("reports.business.view", "hr.payroll.view")

	r.Get("/reports/sales/summary", ops, h.run(h.Svc.SalesSummary))
	r.Get("/reports/sales/by-product", ops, h.run(h.breakdown("product")))
	r.Get("/reports/sales/by-category", ops, h.run(h.breakdown("category")))
	r.Get("/reports/sales/by-cashier", ops, h.run(h.breakdown("cashier")))
	r.Get("/reports/sales/by-customer", ops, h.run(h.breakdown("customer")))
	r.Get("/reports/sales/by-payment-method", ops, h.run(h.Svc.ByPaymentMethod))
	r.Get("/reports/tills", ops, h.run(h.Svc.Tills))

	r.Get("/reports/profit-loss", biz, h.run(h.Svc.ProfitLoss))
	r.Get("/reports/cash-flow", biz, h.run(h.Svc.CashFlow))
	r.Get("/reports/balance-sheet", biz, h.run(h.Svc.BalanceSheet))
	r.Get("/reports/tax", biz, h.run(h.Svc.Tax))
	r.Get("/reports/expenses", biz, h.run(h.Svc.Expenses))

	r.Get("/reports/inventory/on-hand", ops, h.run(h.Svc.OnHand))
	r.Get("/reports/inventory/valuation", biz, h.run(h.Svc.Valuation))
	r.Get("/reports/inventory/movements", ops, h.run(h.Svc.Movements))
	r.Get("/reports/inventory/low-stock", ops, h.run(h.Svc.LowStock))
	r.Get("/reports/inventory/dead-stock", ops, h.run(h.Svc.DeadStock))
	r.Get("/reports/inventory/losses", ops, h.run(h.Svc.Losses))
	r.Get("/reports/inventory/expiring", ops, h.run(h.Svc.Expiring))

	r.Get("/reports/purchases", ops, h.run(h.Svc.Purchases))
	r.Get("/reports/customer-debt", ops, h.run(h.Svc.CustomerDebt))
	r.Get("/reports/supplier-debt", ops, h.run(h.Svc.SupplierDebt))

	r.Get("/reports/payroll/summary", pay, h.run(h.Svc.PayrollSummary))
	r.Get("/reports/payroll/by-department", pay, h.run(h.Svc.PayrollByDepartment))
	r.Get("/reports/payroll/deductions", pay, h.run(h.Svc.PayrollDeductions))
	r.Get("/reports/payroll/loans", pay, h.run(h.Svc.PayrollLoans))

	r.Get("/reports/staff-performance", ops, h.run(h.Svc.StaffPerformance))
	r.Get("/reports/branches", ops, h.run(h.Svc.Branches))
}
