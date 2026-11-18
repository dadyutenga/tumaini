package attendance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/setting"
)

type Service struct {
	DB    *db.DB
	Clock clock.Clock
}

func New(database *db.DB, clk clock.Clock) *Service { return &Service{DB: database, Clock: clk} }

func (s *Service) ClockIn(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	return s.ClockEvent(ctx, p, nil, "in")
}

func (s *Service) ClockEvent(ctx context.Context, p *auth.Principal, employee *uuid.UUID, kind string) (map[string]any, error) {
	if employee == nil {
		employee = p.EmployeeID
	} else if p.EmployeeID == nil || *employee != *p.EmployeeID {
		if _, ok := p.Can("attendance.edit"); !ok {
			return nil, httpx.Forbidden("permission_denied", "You can only clock yourself")
		}
	}
	if employee == nil {
		return nil, httpx.Conflict("employee_required", "Link this login to an employee first")
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		var employeeStatus string
		if err := tx.QueryRow(ctx, `SELECT branch_id,status FROM employees WHERE id=$1 FOR UPDATE`, *employee).Scan(&branch, &employeeStatus); err != nil {
			return httpx.NotFound("not_found", "Employee not found")
		}
		if !p.AllowsBranch(branch) || (p.BranchID != nil && *p.BranchID != branch) {
			return httpx.NotFound("not_found", "Employee not found")
		}
		if employeeStatus != "active" {
			return httpx.Conflict("inactive_employee", "This employee is not active")
		}
		loc, err := time.LoadLocation(setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
		if err != nil {
			return err
		}
		now := s.Clock.Now()
		local := now.In(loc)
		day := local.Format("2006-01-02")
		var first, last *time.Time
		var locked bool
		err = tx.QueryRow(ctx, `SELECT first_in,last_out,is_locked FROM attendance_days WHERE employee_id=$1 AND day=$2 FOR UPDATE`, *employee, day).Scan(&first, &last, &locked)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if locked {
			return httpx.Conflict("day_locked", "This attendance day is locked")
		}
		if kind == "in" && first != nil {
			return httpx.Conflict("already_clocked_in", "You have already clocked in today")
		}
		if kind == "out" && (first == nil || last != nil) {
			return httpx.Conflict("not_clocked_in", "Clock in before clocking out, and only clock out once")
		}
		var startTxt, endTxt *string
		var breakMinutes int
		scheduleErr := tx.QueryRow(ctx, `SELECT to_char(start_time,'HH24:MI'),to_char(end_time,'HH24:MI'),break_minutes FROM work_schedules WHERE is_working AND weekday=$1 AND (employee_id=$2 OR (employee_id IS NULL AND branch_id=$3)) ORDER BY employee_id NULLS LAST LIMIT 1`, int(local.Weekday()), *employee, branch).Scan(&startTxt, &endTxt, &breakMinutes)
		if scheduleErr != nil && scheduleErr != pgx.ErrNoRows {
			return scheduleErr
		}
		atTime := func(text string) time.Time {
			parsed, _ := time.Parse("15:04", text)
			return time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc)
		}
		late := 0
		status := "present"
		worked, overtime := 0, 0
		if kind == "in" {
			if startTxt != nil {
				late = int(local.Sub(atTime(*startTxt)).Minutes())
				if late <= setting.Int(ctx, tx, "payroll.late_grace_minutes", 10) {
					late = 0
				} else {
					status = "late"
				}
			}
			_, err = tx.Exec(ctx, `INSERT INTO attendance_days(id,business_id,branch_id,employee_id,day,status,first_in,late_minutes,needs_review) VALUES($1,$2,$3,$4,$5,$6,$7,$8,true) ON CONFLICT(employee_id,day) DO UPDATE SET first_in=EXCLUDED.first_in,status=EXCLUDED.status,late_minutes=EXCLUDED.late_minutes,needs_review=true`, ids.New(), p.BusinessID, branch, *employee, day, status, now, late)
		} else {
			if now.Before(*first) {
				return httpx.Conflict("invalid_time", "Clock-out must be after clock-in")
			}
			worked = int(now.Sub(*first).Minutes()) - breakMinutes
			if worked < 0 {
				worked = 0
			}
			if startTxt != nil && endTxt != nil {
				end := atTime(*endTxt)
				start := atTime(*startTxt)
				if !end.After(start) {
					end = end.AddDate(0, 0, 1)
				}
				expected := int(end.Sub(start).Minutes()) - breakMinutes
				overtime = worked - expected
				if overtime < 0 {
					overtime = 0
				}
			}
			otStatus := "none"
			if overtime > 0 {
				otStatus = "pending"
			}
			_, err = tx.Exec(ctx, `UPDATE attendance_days SET last_out=$3,worked_minutes=$4,overtime_minutes=$5,overtime_status=$6,needs_review=false WHERE employee_id=$1 AND day=$2`, *employee, day, now, worked, overtime, otStatus)
		}
		if err != nil {
			return err
		}
		source := "self"
		if p.EmployeeID == nil || *p.EmployeeID != *employee {
			source = "manager"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO attendance_events(id,business_id,branch_id,employee_id,type,at,source,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), p.BusinessID, branch, *employee, kind, now, source, p.UserID); err != nil {
			return err
		}
		result = map[string]any{"type": kind, "status": status, "late_minutes": late, "worked_minutes": worked, "overtime_minutes": overtime, "at": now}
		return nil
	})
	return result, err
}

func (s *Service) PutSchedule(ctx context.Context, p *auth.Principal, branch *uuid.UUID, employee *uuid.UUID, weekday int, start, end string) error {
	if branch == nil {
		branch = p.BranchID
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if employee != nil {
			_, _ = tx.Exec(ctx, `DELETE FROM work_schedules WHERE employee_id=$1 AND weekday=$2`, *employee, weekday)
		} else {
			_, _ = tx.Exec(ctx, `DELETE FROM work_schedules WHERE branch_id=$1 AND employee_id IS NULL AND weekday=$2`, *branch, weekday)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO work_schedules (id, business_id, branch_id, employee_id, weekday, is_working, start_time, end_time)
			VALUES ($1,$2,$3,$4,$5,true,$6::time,$7::time)
		`, ids.New(), p.BusinessID, branch, employee, weekday, start, end)
		return err
	})
}

func (s *Service) Mark(ctx context.Context, p *auth.Principal, employee uuid.UUID, day, status string) error {
	if p.BranchID == nil {
		return httpx.BadRequest("branch_required", "Choose a branch first")
	}
	needs := status == "absent" || status == "present" && false
	if status == "absent" {
		needs = true
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, needs_review)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (employee_id, day) DO UPDATE SET status=EXCLUDED.status, needs_review=EXCLUDED.needs_review
		`, ids.New(), p.BusinessID, *p.BranchID, employee, day, status, needs)
		return err
	})
}

func (s *Service) Days(ctx context.Context, p *auth.Principal, employee uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT day::text, status, late_minutes, needs_review, first_in, last_out FROM attendance_days WHERE employee_id=$1 ORDER BY day`, employee)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var day, status string
			var late int
			var review bool
			var in, outAt any
			if err := rows.Scan(&day, &status, &late, &review, &in, &outAt); err != nil {
				return err
			}
			out = append(out, map[string]any{"day": day, "status": status, "late_minutes": late, "needs_review": review, "first_in": in, "last_out": outAt})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}
