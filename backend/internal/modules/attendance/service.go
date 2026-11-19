package attendance

import (
	"context"
	"encoding/json"
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
	"bms/internal/platform/ids"
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

func (s *Service) loc(ctx context.Context, tx pgx.Tx) *time.Location {
	return hrkit.Location(setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
}

type emp struct {
	ID     uuid.UUID
	Branch uuid.UUID
	Name   string
	Status string
}

// employeeFor resolves whose attendance is changed: the caller's own employee,
// or another employee when the caller holds attendance.edit for their branch.
func (s *Service) employeeFor(ctx context.Context, tx pgx.Tx, p *auth.Principal, id *uuid.UUID) (emp, string, error) {
	source := "self"
	target := p.EmployeeID
	if id != nil && (target == nil || *id != *target) {
		if _, ok := p.Can("attendance.edit"); !ok && !p.IsOwner {
			return emp{}, "", httpx.Forbidden("permission_denied", "You can only clock yourself in or out")
		}
		target, source = id, "manager"
	}
	if target == nil {
		return emp{}, "", httpx.Conflict("employee_required", "Link this login to an employee before clocking in")
	}
	e, err := s.loadEmp(ctx, tx, *target)
	if err != nil {
		return emp{}, "", err
	}
	if source == "manager" && !p.SeesBranch("attendance.edit", e.Branch) {
		return emp{}, "", httpx.NotFound("not_found", "Employee not found")
	}
	if e.Status != "active" {
		return emp{}, "", httpx.Conflict("invalid_status", "Only an active employee can clock in or out")
	}
	return e, source, nil
}

func (s *Service) loadEmp(ctx context.Context, tx pgx.Tx, id uuid.UUID) (emp, error) {
	var e emp
	err := tx.QueryRow(ctx, `SELECT id, branch_id, name, status FROM employees WHERE id=$1`, id).Scan(&e.ID, &e.Branch, &e.Name, &e.Status)
	if err != nil {
		return emp{}, httpx.NotFound("not_found", "Employee not found")
	}
	return e, nil
}

// lastEvent returns the type and time of the employee's latest event.
func lastEvent(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, time.Time) {
	var typ string
	var at time.Time
	_ = tx.QueryRow(ctx, `SELECT type, at FROM attendance_events WHERE employee_id=$1 ORDER BY at DESC, created_at DESC LIMIT 1`, id).Scan(&typ, &at)
	return typ, at
}

func (s *Service) ClockIn(ctx context.Context, p *auth.Principal, employeeID *uuid.UUID) (map[string]any, error) {
	return s.clock(ctx, p, employeeID, "in")
}

func (s *Service) ClockOut(ctx context.Context, p *auth.Principal, employeeID *uuid.UUID) (map[string]any, error) {
	return s.clock(ctx, p, employeeID, "out")
}

func (s *Service) clock(ctx context.Context, p *auth.Principal, employeeID *uuid.UUID, typ string) (map[string]any, error) {
	now := s.Clock.Now()
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, source, err := s.employeeFor(ctx, tx, p, employeeID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM employees WHERE id=$1 FOR UPDATE`, e.ID); err != nil {
			return err
		}
		if source == "self" {
			if err := checkNetwork(ctx, tx, p.IP); err != nil {
				return err
			}
		}
		last, lastAt := lastEvent(ctx, tx, e.ID)
		loc := s.loc(ctx, tx)
		day := now.In(loc).Format(hrkit.DateFmt)
		if typ == "in" && last == "in" {
			return httpx.Conflict("already_clocked_in", "You are already clocked in. Clock out first")
		}
		if typ == "out" {
			if last != "in" {
				return httpx.Conflict("not_clocked_in", "You are not clocked in")
			}
			// The out event closes the day the open clock-in belongs to.
			day = lastAt.In(loc).Format(hrkit.DateFmt)
		}
		if err := checkUnlocked(ctx, tx, e.ID, day); err != nil {
			return err
		}
		branch := e.Branch
		if p.BranchID != nil && source == "self" {
			branch = *p.BranchID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_events (id, business_id, branch_id, employee_id, type, at, source, ip, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::inet,$9)
		`, ids.New(), p.BusinessID, branch, e.ID, typ, now, source, p.IP, p.UserID); err != nil {
			return err
		}
		d, err := s.rebuild(ctx, tx, p, e, branch, day)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "attendance.clock_"+typ, "attendance_day", d.ID, nil, map[string]any{"employee_id": e.ID, "at": now, "source": source}); err != nil {
			return err
		}
		out = map[string]any{"id": d.ID, "employee_id": e.ID, "day": day, "status": d.Status, "late_minutes": d.Late, "worked_minutes": d.Worked,
			"overtime_minutes": d.Overtime, "needs_review": d.Review, "at": now, "type": typ}
		return nil
	})
	return out, err
}

func checkUnlocked(ctx context.Context, tx pgx.Tx, employee uuid.UUID, day string) error {
	var locked bool
	err := tx.QueryRow(ctx, `SELECT is_locked FROM attendance_days WHERE employee_id=$1 AND day=$2`, employee, day).Scan(&locked)
	if err == nil && locked {
		return httpx.Conflict("period_locked", "Attendance for this day is locked by an approved payroll")
	}
	return hrkit.PayrollLocked(ctx, tx, employee, day)
}

type dayResult struct {
	ID       uuid.UUID
	Status   string
	Late     int
	Worked   int
	Overtime int
	Review   bool
}

// rebuild recomputes one attendance day from its events: first in, last out,
// worked, late and overtime minutes. A day whose last event is a clock-in is
// flagged for review; it is never closed by guessing.
func (s *Service) rebuild(ctx context.Context, tx pgx.Tx, p *auth.Principal, e emp, branch uuid.UUID, day string) (dayResult, error) {
	loc := s.loc(ctx, tx)
	d := hrkit.MustDate(day)
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	rows, err := tx.Query(ctx, `SELECT type, at FROM attendance_events WHERE employee_id=$1 AND at >= $2 AND at < $3 ORDER BY at, created_at`, e.ID, start, start.AddDate(0, 0, 1))
	if err != nil {
		return dayResult{}, err
	}
	type ev struct {
		typ string
		at  time.Time
	}
	var evs []ev
	for rows.Next() {
		var x ev
		if err := rows.Scan(&x.typ, &x.at); err != nil {
			rows.Close()
			return dayResult{}, err
		}
		evs = append(evs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return dayResult{}, err
	}
	var firstIn, lastOut *time.Time
	worked, pairs := 0, 0
	var open *time.Time
	for i := range evs {
		x := evs[i]
		if x.typ == "in" {
			if firstIn == nil {
				firstIn = &evs[i].at
			}
			if open == nil {
				open = &evs[i].at
			}
		} else if open != nil {
			worked += int(x.at.Sub(*open).Minutes())
			pairs++
			open = nil
			lastOut = &evs[i].at
		}
	}
	review := open != nil || (firstIn == nil && len(evs) > 0)
	cal, err := hrkit.LoadCalendar(ctx, tx, &e.ID, e.Branch)
	if err != nil {
		return dayResult{}, err
	}
	sched, _ := cal.Schedule(d)
	_, holiday := cal.Holiday(d)
	working := sched.Working && !holiday
	if pairs == 1 && working && sched.Break > 0 && worked > sched.Break {
		worked -= sched.Break
	}
	grace := setting.Int(ctx, tx, "payroll.late_grace_minutes", 10)
	status, late := "present", 0
	if firstIn != nil && working && sched.Start != "" {
		st, _ := time.Parse("15:04", sched.Start)
		due := time.Date(d.Year(), d.Month(), d.Day(), st.Hour(), st.Minute(), 0, 0, loc)
		if m := int(firstIn.In(loc).Sub(due).Minutes()); m > grace {
			status, late = "late", m
		}
	}
	overtime := 0
	if lastOut != nil {
		if !working {
			overtime = worked
		} else if planned := sched.Minutes(); planned > 0 && worked > planned {
			overtime = worked - planned
		}
	}
	var id uuid.UUID
	var oldOT int
	var oldStatus, oldOTStatus string
	err = tx.QueryRow(ctx, `SELECT id, overtime_minutes, status, overtime_status FROM attendance_days WHERE employee_id=$1 AND day=$2 FOR UPDATE`, e.ID, day).Scan(&id, &oldOT, &oldStatus, &oldOTStatus)
	exists := err == nil
	if firstIn == nil && exists {
		status = oldStatus
	}
	otStatus := oldOTStatus
	if !exists || overtime != oldOT {
		otStatus = "none"
		if overtime > 0 {
			otStatus = "pending"
		}
	}
	if !exists {
		id = ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, first_in, last_out, worked_minutes, late_minutes, overtime_minutes, overtime_status, needs_review)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`, id, p.BusinessID, branch, e.ID, day, status, firstIn, lastOut, worked, late, overtime, otStatus, review); err != nil {
			return dayResult{}, err
		}
	} else if _, err := tx.Exec(ctx, `
		UPDATE attendance_days SET status=$2, first_in=$3, last_out=$4, worked_minutes=$5, late_minutes=$6, overtime_minutes=$7, overtime_status=$8, needs_review=$9
		WHERE id=$1
	`, id, status, firstIn, lastOut, worked, late, overtime, otStatus, review); err != nil {
		return dayResult{}, err
	}
	if otStatus == "pending" && (oldOTStatus != "pending" || !exists) {
		pending, err := s.Approval.PendingFor(ctx, tx, "attendance_day", id)
		if err != nil {
			return dayResult{}, err
		}
		if pending == nil {
			if _, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &e.Branch, Kind: "overtime", SubjectType: "attendance_day", SubjectID: id,
				Summary: "Overtime for " + e.Name + " on " + day,
			}); err != nil {
				return dayResult{}, err
			}
		}
	}
	return dayResult{ID: id, Status: status, Late: late, Worked: worked, Overtime: overtime, Review: review}, nil
}

// ---------- reads ----------

const daySQL = `SELECT d.id, d.employee_id, e.name AS employee_name, e.employee_no, d.branch_id, d.day, d.status, d.first_in, d.last_out,
	d.worked_minutes, d.late_minutes, d.overtime_minutes, d.overtime_status, d.needs_review, d.is_locked, d.note
	FROM attendance_days d JOIN employees e ON e.id = d.employee_id`

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

func (s *Service) Days(ctx context.Context, p *auth.Principal, f DayFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("attendance.view"); br != nil {
			q.Add("d.branch_id = ANY(?)", br)
		}
		if own := p.OwnOnly("attendance.view"); own != nil {
			q.Add("d.employee_id = ?", derefID(p.EmployeeID))
		}
		q.If(f.EmployeeID != nil, "d.employee_id = ?", f.EmployeeID)
		q.If(f.BranchID != nil, "d.branch_id = ?", f.BranchID)
		q.If(f.Status != "", "d.status = ?", f.Status)
		q.If(f.From != "", "d.day >= ?", f.From)
		q.If(f.To != "", "d.day <= ?", f.To)
		q.If(f.Review != nil, "d.needs_review = ?", f.Review)
		q.If(f.LateOnly, "d.late_minutes > 0")
		q.If(f.OTOnly, "d.overtime_minutes > 0")
		q.If(f.OTStatus != "", "d.overtime_status = ?", f.OTStatus)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attendance_days d`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, daySQL+q.Where()+` ORDER BY d.day DESC, e.name`+q.Page(page.PerPage, page.Offset), q.Args...)
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

// Today lists every active employee of the branch with today's state.
func (s *Service) Today(ctx context.Context, p *auth.Principal, branch *uuid.UUID) (map[string]any, error) {
	if branch == nil {
		branch = p.BranchID
	}
	if branch == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if !p.SeesBranch("attendance.view", *branch) {
		return nil, httpx.NotFound("not_found", "Branch not found")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		day := hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
		rows, err := hrkit.All(ctx, tx, `
			SELECT e.id AS employee_id, e.name AS employee_name, e.employee_no, d.id AS day_id, d.first_in, d.last_out, d.late_minutes,
			       CASE WHEN d.id IS NULL THEN 'not_in'
			            WHEN d.first_in IS NOT NULL AND d.last_out IS NULL THEN 'in'
			            WHEN d.first_in IS NOT NULL THEN 'out'
			            ELSE d.status END AS state,
			       COALESCE(d.status, 'not_in') AS status
			FROM employees e
			LEFT JOIN attendance_days d ON d.employee_id = e.id AND d.day = $2
			WHERE e.branch_id = $1 AND e.status = 'active'
			ORDER BY e.name
		`, *branch, day)
		if err != nil {
			return err
		}
		counts := map[string]int{"in": 0, "out": 0, "late": 0, "absent": 0, "leave": 0, "not_in": 0}
		for _, r := range rows {
			counts[r["state"].(string)]++
			if r["status"] == "late" {
				counts["late"]++
			}
		}
		out = map[string]any{"day": day, "branch_id": *branch, "employees": rows, "counts": counts}
		return nil
	})
	return out, err
}

// Summary returns per-employee counts for a month (YYYY-MM).
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
		q := sqlq.New(first.Format(hrkit.DateFmt), last.Format(hrkit.DateFmt))
		if br := p.ScopeBranches("attendance.view"); br != nil {
			q.Add("e.branch_id = ANY(?)", br)
		}
		q.If(branch != nil, "e.branch_id = ?", branch)
		if own := p.OwnOnly("attendance.view"); own != nil {
			q.Add("e.id = ?", derefID(p.EmployeeID))
		}
		rows, err := hrkit.All(ctx, tx, `
			SELECT e.id AS employee_id, e.name AS employee_name, e.employee_no,
			       count(d.id) FILTER (WHERE d.status IN ('present','late')) AS days_present,
			       count(d.id) FILTER (WHERE d.status = 'late') AS late_count,
			       COALESCE(SUM(d.late_minutes), 0) AS late_minutes,
			       count(d.id) FILTER (WHERE d.status = 'absent') AS days_absent,
			       count(d.id) FILTER (WHERE d.status = 'half_day') AS half_days,
			       count(d.id) FILTER (WHERE d.status = 'leave') AS leave_days,
			       count(d.id) FILTER (WHERE d.status = 'holiday') AS holidays,
			       count(d.id) FILTER (WHERE d.needs_review) AS needs_review,
			       ROUND(COALESCE(SUM(d.overtime_minutes) FILTER (WHERE d.overtime_status = 'approved'), 0) / 60.0, 2) AS overtime_hours,
			       ROUND(COALESCE(SUM(d.worked_minutes), 0) / 60.0, 2) AS worked_hours
			FROM employees e
			LEFT JOIN attendance_days d ON d.employee_id = e.id AND d.day BETWEEN $1 AND $2
			WHERE (e.status <> 'terminated' OR e.terminated_on >= $1) AND e.hire_date <= $2`+q.And()+`
			GROUP BY e.id ORDER BY e.name
		`, q.Args...)
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
	Status  string     `json:"status" validate:"omitempty,oneof=present late absent half_day leave holiday day_off"`
	FirstIn *time.Time `json:"first_in"`
	LastOut *time.Time `json:"last_out"`
	Reason  string     `json:"reason" validate:"required"`
}

// PutDay corrects a day by hand. Times given replace the computed ones and
// worked minutes are recalculated from them.
func (s *Service) PutDay(ctx context.Context, p *auth.Principal, id uuid.UUID, in DayIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1 FOR UPDATE OF d`, id)
		if err != nil {
			return err
		}
		branch, _ := uuid.Parse(before["branch_id"].(string))
		if !p.SeesBranch("attendance.edit", branch) {
			return httpx.NotFound("not_found", "Attendance day not found")
		}
		if before["is_locked"] == true {
			return httpx.Conflict("period_locked", "This day is locked by an approved payroll")
		}
		if in.FirstIn != nil && in.LastOut != nil && !in.LastOut.After(*in.FirstIn) {
			return httpx.BadRequest("invalid_body", "Clock-out must be after clock-in")
		}
		reason := strings.TrimSpace(in.Reason)
		if _, err := tx.Exec(ctx, `
			UPDATE attendance_days SET
			  status = COALESCE(NULLIF($2,''), status),
			  first_in = COALESCE($3, first_in),
			  last_out = COALESCE($4, last_out),
			  worked_minutes = CASE WHEN COALESCE($4, last_out) IS NOT NULL AND COALESCE($3, first_in) IS NOT NULL
			                        THEN GREATEST(0, EXTRACT(EPOCH FROM (COALESCE($4, last_out) - COALESCE($3, first_in)))::int / 60)
			                        ELSE worked_minutes END,
			  late_minutes = CASE WHEN NULLIF($2,'') IS NOT NULL AND $2 <> 'late' THEN 0 ELSE late_minutes END,
			  needs_review = false,
			  note = $5
			WHERE id = $1
		`, id, in.Status, in.FirstIn, in.LastOut, reason); err != nil {
			return err
		}
		after, err := hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1`, id)
		if err != nil {
			return err
		}
		out = after
		return s.Audit.Record(ctx, tx, p, "attendance.corrected", "attendance_day", id, before, map[string]any{"after": after, "reason": reason})
	})
	return out, err
}

type EventIn struct {
	EmployeeID uuid.UUID `json:"employee_id" validate:"required"`
	Type       string    `json:"type" validate:"required,oneof=in out"`
	At         time.Time `json:"at" validate:"required"`
	Reason     string    `json:"reason" validate:"required"`
}

// AddEvent records a missed clock event entered by a manager. It is stored
// with source 'manual' so it stays distinguishable from real clock-ins.
func (s *Service) AddEvent(ctx context.Context, p *auth.Principal, in EventIn) (map[string]any, error) {
	if in.At.After(s.Clock.Now()) {
		return nil, httpx.BadRequest("invalid_body", "A missed event cannot be in the future")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, err := s.loadEmp(ctx, tx, in.EmployeeID)
		if err != nil {
			return err
		}
		if !p.SeesBranch("attendance.edit", e.Branch) {
			return httpx.NotFound("not_found", "Employee not found")
		}
		day := in.At.In(s.loc(ctx, tx)).Format(hrkit.DateFmt)
		if err := checkUnlocked(ctx, tx, e.ID, day); err != nil {
			return err
		}
		evID := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_events (id, business_id, branch_id, employee_id, type, at, source, note, ip, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,'manual',$7,NULLIF($8,'')::inet,$9)
		`, evID, p.BusinessID, e.Branch, e.ID, in.Type, in.At, strings.TrimSpace(in.Reason), p.IP, p.UserID); err != nil {
			return err
		}
		d, err := s.rebuild(ctx, tx, p, e, e.Branch, day)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE attendance_days SET note = $2 WHERE id=$1`, d.ID, "Manual "+in.Type+": "+strings.TrimSpace(in.Reason)); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1`, d.ID); err != nil {
			return err
		}
		out["event_id"] = evID
		out["source"] = "manual"
		return s.Audit.Record(ctx, tx, p, "attendance.manual_event", "attendance_day", d.ID, nil, map[string]any{"employee_id": e.ID, "type": in.Type, "at": in.At, "reason": in.Reason})
	})
	return out, err
}

// Mark sets a day's status by hand (absent, half day, day off and so on).
// It never changes a locked day.
func (s *Service) Mark(ctx context.Context, p *auth.Principal, employee uuid.UUID, day, status, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		e, err := s.loadEmp(ctx, tx, employee)
		if err != nil {
			return err
		}
		if !p.SeesBranch("attendance.edit", e.Branch) {
			return httpx.NotFound("not_found", "Employee not found")
		}
		if err := checkUnlocked(ctx, tx, employee, day); err != nil {
			return err
		}
		var before map[string]any
		before, _ = hrkit.One(ctx, tx, "", daySQL+` WHERE d.employee_id=$1 AND d.day=$2`, employee, day)
		id := ids.New()
		if err := tx.QueryRow(ctx, `
			INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, needs_review, note)
			VALUES ($1,$2,$3,$4,$5,$6,false,NULLIF($7,''))
			ON CONFLICT (employee_id, day) DO UPDATE SET status=EXCLUDED.status, needs_review=false,
			  late_minutes = CASE WHEN EXCLUDED.status = 'late' THEN attendance_days.late_minutes ELSE 0 END,
			  note=COALESCE(EXCLUDED.note, attendance_days.note)
			RETURNING id
		`, id, p.BusinessID, e.Branch, employee, day, status, strings.TrimSpace(note)).Scan(&id); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attendance.marked", "attendance_day", id, before, out)
	})
	return out, err
}

// ---------- overtime ----------

func (s *Service) DecideOvertime(ctx context.Context, p *auth.Principal, dayID uuid.UUID, approve bool, note string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		day, err := hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1 FOR UPDATE OF d`, dayID)
		if err != nil {
			return err
		}
		branch, _ := uuid.Parse(day["branch_id"].(string))
		if !p.SeesBranch("overtime.approve", branch) {
			return httpx.NotFound("not_found", "Attendance day not found")
		}
		if p.EmployeeID != nil && day["employee_id"] == p.EmployeeID.String() && !p.IsOwner {
			return httpx.Forbidden("cannot_approve_own", "You cannot approve your own overtime")
		}
		status := "rejected"
		if approve {
			status = "approved"
		}
		if err := s.applyOvertime(ctx, tx, p, day, status, note); err != nil {
			return err
		}
		if err := hrkit.ResolveApproval(ctx, tx, p, "attendance_day", dayID, status); err != nil {
			return err
		}
		out, err = hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1`, dayID)
		return err
	})
	return out, err
}

func (s *Service) applyOvertime(ctx context.Context, tx pgx.Tx, p *auth.Principal, day map[string]any, status, note string) error {
	if day["is_locked"] == true {
		return httpx.Conflict("period_locked", "This day is locked by an approved payroll")
	}
	if day["overtime_status"] != "pending" {
		return httpx.Conflict("invalid_status", "There is no pending overtime on this day")
	}
	id, _ := uuid.Parse(day["id"].(string))
	if _, err := tx.Exec(ctx, `UPDATE attendance_days SET overtime_status=$2 WHERE id=$1`, id, status); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "overtime."+status, "attendance_day", id, map[string]any{"overtime_status": "pending"},
		map[string]any{"overtime_status": status, "minutes": day["overtime_minutes"], "note": note})
}

func (s *Service) overtimeHook(status string) func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
		day, err := hrkit.One(ctx, tx, "Attendance day not found", daySQL+` WHERE d.id=$1 FOR UPDATE OF d`, req.SubjectID)
		if err != nil {
			return err
		}
		if status == "rejected" && day["overtime_status"] != "pending" {
			return nil
		}
		return s.applyOvertime(ctx, tx, p, day, status, req.Note)
	}
}

// ---------- schedules and holidays ----------

func (s *Service) Schedules(ctx context.Context, p *auth.Principal, branch, employee *uuid.UUID) ([]map[string]any, error) {
	if branch == nil && employee == nil {
		branch = p.BranchID
	}
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(employee != nil, "w.employee_id = ?", employee)
		q.If(employee == nil && branch != nil, "w.branch_id = ? AND w.employee_id IS NULL", branch)
		if br := p.ScopeBranches("schedules.manage"); br != nil {
			q.Add("COALESCE(w.branch_id, (SELECT e.branch_id FROM employees e WHERE e.id = w.employee_id)) = ANY(?)", br)
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT w.id, w.branch_id, w.employee_id, w.weekday, w.is_working, to_char(w.start_time,'HH24:MI') AS start_time,
			       to_char(w.end_time,'HH24:MI') AS end_time, w.break_minutes
			FROM work_schedules w`+q.Where()+` ORDER BY w.employee_id NULLS FIRST, w.weekday`, q.Args...)
		return err
	})
	return out, err
}

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

// PutSchedule sets one weekday, or a list of weekdays, for a branch or an employee.
func (s *Service) PutSchedule(ctx context.Context, p *auth.Principal, in ScheduleIn) ([]map[string]any, error) {
	days := in.Days
	if len(days) == 0 {
		days = []ScheduleDay{in.ScheduleDay}
	}
	branch := in.BranchID
	if branch == nil && in.EmployeeID == nil {
		branch = p.BranchID
	}
	if branch == nil && in.EmployeeID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch or an employee")
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		scopeBranch := branch
		if in.EmployeeID != nil {
			e, err := s.loadEmp(ctx, tx, *in.EmployeeID)
			if err != nil {
				return err
			}
			scopeBranch = &e.Branch
			branch = nil
		}
		if !p.SeesBranch("schedules.manage", *scopeBranch) {
			return httpx.NotFound("not_found", "Branch not found")
		}
		for _, d := range days {
			working := true
			if d.IsWorking != nil {
				working = *d.IsWorking
			}
			var start, end any
			if working {
				a, err1 := time.Parse("15:04", d.Start)
				b, err2 := time.Parse("15:04", d.End)
				if err1 != nil || err2 != nil || !b.After(a) {
					return &httpx.Error{Status: 422, Code: "validation_failed", Message: "Start and end must be HH:MM with the end after the start", Fields: map[string]string{"start_time": "time", "end_time": "time"}}
				}
				start, end = d.Start, d.End
			}
			if in.EmployeeID != nil {
				if _, err := tx.Exec(ctx, `DELETE FROM work_schedules WHERE employee_id=$1 AND weekday=$2`, *in.EmployeeID, d.Weekday); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `DELETE FROM work_schedules WHERE branch_id=$1 AND employee_id IS NULL AND weekday=$2`, *branch, d.Weekday); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO work_schedules (id, business_id, branch_id, employee_id, weekday, is_working, start_time, end_time, break_minutes)
				VALUES ($1,$2,$3,$4,$5,$6,$7::time,$8::time,$9)
			`, ids.New(), p.BusinessID, branch, in.EmployeeID, d.Weekday, working, start, end, d.BreakMinutes); err != nil {
				return err
			}
		}
		target := uuid.Nil
		if in.EmployeeID != nil {
			target = *in.EmployeeID
		} else {
			target = *branch
		}
		return s.Audit.Record(ctx, tx, p, "schedule.updated", "work_schedule", target, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.Schedules(ctx, p, branch, in.EmployeeID)
}

func (s *Service) Holidays(ctx context.Context, p *auth.Principal, year int) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, `SELECT id, day, name FROM holidays WHERE ($1 = 0 OR EXTRACT(YEAR FROM day) = $1) ORDER BY day`, year)
		return err
	})
	return out, err
}

type HolidayIn struct {
	Day  string `json:"day" validate:"required,date"`
	Name string `json:"name" validate:"required"`
}

func (s *Service) CreateHoliday(ctx context.Context, p *auth.Principal, in HolidayIn) (map[string]any, error) {
	id := ids.New()
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO holidays (id, business_id, day, name) VALUES ($1,$2,$3,$4)`, id, p.BusinessID, in.Day, strings.TrimSpace(in.Name)); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That day is already a holiday")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "holiday.created", "holiday", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "day": in.Day, "name": strings.TrimSpace(in.Name)}, nil
}

func (s *Service) DeleteHoliday(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Holiday not found", `SELECT id, day, name FROM holidays WHERE id=$1`, id)
		if err != nil {
			return err
		}
		var locked int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attendance_days WHERE day=$1 AND is_locked`, before["day"]).Scan(&locked); err != nil {
			return err
		}
		if locked > 0 {
			return httpx.Conflict("period_locked", "That day is inside an approved payroll period")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM holidays WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "holiday.deleted", "holiday", id, before, nil)
	})
}

// ---------- day close job ----------

// CloseDay runs after a business day ends. For each active employee it marks
// a scheduled working day with no attendance as absent, a holiday as holiday,
// and flags any day still waiting for a clock-out. It never invents times.
func (s *Service) CloseDay(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		if in.Date == "" {
			in.Date = hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).AddDate(0, 0, -1).Format(hrkit.DateFmt)
		}
		day := hrkit.MustDate(in.Date)
		rows, err := tx.Query(ctx, `
			SELECT id, branch_id, name FROM employees
			WHERE hire_date <= $1 AND (status = 'active' OR (status = 'terminated' AND terminated_on >= $1))
		`, in.Date)
		if err != nil {
			return err
		}
		var list []emp
		for rows.Next() {
			var e emp
			if err := rows.Scan(&e.ID, &e.Branch, &e.Name); err != nil {
				rows.Close()
				return err
			}
			list = append(list, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		absent, flagged := 0, 0
		for _, e := range list {
			cal, err := hrkit.LoadCalendar(ctx, tx, &e.ID, e.Branch)
			if err != nil {
				return err
			}
			status := ""
			if name, ok := cal.Holiday(day); ok {
				status, _ = "holiday", name
			} else if cal.IsWorking(day) {
				status = "absent"
			}
			if status != "" {
				tag, err := tx.Exec(ctx, `
					INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, note)
					VALUES ($1,$2,$3,$4,$5,$6,'Set by day close')
					ON CONFLICT (employee_id, day) DO NOTHING
				`, ids.New(), businessID, e.Branch, e.ID, in.Date, status)
				if err != nil {
					return err
				}
				if status == "absent" && tag.RowsAffected() == 1 {
					absent++
				}
			}
			tag, err := tx.Exec(ctx, `
				UPDATE attendance_days SET needs_review = true
				WHERE employee_id = $1 AND day <= $2 AND NOT is_locked AND first_in IS NOT NULL AND last_out IS NULL AND NOT needs_review
			`, e.ID, in.Date)
			if err != nil {
				return err
			}
			flagged += int(tag.RowsAffected())
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attendance_days WHERE day = $1 AND needs_review`, in.Date).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return s.Notify.ToPermission(ctx, tx, businessID, nil, "attendance.edit", notify.Message{
				Type: "attendance_review", Title: "Attendance needs review",
				Body: itoa(open) + " attendance records for " + in.Date + " are missing a clock-out",
				Link: "/attendance?needs_review=true&from=" + in.Date + "&to=" + in.Date,
			})
		}
		_ = absent
		_ = flagged
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

// checkNetwork enforces FR-ATT-12. It only applies when the Owner switched the restriction on
// and listed at least one network, so a half-finished setting cannot lock everyone out.
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
