package expenses

import (
	"bms/internal/platform/readapi"
	"github.com/gofiber/fiber/v3"
)

func registerReads(r fiber.Router, h *Handler) {
	readapi.Register(r, h.Svc.DB,
		readapi.Resource{Path: "/expenses", Permission: "expenses.view", Select: "t.*,c.name AS category_name,u.name AS submitter_name", From: "expenses t JOIN expense_categories c ON c.id=t.category_id JOIN users u ON u.id=t.submitted_by", Order: "t.expense_date DESC,t.id", Search: "t.number || ' ' || t.description || ' ' || COALESCE(t.payee,'')", Branch: "t.branch_id", Own: "t.submitted_by", Date: "t.expense_date", Filters: map[string]readapi.Filter{"category_id": {Column: "t.category_id", Kind: "uuid"}, "submitted_by": {Column: "t.submitted_by", Kind: "uuid"}, "status": {Column: "t.status"}}, Detail: true},
		readapi.Resource{Path: "/recurring-expenses", Permission: "recurring_expenses.manage", Select: "t.*", From: "recurring_expenses t", Order: "t.next_run,t.id", Branch: "t.branch_id", Filters: map[string]readapi.Filter{"is_active": {Column: "t.is_active", Kind: "bool"}}},
	)
}
