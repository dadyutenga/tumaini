package jobs

import (
	"context"
	"encoding/json"
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
)

type Handler func(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error

type Client struct {
	pool     *pgxpool.Pool
	handlers map[string]Handler
}

func New(pool *pgxpool.Pool) *Client {
	return &Client{pool: pool, handlers: map[string]Handler{}}
}

func (c *Client) Register(kind string, h Handler) { c.handlers[kind] = h }

func EnqueueTx(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO jobs (id, business_id, kind, payload, run_at)
		VALUES ($1, $2, $3, $4, now())
	`, ids.New(), businessID, kind, raw)
	return err
}

func (c *Client) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
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
	var id, businessID uuid.UUID
	var kind string
	var payload []byte
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT id, business_id, kind, payload, attempts, max_attempts
		FROM jobs
		WHERE status = 'queued' AND run_at <= now()
		ORDER BY run_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&id, &businessID, &kind, &payload, &attempts, &maxAttempts)
	if err != nil {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status = 'running', locked_at = now(), attempts = attempts + 1 WHERE id = $1`, id); err != nil {
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
		runErr = h(runCtx, businessID, payload)
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
	_, err = c.pool.Exec(ctx, `UPDATE jobs SET status = $2, last_error = NULLIF($3,''), run_at = now() + interval '30 seconds' WHERE id = $1`, id, status, msg)
	return true, err
}

type unknownJob string

func errUnknown(kind string) error { return unknownJob(kind) }
func (u unknownJob) Error() string { return "no handler for " + string(u) }
