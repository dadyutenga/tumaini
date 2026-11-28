package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

// Shared is the business of parts of the API that serve both businesses: finance, staff, reports,
// settings and security. Their records are limited by row security instead.
const Shared = ""

// routeBusiness says which business each part of the API belongs to, by the first segment after
// /api/v1. A test requires every registered route to be listed, so a new one cannot go ungated.
var routeBusiness = map[string]string{
	"goods-receipts": auth.Shop, "purchase-invoices": auth.Shop, "purchase-orders": auth.Shop, "purchase-returns": auth.Shop,
	"quotations": auth.Shop, "sale-returns": auth.Shop, "sales": auth.Shop, "stock": auth.Shop, "stock-adjustments": auth.Shop,
	"stock-entries": auth.Shop, "stock-transfers": auth.Shop, "suppliers": auth.Shop, "tills": auth.Shop,

	"client-documents": auth.Services, "clients": auth.Services, "contractors": auth.Services, "design-jobs": auth.Services,
	"print-orders": auth.Services, "projects": auth.Services, "renewals": auth.Services, "service-catalog": auth.Services,
	"service-costs": auth.Services, "service-invoices": auth.Services, "service-payments": auth.Services,
	"service-quotations": auth.Services, "services": auth.Services, "tasks": auth.Services,

	// The catalogue can be read by anyone with the permission; changing it is shop work.
	"categories": Shared, "products": Shared, "units": Shared,

	"approvals": Shared, "attachments": Shared, "attendance": Shared, "audit-logs": Shared, "auth": Shared, "backups": Shared,
	"branches": Shared, "business": Shared, "business-units": Shared, "commission-rules": Shared, "customers": Shared,
	"dashboard": Shared, "day-closings": Shared, "debts": Shared, "departments": Shared, "drawings": Shared, "employees": Shared,
	"expense-categories": Shared, "expenses": Shared, "exports": Shared, "files": Shared, "holidays": Shared, "income": Shared,
	"income-categories": Shared, "leave-requests": Shared, "leave-types": Shared, "ledger": Shared, "loans": Shared,
	"login-history": Shared, "me": Shared, "money-accounts": Shared, "money-transfers": Shared, "notifications": Shared,
	"pay-adjustments": Shared, "payment-methods": Shared, "payments": Shared, "payroll-runs": Shared, "payslips": Shared,
	"periods": Shared, "permissions": Shared, "petty-cash": Shared, "positions": Shared, "public": Shared,
	"recurring-expenses": Shared, "reports": Shared, "roles": Shared, "salary-components": Shared, "salary-payments": Shared,
	"schedules": Shared, "search": Shared, "settings": Shared, "setup": Shared, "statutory-payments": Shared,
	"tax-rates": Shared, "transactions": Shared, "users": Shared,
}

var catalogue = map[string]bool{"categories": true, "products": true, "units": true}

// wholeParts are worked on as a whole: a payroll run pays the people of both businesses, so seeing
// or changing part of it would mislead. Changing one needs access to every business.
var wholeParts = map[string]bool{"payroll-runs": true}

// everyBusiness is how many businesses there are.
const everyBusiness = 2

func segmentOf(path string) string {
	rest := strings.TrimPrefix(strings.ToLower(path), "/api/v1/")
	segment, _, _ := strings.Cut(rest, "/")
	return segment
}

// RouteBusiness reports the business a request needs ("" when shared) and whether the path is known.
func RouteBusiness(method, path string) (string, bool) {
	segment := segmentOf(path)
	code, known := routeBusiness[segment]
	if catalogue[segment] && !reads(method) {
		code = auth.Shop
	}
	return code, known
}

// RequireBusiness refuses callers who do not work in the business with this code. It is a limit on
// top of role permissions: both are needed.
func RequireBusiness(code string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if p := Principal(c); p == nil || !p.MayUseBusiness(code) {
			return httpx.Forbidden("business_forbidden", "You do not work in the business this belongs to")
		}
		return c.Next()
	}
}

func reads(method string) bool { return method == fiber.MethodGet || method == fiber.MethodHead }

// BusinessRoutes applies RequireBusiness to every part of the API that belongs to one business, and
// settles which businesses the request covers. The X-Business-Unit switcher only filters what is
// read from the shared parts: a change is checked against everything the person works in, so that
// choosing a view can never leave part of a payment or a document unprocessed, and a page of one
// business always shows that business.
func BusinessRoutes() fiber.Handler {
	shop, services := RequireBusiness(auth.Shop), RequireBusiness(auth.Services)
	return func(c fiber.Ctx) error {
		p := Principal(c)
		code, _ := RouteBusiness(c.Method(), c.Path())
		segment := segmentOf(c.Path())
		if p != nil {
			switch {
			case !reads(c.Method()) || wholeParts[segment]:
				p.Active = p.Units
			case code != Shared:
				for _, u := range p.Units {
					if u.Code == code {
						p.Active = []auth.Unit{u}
					}
				}
			}
			if wholeParts[segment] && !reads(c.Method()) && len(p.Units) < everyBusiness {
				return httpx.Forbidden("business_forbidden", "Payroll covers both businesses; only someone who works in both can change it")
			}
		}
		switch code {
		case auth.Shop:
			return shop(c)
		case auth.Services:
			return services(c)
		}
		return c.Next()
	}
}
