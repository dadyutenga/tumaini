package hr

import (
	"bms/internal/platform/readapi"
	"github.com/gofiber/fiber/v3"
)

func registerReads(r fiber.Router, h *Handler) {
	salary := map[string][]string{"employees.view_salary": {"base_pay", "pay_method", "pay_account"}}
	readapi.Register(r, h.Svc.DB,
		readapi.Resource{Path: "/employees", Permission: "employees.view", Select: "t.*,d.name AS department_name,p.name AS position_name,b.name AS branch_name", From: "employees t JOIN branches b ON b.id=t.branch_id LEFT JOIN departments d ON d.id=t.department_id LEFT JOIN positions p ON p.id=t.position_id", Order: "t.name,t.id", Search: "t.name || ' ' || t.employee_no || ' ' || COALESCE(t.phone,'')", Branch: "t.branch_id", Filters: map[string]readapi.Filter{"status": {Column: "t.status"}, "department_id": {Column: "t.department_id", Kind: "uuid"}}, Sensitive: salary, Detail: true},
		readapi.Resource{Path: "/loans", Permission: "loans.view", Select: "t.*,e.name AS employee_name", From: "employee_loans t JOIN employees e ON e.id=t.employee_id", Order: "t.created_at DESC,t.id", Search: "t.number || ' ' || e.name", Branch: "e.branch_id", Filters: map[string]readapi.Filter{"employee_id": {Column: "t.employee_id", Kind: "uuid"}, "status": {Column: "t.status"}}, Detail: true, Children: []readapi.Child{{Name: "repayments", SQL: "SELECT * FROM loan_repayments WHERE loan_id=$1 ORDER BY created_at,id"}}},
		readapi.Resource{Path: "/departments", Permission: "departments.manage", Select: "t.*", From: "departments t", Order: "t.name,t.id", Search: "t.name"},
		readapi.Resource{Path: "/positions", Permission: "departments.manage", Select: "t.*", From: "positions t", Order: "t.name,t.id", Search: "t.name", Sensitive: map[string][]string{"employees.view_salary": {"default_pay"}}},
		readapi.Resource{Path: "/leave-types", Permission: "departments.manage", Select: "t.*", From: "leave_types t", Order: "t.name,t.id"},
		readapi.Resource{Path: "/leave-requests", Permission: "leave.approve", Select: "t.*,e.name AS employee_name,lt.name AS leave_type_name", From: "leave_requests t JOIN employees e ON e.id=t.employee_id JOIN leave_types lt ON lt.id=t.leave_type_id", Order: "t.start_date DESC,t.id", Branch: "e.branch_id", Date: "t.start_date", Filters: map[string]readapi.Filter{"employee_id": {Column: "t.employee_id", Kind: "uuid"}, "status": {Column: "t.status"}}},
		readapi.Resource{Path: "/commission-rules", Permission: "components.manage", Select: "t.*", From: "commission_rules t LEFT JOIN employees e ON e.id=t.employee_id", Order: "t.id", Branch: "e.branch_id"},
		readapi.Resource{Path: "/pay-adjustments", Permission: "adjustments.manage", Select: "t.*,e.name AS employee_name", From: "pay_adjustments t JOIN employees e ON e.id=t.employee_id", Order: "t.period DESC,t.id", Branch: "e.branch_id", Date: "t.period", Filters: map[string]readapi.Filter{"employee_id": {Column: "t.employee_id", Kind: "uuid"}, "kind": {Column: "t.kind"}}},
	)
}
