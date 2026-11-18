// Package hrkit holds helpers shared by the hr, attendance and payroll modules:
// the working-day calendar, approval resolution and small request utilities.
package hrkit

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

const DateFmt = "2006-01-02"

// Sched is one weekday of a work schedule.
type Sched struct {
	Working bool
	Start   string // HH:MM, empty when not set
	End     string
	Break   int
}

// Minutes is the scheduled working time for the day, net of the break.
func (s Sched) Minutes() int {
	if !s.Working || s.Start == "" || s.End == "" {
		return 0
	}
	a, err1 := time.Parse("15:04", s.Start)
	b, err2 := time.Parse("15:04", s.End)
	if err1 != nil || err2 != nil || !b.After(a) {
		return 0
	}
	m := int(b.Sub(a).Minutes()) - s.Break
	if m < 0 {
		return 0
	}
	return m
}

// Calendar answers whether a day is a working day for one employee.
// Precedence: holiday, then the employee's own schedule, then the branch
// schedule, then the default of Monday to Saturday.
type Calendar struct {
	holidays map[string]string
	emp      map[int]Sched
	branch   map[int]Sched
}

func LoadCalendar(ctx context.Context, tx pgx.Tx, employeeID *uuid.UUID, branchID uuid.UUID) (*Calendar, error) {
	c := &Calendar{holidays: map[string]string{}, emp: map[int]Sched{}, branch: map[int]Sched{}}
	rows, err := tx.Query(ctx, `SELECT day::text, name FROM holidays`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d, n string
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return nil, err
		}
		c.holidays[d] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `
		SELECT employee_id, weekday, is_working, COALESCE(to_char(start_time,'HH24:MI'),''), COALESCE(to_char(end_time,'HH24:MI'),''), break_minutes
		FROM work_schedules
		WHERE ($1::uuid IS NOT NULL AND employee_id = $1) OR (employee_id IS NULL AND branch_id = $2)
	`, employeeID, branchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var emp *uuid.UUID
		var wd int
		var s Sched
		if err := rows.Scan(&emp, &wd, &s.Working, &s.Start, &s.End, &s.Break); err != nil {
			return nil, err
		}
		if emp != nil {
			c.emp[wd] = s
		} else {
			c.branch[wd] = s
		}
	}
	return c, rows.Err()
}

// Holiday returns the holiday name for day, if any.
func (c *Calendar) Holiday(day time.Time) (string, bool) {
	n, ok := c.holidays[day.Format(DateFmt)]
	return n, ok
}

// Schedule returns the schedule for the weekday of day and whether one is defined.
func (c *Calendar) Schedule(day time.Time) (Sched, bool) {
	wd := int(day.Weekday())
	if len(c.emp) > 0 {
		s, ok := c.emp[wd]
		if !ok {
			return Sched{}, true
		}
		return s, true
	}
	if len(c.branch) > 0 {
		s, ok := c.branch[wd]
		if !ok {
			return Sched{}, true
		}
		return s, true
	}
	return Sched{Working: day.Weekday() != time.Sunday}, false
}

// IsWorking reports whether day is a scheduled working day and not a holiday.
func (c *Calendar) IsWorking(day time.Time) bool {
	if _, ok := c.Holiday(day); ok {
		return false
	}
	s, _ := c.Schedule(day)
	return s.Working
}

// WorkingDays counts working days from..to inclusive. An empty range is 0.
func (c *Calendar) WorkingDays(from, to time.Time) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if c.IsWorking(d) {
			n++
		}
	}
	return n
}

// Date parses YYYY-MM-DD as a UTC date.
func Date(s string) (time.Time, error) { return time.Parse(DateFmt, s) }

// MustDate parses a date known to be valid (read from the database).
func MustDate(s string) time.Time {
	d, _ := time.Parse(DateFmt, s)
	return d
}

// OptionalIdempotent applies the Idempotency-Key rules when the header is sent.
// Routes that existed before idempotency was required keep accepting calls without it.
func OptionalIdempotent() fiber.Handler {
	inner := middleware.Idempotent()
	return func(c fiber.Ctx) error {
		if c.Get("Idempotency-Key") == "" {
			return c.Next()
		}
		return inner(c)
	}
}

// MethodFor returns the payment method that matches a money account type,
// and fails when the account does not exist or is inactive.
func MethodFor(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (string, error) {
	var typ string
	var active bool
	err := tx.QueryRow(ctx, `SELECT type, is_active FROM money_accounts WHERE id=$1`, accountID).Scan(&typ, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !active) {
		return "", httpx.NotFound("not_found", "Money account not found")
	}
	if err != nil {
		return "", err
	}
	switch typ {
	case "bank":
		return "bank", nil
	case "mobile_money":
		return "mobile_money", nil
	default:
		return "cash", nil
	}
}

// ResolveApproval closes a still-pending approval request for a subject that
// was decided directly from its own screen, so the inbox does not keep it.
func ResolveApproval(ctx context.Context, tx pgx.Tx, p *auth.Principal, subjectType string, subjectID uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, `
		UPDATE approval_requests SET status=$3, decided_by=$4, decided_at=now()
		WHERE subject_type=$1 AND subject_id=$2 AND status='pending'
	`, subjectType, subjectID, status, p.UserID)
	return err
}

// LocalToday returns the business-local date of now.
func LocalToday(now time.Time, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// Location loads a timezone, falling back to UTC.
func Location(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// NullStr maps "" to SQL NULL.
func NullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Maps reads all rows into JSON-ready maps keyed by column name. Numerics
// become decimal strings, dates YYYY-MM-DD and UUIDs their string form.
func Maps(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, f := range fields {
			m[f.Name] = convert(f.DataTypeOID, vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// One reads a single row; no row is a 404 with the given message.
func One(ctx context.Context, tx pgx.Tx, notFound string, sql string, args ...any) (map[string]any, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	list, err := Maps(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, httpx.NotFound("not_found", notFound)
	}
	return list[0], nil
}

// All runs a query and returns the rows as maps.
func All(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return Maps(rows)
}

const (
	oidDate    = 1082
	oidNumeric = 1700
)

func convert(oid uint32, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case [16]byte:
		return uuid.UUID(x).String()
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		d, err := x.Value()
		if err != nil {
			return nil
		}
		return d
	case time.Time:
		if oid == oidDate {
			return x.Format(DateFmt)
		}
		return x.UTC()
	case pgtype.Time:
		if !x.Valid {
			return nil
		}
		us := x.Microseconds
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(us) * time.Microsecond).Format("15:04")
	case netip.Prefix:
		return x.Addr().String()
	}
	_ = oidNumeric
	return v
}
