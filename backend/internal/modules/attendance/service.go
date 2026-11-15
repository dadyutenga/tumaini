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
	if p.EmployeeID == nil {
		return nil, httpx.Conflict("employee_required", "Link this login to an employee before clocking in")
	}
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	now := s.Clock.Now()
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO attendance_events (id, business_id, branch_id, employee_id, type, at, source, created_by)
			VALUES ($1,$2,$3,$4,'in',$5,'self',$6)
		`, ids.New(), p.BusinessID, *p.BranchID, *p.EmployeeID, now, p.UserID); err != nil {
			return err
		}
		loc, _ := time.LoadLocation(setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
		local := now.In(loc)
		grace := setting.Int(ctx, tx, "payroll.late_grace_minutes", 10)
		var start time.Time
		var has bool
		var startTxt *string
		err := tx.QueryRow(ctx, `
			SELECT to_char(start_time, 'HH24:MI') FROM work_schedules
			WHERE weekday=$1 AND (employee_id=$2 OR (employee_id IS NULL AND branch_id=$3))
			ORDER BY employee_id NULLS LAST LIMIT 1
		`, int(local.Weekday()), *p.EmployeeID, *p.BranchID).Scan(&startTxt)
		status := "present"
		late := 0
		if err == nil && startTxt != nil {
			has = true
			parsed, _ := time.Parse("15:04", *startTxt)
			start = time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc)
			mins := int(local.Sub(start).Minutes())
			if mins > grace {
				status = "late"
				late = mins
			}
		}
		_ = has
		day := local.Format("2006-01-02")
		_, err = tx.Exec(ctx, `
			INSERT INTO attendance_days (id, business_id, branch_id, employee_id, day, status, first_in, late_minutes, needs_review)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
			ON CONFLICT (employee_id, day) DO UPDATE SET status=EXCLUDED.status, first_in=EXCLUDED.first_in, late_minutes=EXCLUDED.late_minutes, needs_review=true
		`, ids.New(), p.BusinessID, *p.BranchID, *p.EmployeeID, day, status, now, late)
		if err != nil {
			return err
		}
		result = map[string]any{"status": status, "late_minutes": late, "at": now}
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
