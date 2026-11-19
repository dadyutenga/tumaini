package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type StageIn struct {
	Name    string `json:"name" validate:"required,max=160"`
	EstDays int    `json:"est_days" validate:"gte=0,lte=3650"`
}

type RequiredDocIn struct {
	Name          string `json:"name" validate:"required,max=160"`
	StagePosition *int   `json:"stage_position" validate:"omitempty,gte=1"`
}

type CatalogIn struct {
	Name              string          `json:"name" validate:"max=200"`
	Category          string          `json:"category" validate:"omitempty,oneof=business design other"`
	Description       string          `json:"description" validate:"max=2000"`
	StandardPrice     decimal.Decimal `json:"standard_price" validate:"dgte0"`
	GovernmentFees    decimal.Decimal `json:"government_fees" validate:"dgte0"`
	IncludedRevisions int             `json:"included_revisions" validate:"gte=0,lte=50"`
	IsPackage         bool            `json:"is_package"`
	Stages            []StageIn       `json:"stages" validate:"dive"`
	Documents         []RequiredDocIn `json:"documents" validate:"dive"`
	PackageServices   []uuid.UUID     `json:"package_services"`
}

func (s *Service) ListCatalog(ctx context.Context, p *auth.Principal, q0, category string, all bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if t := clean(q0); t != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.description ILIKE '%' || ? || '%')", t, t)
		}
		q.If(category != "", "c.category = ?", category)
		q.If(!all, "c.is_active")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_catalog c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `
			SELECT c.id, c.name, c.category, c.description, c.standard_price, c.government_fees, c.included_revisions, c.is_package, c.is_active,
			       (SELECT count(*) FROM svc_catalog_stages st WHERE st.service_id = c.id) AS stage_count,
			       (SELECT count(*) FROM svc_package_items pi WHERE pi.package_id = c.id) AS service_count
			FROM svc_catalog c`+q.Where()+` ORDER BY c.category, c.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetCatalog(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return s.loadCatalog(ctx, tx, id, &out)
	})
	return out, err
}

func (s *Service) loadCatalog(ctx context.Context, tx pgx.Tx, id uuid.UUID, out *map[string]any) error {
	c, err := one(ctx, tx, "Service", `SELECT id, name, category, description, standard_price, government_fees, included_revisions, is_package, is_active, created_at FROM svc_catalog WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if c["stages"], err = rows(ctx, tx, `SELECT position, name, est_days FROM svc_catalog_stages WHERE service_id=$1 ORDER BY position`, id); err != nil {
		return err
	}
	if c["documents"], err = rows(ctx, tx, `SELECT id, name, stage_position FROM svc_catalog_documents WHERE service_id=$1 ORDER BY stage_position NULLS FIRST, name`, id); err != nil {
		return err
	}
	if c["package_services"], err = rows(ctx, tx, `SELECT s.id, s.name, s.standard_price FROM svc_package_items pi JOIN svc_catalog s ON s.id = pi.service_id WHERE pi.package_id=$1 ORDER BY s.name`, id); err != nil {
		return err
	}
	*out = c
	return nil
}

func (s *Service) saveCatalogParts(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, in CatalogIn) error {
	if _, err := tx.Exec(ctx, `DELETE FROM svc_catalog_stages WHERE service_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM svc_catalog_documents WHERE service_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM svc_package_items WHERE package_id=$1`, id); err != nil {
		return err
	}
	for i, st := range in.Stages {
		if _, err := tx.Exec(ctx, `INSERT INTO svc_catalog_stages (id, business_id, service_id, position, name, est_days) VALUES ($1,$2,$3,$4,$5,$6)`,
			ids.New(), p.BusinessID, id, i+1, clean(st.Name), st.EstDays); err != nil {
			return err
		}
	}
	for _, d := range in.Documents {
		if d.StagePosition != nil && *d.StagePosition > len(in.Stages) {
			return httpx.BadRequest("invalid_body", "A required document points at a stage that does not exist")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO svc_catalog_documents (id, business_id, service_id, stage_position, name) VALUES ($1,$2,$3,$4,$5)`,
			ids.New(), p.BusinessID, id, d.StagePosition, clean(d.Name)); err != nil {
			return err
		}
	}
	if in.IsPackage {
		seen := map[uuid.UUID]bool{}
		for _, sid := range in.PackageServices {
			if seen[sid] || sid == id {
				continue
			}
			seen[sid] = true
			var pkg bool
			if err := tx.QueryRow(ctx, `SELECT is_package FROM svc_catalog WHERE id=$1`, sid).Scan(&pkg); err != nil {
				return httpx.NotFound("not_found", "A service in the package was not found")
			}
			if pkg {
				return httpx.BadRequest("invalid_body", "A package cannot contain another package")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO svc_package_items (business_id, package_id, service_id) VALUES ($1,$2,$3)`, p.BusinessID, id, sid); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) CreateCatalog(ctx context.Context, p *auth.Principal, in CatalogIn) (map[string]any, error) {
	if clean(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Service name is required")
	}
	if in.Category == "" {
		in.Category = "business"
	}
	id := ids.New()
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_catalog (id, business_id, name, category, description, standard_price, government_fees, included_revisions, is_package)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, p.BusinessID, clean(in.Name), in.Category, nz(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions, in.IsPackage); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A service with that name already exists")
			}
			return err
		}
		if err := s.saveCatalogParts(ctx, tx, p, id, in); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.created", "svc_catalog", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}

// UpdateCatalog changes a service. Projects already started keep their own copy of stages and documents.
func (s *Service) UpdateCatalog(ctx context.Context, p *auth.Principal, id uuid.UUID, in CatalogIn, replaceParts bool) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var isPkg bool
		if err := tx.QueryRow(ctx, `SELECT is_package FROM svc_catalog WHERE id=$1 FOR UPDATE`, id).Scan(&isPkg); err != nil {
			return httpx.NotFound("not_found", "Service not found")
		}
		in.IsPackage = isPkg
		if _, err := tx.Exec(ctx, `
			UPDATE svc_catalog SET name=COALESCE(NULLIF($2,''), name), category=COALESCE(NULLIF($3,''), category), description=COALESCE(NULLIF($4,''), description),
			  standard_price=$5, government_fees=$6, included_revisions=$7 WHERE id=$1`,
			id, clean(in.Name), in.Category, clean(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A service with that name already exists")
			}
			return err
		}
		if replaceParts {
			if err := s.saveCatalogParts(ctx, tx, p, id, in); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.updated", "svc_catalog", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}

func (s *Service) SetCatalogActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE svc_catalog SET is_active=$2 WHERE id=$1`, id, active)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Service not found")
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.active", "svc_catalog", id, nil, map[string]any{"is_active": active})
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}
