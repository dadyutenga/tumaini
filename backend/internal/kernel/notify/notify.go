// Package notify writes in-app notifications (app.notifications).
//
// Recipients are memberships. Callers still name people by their user id; the service finds that
// person's seat in the tenant. Delivery by email or SMS to staff is not part of the approved model:
// app.outbound_messages records messages to customers and must point at a customer, sale, invoice
// and so on, so staff notifications are in-app only. A tenant's rule for a type (enabled, in_app)
// decides whether it is written at all.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/setting"
)

type Service struct{}

func New() *Service { return &Service{} }

type Message struct {
	Type       string
	Title      string
	Body       string
	Link       string
	EntityType string
	EntityID   *uuid.UUID
}

// Types are the values of app.notification_type.
var Types = map[string]bool{
	"low_stock": true, "debt_due": true, "task_overdue": true, "task_assigned": true, "renewal_due": true, "salary_due": true,
	"approval_waiting": true, "approval_decided": true, "design_brief_assigned": true, "design_feedback": true,
	"design_deadline": true, "design_approved": true, "print_ready": true, "account_locked": true, "day_closed": true,
	"project_stage_changed": true, "payment_received": true,
}

// legacy maps the event names modules used before the schema replacement to the enum. A name with no
// entry and no enum match is skipped rather than failing the business operation that raised it.
var legacy = map[string]string{
	"approval_request": "approval_waiting", "approval_decision": "approval_decided", "customer_debt": "debt_due",
	"supplier_debt": "debt_due", "customer_credit": "debt_due", "pending_payment": "debt_due", "security": "account_locked",
	"daily_summary": "day_closed", "transfer_difference": "day_closed", "till_difference": "day_closed",
	"payroll_awaiting_approval": "approval_waiting", "leave_decision": "approval_decided", "project_completed": "project_stage_changed",
	"design_assigned": "design_brief_assigned", "design_client_decision": "design_approved", "design_print_ready": "print_ready",
	"task_due": "task_overdue",
}

func resolve(t string) (string, bool) {
	if Types[t] {
		return t, true
	}
	m, ok := legacy[t]
	return m, ok
}

// ToUser notifies one person. It does nothing when the person has no active seat in the tenant or the
// tenant switched this type off.
func (s *Service) ToUser(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID, m Message) error {
	typ, ok := resolve(m.Type)
	if !ok {
		slog.Debug("notification type not in the approved schema; skipped", "type", m.Type)
		return nil
	}
	enabled, inApp := true, true
	err := tx.QueryRow(ctx, `SELECT enabled, in_app FROM app.notification_rules WHERE tenant_id = $1 AND notification_type = $2::app.notification_type`, tenantID, typ).Scan(&enabled, &inApp)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !enabled || !inApp {
		return nil
	}
	var membership uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM app.memberships WHERE tenant_id = $1 AND user_id = $2 AND status = 'active'`, tenantID, userID).Scan(&membership); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return s.insert(ctx, tx, tenantID, membership, typ, m)
}

func (s *Service) insert(ctx context.Context, tx pgx.Tx, tenantID, membership uuid.UUID, typ string, m Message) error {
	// One notification per person, subject and day: repeating the same event on the same day adds nothing.
	dedupe := typ + ":" + uuid.NewString()
	if m.EntityID != nil {
		dedupe = typ + ":" + m.EntityID.String() + ":" + time.Now().UTC().Format("2006-01-02") + ":" + strings.ToLower(m.Title)
		if len(dedupe) > 200 {
			dedupe = dedupe[:200]
		}
	}
	link := m.Link
	if link != "" && !strings.HasPrefix(link, "/") {
		link = ""
	}
	body := m.Body
	if len(body) > 1000 {
		body = body[:1000]
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO app.notifications (tenant_id, recipient_id, notification_type, title, body, link_path, dedupe_key)
		VALUES ($1, $2, $3::app.notification_type, left($4, 200), NULLIF($5, ''), NULLIF($6, ''), $7)
		ON CONFLICT (tenant_id, recipient_id, dedupe_key) DO NOTHING`,
		tenantID, membership, typ, m.Title, body, link, dedupe)
	return err
}

// DaysBefore is how many days ahead a reminder of this type fires: the tenant's setting if it set
// one, otherwise the fallback.
func (s *Service) DaysBefore(ctx context.Context, tx pgx.Tx, typ string, fallback int) int {
	if t, ok := resolve(typ); ok {
		typ = t
	}
	return setting.Int(ctx, tx, "notifications.days_before_"+typ, fallback)
}

// holders lists the people whose seat currently grants permission. A per-person override (allow or
// deny, unexpired) beats the role, exactly as app.has_permission does.
const holders = `
	SELECT DISTINCT m.user_id, m.id FROM app.memberships m
	WHERE m.tenant_id = $1 AND m.status = 'active' AND ($3::uuid IS NULL OR m.user_id <> $3)
	  AND coalesce(
	    (SELECT o.effect = 'allow' FROM app.membership_permission_overrides o
	      WHERE o.tenant_id = m.tenant_id AND o.membership_id = m.id AND o.permission_code = $2 AND (o.expires_at IS NULL OR o.expires_at > now())),
	    EXISTS (SELECT 1 FROM app.membership_roles mr
	              JOIN app.role_permissions rp ON rp.tenant_id = mr.tenant_id AND rp.role_id = mr.role_id
	              JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id AND r.status = 'active'
	             WHERE mr.tenant_id = m.tenant_id AND mr.membership_id = m.id AND rp.permission_code = $2))`

func (s *Service) toHolders(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, permission string, except *uuid.UUID, m Message) error {
	typ, ok := resolve(m.Type)
	if !ok {
		slog.Debug("notification type not in the approved schema; skipped", "type", m.Type)
		return nil
	}
	enabled, inApp := true, true
	if err := tx.QueryRow(ctx, `SELECT enabled, in_app FROM app.notification_rules WHERE tenant_id = $1 AND notification_type = $2::app.notification_type`, tenantID, typ).Scan(&enabled, &inApp); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !enabled || !inApp {
		return nil
	}
	rows, err := tx.Query(ctx, holders, tenantID, permission, except)
	if err != nil {
		return err
	}
	var to []uuid.UUID
	for rows.Next() {
		var user, membership uuid.UUID
		if err := rows.Scan(&user, &membership); err != nil {
			rows.Close()
			return err
		}
		to = append(to, membership)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, membership := range to {
		if err := s.insert(ctx, tx, tenantID, membership, typ, m); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
	}
	return nil
}

// ToApprovers notifies the people who can decide a request: those who hold kindPerm. The requester is
// never notified of their own request. Amount limits are enforced by the database when the person
// decides (app.assert_can_approve), so they are not repeated here.
func (s *Service) ToApprovers(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, _ *uuid.UUID, kindPerm string, _ *decimal.Decimal, requester uuid.UUID, m Message) error {
	return s.toHolders(ctx, tx, tenantID, kindPerm, &requester, m)
}

// ToPermission notifies everyone who holds a permission.
func (s *Service) ToPermission(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, _ *uuid.UUID, permission string, m Message) error {
	return s.toHolders(ctx, tx, tenantID, permission, nil, m)
}
