// Package system runs the background jobs that do not belong to one business
// module: data export, reminders, the owner's daily summary, integrity checks,
// cleanup and email/SMS delivery of notifications.
package system

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/ids"
	"bms/internal/platform/sender"
	"bms/internal/platform/setting"
	"bms/internal/platform/storage"
)

type Service struct {
	DB     *db.DB
	Notify *notify.Service
	Store  storage.Store
	Clock  clock.Clock
	Email  sender.Sender
	SMS    sender.Sender
}

// exportExcluded are tenant tables whose rows are secrets or transient state.
var exportExcluded = map[string]bool{
	"sessions": true, "idempotency_keys": true, "password_resets": true, "jobs": true, "scheduled_job_marks": true,
}

// exportStripped are columns removed from every exported row (FR-SEC-09).
var exportStripped = []string{"password_hash", "pin_hash", "refresh_hash", "prev_hash", "code_hash", "request_hash"}

const exportMax = 512 << 20

type exportPayload struct {
	ExportID uuid.UUID `json:"export_id"`
}

// BusinessExport writes every tenant table as JSON lines into one zip file
// (FR-SEC-06, NFR-19). Row-level security limits each query to the business.
func (s *Service) BusinessExport(ctx context.Context, businessID uuid.UUID, raw json.RawMessage) error {
	var in exportPayload
	if err := json.Unmarshal(raw, &in); err != nil || in.ExportID == uuid.Nil {
		return fmt.Errorf("export payload: %w", err)
	}
	var user uuid.UUID
	err := s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT user_id FROM export_jobs WHERE id=$1`, in.ExportID).Scan(&user); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE export_jobs SET status='running', error=NULL WHERE id=$1`, in.ExportID)
		return err
	})
	if err != nil {
		return err
	}
	buf, counts, err := s.buildExport(ctx, businessID)
	if err != nil {
		s.failExport(ctx, businessID, in.ExportID, err)
		return err
	}
	name := "business-export-" + s.Clock.Now().Format("20060102-150405") + ".zip"
	file, err := s.Store.Save(ctx, businessID.String(), name, bytes.NewReader(buf), exportMax)
	if err != nil {
		s.failExport(ctx, businessID, in.ExportID, err)
		return err
	}
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		fileID := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO files (id, business_id, path, original_name, mime_type, size_bytes, uploaded_by)
			VALUES ($1,$2,$3,$4,'application/zip',$5,$6)
		`, fileID, businessID, file.Path, name, file.Size, user); err != nil {
			return err
		}
		params, _ := json.Marshal(map[string]any{"rows": counts})
		if _, err := tx.Exec(ctx, `UPDATE export_jobs SET status='done', file_id=$2, finished_at=$3, params=$4 WHERE id=$1`, in.ExportID, fileID, s.Clock.Now(), params); err != nil {
			return err
		}
		return s.Notify.ToUser(ctx, tx, businessID, user, notify.Message{
			Type: "export_ready", Title: "Data export ready", Body: "Your business data export can be downloaded.",
			Link: "/exports/" + in.ExportID.String(), EntityType: "export_job", EntityID: &in.ExportID,
		})
	})
}

func (s *Service) failExport(ctx context.Context, businessID, id uuid.UUID, cause error) {
	_ = s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE export_jobs SET status='failed', error=$2, finished_at=$3 WHERE id=$1`, id, truncate(cause.Error(), 500), s.Clock.Now())
		return err
	})
}

func (s *Service) buildExport(ctx context.Context, businessID uuid.UUID) ([]byte, map[string]int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	counts := map[string]int{}
	err := s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.table_name FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema=c.table_schema AND t.table_name=c.table_name
			WHERE c.table_schema='public' AND c.column_name='business_id' AND t.table_type='BASE TABLE'
			ORDER BY c.table_name`)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			if !exportExcluded[name] {
				tables = append(tables, name)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		tables = append([]string{"businesses"}, tables...)
		strip := ""
		for _, col := range exportStripped {
			strip += " - '" + col + "'"
		}
		for _, table := range tables {
			w, err := zw.Create(table + ".jsonl")
			if err != nil {
				return err
			}
			// The table name comes from the catalogue, and is quoted as an identifier.
			q := `SELECT to_jsonb(t)` + strip + ` FROM ` + pgx.Identifier{table}.Sanitize() + ` t`
			rows, err := tx.Query(ctx, q)
			if err != nil {
				return fmt.Errorf("export %s: %w", table, err)
			}
			n := 0
			for rows.Next() {
				var line []byte
				if err := rows.Scan(&line); err != nil {
					rows.Close()
					return err
				}
				if _, err := w.Write(append(line, '\n')); err != nil {
					rows.Close()
					return err
				}
				n++
				if buf.Len() > exportMax {
					rows.Close()
					return errors.New("export is larger than the allowed size")
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			counts[table] = n
		}
		manifest, _ := json.MarshalIndent(map[string]any{"business_id": businessID, "created_at": s.Clock.Now(), "format": "one JSON object per line", "rows": counts}, "", "  ")
		w, err := zw.Create("manifest.json")
		if err != nil {
			return err
		}
		_, err = w.Write(manifest)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), counts, nil
}

// DebtReminders raises one summary notification per branch for overdue
// customer invoices, customers over their limit, supplier invoices due soon or
// overdue, and approved expenses still unpaid (FR-NTF-03, 05, 06, FR-CUS-11).
func (s *Service) DebtReminders(ctx context.Context, businessID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.today(ctx, tx)
		if err != nil {
			return err
		}
		dueDays := setting.Int(ctx, tx, "notifications.supplier_due_days", 7)
		type agg struct {
			branch uuid.UUID
			n      int
			amt    decimal.Decimal
		}
		collect := func(sql string, args ...any) ([]agg, error) {
			rows, err := tx.Query(ctx, sql, args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []agg
			for rows.Next() {
				var a agg
				if err := rows.Scan(&a.branch, &a.n, &a.amt); err != nil {
					return nil, err
				}
				out = append(out, a)
			}
			return out, rows.Err()
		}
		send := func(list []agg, perm, typ, title, body, link string) error {
			for _, a := range list {
				b := a.branch
				msg := notify.Message{Type: typ, Title: title, Body: fmt.Sprintf(body, a.n, a.amt.StringFixed(2)), Link: link}
				if err := s.Notify.ToPermission(ctx, tx, businessID, &b, perm, msg); err != nil {
					return err
				}
			}
			return nil
		}
		overdue, err := collect(`
			SELECT s.branch_id, count(*), SUM(s.balance)
			FROM sales s JOIN customers c ON c.id = s.customer_id
			WHERE s.kind='sale' AND s.status IN ('completed','partially_refunded') AND s.balance > 0
			  AND COALESCE(s.due_date, s.business_date + c.payment_terms_days) < $1::date
			GROUP BY s.branch_id`, today)
		if err != nil {
			return err
		}
		if err := send(overdue, "customers.view", "customer_debt", "Customer invoices overdue", "%d unpaid customer invoices are past due, total %s", "/customers/debts"); err != nil {
			return err
		}
		overLimit, err := collect(`
			SELECT s.branch_id, count(DISTINCT c.id), SUM(s.balance)
			FROM customers c JOIN sales s ON s.customer_id = c.id AND s.status IN ('completed','partially_refunded') AND s.balance > 0
			WHERE c.credit_limit > 0 AND NOT c.is_walk_in
			  AND (SELECT COALESCE(SUM(x.balance),0) FROM sales x WHERE x.customer_id=c.id AND x.status IN ('completed','partially_refunded')) >= c.credit_limit * 0.9
			GROUP BY s.branch_id`)
		if err != nil {
			return err
		}
		if err := send(overLimit, "customers.credit_limit", "customer_credit", "Customers near their credit limit", "%d customers are at 90%% or more of their credit limit, owing %s", "/customers/debts"); err != nil {
			return err
		}
		supplier, err := collect(`
			SELECT branch_id, count(*), SUM(balance) FROM purchase_invoices
			WHERE status='posted' AND balance > 0 AND COALESCE(due_date, invoice_date) <= $1::date + $2::int
			GROUP BY branch_id`, today, dueDays)
		if err != nil {
			return err
		}
		if err := send(supplier, "suppliers.view", "supplier_debt", "Supplier invoices due", "%d supplier invoices are due within the reminder window or overdue, total %s", "/suppliers/debts"); err != nil {
			return err
		}
		unpaid, err := collect(`SELECT branch_id, count(*), SUM(amount) FROM expenses WHERE status='approved' GROUP BY branch_id`)
		if err != nil {
			return err
		}
		return send(unpaid, "expenses.pay", "pending_payment", "Approved expenses unpaid", "%d approved expenses are waiting to be paid, total %s", "/expenses?status=approved")
	})
}

// OwnerDailySummary notifies every Owner with the day's figures (FR-NTF-11).
func (s *Service) OwnerDailySummary(ctx context.Context, businessID uuid.UUID, raw json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(raw, &in)
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		day := in.Date
		if day == "" {
			t, err := s.today(ctx, tx)
			if err != nil {
				return err
			}
			day = t
		}
		var sales, expenses, cash decimal.Decimal
		var count, low, pending int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(total),0), count(*) FROM sales
			WHERE kind='sale' AND business_date=$1 AND status IN ('completed','partially_refunded','refunded')`, day).Scan(&sales, &count); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM expenses WHERE expense_date=$1 AND status IN ('approved','paid')`, day).Scan(&expenses); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(l.debit-l.credit),0) FROM journal_lines l
			JOIN money_accounts m ON m.id=l.money_account_id WHERE m.type IN ('cash','petty_cash')`).Scan(&cash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM stock_levels sl JOIN products p ON p.id=sl.product_id
			WHERE p.kind='stock' AND p.is_active AND p.reorder_level > 0 AND sl.qty_on_hand <= p.reorder_level`).Scan(&low); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM approval_requests WHERE status='pending'`).Scan(&pending); err != nil {
			return err
		}
		body := fmt.Sprintf("%s: %d sales worth %s, expenses %s, cash on hand %s, %d low stock items, %d approvals waiting",
			day, count, sales.StringFixed(2), expenses.StringFixed(2), cash.StringFixed(2), low, pending)
		owners, err := s.owners(ctx, tx)
		if err != nil {
			return err
		}
		for _, o := range owners {
			if err := s.Notify.ToUser(ctx, tx, businessID, o, notify.Message{Type: "daily_summary", Title: "Daily summary", Body: body, Link: "/dashboard"}); err != nil {
				return err
			}
		}
		return nil
	})
}

// LedgerBalanceCheck verifies that every entry balances and the trial balance
// is zero, and alerts the Owners otherwise.
func (s *Service) LedgerBalanceCheck(ctx context.Context, businessID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		var unbalanced int
		var diff decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM (SELECT entry_id FROM journal_lines GROUP BY entry_id HAVING SUM(debit) <> SUM(credit)) x`).Scan(&unbalanced); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(debit-credit),0) FROM journal_lines`).Scan(&diff); err != nil {
			return err
		}
		if unbalanced == 0 && diff.IsZero() {
			return nil
		}
		slog.Error("ledger check failed", "business_id", businessID, "unbalanced_entries", unbalanced, "difference", diff.String())
		return s.toOwners(ctx, tx, businessID, notify.Message{Type: "security", Title: "Ledger check failed",
			Body: fmt.Sprintf("%d journal entries do not balance; trial balance is off by %s. Contact support.", unbalanced, diff.StringFixed(2))})
	})
}

// StockRebuildCheck compares stock levels with the sum of movements (BR-03).
func (s *Service) StockRebuildCheck(ctx context.Context, businessID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		var mismatched int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM stock_levels sl
			LEFT JOIN (SELECT branch_id, product_id, SUM(qty) q FROM stock_movements GROUP BY branch_id, product_id) m
			  ON m.branch_id = sl.branch_id AND m.product_id = sl.product_id
			WHERE sl.qty_on_hand <> COALESCE(m.q, 0)`).Scan(&mismatched); err != nil {
			return err
		}
		if mismatched == 0 {
			return nil
		}
		slog.Error("stock check failed", "business_id", businessID, "mismatched", mismatched)
		return s.toOwners(ctx, tx, businessID, notify.Message{Type: "security", Title: "Stock check failed",
			Body: fmt.Sprintf("%d product stock levels differ from their movement history. Contact support.", mismatched)})
	})
}

// SessionCleanup removes expired sessions and idempotency keys older than 48 hours.
func (s *Service) SessionCleanup(ctx context.Context, businessID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, now.Add(-48*time.Hour)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM password_resets WHERE expires_at < $1`, now.Add(-24*time.Hour))
		return err
	})
}

// SendNotification delivers one notification by the channels its type has on.
// A missing sender for a channel is not an error: that channel is not configured.
func (s *Service) SendNotification(ctx context.Context, businessID uuid.UUID, raw json.RawMessage) error {
	var in struct {
		NotificationID uuid.UUID `json:"notification_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	var title, body string
	var email, phone *string
	var wantEmail, wantSMS bool
	err := s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT n.title, n.body, u.email::text, u.phone, COALESCE(r.email,false), COALESCE(r.sms,false)
			FROM notifications n JOIN users u ON u.id=n.user_id
			LEFT JOIN notification_rules r ON r.type=n.type
			WHERE n.id=$1 AND u.is_active`, in.NotificationID).Scan(&title, &body, &email, &phone, &wantEmail, &wantSMS)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	if wantEmail && s.Email != nil && email != nil && *email != "" {
		if err := s.Email.Send(ctx, sender.Message{To: *email, Subject: title, Body: body}); err != nil {
			errs = append(errs, fmt.Errorf("email: %w", err))
		}
	}
	if wantSMS && s.SMS != nil && phone != nil && *phone != "" {
		if err := s.SMS.Send(ctx, sender.Message{To: *phone, Subject: title, Body: body}); err != nil {
			errs = append(errs, fmt.Errorf("sms: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) today(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func (s *Service) owners(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT u.id FROM users u JOIN roles r ON r.id=u.role_id WHERE r.slug='owner' AND u.is_active`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Service) toOwners(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, m notify.Message) error {
	owners, err := s.owners(ctx, tx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if err := s.Notify.ToUser(ctx, tx, businessID, o, m); err != nil {
			return err
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
