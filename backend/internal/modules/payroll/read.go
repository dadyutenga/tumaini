package payroll

import (
	"bms/internal/platform/readapi"
	"github.com/gofiber/fiber/v3"
)

func registerReads(r fiber.Router, h *Handler) {
	readapi.Register(r, h.Svc.DB,
		readapi.Resource{Path: "/salary-components", Permission: "components.manage", Select: "t.*", From: "salary_components t", Order: "t.sort_order,t.name,t.id", Filters: map[string]readapi.Filter{"kind": {Column: "t.kind"}, "is_active": {Column: "t.is_active", Kind: "bool"}}},
		readapi.Resource{Path: "/payroll-runs", Permission: "payroll.view", Select: "t.*", From: "payroll_runs t", Order: "t.period_start DESC,t.id", Branch: "t.branch_id", Date: "t.period_start", Filters: map[string]readapi.Filter{"status": {Column: "t.status"}}, Detail: true, Children: []readapi.Child{{Name: "payslips", SQL: "SELECT p.*,e.name AS employee_name,e.employee_no FROM payslips p JOIN employees e ON e.id=p.employee_id WHERE p.run_id=$1 ORDER BY e.name,p.id"}}},
		readapi.Resource{Path: "/payslips", Permission: "payslips.view", Select: "t.*,e.name AS employee_name,r.period_start,r.period_end", From: "payslips t JOIN employees e ON e.id=t.employee_id JOIN payroll_runs r ON r.id=t.run_id", Order: "r.period_start DESC,t.id", Branch: "e.branch_id", Employee: "t.employee_id", Detail: true, Children: []readapi.Child{{Name: "lines", SQL: "SELECT * FROM payslip_lines WHERE payslip_id=$1 ORDER BY kind,id"}}},
	)
}
