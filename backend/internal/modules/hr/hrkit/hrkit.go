// Package hrkit holds helpers shared by the hr, attendance and payroll modules:
// the working-day calendar, payroll lock check and small query utilities.
package hrkit

import (
	"context"
	"net/netip"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
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

// Calendar answers whether a day is a working day for an employee (default Monday to Saturday,
// 08:00-17:00 with a 60 minute break). Separate holidays/schedules tables are not in the approved schema.
type Calendar struct{}

func LoadCalendar(_ context.Context, _ pgx.Tx, _ *uuid.UUID, _ uuid.UUID) (*Calendar, error) {
	return &Calendar{}, nil
}

func (c *Calendar) Holiday(_ time.Time) (string, bool) { return "", false }

func (c *Calendar) Schedule(day time.Time) (Sched, bool) {
	if day.Weekday() == time.Sunday {
		return Sched{Working: false}, false
	}
	return Sched{Working: true, Start: "08:00", End: "17:00", Break: 60}, false
}

func (c *Calendar) IsWorking(day time.Time) bool {
	s, _ := c.Schedule(day)
	return s.Working
}

func (c *Calendar) WorkingDays(from, to time.Time) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if c.IsWorking(d) {
			n++
		}
	}
	return n
}

func Date(s string) (time.Time, error) { return time.Parse(DateFmt, s) }

func MustDate(s string) time.Time {
	d, _ := time.Parse(DateFmt, s)
	return d
}

// OptionalIdempotent applies the Idempotency-Key rules when the header is sent.
func OptionalIdempotent() fiber.Handler {
	inner := middleware.Idempotent()
	return func(c fiber.Ctx) error {
		if c.Get("Idempotency-Key") == "" {
			return c.Next()
		}
		return inner(c)
	}
}

// MethodFor returns the payment method that matches an active financial account.
func MethodFor(ctx context.Context, tx pgx.Tx, _ *auth.Principal, _ string, accountID uuid.UUID) (string, error) {
	acc, err := payments.ActiveFin(ctx, tx, accountID)
	if err != nil {
		return "", err
	}
	return payments.MethodForKind(acc.Kind), nil
}

// ResolveApproval is a no-op on the approved schema where leave and overtime live on their own tables.
func ResolveApproval(_ context.Context, _ pgx.Tx, _ *auth.Principal, _ string, _ uuid.UUID, _ string) error {
	return nil
}

func LocalToday(now time.Time, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

func Location(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func NullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

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
			m[f.Name] = convert(f.DataTypeOID, f.TypeModifier, vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

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

func convert(oid uint32, typmod int32, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case [16]byte:
		return uuid.UUID(x).String()
	case pgtype.Numeric:
		if !x.Valid || x.NaN || x.InfinityModifier != pgtype.Finite || x.Int == nil {
			return nil
		}
		scale := int32(-1)
		if typmod >= 4 {
			scale = (typmod - 4) & 0xffff
		} else if x.Exp < 0 {
			scale = -x.Exp
		} else if x.Int.Sign() == 0 {
			scale = 2
		}
		d := decimal.NewFromBigInt(x.Int, x.Exp)
		if scale >= 0 {
			return d.StringFixed(scale)
		}
		return d.String()
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

// PayrollLocked refuses a change to a day that falls inside the period of an approved or paid
// payroll run that includes the employee.
func PayrollLocked(ctx context.Context, tx pgx.Tx, employee uuid.UUID, day string) error {
	var locked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM app.payroll_lines pl
			JOIN app.payroll_runs r ON r.id = pl.payroll_run_id
			WHERE pl.employee_id = $1 AND r.status IN ('approved','paid')
			  AND r.period_year = EXTRACT(YEAR FROM $2::date)::smallint
			  AND r.period_month = EXTRACT(MONTH FROM $2::date)::smallint
		)`, employee, day).Scan(&locked); err != nil {
		return err
	}
	if locked {
		return httpx.Conflict("period_locked", "Attendance for "+day+" is locked by an approved payroll")
	}
	return nil
}
