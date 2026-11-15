package notify

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications (id, business_id, user_id, type, title, body, link, entity_type, entity_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, ids.New(), businessID, userID, m.Type, m.Title, m.Body, m.Link, nullStr(m.EntityType), entity)
	return err
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
