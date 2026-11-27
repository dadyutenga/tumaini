package hr

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/sqlq"
)

type LeaveIn struct {
	EmployeeID  *uuid.UUID `json:"employee_id"`
	LeaveTypeID uuid.UUID  `json:"leave_type_id" validate:"required"`
	StartDate   string     `json:"start_date" validate:"required,date"`
	EndDate     string     `json:"end_date" validate:"required,date"`
	Reason      string     `json:"reason"`
}

const leaveSQL = `SELECT lr.id, lr.employee_id, e.full_name::text AS employee_name, e.location_id AS branch_id,
	lr.leave_type_id, lt.name::text AS leave_type, lt.is_paid, lr.start_date, lr.end_date, lr.days,
	lr.reason, lr.status::text AS status, lr.decided_by, lr.decided_at, lr.decision_note, lr.created_by, lr.created_at
	FROM app.leave_requests lr
	JOIN app.employees e ON e.tenant_id = lr.tenant_id AND e.id = lr.employee_id
	JOIN app.leave_types lt ON lt.tenant_id = lr.tenant_id AND lt.id = lr.leave_type_id`

type LeaveFilter struct {
	EmployeeID *uuid.UUID
	Status     string
	From, To   string
}

func normalizeLeaveStatus(s string) string {
	if s == "pending" {
		return "requested"
	}
	return s
}

func (s *Service) LeaveRequests(ctx context.Context, p *auth.Principal, f LeaveFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("lr.tenant_id = $1")
		q.If(p.BranchID != nil, "e.location_id = ?", p.BranchID)
		q.If(f.EmployeeID != nil, "lr.employee_id = ?", f.EmployeeID)
		if st := normalizeLeaveStatus(f.Status); st != "" {
			q.Add("lr.status = ?::app.leave_status", st)
		}
		q.If(f.From != "", "lr.end_date >= ?::date", f.From)
		q.If(f.To != "", "lr.start_date <= ?::date", f.To)
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.leave_requests lr
			JOIN app.employees e ON e.tenant_id = lr.tenant_id AND e.id = lr.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, leaveSQL+q.Where()+` ORDER BY lr.start_date DESC, lr.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// RequestLeave files a leave request in 'requested' status. Any linked employee may request leave
// for themselves; holders of hr.leave.approve may also file on behalf of another employee.
func (s *Service) RequestLeave(ctx context.Context, p *auth.Principal, in LeaveIn) (map[string]any, error) {
	if in.EndDate < in.StartDate {
		return nil, httpx.BadRequest("invalid_body", "The end date cannot be before the start date")
	}
	emp := p.EmployeeID
	if in.EmployeeID != nil && (emp == nil || *in.EmployeeID != *emp) {
		if !p.Has("hr.leave.approve") && !p.Has("hr.employee.manage") {
			return nil, httpx.Forbidden("permission_denied", "You can only request leave for yourself")
		}
		emp = in.EmployeeID
	}
	if emp == nil {
		return nil, httpx.Conflict("employee_required", "To request leave, your account must be linked to an employee record. Ask an Admin to link it.")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, err := s.loadEmployee(ctx, tx, p, *emp)
		if err != nil {
			return err
		}
		if e["status"] != "active" {
			return httpx.Conflict("invalid_status", "Only an active employee can take leave")
		}
		var allowed *decimal.Decimal
		var active bool
		if err := tx.QueryRow(ctx, `SELECT annual_days, status = 'active' FROM app.leave_types WHERE tenant_id = $1 AND id = $2`, p.TenantID, in.LeaveTypeID).Scan(&allowed, &active); err != nil || !active {
			return httpx.BadRequest("invalid_body", "Leave type not found")
		}
		cal, _ := hrkit.LoadCalendar(ctx, tx, emp, uuid.Nil)
		start, _ := hrkit.Date(in.StartDate)
		end, _ := hrkit.Date(in.EndDate)
		if start.Year() != end.Year() {
			return httpx.BadRequest("invalid_body", "Split leave that crosses a year into two requests")
		}
		days := cal.WorkingDays(start, end)
		if days == 0 {
			return httpx.BadRequest("invalid_body", "There are no working days in those dates")
		}
		var overlap int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.leave_requests
			WHERE tenant_id = $1 AND employee_id = $2 AND status IN ('requested','approved')
			  AND start_date <= $4::date AND end_date >= $3::date`,
			p.TenantID, *emp, in.StartDate, in.EndDate).Scan(&overlap); err != nil {
			return err
		}
		if overlap > 0 {
			return httpx.Conflict("duplicate", "There is already leave requested in those dates")
		}
		if allowed != nil && allowed.IsPositive() {
			var used decimal.Decimal
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(days), 0) FROM app.leave_requests
				WHERE tenant_id = $1 AND employee_id = $2 AND leave_type_id = $3
				  AND status IN ('requested','approved') AND EXTRACT(YEAR FROM start_date) = $4`,
				p.TenantID, *emp, in.LeaveTypeID, start.Year()).Scan(&used); err != nil {
				return err
			}
			if used.Add(decimal.NewFromInt(int64(days))).GreaterThan(*allowed) {
				return httpx.Conflict("leave_balance_exceeded", "This request is more than the remaining leave balance")
			}
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.leave_requests (tenant_id, employee_id, leave_type_id, start_date, end_date, days, reason, status)
			VALUES ($1,$2,$3,$4::date,$5::date,$6,NULLIF($7,''),'requested') RETURNING id`,
			p.TenantID, *emp, in.LeaveTypeID, in.StartDate, in.EndDate, days, strings.TrimSpace(in.Reason)).Scan(&id); err != nil {
			return err
		}
		_ = s.Notify.ToApprovers(ctx, tx, p.TenantID, p.BranchID, "hr.leave.approve", nil, p.UserID, notify.Message{
			Type: "approval_waiting", Title: "Leave request awaiting approval",
			Body: "Leave for " + asString(e["name"]) + " from " + in.StartDate + " to " + in.EndDate,
			Link: "/leave/" + id.String(), EntityType: "leave_request", EntityID: &id,
		})
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.tenant_id = $1 AND lr.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "leave.requested", "leave_requests", id, nil, out)
	})
	return out, err
}

// DecideLeave approves or rejects a requested leave. Database trigger tg_leave_decide enforces
// hr.leave.approve and prevents self-decision.
func (s *Service) DecideLeave(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lr, err := s.lockLeave(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if lr["status"] != "requested" {
			return httpx.Conflict("invalid_status", "This leave request is no longer pending")
		}
		status := "rejected"
		if approve {
			status = "approved"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.leave_requests SET status = $3::app.leave_status, decision_note = NULLIF($4,'')
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, status, strings.TrimSpace(note)); err != nil {
			return err
		}
		if approve {
			if err := s.markLeaveDays(ctx, tx, p.TenantID, lr); err != nil {
				return err
			}
		}
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.tenant_id = $1 AND lr.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "leave."+status, "leave_requests", id, map[string]any{"status": "requested"}, map[string]any{"status": status, "note": note}); err != nil {
			return err
		}
		return s.notifyLeaveRequester(ctx, tx, p, lr, "Leave "+status, note)
	})
	return out, err
}

func (s *Service) notifyLeaveRequester(ctx context.Context, tx pgx.Tx, p *auth.Principal, lr map[string]any, title, note string) error {
	empID, err := uuid.Parse(asString(lr["employee_id"]))
	if err != nil {
		return nil
	}
	var userID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT m.user_id FROM app.employees e
		JOIN app.memberships m ON m.tenant_id = e.tenant_id AND m.id = e.membership_id
		WHERE e.tenant_id = $1 AND e.id = $2`, p.TenantID, empID).Scan(&userID); err != nil || userID == p.UserID {
		return nil
	}
	id, _ := uuid.Parse(asString(lr["id"]))
	body := asString(lr["start_date"]) + " to " + asString(lr["end_date"])
	if note != "" {
		body += ". " + note
	}
	return s.Notify.ToUser(ctx, tx, p.TenantID, userID, notify.Message{
		Type: "approval_decided", Title: title, Body: body, Link: "/leave/" + id.String(), EntityType: "leave_request", EntityID: &id,
	})
}

func (s *Service) lockLeave(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (map[string]any, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM app.leave_requests WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, id); err != nil {
		return nil, err
	}
	return hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.tenant_id = $1 AND lr.id = $2`, tenantID, id)
}

func (s *Service) markLeaveDays(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, lr map[string]any) error {
	emp, _ := uuid.Parse(asString(lr["employee_id"]))
	cal, _ := hrkit.LoadCalendar(ctx, tx, &emp, uuid.Nil)
	start := hrkit.MustDate(asString(lr["start_date"]))
	end := hrkit.MustDate(asString(lr["end_date"]))
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !cal.IsWorking(d) {
			continue
		}
		day := d.Format(hrkit.DateFmt)
		if err := hrkit.PayrollLocked(ctx, tx, emp, day); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.attendance_records (tenant_id, employee_id, work_date, status, source)
			VALUES ($1,$2,$3::date,'on_leave','system')
			ON CONFLICT (tenant_id, employee_id, work_date) DO UPDATE SET status = 'on_leave'`,
			tenantID, emp, day); err != nil {
			return err
		}
	}
	return nil
}

// CancelLeave lets the requester or a leave approver withdraw a request.
func (s *Service) CancelLeave(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lr, err := s.lockLeave(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		mine := asString(lr["created_by"]) == p.MembershipID.String() || (p.EmployeeID != nil && asString(lr["employee_id"]) == p.EmployeeID.String())
		if !mine && !p.Has("hr.leave.approve") {
			return httpx.Forbidden("permission_denied", "Only the requester or an approver can cancel this leave")
		}
		switch lr["status"] {
		case "requested":
		case "approved":
			if asString(lr["start_date"]) <= s.today(ctx, tx) {
				return httpx.Conflict("invalid_status", "Leave that has started cannot be cancelled. Correct the attendance days instead")
			}
			emp, _ := uuid.Parse(asString(lr["employee_id"]))
			if _, err := tx.Exec(ctx, `
				DELETE FROM app.attendance_records
				WHERE tenant_id = $1 AND employee_id = $2 AND work_date BETWEEN $3::date AND $4::date
				  AND status = 'on_leave' AND clock_in_at IS NULL`,
				p.TenantID, emp, lr["start_date"], lr["end_date"]); err != nil {
				return err
			}
		default:
			return httpx.Conflict("invalid_status", "This leave request is already decided")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.leave_requests SET status = 'cancelled' WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.tenant_id = $1 AND lr.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "leave.cancelled", "leave_requests", id, map[string]any{"status": lr["status"]}, map[string]any{"status": "cancelled"})
	})
	return out, err
}
