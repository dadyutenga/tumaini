// Package outbound writes customer messages into the transactional outbox
// (app.outbound_messages) and drains ready rows from a background worker.
package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/sender"
	"bms/internal/platform/setting"
)

const unavailableMsg = "This channel is not set up. Ask an administrator to configure it."

type Service struct {
	DB       *db.DB
	Clock    clock.Clock
	Channels sender.Channels
}

func New(database *db.DB, clk clock.Clock, ch sender.Channels) *Service {
	return &Service{DB: database, Clock: clk, Channels: ch}
}

type Message struct {
	Channel           string
	Recipient         string
	Subject           string
	Body              string
	AttachmentFileID  *uuid.UUID
	IdempotencyKey    string
	CustomerID        *uuid.UUID
	SaleID            *uuid.UUID
	InvoiceID         *uuid.UUID
	QuotationID       *uuid.UUID
	PaymentReceivedID *uuid.UUID
	ProjectID         *uuid.UUID
	DesignJobID       *uuid.UUID
	RenewalID         *uuid.UUID
	PayslipID         *uuid.UUID
}

type Row struct {
	ID       uuid.UUID `json:"id"`
	Status   string    `json:"status"`
	Existing bool      `json:"-"`
}

// ValidateChannel normalizes and validates a customer outbound channel.
func ValidateChannel(ch string) (string, error) {
	ch = strings.TrimSpace(strings.ToLower(ch))
	switch ch {
	case "sms", "whatsapp", "email":
		return ch, nil
	default:
		return "", httpx.BadRequest("invalid_body", "channel must be sms, whatsapp, or email")
	}
}

// CheckAvailable validates the channel and returns 503 channel_unavailable if no sender is configured.
func (s *Service) CheckAvailable(channel string) (string, error) {
	ch, err := ValidateChannel(channel)
	if err != nil {
		return "", err
	}
	if !s.Channels.Available(ch) {
		return "", httpx.ServiceUnavailable("channel_unavailable", unavailableMsg)
	}
	return ch, nil
}

// Enqueue writes an app.outbound_messages row in tx and queues a dispatch_outbound worker job.
// If the idempotency key already exists, the existing row is returned cleanly.
func (s *Service) Enqueue(ctx context.Context, tx pgx.Tx, m Message) (Row, error) {
	ch, err := s.CheckAvailable(m.Channel)
	if err != nil {
		return Row{}, err
	}
	recipient := strings.TrimSpace(m.Recipient)
	if len(recipient) < 5 || len(recipient) > 254 {
		return Row{}, httpx.BadRequest("no_recipient", "Customer has no valid phone number or email for this channel")
	}
	body := truncate(strings.TrimSpace(m.Body), 4000)
	if body == "" {
		return Row{}, httpx.BadRequest("invalid_body", "Message body is required")
	}
	if m.CustomerID == nil && m.SaleID == nil && m.InvoiceID == nil && m.QuotationID == nil &&
		m.PaymentReceivedID == nil && m.ProjectID == nil && m.DesignJobID == nil && m.RenewalID == nil && m.PayslipID == nil {
		return Row{}, httpx.BadRequest("invalid_body", "Outbound message must reference a business record")
	}
	subj := truncate(strings.TrimSpace(m.Subject), 200)
	var subjArg any
	if subj != "" {
		subjArg = subj
	}
	idemKey := strings.TrimSpace(m.IdempotencyKey)
	if idemKey == "" {
		idemKey = ids.New().String()
	}
	idemKey = truncate(idemKey, 200)

	id := ids.New()
	sub, err := tx.Begin(ctx)
	if err != nil {
		return Row{}, err
	}
	if _, err := sub.Exec(ctx, `
		INSERT INTO app.outbound_messages (
			id, channel, recipient, subject, body, attachment_file_id,
			status, next_attempt_at, idempotency_key,
			customer_id, sale_id, invoice_id, quotation_id, payment_received_id,
			project_id, design_job_id, renewal_id, payslip_id
		) VALUES (
			$1, $2::app.message_channel, $3, $4, $5, $6,
			'queued', now(), $7,
			$8, $9, $10, $11, $12,
			$13, $14, $15, $16
		)`,
		id, ch, recipient, subjArg, body, m.AttachmentFileID,
		idemKey,
		m.CustomerID, m.SaleID, m.InvoiceID, m.QuotationID, m.PaymentReceivedID,
		m.ProjectID, m.DesignJobID, m.RenewalID, m.PayslipID,
	); err != nil {
		_ = sub.Rollback(ctx)
		if db.IsUnique(err) {
			var existing Row
			if qerr := tx.QueryRow(ctx, "\n\t\t\t\tSELECT id, status::text FROM app.outbound_messages\n\t\t\t\tWHERE idempotency_key = $1", idemKey).Scan(&existing.ID, &existing.Status); qerr == nil {
				existing.Existing = true
				return existing, nil
			}
		}
		return Row{}, err
	}
	if err := sub.Commit(ctx); err != nil {
		return Row{}, err
	}
	if err := jobs.EnqueueTx(ctx, tx, jobs.DispatchOutbound, map[string]any{"message_id": id}); err != nil {
		return Row{}, err
	}
	return Row{ID: id, Status: "queued"}, nil
}

// RenderTemplate reads a business setting template (falling back to fallback) and replaces {{key}} tokens.
func RenderTemplate(ctx context.Context, tx pgx.Tx, key, fallback string, vars map[string]string) string {
	tpl := strings.TrimSpace(setting.String(ctx, tx, key, ""))
	if tpl == "" {
		tpl = fallback
	}
	for k, v := range vars {
		tpl = strings.ReplaceAll(tpl, "{{"+k+"}}", v)
	}
	return strings.TrimSpace(tpl)
}

// Dispatch claims ready app.outbound_messages rows using FOR UPDATE SKIP LOCKED
// and delivers them via the configured channel sender.
func (s *Service) Dispatch(ctx context.Context, _ json.RawMessage) error {
	for i := 0; i < 20; i++ {
		ok, err := s.dispatchOne(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	return nil
}

func (s *Service) dispatchOne(ctx context.Context) (bool, error) {
	type claimed struct {
		id       uuid.UUID
		channel  string
		to       string
		subject  string
		body     string
		attempts int
	}
	var row claimed
	found := false
	err := s.DB.InJobsTx(ctx, db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `SET LOCAL ROLE bms_worker`)
		err := tx.QueryRow(ctx, "\n\t\t\tSELECT id, channel::text, recipient, COALESCE(subject, ''), body, attempts\n\t\t\tFROM app.outbound_messages\n\t\t\tWHERE status IN ('queued', 'failed')\n\t\t\t  AND next_attempt_at <= now()\n\t\t\t  AND (locked_until IS NULL OR locked_until < now())\n\t\t\tORDER BY next_attempt_at, created_at\n\t\t\tFOR UPDATE SKIP LOCKED\n\t\t\tLIMIT 1").Scan(&row.id, &row.channel, &row.to, &row.subject, &row.body, &row.attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		row.attempts++
		_, err = tx.Exec(ctx, `
			UPDATE app.outbound_messages
			SET status = 'sending', locked_until = now() + interval '5 minutes', attempts = $2
			WHERE id = $1`, row.id, row.attempts)
		return err
	})
	if err != nil || !found {
		return false, err
	}

	snd, provider := s.Channels.For(row.channel)
	var sendErr error
	if snd == nil {
		sendErr = errors.New("channel is not configured")
	} else {
		sendErr = snd.Send(ctx, sender.Message{To: row.to, Subject: row.subject, Body: row.body})
	}

	err = s.DB.InJobsTx(ctx, db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `SET LOCAL ROLE bms_worker`)
		if sendErr == nil {
			_, err := tx.Exec(ctx, `
				UPDATE app.outbound_messages
				SET status = 'sent', sent_at = $2, provider = $3, locked_until = NULL, last_error = NULL
				WHERE id = $1`, row.id, s.Clock.Now(), truncate(provider, 40))
			return err
		}
		lastErr := truncate(sendErr.Error(), 1000)
		shift := row.attempts
		if shift > 7 {
			shift = 7
		}
		backoff := time.Duration(30*(1<<shift)) * time.Second
		if backoff > time.Hour {
			backoff = time.Hour
		}
		nextAt := time.Now().UTC().Add(backoff)
		if _, err := tx.Exec(ctx, `
			UPDATE app.outbound_messages
			SET status = 'failed', last_error = $2, next_attempt_at = $3, locked_until = NULL
			WHERE id = $1`, row.id, lastErr, nextAt); err != nil {
			return err
		}
		if row.attempts >= 20 {
			_, err := tx.Exec(ctx, `UPDATE app.outbound_messages SET status = 'cancelled' WHERE id = $1`, row.id)
			return err
		}
		return nil
	})
	return true, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
