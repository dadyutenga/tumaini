package expenses

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
)

type CategoryIn struct {
	Name          string `json:"name" validate:"required,max=100"`
	IsProjectCost *bool  `json:"is_project_cost"`
	IsActive      *bool  `json:"is_active"`
}

func (s *Service) Categories(ctx context.Context, p *auth.Principal, all bool) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, name::text, is_project_cost, status = 'active'
			FROM app.expense_categories
			WHERE tenant_id = $1 AND (status = 'active' OR $2)
			ORDER BY name`, p.TenantID, all)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var isProj, active bool
			if err := rows.Scan(&id, &name, &isProj, &active); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "is_project_cost": isProj, "is_active": active})
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) CreateCategory(ctx context.Context, p *auth.Principal, in CategoryIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	isProj := false
	if in.IsProjectCost != nil {
		isProj = *in.IsProjectCost
	}
	var id uuid.UUID
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.expense_categories (tenant_id, name, is_project_cost)
			VALUES ($1,$2,$3) RETURNING id`, p.TenantID, in.Name, isProj).Scan(&id); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A category with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense_category.created", "expense_category", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.category(ctx, p, id)
}

func (s *Service) UpdateCategory(ctx context.Context, p *auth.Principal, id uuid.UUID, in CategoryIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var oldName string
		var oldProj, oldActive bool
		if err := tx.QueryRow(ctx, `
			SELECT name::text, is_project_cost, status = 'active'
			FROM app.expense_categories WHERE tenant_id = $1 AND id = $2`, p.TenantID, id).Scan(&oldName, &oldProj, &oldActive); err != nil {
			return httpx.NotFound("not_found", "Expense category not found")
		}
		isProj := oldProj
		if in.IsProjectCost != nil {
			isProj = *in.IsProjectCost
		}
		st := "active"
		if in.IsActive != nil && !*in.IsActive {
			st = "inactive"
		} else if in.IsActive == nil && !oldActive {
			st = "inactive"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.expense_categories SET name = $3, is_project_cost = $4, status = $5::app.record_status
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, in.Name, isProj, st); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A category with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "expense_category.updated", "expense_category", id,
			map[string]any{"name": oldName, "is_project_cost": oldProj, "is_active": oldActive},
			map[string]any{"name": in.Name, "is_project_cost": isProj, "is_active": st == "active"})
	})
	if err != nil {
		return nil, err
	}
	return s.category(ctx, p, id)
}

func (s *Service) category(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	list, err := s.Categories(ctx, p, true)
	if err != nil {
		return nil, err
	}
	for _, c := range list {
		if c["id"] == id {
			return c, nil
		}
	}
	return nil, httpx.NotFound("not_found", "Expense category not found")
}
