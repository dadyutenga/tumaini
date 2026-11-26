// Package system runs the background jobs that do not belong to one business
// module: data export, reminders, the owner's daily summary, integrity checks,
// cleanup and email/SMS delivery of notifications.
package system

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/ids"
	"bms/internal/platform/sender"
	"bms/internal/platform/setting"
	"bms/internal/platform/storage"
)

type Service struct {
	DB       *db.DB
	Notify   *notify.Service
	Outbound *outbound.Service
	Store    storage.Store
	Clock    clock.Clock
	Email    sender.Sender
	SMS      sender.Sender
}

// exportExcluded are tenant tables whose rows are transient queue/idempotency/backup state.
var exportExcluded = map[string]bool{
	"idempotency_keys":    true,
	"job_queue":           true,
	"scheduled_job_marks": true,
	"backup_runs":         true,
}

// exportStripped are secret, credential, or encrypted columns removed from every exported row.
var exportStripped = []string{
	"password_hash", "pin_hash", "refresh_hash", "prev_hash", "code_hash", "request_hash",
	"token_hash", "secret_ciphertext", "key_hash", "key_prefix",
	"national_id_ciphertext", "national_id_hmac",
	"tin_ciphertext", "tin_hmac",
	"bank_account_ciphertext", "bank_account_hmac",
	"mobile_money_ciphertext", "mobile_money_hmac",
}

// exportMax matches app.files.size_bytes CHECK (size_bytes <= 52428800, 50 MiB).
const exportMax = 50 << 20

type exportPayload struct {
	ExportID     uuid.UUID `json:"export_id"`
	UserID       uuid.UUID `json:"user_id"`
	MembershipID uuid.UUID `json:"membership_id"`
}

// BusinessExport writes every app.* tenant table as JSON lines into one zip file.
// Row-level security limits each query to the tenant.
func (s *Service) BusinessExport(ctx context.Context, tenantID uuid.UUID, raw json.RawMessage) error {
	var in exportPayload
	if err := json.Unmarshal(raw, &in); err != nil || in.ExportID == uuid.Nil {
		return fmt.Errorf("export payload: %w", err)
	}
	actor := db.Actor{TenantID: tenantID, UserID: in.UserID, MembershipID: in.MembershipID}
	if err := s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.backup_runs WHERE id = $1 AND tenant_id = $2)`, in.ExportID, tenantID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		if actor.MembershipID == uuid.Nil {
			_ = tx.QueryRow(ctx, `
				SELECT m.id, m.user_id FROM app.memberships m
				JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
				JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id
				WHERE m.tenant_id = $1 AND m.status = 'active' AND r.code = 'admin'
				LIMIT 1`, tenantID).Scan(&actor.MembershipID, &actor.UserID)
		}
		_, err := tx.Exec(ctx, `UPDATE app.backup_runs SET status = 'running', error = NULL WHERE id = $1`, in.ExportID)
		return err
	}); err != nil {
		return err
	}
	buf, counts, err := s.buildExport(ctx, actor)
	if err != nil {
		s.failExport(ctx, tenantID, in.ExportID, err)
		return err
	}
	name := "business-export-" + s.Clock.Now().Format("20060102-150405") + ".zip"
	file, err := s.Store.Save(ctx, tenantID.String(), name, bytes.NewReader(buf), exportMax)
	if err != nil {
		s.failExport(ctx, tenantID, in.ExportID, err)
		return err
	}
	sum := sha256.Sum256(buf)
	return s.DB.InActorTx(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		fileID := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.files (id, tenant_id, storage_key, original_name, mime_type, size_bytes, sha256, classification)
			VALUES ($1,$2,$3,$4,'application/zip',$5,$6,'restricted')
		`, fileID, tenantID, file.Path, name, file.Size, sum[:]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.backup_runs
			SET status = 'succeeded', finished_at = $2, size_bytes = $3, storage_uri = $4, checksum_sha256 = $5, error = NULL
			WHERE id = $1
		`, in.ExportID, s.Clock.Now(), file.Size, file.Path, sum[:]); err != nil {
			return err
		}
		if actor.UserID != uuid.Nil {
			_ = s.Notify.ToUser(ctx, tx, tenantID, actor.UserID, notify.Message{
				Type: "day_closed", Title: "Data export ready",
				Body: fmt.Sprintf("Your business data export (%d tables) can be downloaded.", len(counts)),
				Link: "/exports/" + in.ExportID.String(), EntityType: "backup_runs", EntityID: &in.ExportID,
			})
		}
		return nil
	})
}

func (s *Service) failExport(ctx context.Context, tenantID, id uuid.UUID, cause error) {
	_ = s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app.backup_runs SET status = 'failed', error = $2, finished_at = $3 WHERE id = $1`, id, truncate(cause.Error(), 500), s.Clock.Now())
		return err
	})
}

func (s *Service) buildExport(ctx context.Context, actor db.Actor) ([]byte, map[string]int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	counts := map[string]int{}
	err := s.DB.InActorTx(ctx, actor, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.table_name FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
			WHERE c.table_schema = 'app' AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE'
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
		tables = append([]string{"tenants"}, tables...)
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
			q := `SELECT to_jsonb(t)` + strip + ` FROM ` + pgx.Identifier{"app", table}.Sanitize() + ` t`
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
		manifest, _ := json.MarshalIndent(map[string]any{
			"tenant_id":  actor.TenantID,
			"created_at": s.Clock.Now(),
			"format":     "one JSON object per line",
			"rows":       counts,
		}, "", "  ")
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

// DebtReminders raises summary notifications for overdue customer sales/invoices,
// customers near their credit limit, supplier purchases due soon or overdue,
// and approved expenses still unpaid.
func (s *Service) DebtReminders(ctx context.Context, tenantID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.today(ctx, tx)
		if err != nil {
			return err
		}
		dueDays := s.Notify.DaysBefore(ctx, tx, "debt_due", setting.Int(ctx, tx, "notifications.supplier_due_days", 7))
		send := func(n int, amt decimal.Decimal, perm, title, body, link string) error {
			if n == 0 {
				return nil
			}
			msg := notify.Message{Type: "debt_due", Title: title, Body: fmt.Sprintf(body, n, amt.StringFixed(2)), Link: link}
			return s.Notify.ToPermission(ctx, tx, tenantID, nil, perm, msg)
		}

		// Overdue customer sales + service invoices.
		var overdueN int
		var overdueAmt decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(bal), 0) FROM (
				SELECT s.balance_due AS bal FROM app.sales s
				WHERE s.status = 'completed' AND s.balance_due > 0 AND COALESCE(s.due_date, s.business_date) < $1::date
				UNION ALL
				SELECT i.balance_due AS bal FROM app.invoices i
				WHERE i.status IN ('issued', 'partially_paid') AND i.balance_due > 0 AND i.due_date < $1::date
			) x`, today).Scan(&overdueN, &overdueAmt); err != nil {
			return err
		}
		if err := send(overdueN, overdueAmt, "finance.debt.view", "Customer invoices overdue",
			"%d unpaid customer invoices are past due, total %s", "/customers/debts"); err != nil {
			return err
		}

		// Optional customer outbound reminders only when the tenant explicitly enabled sms or whatsapp on debt_due.
		if overdueN > 0 && s.Outbound != nil {
			var ruleEnabled, ruleSMS, ruleWA bool
			err := tx.QueryRow(ctx, `
				SELECT enabled, sms, whatsapp FROM app.notification_rules
				WHERE tenant_id = $1 AND notification_type = 'debt_due'::app.notification_type`,
				tenantID).Scan(&ruleEnabled, &ruleSMS, &ruleWA)
			if err == nil && ruleEnabled && (ruleSMS || ruleWA) {
				var channels []string
				if ruleSMS && s.Outbound.Channels.Available("sms") {
					channels = append(channels, "sms")
				}
				if ruleWA && s.Outbound.Channels.Available("whatsapp") {
					channels = append(channels, "whatsapp")
				}
				if len(channels) > 0 {
					crows, err := tx.Query(ctx, `
						SELECT c.id, c.full_name::text, c.phone::text, SUM(x.bal) AS due
						FROM app.customers c
						JOIN (
							SELECT s.customer_id, s.balance_due AS bal FROM app.sales s
							WHERE s.tenant_id = $1 AND s.customer_id IS NOT NULL AND s.status = 'completed' AND s.balance_due > 0 AND COALESCE(s.due_date, s.business_date) < $2::date
							UNION ALL
							SELECT i.customer_id, i.balance_due AS bal FROM app.invoices i
							WHERE i.tenant_id = $1 AND i.status IN ('issued', 'partially_paid') AND i.balance_due > 0 AND i.due_date < $2::date
						) x ON x.customer_id = c.id
						WHERE c.tenant_id = $1 AND c.phone IS NOT NULL AND length(c.phone::text) >= 5
						GROUP BY c.id, c.full_name, c.phone`, tenantID, today)
					if err != nil {
						return err
					}
					type dueCust struct {
						id    uuid.UUID
						name  string
						phone string
						bal   decimal.Decimal
					}
					var list []dueCust
					for crows.Next() {
						var dc dueCust
						if err := crows.Scan(&dc.id, &dc.name, &dc.phone, &dc.bal); err != nil {
							crows.Close()
							return err
						}
						list = append(list, dc)
					}
					crows.Close()
					if err := crows.Err(); err != nil {
						return err
					}
					for _, dc := range list {
						body := outbound.RenderTemplate(ctx, tx, "notifications.sms_template_debt",
							"Reminder: {{name}}, your outstanding balance is {{balance}}.",
							map[string]string{"name": dc.name, "balance": dc.bal.StringFixed(2)},
						)
						for _, ch := range channels {
							cid := dc.id
							if _, err := s.Outbound.Enqueue(ctx, tx, tenantID, outbound.Message{
								Channel:        ch,
								Recipient:      strings.TrimSpace(dc.phone),
								Subject:        "Payment reminder",
								Body:           body,
								IdempotencyKey: fmt.Sprintf("auto-debt:%s:%s:%s", dc.id.String(), ch, today),
								CustomerID:     &cid,
							}); err != nil {
								return err
							}
						}
					}
				}
			}
		}

		// Customers at 90% or more of credit limit.
		var overLimitN int
		var overLimitAmt decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(bal), 0) FROM (
				SELECT c.id, SUM(s.balance_due) AS bal
				FROM app.customers c
				JOIN app.sales s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id
				WHERE c.credit_limit IS NOT NULL AND c.credit_limit > 0
				  AND s.status = 'completed' AND s.balance_due > 0
				GROUP BY c.id, c.credit_limit
				HAVING SUM(s.balance_due) >= c.credit_limit * 0.9
			) x`).Scan(&overLimitN, &overLimitAmt); err != nil {
			return err
		}
		if err := send(overLimitN, overLimitAmt, "shop.customer.credit_limit", "Customers near their credit limit",
			"%d customers are at 90%% or more of their credit limit, owing %s", "/customers/debts"); err != nil {
			return err
		}

		// Supplier purchases due within reminder window or overdue.
		var supN int
		var supAmt decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(balance_due), 0)
			FROM app.purchases
			WHERE status <> 'cancelled' AND balance_due > 0 AND COALESCE(due_date, order_date) <= $1::date + $2::int`,
			today, dueDays).Scan(&supN, &supAmt); err != nil {
			return err
		}
		if err := send(supN, supAmt, "finance.debt.view", "Supplier invoices due",
			"%d supplier invoices are due within the reminder window or overdue, total %s", "/suppliers/debts"); err != nil {
			return err
		}

		// Approved expenses waiting to be paid.
		var expN int
		var expAmt decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(amount), 0) FROM app.expenses WHERE status = 'approved'`).Scan(&expN, &expAmt); err != nil {
			return err
		}
		return send(expN, expAmt, "finance.expense.pay", "Approved expenses unpaid",
			"%d approved expenses are waiting to be paid, total %s", "/expenses?status=approved")
	})
}

// OwnerDailySummary notifies every Admin with the day's figures.
func (s *Service) OwnerDailySummary(ctx context.Context, tenantID uuid.UUID, raw json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(raw, &in)
	return s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
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
			SELECT COALESCE(SUM(total), 0), count(*) FROM app.sales
			WHERE business_date = $1::date AND status = 'completed'`, day).Scan(&sales, &count); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0) FROM app.expenses
			WHERE expense_date = $1::date AND status IN ('approved', 'paid')`, day).Scan(&expenses); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(current_balance), 0) FROM app.financial_accounts
			WHERE status = 'active' AND kind IN ('cash', 'petty_cash')`).Scan(&cash); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.stock_balances sb
			JOIN app.products p ON p.tenant_id = sb.tenant_id AND p.id = sb.product_id
			JOIN app.tenant_settings ts ON ts.tenant_id = p.tenant_id
			WHERE p.status = 'active' AND COALESCE(p.min_stock_level, ts.low_stock_default) > 0
			  AND sb.quantity_on_hand <= COALESCE(p.min_stock_level, ts.low_stock_default)`).Scan(&low); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.v_pending_approvals`).Scan(&pending); err != nil {
			return err
		}
		body := fmt.Sprintf("%s: %d sales worth %s, expenses %s, cash on hand %s, %d low stock items, %d approvals waiting",
			day, count, sales.StringFixed(2), expenses.StringFixed(2), cash.StringFixed(2), low, pending)
		return s.toOwners(ctx, tx, tenantID, notify.Message{Type: "day_closed", Title: "Daily summary", Body: body, Link: "/dashboard"})
	})
}

// LedgerBalanceCheck verifies that every financial account's current_balance matches
// the sum of its ledger transactions, and alerts Admins otherwise.
func (s *Service) LedgerBalanceCheck(ctx context.Context, tenantID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var mismatched int
		var diff decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT count(*), COALESCE(SUM(ABS(a.current_balance - COALESCE(t.net, 0))), 0)
			FROM app.financial_accounts a
			LEFT JOIN (
				SELECT account_id, SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END) AS net
				FROM app.financial_transactions GROUP BY account_id
			) t ON t.account_id = a.id
			WHERE a.current_balance <> COALESCE(t.net, 0)`).Scan(&mismatched, &diff); err != nil {
			return err
		}
		if mismatched == 0 {
			return nil
		}
		slog.Error("ledger check failed", "tenant_id", tenantID, "mismatched_accounts", mismatched, "difference", diff.String())
		return s.toOwners(ctx, tx, tenantID, notify.Message{
			Type: "account_locked", Title: "Ledger check failed",
			Body: fmt.Sprintf("%d financial account balances differ from their ledger by %s. Contact support.", mismatched, diff.StringFixed(2)),
		})
	})
}

// StockRebuildCheck compares stock_balances with the sum of stock_movements.
func (s *Service) StockRebuildCheck(ctx context.Context, tenantID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var mismatched int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM app.stock_balances sb
			LEFT JOIN (
				SELECT location_id, product_id, SUM(quantity_delta) AS q
				FROM app.stock_movements GROUP BY location_id, product_id
			) m ON m.location_id = sb.location_id AND m.product_id = sb.product_id
			WHERE sb.quantity_on_hand <> COALESCE(m.q, 0)`).Scan(&mismatched); err != nil {
			return err
		}
		if mismatched == 0 {
			return nil
		}
		slog.Error("stock check failed", "tenant_id", tenantID, "mismatched", mismatched)
		return s.toOwners(ctx, tx, tenantID, notify.Message{
			Type: "account_locked", Title: "Stock check failed",
			Body: fmt.Sprintf("%d product stock levels differ from their movement history. Contact support.", mismatched),
		})
	})
}

// SessionCleanup removes expired sessions, password reset requests, and old idempotency keys.
// Runs on the worker pool because bms_worker holds DELETE grants on iam.sessions and iam.password_reset_requests.
func (s *Service) SessionCleanup(ctx context.Context, tenantID uuid.UUID, _ json.RawMessage) error {
	return s.DB.InJobsTx(ctx, db.Actor{TenantID: tenantID}, func(ctx context.Context, tx pgx.Tx) error {
		now := s.Clock.Now()
		if _, err := tx.Exec(ctx, `DELETE FROM iam.sessions WHERE absolute_expires_at < $1`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.idempotency_keys WHERE expires_at < $1`, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM iam.password_reset_requests WHERE expires_at < $1`, now.Add(-24*time.Hour))
		return err
	})
}

// SendNotification delivers one notification by email/SMS if the tenant rule enables it
// and a sender is configured.
func (s *Service) SendNotification(ctx context.Context, tenantID uuid.UUID, raw json.RawMessage) error {
	var in struct {
		NotificationID uuid.UUID `json:"notification_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.NotificationID == uuid.Nil {
		return err
	}
	var recipientID uuid.UUID
	var title, body string
	var wantEmail, wantSMS bool
	err := s.DB.InJobsTx(ctx, db.Actor{TenantID: tenantID}, func(ctx context.Context, tx pgx.Tx) error {
		// app.notifications restrictive policy allows current_user = 'bms_worker'.
		_, _ = tx.Exec(ctx, `SET LOCAL ROLE bms_worker`)
		return tx.QueryRow(ctx, `
			SELECT n.recipient_id, n.title, COALESCE(n.body, ''), COALESCE(r.email, false), COALESCE(r.sms, false)
			FROM app.notifications n
			LEFT JOIN app.notification_rules r ON r.tenant_id = n.tenant_id AND r.notification_type = n.notification_type
			WHERE n.id = $1`, in.NotificationID).Scan(&recipientID, &title, &body, &wantEmail, &wantSMS)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !wantEmail && !wantSMS {
		return nil
	}
	var email, phone *string
	err = s.DB.InTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT u.email::text, u.phone
			FROM app.memberships m JOIN iam.users u ON u.id = m.user_id
			WHERE m.id = $1 AND m.status = 'active' AND u.status = 'active'`, recipientID).Scan(&email, &phone)
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
	tz := "Africa/Dar_es_Salaam"
	_ = tx.QueryRow(ctx, `SELECT timezone FROM app.tenants WHERE id = app.current_tenant_id()`).Scan(&tz)
	d, err := clock.BusinessDate(s.Clock.Now(), tz, setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func (s *Service) owners(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT m.user_id
		FROM app.memberships m
		JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
		JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id
		WHERE m.status = 'active' AND r.status = 'active' AND r.code = 'admin'`)
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

func (s *Service) toOwners(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, m notify.Message) error {
	owners, err := s.owners(ctx, tx)
	if err != nil {
		return err
	}
	for _, o := range owners {
		if err := s.Notify.ToUser(ctx, tx, tenantID, o, m); err != nil {
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
