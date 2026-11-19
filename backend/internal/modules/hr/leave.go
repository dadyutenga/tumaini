package hr

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/notify"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/sqlq"
)

type LeaveIn struct {
	EmployeeID  *uuid.UUID `json:"employee_id"`
	LeaveTypeID uuid.UUID  `json:"leave_type_id" validate:"required"`
	StartDate   string     `json:"start_date" validate:"required,date"`
	EndDate     string     `json:"end_date" validate:"required,date"`
	Reason      string     `json:"reason"`
}

const leaveSQL = `SELECT lr.id, lr.employee_id, e.name AS employee_name, e.branch_id, lr.leave_type_id, lt.name AS leave_type, lt.is_paid,
	lr.start_date, lr.end_date, lr.days, lr.reason, lr.status, lr.decided_by, lr.decided_at, lr.created_by, lr.created_at
	FROM leave_requests lr JOIN employees e ON e.id = lr.employee_id JOIN leave_types lt ON lt.id = lr.leave_type_id`

type LeaveFilter struct {
	EmployeeID *uuid.UUID
	Status     string
	From, To   string
}

func (s *Service) LeaveRequests(ctx context.Context, p *auth.Principal, f LeaveFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("leave.approve"); br != nil {
			q.Add("e.branch_id = ANY(?)", br)
		}
		q.If(f.EmployeeID != nil, "lr.employee_id = ?", f.EmployeeID)
		q.If(f.Status != "", "lr.status = ?", f.Status)
		q.If(f.From != "", "lr.end_date >= ?", f.From)
		q.If(f.To != "", "lr.start_date <= ?", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM leave_requests lr JOIN employees e ON e.id = lr.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, leaveSQL+q.Where()+` ORDER BY lr.start_date DESC, lr.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// RequestLeave creates a pending request for the caller, or for another
// employee when the caller holds leave.approve.
func (s *Service) RequestLeave(ctx context.Context, p *auth.Principal, in LeaveIn) (map[string]any, error) {
	if in.EndDate < in.StartDate {
		return nil, httpx.BadRequest("invalid_body", "The end date cannot be before the start date")
	}
	emp := p.EmployeeID
	if in.EmployeeID != nil && (emp == nil || *in.EmployeeID != *emp) {
		if _, ok := p.Can("leave.approve"); !ok && !p.IsOwner {
			return nil, httpx.Forbidden("permission_denied", "You can only request leave for yourself")
		}
		emp = in.EmployeeID
	}
	if emp == nil {
		return nil, httpx.Conflict("employee_required", "Link this login to an employee before requesting leave")
	}
	id := ids.New()
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		key := "leave.request"
		if *emp != derefID(p.EmployeeID) {
			key = "leave.approve"
		}
		e, err := s.loadEmployee(ctx, tx, p, key, *emp)
		if err != nil {
			return err
		}
		if e["status"] != "active" {
			return httpx.Conflict("invalid_status", "Only an active employee can take leave")
		}
		var allowed decimal.Decimal
		var active bool
		if err := tx.QueryRow(ctx, `SELECT days_per_year, is_active FROM leave_types WHERE id=$1`, in.LeaveTypeID).Scan(&allowed, &active); err != nil || !active {
			return httpx.BadRequest("invalid_body", "Leave type not found")
		}
		branch, _ := uuid.Parse(asString(e["branch_id"]))
		cal, err := hrkit.LoadCalendar(ctx, tx, emp, branch)
		if err != nil {
			return err
		}
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
			SELECT count(*) FROM leave_requests WHERE employee_id=$1 AND status IN ('pending','approved')
			AND start_date <= $3 AND end_date >= $2
		`, *emp, in.StartDate, in.EndDate).Scan(&overlap); err != nil {
			return err
		}
		if overlap > 0 {
			return httpx.Conflict("duplicate", "There is already leave requested in those dates")
		}
		if allowed.IsPositive() {
			var used decimal.Decimal
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(days),0) FROM leave_requests WHERE employee_id=$1 AND leave_type_id=$2
				AND status IN ('pending','approved') AND EXTRACT(YEAR FROM start_date) = $3
			`, *emp, in.LeaveTypeID, start.Year()).Scan(&used); err != nil {
				return err
			}
			if used.Add(decimal.NewFromInt(int64(days))).GreaterThan(allowed) {
				return httpx.Conflict("leave_balance_exceeded", "This request is more than the remaining leave balance")
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO leave_requests (id, business_id, employee_id, leave_type_id, start_date, end_date, days, reason, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)
		`, id, p.BusinessID, *emp, in.LeaveTypeID, in.StartDate, in.EndDate, days, strings.TrimSpace(in.Reason), p.UserID); err != nil {
			return err
		}
		if _, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: &branch, Kind: "leave", SubjectType: "leave_request", SubjectID: id,
			Summary: "Leave for " + asString(e["name"]) + " from " + in.StartDate + " to " + in.EndDate,
		}); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "leave.requested", "leave_request", id, nil, out)
	})
	return out, err
}

func derefID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// DecideLeave approves or rejects from the leave screen.
func (s *Service) DecideLeave(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lr, err := s.lockLeave(ctx, tx, p, id, "leave.approve")
		if err != nil {
			return err
		}
		if asString(lr["created_by"]) == p.UserID.String() && !p.IsOwner {
			return httpx.Forbidden("cannot_approve_own", "You cannot decide your own leave request")
		}
		if p.EmployeeID != nil && asString(lr["employee_id"]) == p.EmployeeID.String() && !p.IsOwner {
			return httpx.Forbidden("cannot_approve_own", "You cannot decide your own leave request")
		}
		status := "rejected"
		if approve {
			status = "approved"
		}
		if err := s.applyLeave(ctx, tx, p, lr, status); err != nil {
			return err
		}
		if err := hrkit.ResolveApproval(ctx, tx, p, "leave_request", id, status); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.id=$1`, id); err != nil {
			return err
		}
		return s.notifyRequester(ctx, tx, p, lr, "Leave "+status, note)
	})
	return out, err
}

func (s *Service) notifyRequester(ctx context.Context, tx pgx.Tx, p *auth.Principal, lr map[string]any, title, note string) error {
	by, err := uuid.Parse(asString(lr["created_by"]))
	if err != nil || by == p.UserID {
		return nil
	}
	id, _ := uuid.Parse(asString(lr["id"]))
	body := asString(lr["start_date"]) + " to " + asString(lr["end_date"])
	if note != "" {
		body += ". " + note
	}
	return s.Notify.ToUser(ctx, tx, p.BusinessID, by, notify.Message{Type: "leave_decision", Title: title, Body: body, Link: "/leave/" + id.String(), EntityType: "leave_request", EntityID: &id})
}

func (s *Service) lockLeave(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, key string) (map[string]any, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM leave_requests WHERE id=$1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	lr, err := hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.id=$1`, id)
	if err != nil {
		return nil, err
	}
	branch, _ := uuid.Parse(asString(lr["branch_id"]))
	if key != "" && !p.SeesBranch(key, branch) && asString(lr["created_by"]) != p.UserID.String() {
		return nil, httpx.NotFound("not_found", "Leave request not found")
	}
	return lr, nil
}

// applyLeave moves a pending request to status and, when approved, marks the
// working days in its range as leave in attendance.
func (s *Service) applyLeave(ctx context.Context, tx pgx.Tx, p *auth.Principal, lr map[string]any, status string) error {
	if lr["status"] != "pending" {
		return httpx.Conflict("invalid_status", "This leave request is no longer pending")
	}
	id, _ := uuid.Parse(asString(lr["id"]))
	if _, err := tx.Exec(ctx, `UPDATE leave_requests SET status=$2, decided_by=$3, decided_at=now() WHERE id=$1`, id, status, p.UserID); err != nil {
		return err
	}
	if status == "approved" {
		if err := s.markLeaveDays(ctx, tx, p, lr); err != nil {
			return err
		}
	}
	return s.Audit.Record(ctx, tx, p, "leave."+status, "leave_request", id, map[string]any{"status": "pending"}, map[string]any{"status": status})
}

func (s *Service) markLeaveDays(ctx context.Context, tx pgx.Tx, p *auth.Principal, lr map[string]any) error {
	emp, _ := uuid.Parse(asString(lr["employee_id"]))
	branch, _ := uuid.Parse(asString(lr["branch_id"]))
	cal, err := hrkit.LoadCalendar(ctx, tx, &emp, branch)
	if err != nil {
		return err
	}
	start := hrkit.MustDate(asString(lr["start_date"]))
	end := hrkit.MustDate(asString(lr["end_date"]))
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if !cal.IsWorking(d) {
			continue
		}
		var locked bool
		err := tx.QueryRow(ctx, `SELECT is_locked FROM attendance_days WHERE employee_id=$1 AND day=$2`, emp, d.Format(hrkit.DateFmt)).Scan(&locked)
		if err == nil && locked {
			return httpx.Conflict("period_locked", "Attendance for "+d.Format(hrkit.DateFmt)+" is locked by an approved payroll")
		}
		if err := hrkit.PayrollLocked(ctx, tx, emp, d.Format(hrkit.DateFmt)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, note)
			VALUES ($1,$2,$3,$4,$5,'leave',$6)
			ON CONFLICT (employee_id, day) DO UPDATE SET status='leave', needs_review=false, note=EXCLUDED.note
		`, ids.New(), p.BusinessID, branch, emp, d.Format(hrkit.DateFmt), "Leave: "+asString(lr["leave_type"])); err != nil {
			return err
		}
	}
	return nil
}

// CancelLeave lets the requester (or a leave approver) withdraw a request.
// Cancelling approved leave that has started is not allowed; future approved
// days are released.
func (s *Service) CancelLeave(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lr, err := s.lockLeave(ctx, tx, p, id, "leave.approve")
		if err != nil {
			return err
		}
		mine := asString(lr["created_by"]) == p.UserID.String() || (p.EmployeeID != nil && asString(lr["employee_id"]) == p.EmployeeID.String())
		_, approver := p.Can("leave.approve")
		if !mine && !approver && !p.IsOwner {
			return httpx.Forbidden("permission_denied", "Only the requester or an approver can cancel this leave")
		}
		switch lr["status"] {
		case "pending":
		case "approved":
			if asString(lr["start_date"]) <= s.today(ctx, tx) {
				return httpx.Conflict("invalid_status", "Leave that has started cannot be cancelled. Correct the attendance days instead")
			}
			emp, _ := uuid.Parse(asString(lr["employee_id"]))
			var locked int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM attendance_days WHERE employee_id=$1 AND day BETWEEN $2 AND $3 AND is_locked`, emp, lr["start_date"], lr["end_date"]).Scan(&locked); err != nil {
				return err
			}
			if locked > 0 {
				return httpx.Conflict("period_locked", "Those days are locked by an approved payroll")
			}
			if _, err := tx.Exec(ctx, `
				DELETE FROM attendance_days d WHERE d.employee_id=$1 AND d.day BETWEEN $2 AND $3 AND d.status='leave'
				AND NOT EXISTS (SELECT 1 FROM attendance_events ev WHERE ev.employee_id = d.employee_id AND ev.at::date = d.day)
			`, emp, lr["start_date"], lr["end_date"]); err != nil {
				return err
			}
		default:
			return httpx.Conflict("invalid_status", "This leave request is already decided")
		}
		if _, err := tx.Exec(ctx, `UPDATE leave_requests SET status='cancelled', decided_by=$2, decided_at=now() WHERE id=$1`, id, p.UserID); err != nil {
			return err
		}
		if err := hrkit.ResolveApproval(ctx, tx, p, "leave_request", id, "cancelled"); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Leave request not found", leaveSQL+` WHERE lr.id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "leave.cancelled", "leave_request", id, map[string]any{"status": lr["status"]}, map[string]any{"status": "cancelled"})
	})
	return out, err
}

// Approval inbox hooks.
func (s *Service) onLeaveApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	lr, err := s.lockLeave(ctx, tx, p, req.SubjectID, "")
	if err != nil {
		return err
	}
	return s.applyLeave(ctx, tx, p, lr, "approved")
}

func (s *Service) onLeaveRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	lr, err := s.lockLeave(ctx, tx, p, req.SubjectID, "")
	if err != nil {
		return err
	}
	status := "rejected"
	if req.Status == "cancelled" {
		status = "cancelled"
	}
	if lr["status"] != "pending" {
		return nil
	}
	id := req.SubjectID
	if _, err := tx.Exec(ctx, `UPDATE leave_requests SET status=$2, decided_by=$3, decided_at=now() WHERE id=$1`, id, status, p.UserID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "leave."+status, "leave_request", id, map[string]any{"status": "pending"}, map[string]any{"status": status})
}
