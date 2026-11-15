package clock

import (
	"fmt"
	"time"
)

type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fixed struct{ T time.Time }

func (f Fixed) Now() time.Time { return f.T }

// BusinessDate returns the business date for now, using the business timezone
// and the time of day at which a new business day starts (BR-15).
func BusinessDate(now time.Time, timezone, dayStart string) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("timezone %s: %w", timezone, err)
	}
	local := now.In(loc)
	start, err := time.Parse("15:04", dayStart)
	if err != nil {
		start, _ = time.Parse("15:04", "00:00")
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	boundary := time.Date(local.Year(), local.Month(), local.Day(), start.Hour(), start.Minute(), 0, 0, loc)
	if local.Before(boundary) {
		day = day.AddDate(0, 0, -1)
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), nil
}
