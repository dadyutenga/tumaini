package hr

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Handler struct{ Svc *Service }

var P = middleware.Principal // P returns the caller; short because every handler uses it.

func (h *Handler) ListEmployees(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	f := EmployeeFilter{Q: c.Query("q"), Status: c.Query("status")}
	var err error
	if f.BranchID, err = httpx.QueryUUID(c, "branch_id"); err != nil {
		return err
	}
	if f.DepartmentID, err = httpx.QueryUUID(c, "department_id"); err != nil {
		return err
	}
	if f.PositionID, err = httpx.QueryUUID(c, "position_id"); err != nil {
		return err
	}
	f.Sort = httpx.SortFrom(c, map[string]string{"name": "e.name", "employee_no": "e.employee_no", "hire_date": "e.hire_date", "created_at": "e.created_at"}, "e.name ASC")
	rows, total, err := h.Svc.ListEmployees(c.Context(), P(c), f, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) CreateEmployee(c fiber.Ctx) error {
	var in EmployeeIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.CreateEmployee(c.Context(), P(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) GetEmployee(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetEmployee(c.Context(), P(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) UpdateEmployee(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in EmployeeIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.UpdateEmployee(c.Context(), P(c), id, in)
	return httpx.OK(c, out, err)
}

func (h *Handler) Terminate(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in TerminateIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.Terminate(c.Context(), P(c), id, in)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateLogin(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in UserIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.CreateLogin(c.Context(), P(c), id, in)
	return httpx.Created(c, out, err)
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
	out, err := h.Svc.Statement(c.Context(), P(c), id, from, to)
	return httpx.OK(c, out, err)
}

func (h *Handler) Components(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.Components(c.Context(), P(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) PutComponents(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		Components []ComponentAssign `json:"components" validate:"dive"`
	}
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.PutComponents(c.Context(), P(c), id, in.Components)
	return httpx.OK(c, out, err)
}

func (h *Handler) LeaveBalance(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	year, _ := strconv.Atoi(c.Query("year"))
	out, err := h.Svc.LeaveBalance(c.Context(), P(c), id, year)
	return httpx.OK(c, out, err)
}

// optID reads :id when present.
func optID(c fiber.Ctx) (*uuid.UUID, error) {
	if c.Params("id") == "" {
		return nil, nil
	}
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func respond(c fiber.Ctx, id *uuid.UUID, out any, err error) error {
	if id == nil {
		return httpx.Created(c, out, err)
	}
	return httpx.OK(c, out, err)
}

func (h *Handler) Departments(c fiber.Ctx) error {
	out, err := h.Svc.Departments(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) SaveDepartment(c fiber.Ctx) error {
	id, err := optID(c)
	if err != nil {
		return err
	}
	var in DepartmentIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.SaveDepartment(c.Context(), P(c), id, in)
	return respond(c, id, out, err)
}

func (h *Handler) DeleteDepartment(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeleteDepartment(c.Context(), P(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) Positions(c fiber.Ctx) error {
	out, err := h.Svc.Positions(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) SavePosition(c fiber.Ctx) error {
	id, err := optID(c)
	if err != nil {
		return err
	}
	var in PositionIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.SavePosition(c.Context(), P(c), id, in)
	return respond(c, id, out, err)
}

func (h *Handler) DeletePosition(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeletePosition(c.Context(), P(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) LeaveTypes(c fiber.Ctx) error {
	out, err := h.Svc.LeaveTypes(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) SaveLeaveType(c fiber.Ctx) error {
	id, err := optID(c)
	if err != nil {
		return err
	}
	var in LeaveTypeIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.SaveLeaveType(c.Context(), P(c), id, in)
	return respond(c, id, out, err)
}

func (h *Handler) LeaveRequests(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	f := LeaveFilter{Status: c.Query("status")}
	var err error
	if f.EmployeeID, err = httpx.QueryUUID(c, "employee_id"); err != nil {
		return err
	}
	if f.From, f.To, err = httpx.Range(c); err != nil {
		return err
	}
	rows, total, err := h.Svc.LeaveRequests(c.Context(), P(c), f, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) RequestLeave(c fiber.Ctx) error {
	var in LeaveIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.RequestLeave(c.Context(), P(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) decideLeave(approve bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		var in struct {
			Note string `json:"note"`
		}
		if err := httpx.BindOptional(c, &in); err != nil {
			return err
		}
		out, err := h.Svc.DecideLeave(c.Context(), P(c), id, approve, in.Note)
		return httpx.OK(c, out, err)
	}
}

func (h *Handler) CancelLeave(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.CancelLeave(c.Context(), P(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) Loans(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	f := LoanFilter{Status: c.Query("status"), Kind: c.Query("kind")}
	var err error
	if f.EmployeeID, err = httpx.QueryUUID(c, "employee_id"); err != nil {
		return err
	}
	rows, total, err := h.Svc.Loans(c.Context(), P(c), f, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) GetLoan(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.GetLoan(c.Context(), P(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) CreateLoan(c fiber.Ctx) error {
	var in LoanIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	status, raw, err := h.Svc.CreateLoan(c.Context(), P(c), in)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Disburse(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in struct {
		MoneyAccountID uuid.UUID `json:"money_account_id" validate:"required"`
	}
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	status, raw, err := h.Svc.Disburse(c.Context(), P(c), id, in.MoneyAccountID)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) Repay(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	var in RepayIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	status, raw, err := h.Svc.Repay(c.Context(), P(c), id, in)
	return httpx.Send(c, status, raw, err)
}

func (h *Handler) CommissionRules(c fiber.Ctx) error {
	out, err := h.Svc.CommissionRules(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) SaveCommissionRule(c fiber.Ctx) error {
	id, err := optID(c)
	if err != nil {
		return err
	}
	var in CommissionIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.SaveCommissionRule(c.Context(), P(c), id, in)
	return respond(c, id, out, err)
}

func (h *Handler) Adjustments(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	emp, err := httpx.QueryUUID(c, "employee_id")
	if err != nil {
		return err
	}
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	rows, total, err := h.Svc.Adjustments(c.Context(), P(c), emp, from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) CreateAdjustment(c fiber.Ctx) error {
	var in AdjustmentIn
	if err := httpx.Bind(c, &in); err != nil {
		return err
	}
	out, err := h.Svc.CreateAdjustment(c.Context(), P(c), in)
	return httpx.Created(c, out, err)
}

func (h *Handler) DeleteAdjustment(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeleteAdjustment(c.Context(), P(c), id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handler) MeEmployee(c fiber.Ctx) error {
	out, err := h.Svc.MeEmployee(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) MeAttendance(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	rows, total, err := h.Svc.MeAttendance(c.Context(), P(c), from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) MeLeave(c fiber.Ctx) error {
	out, err := h.Svc.MeLeave(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) MeLoans(c fiber.Ctx) error {
	out, err := h.Svc.MeLoans(c.Context(), P(c))
	return httpx.OK(c, out, err)
}

func (h *Handler) MePayslips(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	rows, total, err := h.Svc.MePayslips(c.Context(), P(c), page)
	return httpx.Paged(c, rows, page, total, err)
}

func (h *Handler) MePayments(c fiber.Ctx) error {
	page := httpx.PageFrom(c)
	from, to, err := httpx.Range(c)
	if err != nil {
		return err
	}
	rows, total, err := h.Svc.MePayments(c.Context(), P(c), from, to, page)
	return httpx.Paged(c, rows, page, total, err)
}

func Register(r fiber.Router, h *Handler) {
	req := middleware.Require
	r.Get("/employees", req("employees.view"), h.ListEmployees)
	r.Post("/employees", req("employees.manage"), h.CreateEmployee)
	r.Get("/employees/:id", req("employees.view"), h.GetEmployee)
	r.Put("/employees/:id", req("employees.manage"), h.UpdateEmployee)
	r.Post("/employees/:id/terminate", req("employees.manage"), h.Terminate)
	r.Post("/employees/:id/user", req("users.manage"), h.CreateLogin)
	r.Get("/employees/:id/statement", req("employees.view_salary"), h.Statement)
	r.Get("/employees/:id/components", req("components.manage"), h.Components)
	r.Put("/employees/:id/components", req("components.manage"), h.PutComponents)
	r.Get("/employees/:id/documents", req("employees.manage"), h.ListDocuments)
	r.Post("/employees/:id/documents", req("employees.manage"), h.AddDocument)
	r.Delete("/employees/:id/documents/:doc_id", req("employees.manage"), h.DeleteDocument)
	r.Get("/employees/:id/leave-balance", req("employees.view"), h.LeaveBalance)

	r.Get("/departments", req("departments.manage"), h.Departments)
	r.Post("/departments", req("departments.manage"), h.SaveDepartment)
	r.Put("/departments/:id", req("departments.manage"), h.SaveDepartment)
	r.Delete("/departments/:id", req("departments.manage"), h.DeleteDepartment)
	r.Get("/positions", req("departments.manage"), h.Positions)
	r.Post("/positions", req("departments.manage"), h.SavePosition)
	r.Put("/positions/:id", req("departments.manage"), h.SavePosition)
	r.Delete("/positions/:id", req("departments.manage"), h.DeletePosition)
	r.Get("/leave-types", req("departments.manage"), h.LeaveTypes)
	r.Post("/leave-types", req("departments.manage"), h.SaveLeaveType)
	r.Put("/leave-types/:id", req("departments.manage"), h.SaveLeaveType)

	r.Get("/leave-requests", req("leave.approve"), h.LeaveRequests)
	r.Post("/leave-requests", req("leave.request"), h.RequestLeave)
	r.Post("/leave-requests/:id/approve", req("leave.approve"), h.decideLeave(true))
	r.Post("/leave-requests/:id/reject", req("leave.approve"), h.decideLeave(false))
	r.Post("/leave-requests/:id/cancel", middleware.RequireAny("leave.request", "leave.approve"), h.CancelLeave)

	r.Get("/loans", req("loans.view"), h.Loans)
	r.Post("/loans", req("loans.create"), hrkit.OptionalIdempotent(), h.CreateLoan)
	r.Get("/loans/:id", req("loans.view"), h.GetLoan)
	r.Post("/loans/:id/disburse", req("loans.approve"), hrkit.OptionalIdempotent(), h.Disburse)
	r.Post("/loans/:id/repay", req("payments.receive"), middleware.Idempotent(), h.Repay)

	r.Get("/commission-rules", req("components.manage"), h.CommissionRules)
	r.Post("/commission-rules", req("components.manage"), h.SaveCommissionRule)
	r.Put("/commission-rules/:id", req("components.manage"), h.SaveCommissionRule)
	r.Get("/pay-adjustments", req("adjustments.manage"), h.Adjustments)
	r.Post("/pay-adjustments", req("adjustments.manage"), h.CreateAdjustment)
	r.Delete("/pay-adjustments/:id", req("adjustments.manage"), h.DeleteAdjustment)

	r.Get("/me/employee", h.MeEmployee)
	r.Get("/me/attendance", h.MeAttendance)
	r.Get("/me/leave", h.MeLeave)
	r.Get("/me/loans", h.MeLoans)
	r.Get("/me/payslips", h.MePayslips)
	r.Get("/me/payments", h.MePayments)
}
