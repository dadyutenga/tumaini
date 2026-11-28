package finance

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
)

type CloseDayIn struct {
	Date        string          `json:"date"`
	CountedCash decimal.Decimal `json:"counted_cash"`
	Note        string          `json:"note"`
}

// dayFigures returns the day's takings by payment method, money received, expenses and the cash the
// system expects to be in the location's cash accounts (app.expected_account_balances).
func (s *Service) dayFigures(ctx context.Context, tx pgx.Tx, p *auth.Principal, location uuid.UUID, date string) (map[string]decimal.Decimal, decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	zero := money.Zero
	byMethod := map[string]decimal.Decimal{}
	rows, err := tx.Query(ctx, "\n\t\tSELECT method::text, coalesce(sum(amount), 0) FROM app.payments_received\n\t\tWHERE location_id = $1 AND business_date = $2::date AND status = 'posted' AND purpose = 'sale'\n\t\tGROUP BY method", location, date)
	if err != nil {
		return nil, zero, zero, zero, err
	}
	for rows.Next() {
		var m string
		var amt decimal.Decimal
		if err := rows.Scan(&m, &amt); err != nil {
			rows.Close()
			return nil, zero, zero, zero, err
		}
		byMethod[payments.MethodOut(m)] = amt
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, zero, zero, zero, err
	}
	var paysIn, expenses, expected decimal.Decimal
	if err := tx.QueryRow(ctx, "SELECT coalesce(sum(amount), 0) FROM app.payments_received WHERE location_id = $1 AND business_date = $2::date AND status = 'posted'", location, date).Scan(&paysIn); err != nil {
		return nil, zero, zero, zero, err
	}
	if err := tx.QueryRow(ctx, "\n\t\tSELECT coalesce(sum(amount), 0) FROM app.expenses\n\t\tWHERE location_id = $1 AND expense_date = $2::date AND status IN ('approved', 'paid')", location, date).Scan(&expenses); err != nil {
		return nil, zero, zero, zero, err
	}
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(expected), 0) FROM app.expected_account_balances($1, $2::date) WHERE kind IN ('cash', 'petty_cash')`, location, date).Scan(&expected); err != nil {
		return nil, zero, zero, zero, err
	}
	return byMethod, paysIn, expenses, money.Money(expected), nil
}

// DaySummary returns end-of-day figures and any existing closing record for the date under
// a single REPEATABLE READ snapshot so takings, expenses, and expected cash cannot tear.
func (s *Service) DaySummary(ctx context.Context, p *auth.Principal, date string) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out map[string]any
	err := s.DB.InActorReadTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		d := strings.TrimSpace(date)
		if d == "" {
			var err error
			if d, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		} else if _, err := time.Parse("2006-01-02", d); err != nil {
			return httpx.BadRequest("invalid_query", "date must be YYYY-MM-DD")
		}
		byMethod, paysIn, expTotal, expectedCash, err := s.dayFigures(ctx, tx, p, *p.BranchID, d)
		if err != nil {
			return err
		}
		var closing map[string]any
		var cid uuid.UUID
		var exp, cnt, diff *decimal.Decimal
		var status string
		var note, reopenReason, closedByName, reopenedByName *string
		var closedAt, reopenedAt *time.Time
		err = tx.QueryRow(ctx, "\n\t\t\tSELECT dc.id, dc.expected_cash, dc.counted_cash, dc.difference, dc.status::text, dc.note, dc.closed_at,\n\t\t\t       u1.full_name::text, dc.reopened_at, u2.full_name::text, dc.reopen_reason\n\t\t\tFROM app.day_closings dc\n\t\t\tLEFT JOIN app.memberships m1 ON m1.id = dc.closed_by LEFT JOIN iam.users u1 ON u1.id = m1.user_id\n\t\t\tLEFT JOIN app.memberships m2 ON m2.id = dc.reopened_by LEFT JOIN iam.users u2 ON u2.id = m2.user_id\n\t\t\tWHERE dc.location_id = $1 AND dc.business_date = $2::date", *p.BranchID, d).
			Scan(&cid, &exp, &cnt, &diff, &status, &note, &closedAt, &closedByName, &reopenedAt, &reopenedByName, &reopenReason)
		if err == nil {
			closing = map[string]any{
				"id": cid, "expected_cash": exp, "counted_cash": cnt, "difference": diff,
				"status": status, "note": note, "closed_at": closedAt, "closed_by_name": closedByName,
				"reopened_at": reopenedAt, "reopened_by_name": reopenedByName, "reopen_reason": reopenReason,
			}
		} else if err != pgx.ErrNoRows {
			return err
		}
		cur := setting.String(ctx, tx, "currency.code", "TZS")
		out = map[string]any{
			"date": d, "currency": cur, "sales_by_method": byMethod, "payments_received": paysIn, "expenses_total": expTotal,
			"expected_cash": expectedCash, "closing": closing,
		}
		return nil
	})
	return out, err
}

func (s *Service) ListDayClosings(ctx context.Context, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.day_closings WHERE ($1::uuid IS NULL OR location_id = $1) AND status <> 'open'", p.BranchID).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT dc.id, dc.business_date::text, l.name::text, dc.expected_cash, dc.counted_cash, dc.difference, dc.status::text, dc.note, dc.closed_at, u.full_name::text\n\t\t\tFROM app.day_closings dc\n\t\t\tJOIN app.locations l ON l.id = dc.location_id\n\t\t\tLEFT JOIN app.memberships m ON m.id = dc.closed_by LEFT JOIN iam.users u ON u.id = m.user_id\n\t\t\tWHERE ($1::uuid IS NULL OR dc.location_id = $1) AND dc.status <> 'open'\n\t\t\tORDER BY dc.business_date DESC LIMIT $2 OFFSET $3", p.BranchID, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var date, branch, status string
			var exp, cnt, diff *decimal.Decimal
			var note, by *string
			var closedAt *time.Time
			if err := rows.Scan(&id, &date, &branch, &exp, &cnt, &diff, &status, &note, &closedAt, &by); err != nil {
				return err
			}
			out = append(out, map[string]any{
				"id": id, "date": date, "branch_name": branch, "expected_cash": exp, "counted_cash": cnt, "difference": diff,
				"payments_received": nil, "expenses_total": nil, "status": status, "note": note, "closed_at": closedAt, "closed_by_name": by,
			})
		}
		return rows.Err()
	})
	return out, total, err
}

// CloseDay records the cash count for a location and day and locks the day. The database refuses
// later postings to a closed day (BM002) and requires a note when the count differs from the system.
func (s *Service) CloseDay(ctx context.Context, p *auth.Principal, in CloseDayIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if in.CountedCash.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Counted cash cannot be negative")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		d := strings.TrimSpace(in.Date)
		if d == "" {
			var err error
			if d, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		} else if _, err := time.Parse("2006-01-02", d); err != nil {
			return httpx.BadRequest("invalid_body", "date must be YYYY-MM-DD")
		}
		var existingStatus string
		var existingID uuid.UUID
		err := tx.QueryRow(ctx, "SELECT id, status::text FROM app.day_closings WHERE location_id = $1 AND business_date = $2::date FOR UPDATE", *p.BranchID, d).Scan(&existingID, &existingStatus)
		if err == nil && existingStatus == "closed" {
			return httpx.Conflict("already_closed", "This day is already closed")
		}
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		_, paysIn, expTotal, expectedCash, err := s.dayFigures(ctx, tx, p, *p.BranchID, d)
		if err != nil {
			return err
		}
		counted := money.Money(in.CountedCash)
		diff := counted.Sub(expectedCash)
		note := strings.TrimSpace(in.Note)
		if !diff.IsZero() && note == "" {
			return httpx.BadRequest("note_required", "The counted cash differs from the system. Say why")
		}
		id := existingID
		if id == uuid.Nil {
			id = uuid.New()
			if _, err := tx.Exec(ctx, "INSERT INTO app.day_closings (id, location_id, business_date) VALUES ($1, $2, $3::date)", id, *p.BranchID, d); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM app.day_closing_lines WHERE day_closing_id = $1", id); err != nil {
			return err
		}
		// One line per account: cash accounts share the counted figure in proportion to what each should hold,
		// assigning any rounding remainder to the final cash account so SUM(cash counted_amount) == counted_cash.
		// Non-cash accounts are recorded as matching expected.
		type expAcc struct {
			id       uuid.UUID
			kind     string
			expected decimal.Decimal
		}
		accRows, err := tx.Query(ctx, `SELECT account_id, kind::text, expected FROM app.expected_account_balances($1, $2::date) ORDER BY account_id`, *p.BranchID, d)
		if err != nil {
			return err
		}
		var accs []expAcc
		lastCashIdx := -1
		for accRows.Next() {
			var a expAcc
			if err := accRows.Scan(&a.id, &a.kind, &a.expected); err != nil {
				accRows.Close()
				return err
			}
			if a.kind == "cash" || a.kind == "petty_cash" {
				lastCashIdx = len(accs)
			}
			accs = append(accs, a)
		}
		accRows.Close()
		if err := accRows.Err(); err != nil {
			return err
		}
		cashAllocated := money.Zero
		for idx, a := range accs {
			lineCounted := a.expected
			if a.kind == "cash" || a.kind == "petty_cash" {
				if idx == lastCashIdx {
					lineCounted = counted.Sub(cashAllocated)
				} else if !expectedCash.IsZero() {
					lineCounted = money.Money(counted.Mul(a.expected).Div(expectedCash))
				} else {
					lineCounted = money.Zero
				}
				cashAllocated = cashAllocated.Add(lineCounted)
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.day_closing_lines (day_closing_id, account_id, expected_amount, counted_amount)\n\t\t\t\tVALUES ($1, $2, $3, $4)", id, a.id, a.expected, lineCounted); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.day_closings SET status = 'closed', expected_cash = $2, counted_cash = $3, note = NULLIF($4,''),
			       reopened_by = NULL, reopened_at = NULL, reopen_reason = NULL, reopen_approval_id = NULL WHERE id = $1`, id, expectedCash, counted, note); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "day_closing.closed", "day_closings", id, nil, map[string]any{"date": d, "expected_cash": expectedCash, "counted_cash": counted, "difference": diff}); err != nil {
			return err
		}
		cur := setting.String(ctx, tx, "currency.code", "TZS")
		out = map[string]any{
			"id": id, "date": d, "currency": cur, "expected_cash": expectedCash, "counted_cash": counted, "difference": diff,
			"status": "closed", "payments_received": paysIn, "expenses_total": expTotal,
		}
		return nil
	})
	return out, err
}

// ReopenDay unlocks a closed day. The database allows it only for someone who holds
// finance.day.reopen (Admin by default) or with an approved day_reopen request.
func (s *Service) ReopenDay(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required to reopen a closed day")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var status, date string
		if err := tx.QueryRow(ctx, "SELECT status::text, business_date::text FROM app.day_closings WHERE id = $1 FOR UPDATE", id).Scan(&status, &date); err != nil {
			return httpx.NotFound("not_found", "Day closing not found")
		}
		if status != "closed" {
			return httpx.Conflict("invalid_status", "This day is already open")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.day_closings SET status = 'reopened', reopen_reason = $2, reopened_by = $3, reopened_at = now() WHERE id = $1`, id, reason, p.MembershipID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "day_closing.reopened", "day_closings", id, map[string]any{"status": "closed"}, map[string]any{"status": "reopened", "date": date, "reason": reason}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "date": date, "status": "reopened", "reopen_reason": reason}
		return nil
	})
	return out, err
}
