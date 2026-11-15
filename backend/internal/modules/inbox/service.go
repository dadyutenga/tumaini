package inbox

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
)

type Service struct {
	DB       *db.DB
	Approval *approval.Service
}

func New(database *db.DB, ap *approval.Service) *Service {
	return &Service{DB: database, Approval: ap}
}

func (s *Service) Decide(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return s.Approval.Decide(ctx, tx, p, id, approve, note)
	})
}

func (s *Service) List(ctx context.Context, p *auth.Principal, status string) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, kind, subject_type, subject_id, amount, summary, status, requested_by, created_at
			FROM approval_requests
			WHERE ($1='' OR status=$1) AND (requested_by=$2 OR status='pending')
			ORDER BY created_at DESC LIMIT 100
		`, status, p.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, subject, by uuid.UUID
			var kind, subjectType, summary, st string
			var amount decimal.NullDecimal
			var created any
			if err := rows.Scan(&id, &kind, &subjectType, &subject, &amount, &summary, &st, &by, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "kind": kind, "subject_type": subjectType, "subject_id": subject, "amount": amount, "summary": summary, "status": st, "requested_by": by, "created_at": created})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) Notifications(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, type, title, body, read_at, created_at FROM notifications WHERE user_id=$1 ORDER BY created_at DESC LIMIT 50`, p.UserID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var typ, title, body string
			var read, created any
			if err := rows.Scan(&id, &typ, &title, &body, &read, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "type": typ, "title": title, "body": body, "read_at": read, "created_at": created})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) Audit(ctx context.Context, p *auth.Principal, action string) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, action, entity_type, entity_id, user_id, created_at
			FROM audit_logs WHERE ($1='' OR action=$1) ORDER BY created_at DESC LIMIT 200
		`, action)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var entity *uuid.UUID
			var user *uuid.UUID
			var act, typ string
			var created any
			if err := rows.Scan(&id, &act, &typ, &entity, &user, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "action": act, "entity_type": typ, "entity_id": entity, "user_id": user, "created_at": created})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}

func (s *Service) ReadAll(ctx context.Context, p *auth.Principal) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL`, p.UserID)
		return err
	})
}

func (s *Service) GetApproval(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	req, err := s.must(ctx, p, id)
	return req, err
}

func (s *Service) must(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var kind, summary, status, subjectType string
		var subject, by uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT kind, subject_type, subject_id, summary, status, requested_by FROM approval_requests WHERE id=$1`, id).Scan(&kind, &subjectType, &subject, &summary, &status, &by); err != nil {
			return httpx.NotFound("not_found", "Approval request not found")
		}
		out = map[string]any{"id": id, "kind": kind, "subject_type": subjectType, "subject_id": subject, "summary": summary, "status": status, "requested_by": by}
		return nil
	})
	return out, err
}
