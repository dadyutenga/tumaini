package finance

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type CloseDayIn struct {
	Date        string          `json:"date"`
	CountedCash decimal.Decimal `json:"counted_cash"`
	Note        string          `json:"note"`
}

func (s *Service) dayFigures(ctx context.Context, tx pgx.Tx, branch uuid.UUID, date string) (map[string]decimal.Decimal, decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	byMethod := map[string]decimal.Decimal{}
	rows, err := tx.Query(ctx, `
		SELECT method, COALESCE(SUM(CASE WHEN direction='in' THEN amount ELSE -amount END), 0)
		FROM payments
		WHERE branch_id=$1 AND payment_date=$2::date AND status='posted' AND kind IN ('sale','sale_refund')
		GROUP BY method`, branch, date)
	if err != nil {
		return nil, money.Zero, money.Zero, money.Zero, err
	}
	for rows.Next() {
		var m string
		var amt decimal.Decimal
		if err := rows.Scan(&m, &amt); err != nil {
			rows.Close()
			return nil, money.Zero, money.Zero, money.Zero, err
		}
		byMethod[m] = amt
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, money.Zero, money.Zero, money.Zero, err
	}

	var paysIn, cashIn, cashOut, expensesTotal decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction='in' THEN amount ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN direction='in' AND method='cash' THEN amount ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN direction='out' AND method='cash' THEN amount ELSE 0 END), 0)
		FROM payments
		WHERE branch_id=$1 AND payment_date=$2::date AND status='posted'`, branch, date).Scan(&paysIn, &cashIn, &cashOut); err != nil {
		return nil, money.Zero, money.Zero, money.Zero, err
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM expenses
		WHERE branch_id=$1 AND expense_date=$2::date AND status IN ('approved','paid')`, branch, date).Scan(&expensesTotal); err != nil {
		return nil, money.Zero, money.Zero, money.Zero, err
	}
	var tillFloat decimal.Decimal
	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(opening_float), 0) FROM till_sessions
		WHERE branch_id=$1 AND opened_at::date=$2::date`, branch, date).Scan(&tillFloat)

	expectedCash := money.Money(tillFloat.Add(cashIn).Sub(cashOut))
	return byMethod, paysIn, expensesTotal, expectedCash, nil
}

// DaySummary returns end-of-day figures and any existing closing record for the date.
func (s *Service) DaySummary(ctx context.Context, p *auth.Principal, date string) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		d := strings.TrimSpace(date)
		if d == "" {
			var err error
			if d, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		} else if _, err := time.Parse("2006-01-02", d); err != nil {
			return httpx.BadRequest("invalid_query", "date must be YYYY-MM-DD")
		}
		byMethod, paysIn, expTotal, expectedCash, err := s.dayFigures(ctx, tx, *p.BranchID, d)
		if err != nil {
			return err
		}
		var closing map[string]any
		var cid uuid.UUID
		var exp, cnt, diff decimal.Decimal
		var status string
		var note, reopenReason, closedByName, reopenedByName *string
		var closedAt time.Time
		var reopenedAt *time.Time
		err = tx.QueryRow(ctx, `
			SELECT dc.id, dc.expected_cash, dc.counted_cash, dc.difference, dc.status, dc.note, dc.closed_at,
			       u1.name, dc.reopened_at, u2.name, dc.reopen_reason
			FROM day_closings dc
			LEFT JOIN users u1 ON u1.id = dc.closed_by
			LEFT JOIN users u2 ON u2.id = dc.reopened_by
			WHERE dc.branch_id=$1 AND dc.business_date=$2::date`, *p.BranchID, d).
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
		out = map[string]any{
			"date":              d,
			"sales_by_method":   byMethod,
			"payments_received": paysIn,
			"expenses_total":    expTotal,
			"expected_cash":     expectedCash,
			"closing":           closing,
		}
		return nil
	})
	return out, err
}

func (s *Service) ListDayClosings(ctx context.Context, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM day_closings WHERE ($1::uuid IS NULL OR branch_id=$1)`, p.BranchID).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT dc.id, dc.business_date::text, b.name, dc.expected_cash, dc.counted_cash, dc.difference,
			       dc.payments_received, dc.expenses_total, dc.status, dc.note, dc.closed_at, u.name
			FROM day_closings dc
			JOIN branches b ON b.id = dc.branch_id
			LEFT JOIN users u ON u.id = dc.closed_by
			WHERE ($1::uuid IS NULL OR dc.branch_id=$1)
			ORDER BY dc.business_date DESC LIMIT $2 OFFSET $3`, p.BranchID, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var date, branch, status string
			var exp, cnt, diff, pays, exps decimal.Decimal
			var note, by *string
			var closedAt time.Time
			if err := rows.Scan(&id, &date, &branch, &exp, &cnt, &diff, &pays, &exps, &status, &note, &closedAt, &by); err != nil {
				return err
			}
			out = append(out, map[string]any{
				"id": id, "date": date, "branch_name": branch, "expected_cash": exp, "counted_cash": cnt,
				"difference": diff, "payments_received": pays, "expenses_total": exps,
				"status": status, "note": note, "closed_at": closedAt, "closed_by_name": by,
			})
		}
		return rows.Err()
	})
	return out, total, err
}

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
		err := tx.QueryRow(ctx, `SELECT id, status FROM day_closings WHERE branch_id=$1 AND business_date=$2::date FOR UPDATE`, *p.BranchID, d).Scan(&existingID, &existingStatus)
		if err == nil && existingStatus == "closed" {
			return httpx.Conflict("already_closed", "This day is already closed")
		}
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		byMethod, paysIn, expTotal, expectedCash, err := s.dayFigures(ctx, tx, *p.BranchID, d)
		if err != nil {
			return err
		}
		counted := money.Money(in.CountedCash)
		diff := counted.Sub(expectedCash)
		rawMethods, _ := json.Marshal(byMethod)
		id := existingID
		if id == uuid.Nil {
			id = ids.New()
			if _, err := tx.Exec(ctx, `
				INSERT INTO day_closings (id, business_id, branch_id, business_date, expected_cash, counted_cash, difference, sales_by_method, payments_received, expenses_total, note, status, closed_by, closed_at)
				VALUES ($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10,NULLIF($11,''),'closed',$12,now())`,
				id, p.BusinessID, *p.BranchID, d, expectedCash, counted, diff, rawMethods, paysIn, expTotal, strings.TrimSpace(in.Note), p.UserID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE day_closings SET expected_cash=$2, counted_cash=$3, difference=$4, sales_by_method=$5, payments_received=$6, expenses_total=$7,
				       note=NULLIF($8,''), status='closed', closed_by=$9, closed_at=now(), reopened_by=NULL, reopened_at=NULL, reopen_reason=NULL
				WHERE id=$1`,
				id, expectedCash, counted, diff, rawMethods, paysIn, expTotal, strings.TrimSpace(in.Note), p.UserID); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "day_closing.closed", "day_closing", id, nil, map[string]any{"date": d, "expected_cash": expectedCash, "counted_cash": counted, "difference": diff}); err != nil {
			return err
		}
		out = map[string]any{
			"id": id, "date": d, "expected_cash": expectedCash, "counted_cash": counted, "difference": diff,
			"status": "closed", "payments_received": paysIn, "expenses_total": expTotal,
		}
		return nil
	})
	return out, err
}

// ReopenDay unlocks a closed day. SRS Section 8: only the Owner can reopen a closed day.
func (s *Service) ReopenDay(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	if !p.IsOwner {
		return nil, httpx.Forbidden("owner_only", "Only the Owner can reopen a closed day")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required to reopen a closed day")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var status, date string
		if err := tx.QueryRow(ctx, `SELECT status, business_date::text FROM day_closings WHERE id=$1 FOR UPDATE`, id).Scan(&status, &date); err != nil {
			return httpx.NotFound("not_found", "Day closing not found")
		}
		if status != "closed" {
			return httpx.Conflict("invalid_status", "This day is already open")
		}
		if _, err := tx.Exec(ctx, `UPDATE day_closings SET status='reopened', reopened_by=$2, reopened_at=now(), reopen_reason=$3 WHERE id=$1`, id, p.UserID, reason); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "day_closing.reopened", "day_closing", id, map[string]any{"status": "closed"}, map[string]any{"status": "reopened", "date": date, "reason": reason}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "date": date, "status": "reopened", "reopen_reason": reason}
		return nil
	})
	return out, err
}
