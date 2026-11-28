package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bms/internal/platform/ids"
)

const (
	CheckLowStock      = "check_low_stock"
	RecurringExpenses  = "recurring_expenses"
	DebtReminders      = "debt_reminders"
	SalaryDue          = "salary_due"
	AttendanceClose    = "attendance_close"
	OwnerDailySummary  = "owner_daily_summary"
	StockRebuildCheck  = "stock_rebuild_check"
	LedgerBalanceCheck = "ledger_balance_check"
	ReportExport       = "report_export"
	SendNotification   = "send_notification"
	SessionCleanup     = "session_cleanup"
	BusinessExport     = "business_export"
	PayrollCalculate   = "payroll_calculate"
	ServiceReminders   = "service_reminders"
	DispatchOutbound   = "dispatch_outbound"
	AuditSeal          = "audit_seal"
	ProtectNationalIDs = "protect_national_ids"
)

type Handler func(ctx context.Context, payload json.RawMessage) error

type Client struct {
	pool     *pgxpool.Pool
	handlers map[string]Handler
	daily    []daily
	now      func() time.Time
}

// daily is a job enqueued once per local date, at or after a local time.
type daily struct {
	kind string
	at   string // HH:MM in the business timezone; "end" means at the next business day start
}

func New(pool *pgxpool.Pool) *Client {
	return &Client{pool: pool, handlers: map[string]Handler{}, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock lets tests drive the scheduler with a fixed clock.
func (c *Client) SetClock(now func() time.Time) { c.now = now }

func (c *Client) Register(kind string, h Handler) { c.handlers[kind] = h }

// Daily schedules kind once a day per business at local time at (HH:MM).
func (c *Client) Daily(kind, at string) { c.daily = append(c.daily, daily{kind: kind, at: at}) }

// Enqueue adds a job outside any transaction.
func (c *Client) Enqueue(ctx context.Context, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `INSERT INTO app.job_queue (id, kind, payload, run_at) VALUES ($1,$2,$3, now())`, ids.New(), kind, raw)
	return err
}

// Schedule enqueues every daily job that is due and not yet enqueued for the
// business's current local date. The mark row makes this safe to run from
// several processes at once.
func (c *Client) Schedule(ctx context.Context) error {
	if len(c.daily) == 0 {
		return nil
	}
	rows, err := c.pool.Query(ctx, `
		SELECT timezone,
		       coalesce((SELECT v.value #>> '{}' FROM app.setting_values v WHERE v.key = 'system.business_day_start'), '00:00')
		FROM app.business_profile WHERE status = 'active'`)
	if err != nil {
		return err
	}
	type biz struct {
		tz, day0 string
	}
	var list []biz
	for rows.Next() {
		var b biz
		if err := rows.Scan(&b.tz, &b.day0); err != nil {
			rows.Close()
			return err
		}
		list = append(list, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := c.now()
	for _, b := range list {
		loc, err := time.LoadLocation(b.tz)
		if err != nil {
			loc = time.UTC
		}
		local := now.In(loc)
		for _, d := range c.daily {
			at := d.at
			if at == "end" {
				at = b.day0
			}
			due, err := time.ParseInLocation("15:04", at, loc)
			if err != nil {
				continue
			}
			dueToday := time.Date(local.Year(), local.Month(), local.Day(), due.Hour(), due.Minute(), 0, 0, loc)
			if local.Before(dueToday) {
				continue
			}
			runDate := local.Format("2006-01-02")
			// For the day-end job the run date is the business day that just ended.
			if d.at == "end" {
				runDate = local.AddDate(0, 0, -1).Format("2006-01-02")
			}
			tag, err := c.pool.Exec(ctx, "\n\t\t\t\tINSERT INTO app.scheduled_job_marks (kind, run_date) VALUES ($1, $2)\n\t\t\t\tON CONFLICT DO NOTHING\n\t\t\t", d.kind, runDate)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			if err := c.Enqueue(ctx, d.kind, map[string]any{"date": runDate}); err != nil {
				return err
			}
		}
	}
	return nil
}

// RunPending processes queued jobs until none are ready.
func (c *Client) RunPending(ctx context.Context) error {
	for i := 0; i < 1000; i++ {
		ok, err := c.one(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

func EnqueueTx(ctx context.Context, tx pgx.Tx, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app.job_queue (id, kind, payload, run_at)
		VALUES ($1, $2, $3, now())
	`, ids.New(), kind, raw)
	return err
}

func (c *Client) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	sched := time.NewTicker(time.Minute)
	defer sched.Stop()
	if err := c.Schedule(ctx); err != nil {
		slog.Error("schedule", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-sched.C:
			if err := c.Schedule(ctx); err != nil {
				slog.Error("schedule", "err", err)
			}
		case <-ticker.C:
			c.drain(ctx)
		}
	}
}

func (c *Client) drain(ctx context.Context) {
	for i := 0; i < 20; i++ {
		ok, err := c.one(ctx)
		if err != nil {
			slog.Error("job", "err", err)
			return
		}
		if !ok {
			return
		}
	}
}

func (c *Client) one(ctx context.Context) (bool, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	var kind string
	var payload []byte
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT id, kind, payload, attempts, max_attempts
		FROM app.job_queue
		WHERE status = 'queued' AND run_at <= now()
		ORDER BY run_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&id, &kind, &payload, &attempts, &maxAttempts)
	if err != nil {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE app.job_queue SET status = 'running', locked_at = now(), attempts = attempts + 1, updated_at = now() WHERE id = $1`, id); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	h, ok := c.handlers[kind]
	var runErr error
	if !ok {
		runErr = errUnknown(kind)
	} else {
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		runErr = safeRun(runCtx, h, payload)
		cancel()
	}
	status := "done"
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
		if attempts+1 >= maxAttempts {
			status = "failed"
		} else {
			status = "queued"
		}
		slog.Error("job failed", "kind", kind, "err", runErr)
	}
	_, err = c.pool.Exec(ctx, `UPDATE app.job_queue SET status = $2, last_error = left(NULLIF($3,''), 2000), run_at = now() + interval '30 seconds', updated_at = now() WHERE id = $1`, id, status, msg)
	return true, err
}

// safeRun turns a handler panic into an error so one bad job cannot stop the worker.
func safeRun(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("job panicked: %v", r)
		}
	}()
	return h(ctx, payload)
}

type unknownJob string

func errUnknown(kind string) error { return unknownJob(kind) }
func (u unknownJob) Error() string { return "no handler for " + string(u) }
