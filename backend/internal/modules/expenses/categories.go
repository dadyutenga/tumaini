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
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT id, name::text, is_project_cost, status = 'active'\n\t\t\tFROM app.expense_categories\n\t\t\tWHERE (status = 'active' OR $1)\n\t\t\tORDER BY name", all)
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
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.expense_categories (name, is_project_cost)\n\t\t\tVALUES ($1, $2) RETURNING id", in.Name, isProj).Scan(&id); err != nil {
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
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT name::text, is_project_cost, status = 'active'\n\t\t\tFROM app.expense_categories WHERE id = $1", id).Scan(&oldName, &oldProj, &oldActive); err != nil {
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
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.expense_categories SET name = $2, is_project_cost = $3, status = $4::app.record_status\n\t\t\tWHERE id = $1", id, in.Name, isProj, st); err != nil {
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
