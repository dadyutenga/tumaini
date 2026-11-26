package attendance

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Approval: ap, Notify: n, Clock: clk}
}

func removed(what string) error {
	return &httpx.Error{Status: 410, Code: "feature_removed", Message: what + " is not part of the approved model"}
}

func (s *Service) loc(ctx context.Context, tx pgx.Tx) *time.Location {
	return hrkit.Location(setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
}

type emp struct {
	ID       uuid.UUID
	Location *uuid.UUID
	Name     string
	Status   string
}

func (s *Service) employeeFor(ctx context.Context, tx pgx.Tx, p *auth.Principal, id *uuid.UUID) (emp, string, error) {
	source := "self"
	target := p.EmployeeID
	if id != nil && (target == nil || *id != *target) {
		if !p.Has("hr.attendance.manage") {
			return emp{}, "", httpx.Forbidden("permission_denied", "You can only clock yourself in or out")
		}
		target, source = id, "manager"
	}
	if target == nil {
		return emp{}, "", httpx.Conflict("employee_required", "Link this login to an employee before clocking in")
	}
	e, err := s.loadEmp(ctx, tx, p.TenantID, *target)
	if err != nil {
		return emp{}, "", err
	}
	if e.Status != "active" {
		return emp{}, "", httpx.Conflict("invalid_status", "Only an active employee can clock in or out")
	}
	return e, source, nil
}

func (s *Service) loadEmp(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (emp, error) {
	var e emp
	err := tx.QueryRow(ctx, `SELECT id, location_id, full_name::text, status::text FROM app.employees WHERE tenant_id = $1 AND id = $2`, tenantID, id).
		Scan(&e.ID, &e.Location, &e.Name, &e.Status)
	if err != nil {
		return emp{}, httpx.NotFound("not_found", "Employee not found")
	}
	return e, nil
}

func (s *Service) ClockIn(ctx context.Context, p *auth.Principal, employeeID *uuid.UUID) (map[string]any, error) {
	now := s.Clock.Now()
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, source, err := s.employeeFor(ctx, tx, p, employeeID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM app.employees WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, e.ID); err != nil {
			return err
		}
		if source == "self" {
			if err := checkNetwork(ctx, tx, p.IP); err != nil {
				return err
			}
		}
		var openID uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT id FROM app.attendance_records
			WHERE tenant_id = $1 AND employee_id = $2 AND clock_in_at IS NOT NULL AND clock_out_at IS NULL
			ORDER BY work_date DESC LIMIT 1`, p.TenantID, e.ID).Scan(&openID); err == nil {
			return httpx.Conflict("already_clocked_in", "You are already clocked in. Clock out first")
		}
		loc := s.loc(ctx, tx)
		day := now.In(loc).Format(hrkit.DateFmt)
		var closed bool
		if err := tx.QueryRow(ctx, `
			SELECT clock_out_at IS NOT NULL FROM app.attendance_records
			WHERE tenant_id = $1 AND employee_id = $2 AND work_date = $3::date`, p.TenantID, e.ID, day).Scan(&closed); err == nil && closed {
			return httpx.Conflict("already_clocked_out", "This day already has a clock-out. One attendance record is kept per day")
		}
		if err := hrkit.PayrollLocked(ctx, tx, e.ID, day); err != nil {
			return err
		}
		cal, _ := hrkit.LoadCalendar(ctx, tx, &e.ID, uuid.Nil)
		sched, _ := cal.Schedule(hrkit.MustDate(day))
		grace := setting.Int(ctx, tx, "payroll.late_grace_minutes", 10)
		status, late := "present", 0
		if sched.Working && sched.Start != "" {
			st, _ := time.Parse("15:04", sched.Start)
			d := hrkit.MustDate(day)
			due := time.Date(d.Year(), d.Month(), d.Day(), st.Hour(), st.Minute(), 0, 0, loc)
			if m := int(now.In(loc).Sub(due).Minutes()); m > grace {
				status, late = "late", m
			}
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.attendance_records (tenant_id, employee_id, work_date, clock_in_at, status, source, minutes_late, clock_in_ip)
			VALUES ($1,$2,$3::date,$4,$5::app.attendance_status,$6::app.attendance_source,$7,NULLIF($8,'')::inet)
			ON CONFLICT (tenant_id, employee_id, work_date) DO UPDATE
			SET clock_in_at = COALESCE(app.attendance_records.clock_in_at, EXCLUDED.clock_in_at),
			    status = EXCLUDED.status, source = EXCLUDED.source, minutes_late = EXCLUDED.minutes_late,
			    clock_in_ip = COALESCE(app.attendance_records.clock_in_ip, EXCLUDED.clock_in_ip)
			RETURNING id`, p.TenantID, e.ID, day, now, status, source, late, p.IP).Scan(&id); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "attendance.clock_in", "attendance_records", id, nil, map[string]any{"employee_id": e.ID, "at": now, "source": source}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "employee_id": e.ID, "day": day, "status": status, "late_minutes": late, "worked_minutes": 0,
			"overtime_minutes": 0, "needs_review": true, "at": now, "type": "in"}
		return nil
	})
	return out, err
}

func (s *Service) ClockOut(ctx context.Context, p *auth.Principal, employeeID *uuid.UUID) (map[string]any, error) {
	now := s.Clock.Now()
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, source, err := s.employeeFor(ctx, tx, p, employeeID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM app.employees WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, e.ID); err != nil {
			return err
		}
		if source == "self" {
			if err := checkNetwork(ctx, tx, p.IP); err != nil {
				return err
			}
		}
		var id uuid.UUID
		var day, st string
		var clockIn time.Time
		var late int
		err = tx.QueryRow(ctx, `
			SELECT id, work_date::text, status::text, clock_in_at, minutes_late
			FROM app.attendance_records
			WHERE tenant_id = $1 AND employee_id = $2 AND clock_in_at IS NOT NULL AND clock_out_at IS NULL
			ORDER BY work_date DESC LIMIT 1 FOR UPDATE`, p.TenantID, e.ID).Scan(&id, &day, &st, &clockIn, &late)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.Conflict("not_clocked_in", "You are not clocked in")
		}
		if err != nil {
			return err
		}
		if err := hrkit.PayrollLocked(ctx, tx, e.ID, day); err != nil {
			return err
		}
		if !now.After(clockIn) {
			now = clockIn.Add(time.Second)
		}
		worked := int(now.Sub(clockIn).Minutes())
		stdHours := setting.Int(ctx, tx, "payroll.standard_hours", 8)
		planned := stdHours * 60
		ot := 0
		if planned > 0 && worked > planned {
			ot = worked - planned
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.attendance_records SET clock_out_at = $3, overtime_minutes = $4
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, now, ot); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "attendance.clock_out", "attendance_records", id, nil, map[string]any{"employee_id": e.ID, "at": now, "source": source}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "employee_id": e.ID, "day": day, "status": st, "late_minutes": late,
			"worked_minutes": worked, "overtime_minutes": ot, "needs_review": false, "at": now, "type": "out"}
		return nil
	})
	return out, err
}

// ---------- reads ----------

const daySQL = `SELECT d.id, d.employee_id, e.full_name::text AS employee_name, e.employee_no, e.location_id AS branch_id,
	d.work_date AS day, d.status::text AS status, d.clock_in_at AS first_in, d.clock_out_at AS last_out,
	CASE WHEN d.clock_in_at IS NOT NULL AND d.clock_out_at IS NOT NULL
	     THEN GREATEST(0, EXTRACT(EPOCH FROM (d.clock_out_at - d.clock_in_at))::int / 60) ELSE 0 END AS worked_minutes,
	d.minutes_late AS late_minutes, d.overtime_minutes, d.source::text AS source, d.is_corrected,
	(d.clock_in_at IS NOT NULL AND d.clock_out_at IS NULL) AS needs_review
	FROM app.attendance_records d
	JOIN app.employees e ON e.tenant_id = d.tenant_id AND e.id = d.employee_id`

type DayFilter struct {
	EmployeeID *uuid.UUID
	BranchID   *uuid.UUID
	Status     string
	From, To   string
	Review     *bool
	OTStatus   string
	LateOnly   bool
	OTOnly     bool
}

func canViewAll(p *auth.Principal) bool {
	return p.Has("hr.employee.view") || p.Has("hr.attendance.manage") || p.Has("hr.employee.manage")
}

func normalizeStatus(s string) string {
	switch s {
	case "leave":
		return "on_leave"
	case "day_off":
		return "holiday"
	default:
		return s
	}
}

func (s *Service) Days(ctx context.Context, p *auth.Principal, f DayFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("d.tenant_id = $1")
		if !canViewAll(p) {
			q.Add("d.employee_id = ?", derefID(p.EmployeeID))
		}
		q.If(f.EmployeeID != nil, "d.employee_id = ?", f.EmployeeID)
		branch := f.BranchID
		if branch == nil {
			branch = p.BranchID
		}
		q.If(branch != nil && canViewAll(p), "e.location_id = ?", branch)
		if st := normalizeStatus(f.Status); st != "" {
			q.Add("d.status = ?::app.attendance_status", st)
		}
		q.If(f.From != "", "d.work_date >= ?::date", f.From)
		q.If(f.To != "", "d.work_date <= ?::date", f.To)
		if f.Review != nil {
			if *f.Review {
				q.Add("(d.clock_in_at IS NOT NULL AND d.clock_out_at IS NULL)")
			} else {
				q.Add("NOT (d.clock_in_at IS NOT NULL AND d.clock_out_at IS NULL)")
			}
		}
		q.If(f.LateOnly, "d.minutes_late > 0")
		q.If(f.OTOnly, "d.overtime_minutes > 0")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.attendance_records d JOIN app.employees e ON e.tenant_id = d.tenant_id AND e.id = d.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, daySQL+q.Where()+` ORDER BY d.work_date DESC, e.full_name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func derefID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// Today lists active employees with today's attendance state.
func (s *Service) Today(ctx context.Context, p *auth.Principal, branch *uuid.UUID) (map[string]any, error) {
	if branch == nil {
		branch = p.BranchID
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		day := hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
		q := sqlq.New(p.TenantID, day)
		q.Add("e.tenant_id = $1 AND e.status = 'active'")
		q.If(branch != nil, "e.location_id = ?", branch)
		q.If(!canViewAll(p), "e.id = ?", derefID(p.EmployeeID))
		rows, err := hrkit.All(ctx, tx, `
			SELECT e.id AS employee_id, e.full_name::text AS employee_name, e.employee_no, d.id AS day_id,
			       d.clock_in_at AS first_in, d.clock_out_at AS last_out, COALESCE(d.minutes_late, 0) AS late_minutes,
			       CASE WHEN d.id IS NULL THEN 'not_in'
			            WHEN d.clock_in_at IS NOT NULL AND d.clock_out_at IS NULL THEN 'in'
			            WHEN d.clock_in_at IS NOT NULL THEN 'out'
			            ELSE d.status::text END AS state,
			       COALESCE(d.status::text, 'not_in') AS status
			FROM app.employees e
			LEFT JOIN app.attendance_records d ON d.tenant_id = e.tenant_id AND d.employee_id = e.id AND d.work_date = $2::date`+
			q.Where()+` ORDER BY e.full_name`, q.Args...)
		if err != nil {
			return err
		}
		counts := map[string]int{"in": 0, "out": 0, "late": 0, "absent": 0, "on_leave": 0, "not_in": 0}
		for _, r := range rows {
			counts[r["state"].(string)]++
			if r["status"] == "late" {
				counts["late"]++
			}
		}
		out = map[string]any{"day": day, "branch_id": branch, "employees": rows, "counts": counts}
		return nil
	})
	return out, err
}

// Summary returns per-employee attendance counts for a month (YYYY-MM).
func (s *Service) Summary(ctx context.Context, p *auth.Principal, month string, branch *uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if month == "" {
			month = hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format("2006-01")
		}
		first, err := time.Parse("2006-01", month)
		if err != nil {
			return httpx.BadRequest("invalid_query", "month must be YYYY-MM")
		}
		last := first.AddDate(0, 1, -1)
		q := sqlq.New(p.TenantID, first.Format(hrkit.DateFmt), last.Format(hrkit.DateFmt))
		q.Add("e.tenant_id = $1 AND (e.status <> 'terminated' OR e.end_date >= $2::date) AND e.start_date <= $3::date")
		if branch == nil {
			branch = p.BranchID
		}
		q.If(branch != nil && canViewAll(p), "e.location_id = ?", branch)
		q.If(!canViewAll(p), "e.id = ?", derefID(p.EmployeeID))
		rows, err := hrkit.All(ctx, tx, `
			SELECT e.id AS employee_id, e.full_name::text AS employee_name, e.employee_no,
			       count(d.id) FILTER (WHERE d.status IN ('present','late')) AS days_present,
			       count(d.id) FILTER (WHERE d.status = 'late') AS late_count,
			       COALESCE(SUM(d.minutes_late), 0) AS late_minutes,
			       count(d.id) FILTER (WHERE d.status = 'absent') AS days_absent,
			       count(d.id) FILTER (WHERE d.status = 'half_day') AS half_days,
			       count(d.id) FILTER (WHERE d.status = 'on_leave') AS leave_days,
			       count(d.id) FILTER (WHERE d.status = 'holiday') AS holidays,
			       count(d.id) FILTER (WHERE d.clock_in_at IS NOT NULL AND d.clock_out_at IS NULL) AS needs_review,
			       ROUND(COALESCE(SUM(d.overtime_minutes), 0) / 60.0, 2) AS overtime_hours,
			       ROUND(COALESCE(SUM(CASE WHEN d.clock_in_at IS NOT NULL AND d.clock_out_at IS NOT NULL
			                               THEN GREATEST(0, EXTRACT(EPOCH FROM (d.clock_out_at - d.clock_in_at)) / 60.0) ELSE 0 END), 0) / 60.0, 2) AS worked_hours
			FROM app.employees e
			LEFT JOIN app.attendance_records d ON d.tenant_id = e.tenant_id AND d.employee_id = e.id AND d.work_date BETWEEN $2::date AND $3::date`+
			q.Where()+` GROUP BY e.id ORDER BY e.full_name`, q.Args...)
		if err != nil {
			return err
		}
		out = map[string]any{"month": month, "from": first.Format(hrkit.DateFmt), "to": last.Format(hrkit.DateFmt), "employees": rows}
		return nil
	})
	return out, err
}

// ---------- corrections ----------

type DayIn struct {
	Status  string     `json:"status"`
	FirstIn *time.Time `json:"first_in"`
	LastOut *time.Time `json:"last_out"`
	Reason  string     `json:"reason" validate:"required"`
}

// PutDay corrects an attendance record and appends an immutable row to app.attendance_corrections.
func (s *Service) PutDay(ctx context.Context, p *auth.Principal, id uuid.UUID, in DayIn) (map[string]any, error) {
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 5 {
		return nil, httpx.BadRequest("invalid_body", "Correction reason must be at least 5 characters")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var empID uuid.UUID
		var workDate, oldStatus string
		var oldIn, oldOut *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT employee_id, work_date::text, status::text, clock_in_at, clock_out_at
			FROM app.attendance_records WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).
			Scan(&empID, &workDate, &oldStatus, &oldIn, &oldOut); err != nil {
			return httpx.NotFound("not_found", "Attendance record not found")
		}
		if err := hrkit.PayrollLocked(ctx, tx, empID, workDate); err != nil {
			return err
		}
		newStatus := normalizeStatus(strings.TrimSpace(in.Status))
		if newStatus == "" {
			newStatus = oldStatus
		}
		newIn := oldIn
		if in.FirstIn != nil {
			newIn = in.FirstIn
		}
		newOut := oldOut
		if in.LastOut != nil {
			newOut = in.LastOut
		}
		if newIn != nil && newOut != nil && !newOut.After(*newIn) {
			return httpx.BadRequest("invalid_body", "Clock-out must be after clock-in")
		}
		if (newStatus == "present" || newStatus == "late" || newStatus == "half_day") && newIn == nil {
			d := hrkit.MustDate(workDate)
			t := time.Date(d.Year(), d.Month(), d.Day(), 8, 0, 0, 0, s.loc(ctx, tx))
			newIn = &t
		}
		ot := 0
		if newIn != nil && newOut != nil {
			worked := int(newOut.Sub(*newIn).Minutes())
			planned := setting.Int(ctx, tx, "payroll.standard_hours", 8) * 60
			if planned > 0 && worked > planned {
				ot = worked - planned
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.attendance_corrections (tenant_id, attendance_id, old_clock_in_at, new_clock_in_at, old_clock_out_at, new_clock_out_at, old_status, new_status, reason)
			VALUES ($1,$2,$3,$4,$5,$6,$7::app.attendance_status,$8::app.attendance_status,$9)`,
			p.TenantID, id, oldIn, newIn, oldOut, newOut, oldStatus, newStatus, reason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.attendance_records
			SET status = $3::app.attendance_status, clock_in_at = $4, clock_out_at = $5,
			    minutes_late = CASE WHEN $3 <> 'late' THEN 0 ELSE minutes_late END,
			    overtime_minutes = $6, is_corrected = true
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, newStatus, newIn, newOut, ot); err != nil {
			return err
		}
		var err error
		out, err = hrkit.One(ctx, tx, "Attendance record not found", daySQL+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, id)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attendance.corrected", "attendance_records", id, map[string]any{"status": oldStatus}, map[string]any{"status": newStatus, "reason": reason})
	})
	return out, err
}

type EventIn struct {
	EmployeeID uuid.UUID `json:"employee_id" validate:"required"`
	Type       string    `json:"type" validate:"required,oneof=in out"`
	At         time.Time `json:"at" validate:"required"`
	Reason     string    `json:"reason" validate:"required"`
}

// AddEvent records a missed clock-in or clock-out entered by a manager.
func (s *Service) AddEvent(ctx context.Context, p *auth.Principal, in EventIn) (map[string]any, error) {
	if in.At.After(s.Clock.Now()) {
		return nil, httpx.BadRequest("invalid_body", "A missed event cannot be in the future")
	}
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 5 {
		return nil, httpx.BadRequest("invalid_body", "Reason must be at least 5 characters")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, err := s.loadEmp(ctx, tx, p.TenantID, in.EmployeeID)
		if err != nil {
			return err
		}
		day := in.At.In(s.loc(ctx, tx)).Format(hrkit.DateFmt)
		if err := hrkit.PayrollLocked(ctx, tx, e.ID, day); err != nil {
			return err
		}
		var id uuid.UUID
		var oldStatus string
		var oldIn, oldOut *time.Time
		err = tx.QueryRow(ctx, `
			SELECT id, status::text, clock_in_at, clock_out_at
			FROM app.attendance_records WHERE tenant_id = $1 AND employee_id = $2 AND work_date = $3::date FOR UPDATE`,
			p.TenantID, e.ID, day).Scan(&id, &oldStatus, &oldIn, &oldOut)
		if errors.Is(err, pgx.ErrNoRows) {
			if in.Type == "out" {
				return httpx.Conflict("not_clocked_in", "Record a clock-in before adding a clock-out")
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO app.attendance_records (tenant_id, employee_id, work_date, clock_in_at, status, source, is_corrected)
				VALUES ($1,$2,$3::date,$4,'present','manager',true) RETURNING id`,
				p.TenantID, e.ID, day, in.At).Scan(&id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.attendance_corrections (tenant_id, attendance_id, new_clock_in_at, old_status, new_status, reason)
				VALUES ($1,$2,$3,'absent','present',$4)`, p.TenantID, id, in.At, reason); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			newIn, newOut := oldIn, oldOut
			if in.Type == "in" {
				t := in.At
				newIn = &t
			} else {
				t := in.At
				newOut = &t
			}
			if newIn == nil {
				return httpx.Conflict("not_clocked_in", "Record a clock-in before adding a clock-out")
			}
			if newOut != nil && !newOut.After(*newIn) {
				return httpx.BadRequest("invalid_body", "Clock-out must be after clock-in")
			}
			newStatus := oldStatus
			if newStatus == "absent" {
				newStatus = "present"
			}
			ot := 0
			if newOut != nil {
				worked := int(newOut.Sub(*newIn).Minutes())
				planned := setting.Int(ctx, tx, "payroll.standard_hours", 8) * 60
				if planned > 0 && worked > planned {
					ot = worked - planned
				}
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.attendance_corrections (tenant_id, attendance_id, old_clock_in_at, new_clock_in_at, old_clock_out_at, new_clock_out_at, old_status, new_status, reason)
				VALUES ($1,$2,$3,$4,$5,$6,$7::app.attendance_status,$8::app.attendance_status,$9)`,
				p.TenantID, id, oldIn, newIn, oldOut, newOut, oldStatus, newStatus, reason); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE app.attendance_records SET clock_in_at = $3, clock_out_at = $4, status = $5::app.attendance_status, overtime_minutes = $6, is_corrected = true
				WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, newIn, newOut, newStatus, ot); err != nil {
				return err
			}
		}
		if out, err = hrkit.One(ctx, tx, "Attendance record not found", daySQL+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attendance.manual_event", "attendance_records", id, nil, map[string]any{"employee_id": e.ID, "type": in.Type, "at": in.At, "reason": reason})
	})
	return out, err
}

// Mark sets a day's status by hand (absent, half_day, on_leave, holiday, present, late).
func (s *Service) Mark(ctx context.Context, p *auth.Principal, employee uuid.UUID, day, status, note string) (map[string]any, error) {
	status = normalizeStatus(strings.TrimSpace(status))
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmp(ctx, tx, p.TenantID, employee); err != nil {
			return err
		}
		if err := hrkit.PayrollLocked(ctx, tx, employee, day); err != nil {
			return err
		}
		var clockIn *time.Time
		if status == "present" || status == "late" || status == "half_day" {
			d := hrkit.MustDate(day)
			t := time.Date(d.Year(), d.Month(), d.Day(), 8, 0, 0, 0, s.loc(ctx, tx))
			clockIn = &t
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.attendance_records (tenant_id, employee_id, work_date, clock_in_at, status, source)
			VALUES ($1,$2,$3::date,$4,$5::app.attendance_status,'manager')
			ON CONFLICT (tenant_id, employee_id, work_date) DO UPDATE
			SET status = EXCLUDED.status,
			    clock_in_at = COALESCE(app.attendance_records.clock_in_at, EXCLUDED.clock_in_at),
			    minutes_late = CASE WHEN EXCLUDED.status = 'late' THEN app.attendance_records.minutes_late ELSE 0 END,
			    is_corrected = true
			RETURNING id`, p.TenantID, employee, day, clockIn, status).Scan(&id); err != nil {
			return err
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Attendance record not found", daySQL+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attendance.marked", "attendance_records", id, nil, map[string]any{"status": status, "note": note})
	})
	return out, err
}

// DecideOvertime approves (keeps) or rejects (zeroes) overtime minutes on an attendance record.
func (s *Service) DecideOvertime(ctx context.Context, p *auth.Principal, dayID uuid.UUID, approve bool, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var empID uuid.UUID
		var workDate string
		var ot int
		if err := tx.QueryRow(ctx, `
			SELECT employee_id, work_date::text, overtime_minutes
			FROM app.attendance_records WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, dayID).
			Scan(&empID, &workDate, &ot); err != nil {
			return httpx.NotFound("not_found", "Attendance record not found")
		}
		if err := hrkit.PayrollLocked(ctx, tx, empID, workDate); err != nil {
			return err
		}
		if !approve {
			if _, err := tx.Exec(ctx, `UPDATE app.attendance_records SET overtime_minutes = 0, is_corrected = true WHERE tenant_id = $1 AND id = $2`, p.TenantID, dayID); err != nil {
				return err
			}
		}
		var err error
		out, err = hrkit.One(ctx, tx, "Attendance record not found", daySQL+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, dayID)
		if err != nil {
			return err
		}
		action := "overtime.approved"
		if !approve {
			action = "overtime.rejected"
		}
		return s.Audit.Record(ctx, tx, p, action, "attendance_records", dayID, map[string]any{"overtime_minutes": ot}, map[string]any{"approved": approve, "note": note})
	})
	return out, err
}

// ---------- schedules and holidays (not in the approved schema) ----------

type ScheduleDay struct {
	Weekday      int    `json:"weekday" validate:"min=0,max=6"`
	IsWorking    *bool  `json:"is_working"`
	Start        string `json:"start_time"`
	End          string `json:"end_time"`
	BreakMinutes int    `json:"break_minutes" validate:"min=0,max=720"`
}

type ScheduleIn struct {
	BranchID   *uuid.UUID `json:"branch_id"`
	EmployeeID *uuid.UUID `json:"employee_id"`
	ScheduleDay
	Days []ScheduleDay `json:"days" validate:"dive"`
}

type HolidayIn struct {
	Day  string `json:"day" validate:"required,date"`
	Name string `json:"name" validate:"required"`
}

func (s *Service) Schedules(ctx context.Context, p *auth.Principal, branch, employee *uuid.UUID) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) PutSchedule(ctx context.Context, p *auth.Principal, in ScheduleIn) ([]map[string]any, error) {
	return nil, removed("Work schedules")
}

func (s *Service) Holidays(ctx context.Context, p *auth.Principal, year int) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) CreateHoliday(ctx context.Context, p *auth.Principal, in HolidayIn) (map[string]any, error) {
	return nil, removed("Public holidays table")
}

func (s *Service) DeleteHoliday(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return removed("Public holidays table")
}

// ---------- day close job ----------

func (s *Service) CloseDay(ctx context.Context, tenantID uuid.UUID, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if in.Date == "" {
			in.Date = hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).AddDate(0, 0, -1).Format(hrkit.DateFmt)
		}
		day := hrkit.MustDate(in.Date)
		cal, _ := hrkit.LoadCalendar(ctx, tx, nil, uuid.Nil)
		if cal.IsWorking(day) {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.attendance_records (tenant_id, employee_id, work_date, status, source)
				SELECT e.tenant_id, e.id, $2::date, 'absent', 'system'
				FROM app.employees e
				WHERE e.tenant_id = $1 AND e.start_date <= $2::date
				  AND (e.status = 'active' OR (e.status = 'terminated' AND e.end_date >= $2::date))
				ON CONFLICT (tenant_id, employee_id, work_date) DO NOTHING`, tenantID, in.Date); err != nil {
				return err
			}
		}
		var open int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.attendance_records
			WHERE tenant_id = $1 AND work_date = $2::date AND clock_in_at IS NOT NULL AND clock_out_at IS NULL`,
			tenantID, in.Date).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return s.Notify.ToPermission(ctx, tx, tenantID, nil, "hr.attendance.manage", notify.Message{
				Type: "system_alert", Title: "Attendance needs review",
				Body: itoa(open) + " attendance records for " + in.Date + " are missing a clock-out",
				Link: "/attendance?needs_review=true&from=" + in.Date + "&to=" + in.Date,
			})
		}
		return nil
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func checkNetwork(ctx context.Context, tx pgx.Tx, ip string) error {
	if !setting.Bool(ctx, tx, "attendance.restrict_clock_in", false) {
		return nil
	}
	allowed := setting.Strings(ctx, tx, "attendance.allowed_networks")
	if len(allowed) == 0 {
		return nil
	}
	addr := net.ParseIP(ip)
	for _, entry := range allowed {
		if _, n, err := net.ParseCIDR(entry); err == nil {
			if addr != nil && n.Contains(addr) {
				return nil
			}
		} else if a := net.ParseIP(entry); a != nil && addr != nil && a.Equal(addr) {
			return nil
		}
	}
	return httpx.Forbidden("network_not_allowed", "Clock in from the branch network. Ask your manager to clock you in if you are elsewhere.")
}
