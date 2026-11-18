package hr

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/middleware"
)

type LeaveTypeIn struct {
	Name        string          `json:"name"`
	DaysPerYear decimal.Decimal `json:"days_per_year"`
	IsPaid      *bool           `json:"is_paid"`
	IsActive    *bool           `json:"is_active"`
}

func (s *Service) SaveLeaveType(ctx context.Context, p *auth.Principal, id *uuid.UUID, in LeaveTypeIn) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || in.DaysPerYear.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Name and a non-negative days per year are required")
	}
	paid, active := true, true
	if in.IsPaid != nil {
		paid = *in.IsPaid
	}
	if in.IsActive != nil {
		active = *in.IsActive
	}
	out := map[string]any{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if id == nil {
			n := ids.New()
			out["id"] = n
			_, err := tx.Exec(ctx, `INSERT INTO leave_types(id,business_id,name,days_per_year,is_paid,is_active) VALUES($1,$2,$3,$4,$5,$6)`,
				n, p.BusinessID, name, in.DaysPerYear, paid, active)
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE leave_types SET name=$2,days_per_year=$3,is_paid=$4,is_active=$5 WHERE id=$1`, *id, name, in.DaysPerYear, paid, active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Leave type not found")
		}
		out["id"] = *id
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "leave_types_business_id_name_key") {
		return nil, httpx.Conflict("duplicate", "A leave type with that name already exists")
	}
	return out, err
}

type LeaveIn struct {
	EmployeeID  *uuid.UUID `json:"employee_id"`
	LeaveTypeID uuid.UUID  `json:"leave_type_id"`
	StartDate   string     `json:"start_date"`
	EndDate     string     `json:"end_date"`
	Days        string     `json:"days"`
	Reason      string     `json:"reason"`
}

// RequestLeave files a request for the caller, or for another employee when the caller can approve leave.
func (s *Service) RequestLeave(ctx context.Context, p *auth.Principal, in LeaveIn) (map[string]any, error) {
	start, err1 := time.Parse("2006-01-02", in.StartDate)
	end, err2 := time.Parse("2006-01-02", in.EndDate)
	if err1 != nil || err2 != nil || end.Before(start) {
		return nil, httpx.BadRequest("invalid_dates", "Use start and end dates as YYYY-MM-DD, with the end on or after the start")
	}
	days := decimal.NewFromInt(int64(end.Sub(start).Hours()/24) + 1)
	if in.Days != "" {
		d, err := decimal.NewFromString(in.Days)
		if err != nil || !d.IsPositive() || d.GreaterThan(days) {
			return nil, httpx.BadRequest("invalid_days", "Days must be positive and fit inside the dates (use 0.5 for a half day)")
		}
		days = d
	}
	employee := p.EmployeeID
	if in.EmployeeID != nil && (employee == nil || *in.EmployeeID != *employee) {
		if _, ok := p.Can("leave.approve"); !ok {
			return nil, httpx.Forbidden("permission_denied", "You can only request leave for yourself")
		}
		employee = in.EmployeeID
	}
	if employee == nil {
		return nil, httpx.Conflict("employee_required", "Link this login to an employee first")
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT branch_id FROM employees WHERE id=$1 AND status='active'`, *employee).Scan(&branch); err != nil {
			return httpx.NotFound("not_found", "Employee not found")
		}
		if !p.AllowsBranch(branch) {
			return httpx.NotFound("not_found", "Employee not found")
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM leave_types WHERE id=$1`, in.LeaveTypeID).Scan(&active); err != nil || !active {
			return httpx.BadRequest("invalid_leave_type", "Choose an active leave type")
		}
		var overlap bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leave_requests WHERE employee_id=$1 AND status IN ('pending','approved') AND start_date<=$3 AND end_date>=$2)`,
			*employee, in.StartDate, in.EndDate).Scan(&overlap); err != nil {
			return err
		}
		if overlap {
			return httpx.Conflict("leave_overlap", "This employee already has leave in those dates")
		}
		_, err := tx.Exec(ctx, `INSERT INTO leave_requests(id,business_id,employee_id,leave_type_id,start_date,end_date,days,reason,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`,
			id, p.BusinessID, *employee, in.LeaveTypeID, in.StartDate, in.EndDate, days, strings.TrimSpace(in.Reason), p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "pending", "days": days.String()}, nil
}

// DecideLeave moves a pending request to approved, rejected or cancelled.
func (s *Service) DecideLeave(ctx context.Context, p *auth.Principal, id uuid.UUID, action string) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var employee uuid.UUID
		var branch uuid.UUID
		var createdBy *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT r.status,r.employee_id,e.branch_id,r.created_by FROM leave_requests r JOIN employees e ON e.id=r.employee_id WHERE r.id=$1 FOR UPDATE OF r`, id).
			Scan(&status, &employee, &branch, &createdBy); err != nil {
			return httpx.NotFound("not_found", "Leave request not found")
		}
		if !p.AllowsBranch(branch) {
			return httpx.NotFound("not_found", "Leave request not found")
		}
		if status != "pending" {
			return httpx.Conflict("not_pending", "Only pending requests can be changed")
		}
		next := map[string]string{"approve": "approved", "reject": "rejected", "cancel": "cancelled"}[action]
		_, canDecide := p.Can("leave.approve")
		if action == "cancel" {
			own := createdBy != nil && *createdBy == p.UserID
			if !own && !canDecide {
				return httpx.Forbidden("permission_denied", "Only the requester or an approver can cancel")
			}
		} else {
			if !canDecide {
				return httpx.Forbidden("permission_denied", "You cannot decide leave")
			}
			if p.EmployeeID != nil && *p.EmployeeID == employee {
				return httpx.Forbidden("self_approval", "You cannot decide your own leave")
			}
		}
		_, err := tx.Exec(ctx, `UPDATE leave_requests SET status=$2,decided_by=$3,decided_at=now() WHERE id=$1`, id, next, p.UserID)
		return err
	})
}

// LeaveBalance reports allowed, taken (approved) and remaining days per active leave type for the current year.
func (s *Service) LeaveBalance(ctx context.Context, p *auth.Principal, employee uuid.UUID) ([]map[string]any, error) {
	out := []map[string]any{}
	year := s.Clock.Now().Year()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT branch_id FROM employees WHERE id=$1`, employee).Scan(&branch); err != nil || !p.AllowsBranch(branch) {
			return httpx.NotFound("not_found", "Employee not found")
		}
		rows, err := tx.Query(ctx, `
			SELECT lt.id, lt.name, lt.days_per_year, lt.is_paid,
			       COALESCE(SUM(r.days) FILTER (WHERE r.status='approved'),0),
			       COALESCE(SUM(r.days) FILTER (WHERE r.status='pending'),0)
			FROM leave_types lt
			LEFT JOIN leave_requests r ON r.leave_type_id=lt.id AND r.employee_id=$1 AND EXTRACT(YEAR FROM r.start_date)=$2
			WHERE lt.is_active GROUP BY lt.id ORDER BY lt.name`, employee, year)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var allowed, taken, pending decimal.Decimal
			var paid bool
			if err := rows.Scan(&id, &name, &allowed, &paid, &taken, &pending); err != nil {
				return err
			}
			out = append(out, map[string]any{"leave_type_id": id, "name": name, "is_paid": paid, "allowed": allowed.String(),
				"taken": taken.String(), "pending": pending.String(), "remaining": allowed.Sub(taken).String()})
		}
		return rows.Err()
	})
	return out, err
}

func (h *Handler) SaveLeaveType(c fiber.Ctx) error {
	var body LeaveTypeIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	var id *uuid.UUID
	if c.Params("id") != "" {
		v, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		id = &v
	}
	out, err := h.Svc.SaveLeaveType(c.Context(), middleware.Principal(c), id, body)
	if err != nil {
		return err
	}
	if id == nil {
		return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
	}
	return c.JSON(httpx.Data(out))
}

func (h *Handler) RequestLeave(c fiber.Ctx) error {
	var body LeaveIn
	if err := c.Bind().Body(&body); err != nil {
		return httpx.BadRequest("invalid_body", "Request body is not valid JSON")
	}
	out, err := h.Svc.RequestLeave(c.Context(), middleware.Principal(c), body)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(httpx.Data(out))
}

func (h *Handler) decideLeave(action, status string) fiber.Handler {
	return func(c fiber.Ctx) error {
		id, err := httpx.UUIDParam(c, "id")
		if err != nil {
			return err
		}
		if err := h.Svc.DecideLeave(c.Context(), middleware.Principal(c), id, action); err != nil {
			return err
		}
		return c.JSON(httpx.Data(fiber.Map{"status": status}))
	}
}

func (h *Handler) LeaveBalance(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.LeaveBalance(c.Context(), middleware.Principal(c), id)
	if err != nil {
		return err
	}
	return c.JSON(httpx.Data(out))
}

func registerLeave(r fiber.Router, h *Handler) {
	r.Post("/leave-types", middleware.Require("departments.manage"), h.SaveLeaveType)
	r.Put("/leave-types/:id", middleware.Require("departments.manage"), h.SaveLeaveType)
	r.Post("/leave-requests", middleware.Require("leave.request"), h.RequestLeave)
	r.Post("/leave-requests/:id/approve", middleware.Require("leave.approve"), h.decideLeave("approve", "approved"))
	r.Post("/leave-requests/:id/reject", middleware.Require("leave.approve"), h.decideLeave("reject", "rejected"))
	r.Post("/leave-requests/:id/cancel", middleware.RequireAny("leave.request", "leave.approve"), h.decideLeave("cancel", "cancelled"))
	r.Get("/employees/:id/leave-balance", middleware.Require("employees.view"), h.LeaveBalance)
}
