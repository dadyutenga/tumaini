package notify

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/ids"
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

func (s *Service) ToUser(ctx context.Context, tx pgx.Tx, businessID, userID uuid.UUID, m Message) error {
	var entity any
	if m.EntityID != nil {
		entity = *m.EntityID
	}
	// The business's rule for this type (FR-SET-08): in the app, by email, by SMS. A type with no rule
	// row is in-app only.
	inApp, email, sms := true, false, false
	err := tx.QueryRow(ctx, `SELECT in_app, email, sms FROM notification_rules WHERE type = $1`, m.Type).Scan(&inApp, &email, &sms)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !inApp && !email && !sms {
		return nil // every channel is off for this type
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications (id, business_id, user_id, type, title, body, link, entity_type, entity_id, in_app)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, businessID, userID, m.Type, m.Title, m.Body, m.Link, nullStr(m.EntityType), entity, inApp); err != nil {
		return err
	}
	// Email and SMS fan-out runs as a job, only when the business turned a channel on for this type.
	if email || sms {
		if _, err := tx.Exec(ctx, `
			INSERT INTO jobs (id, business_id, kind, payload, run_at)
			VALUES ($1, $2, 'send_notification', jsonb_build_object('notification_id', $3::uuid), now())
		`, ids.New(), businessID, id); err != nil {
			return err
		}
	}
	return nil
}

// DaysBefore is how many days ahead a reminder of this type fires: the rule's days_before if the
// Owner set one, otherwise the fallback (the older notifications.* setting).
func (s *Service) DaysBefore(ctx context.Context, tx pgx.Tx, typ string, fallback int) int {
	var days *int
	if err := tx.QueryRow(ctx, `SELECT days_before FROM notification_rules WHERE type = $1`, typ).Scan(&days); err != nil || days == nil {
		return fallback
	}
	return *days
}

// ToApprovers notifies the people who can actually decide a request: they hold approvals.decide and the
// kind's own permission (kindPerm, if any), both with a limit that covers amount, in the branch's scope.
// The requester is never notified of their own request (FR-APR-02).
func (s *Service) ToApprovers(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, branchID *uuid.UUID, kindPerm string, amount *decimal.Decimal, requester uuid.UUID, m Message) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT u.id
		FROM users u
		JOIN role_permissions rd ON rd.role_id = u.role_id AND rd.permission_key = 'approvals.decide'
		LEFT JOIN role_permissions rk ON rk.role_id = u.role_id AND rk.permission_key = NULLIF($3, '')
		WHERE u.business_id = $1 AND u.is_active AND u.id <> $5
		  AND ($3 = '' OR rk.permission_key IS NOT NULL)
		  AND ($4::numeric IS NULL OR rd.approval_limit IS NULL OR rd.approval_limit >= $4::numeric)
		  AND ($3 = '' OR $4::numeric IS NULL OR rk.approval_limit IS NULL OR rk.approval_limit >= $4::numeric)
		  AND (rd.scope = 'all' OR $2::uuid IS NULL
		       OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.user_id = u.id AND ub.branch_id = $2))
	`, businessID, branchID, kindPerm, amount, requester)
	if err != nil {
		return err
	}
	var to []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		to = append(to, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range to {
		if err := s.ToUser(ctx, tx, businessID, id, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ToPermission(ctx context.Context, tx pgx.Tx, businessID uuid.UUID, branchID *uuid.UUID, permission string, m Message) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT u.id
		FROM users u
		JOIN role_permissions rp ON rp.role_id = u.role_id AND rp.permission_key = $2
		WHERE u.business_id = $1 AND u.is_active
		  AND (
		    rp.scope = 'all'
		    OR $3::uuid IS NULL
		    OR EXISTS (SELECT 1 FROM user_branches ub WHERE ub.user_id = u.id AND ub.branch_id = $3)
		    OR EXISTS (SELECT 1 FROM roles r WHERE r.id = u.role_id AND r.slug = 'owner')
		  )
	`, businessID, permission, branchID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var idsTo []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return err
		}
		idsTo = append(idsTo, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range idsTo {
		if err := s.ToUser(ctx, tx, businessID, id, m); err != nil {
			return err
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
