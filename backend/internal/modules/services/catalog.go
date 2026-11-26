package services

import (
	"context"
	"fmt"
	"strings"

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
	Name            string `json:"name" validate:"required,max=160"`
	EstDays         int    `json:"est_days" validate:"gte=0,lte=365"`
	IsBrandingStage bool   `json:"is_branding_stage"`
}

type RequiredDocIn struct {
	Name          string `json:"name" validate:"required,max=160"`
	StagePosition *int   `json:"stage_position" validate:"omitempty,gte=1"`
}

type CatalogIn struct {
	Code              string          `json:"code" validate:"max=30"`
	Name              string          `json:"name" validate:"max=200"`
	Category          string          `json:"category" validate:"omitempty,oneof=business business_registration design printing licensing consulting package other"`
	Description       string          `json:"description" validate:"max=4000"`
	StandardPrice     decimal.Decimal `json:"standard_price" validate:"dgte0"`
	GovernmentFees    decimal.Decimal `json:"government_fees" validate:"dgte0"`
	IncludedRevisions int             `json:"included_revisions" validate:"gte=0,lte=50"`
	IsPackage         bool            `json:"is_package"`
	Stages            []StageIn       `json:"stages" validate:"dive"`
	Documents         []RequiredDocIn `json:"documents" validate:"dive"`
	PackageServices   []uuid.UUID     `json:"package_services"`
}

func mapServiceCategory(cat string) string {
	switch strings.ToLower(strings.TrimSpace(cat)) {
	case "business", "business_registration", "licensing":
		return "business_registration"
	case "design", "printing":
		return "design"
	case "consulting":
		return "consulting"
	default:
		return "other"
	}
}

func upsertDocumentType(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO app.document_types (tenant_id, name) VALUES ($1, $2)
		ON CONFLICT (tenant_id, name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`, tenantID, clean(name)).Scan(&id)
	return id, err
}

func (s *Service) nextServiceCode(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, requested, name string) (string, error) {
	base := clean(requested)
	if base != "" {
		base = serviceCodeFromName(base)
	} else {
		base = serviceCodeFromName(name)
	}
	code := base
	for i := 0; i < 100; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.services WHERE tenant_id = $1 AND code = $2)`, tenantID, code).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
		suffix := fmt.Sprintf("_%d", i+2)
		cut := base
		if len(cut)+len(suffix) > 30 {
			cut = cut[:30-len(suffix)]
		}
		code = cut + suffix
	}
	return "SVC_" + strings.ToUpper(uuid.NewString()[:8]), nil
}

func (s *Service) ListCatalog(ctx context.Context, p *auth.Principal, q0, category string, all bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("c.tenant_id = $1")
		if t := clean(q0); t != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.description ILIKE '%' || ? || '%' OR c.code ILIKE '%' || ? || '%')", t, t, t)
		}
		if category != "" {
			q.Add("c.category = ?::app.service_category", mapServiceCategory(category))
		}
		q.If(!all, "c.status = 'active'")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.services c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `
			SELECT c.id, c.code, c.name::text AS name,
			       CASE WHEN c.category = 'business_registration' THEN 'business' ELSE c.category::text END AS category,
			       c.description, c.standard_price, c.government_fee AS government_fees,
			       c.revisions_included AS included_revisions, c.is_package, (c.status = 'active') AS is_active,
			       (SELECT count(*) FROM app.service_stages st WHERE st.tenant_id = c.tenant_id AND st.service_id = c.id) AS stage_count,
			       (SELECT count(*) FROM app.service_package_items pi WHERE pi.tenant_id = c.tenant_id AND pi.package_service_id = c.id) AS service_count
			FROM app.services c`+q.Where()+` ORDER BY c.category, c.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetCatalog(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.loadCatalog(ctx, tx, p.TenantID, id, &out)
	})
	return out, err
}

func (s *Service) loadCatalog(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, out *map[string]any) error {
	c, err := one(ctx, tx, "Service", `
		SELECT id, code, name::text AS name,
		       CASE WHEN category = 'business_registration' THEN 'business' ELSE category::text END AS category,
		       description, standard_price, government_fee AS government_fees,
		       revisions_included AS included_revisions, is_package, (status = 'active') AS is_active, created_at
		FROM app.services WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return err
	}
	if c["stages"], err = rows(ctx, tx, `
		SELECT seq AS position, name::text AS name, estimated_days AS est_days, is_branding_stage
		FROM app.service_stages WHERE tenant_id = $1 AND service_id = $2 ORDER BY seq`, tenantID, id); err != nil {
		return err
	}
	if c["documents"], err = rows(ctx, tx, `
		SELECT dt.id, dt.name::text AS name, st.seq AS stage_position
		FROM app.service_stage_documents sd
		JOIN app.service_stages st ON st.tenant_id = sd.tenant_id AND st.id = sd.service_stage_id
		JOIN app.document_types dt ON dt.tenant_id = sd.tenant_id AND dt.id = sd.document_type_id
		WHERE sd.tenant_id = $1 AND st.service_id = $2
		ORDER BY st.seq, dt.name`, tenantID, id); err != nil {
		return err
	}
	if c["package_services"], err = rows(ctx, tx, `
		SELECT s.id, s.name::text AS name, s.standard_price
		FROM app.service_package_items pi
		JOIN app.services s ON s.tenant_id = pi.tenant_id AND s.id = pi.component_service_id
		WHERE pi.tenant_id = $1 AND pi.package_service_id = $2
		ORDER BY pi.seq, s.name`, tenantID, id); err != nil {
		return err
	}
	*out = c
	return nil
}

func (s *Service) saveCatalogParts(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, in CatalogIn) error {
	if _, err := tx.Exec(ctx, `DELETE FROM app.service_stages WHERE tenant_id = $1 AND service_id = $2`, p.TenantID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.service_package_items WHERE tenant_id = $1 AND package_service_id = $2`, p.TenantID, id); err != nil {
		return err
	}
	stageIDs := make([]uuid.UUID, 0, len(in.Stages))
	for i, st := range in.Stages {
		sid := ids.New()
		isBranding := st.IsBrandingStage || strings.Contains(strings.ToLower(st.Name), "brand")
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.service_stages (id, tenant_id, service_id, seq, name, estimated_days, is_branding_stage)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			sid, p.TenantID, id, i+1, clean(st.Name), st.EstDays, isBranding); err != nil {
			return err
		}
		stageIDs = append(stageIDs, sid)
	}
	if len(in.Documents) > 0 && len(stageIDs) == 0 {
		return httpx.BadRequest("invalid_body", "Add at least one stage before adding required documents")
	}
	for _, d := range in.Documents {
		pos := 1
		if d.StagePosition != nil {
			pos = *d.StagePosition
		}
		if pos < 1 || pos > len(stageIDs) {
			return httpx.BadRequest("invalid_body", "A required document points at a stage that does not exist")
		}
		dtID, err := upsertDocumentType(ctx, tx, p.TenantID, d.Name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.service_stage_documents (tenant_id, service_stage_id, document_type_id, is_mandatory)
			VALUES ($1,$2,$3,true) ON CONFLICT DO NOTHING`,
			p.TenantID, stageIDs[pos-1], dtID); err != nil {
			return err
		}
	}
	if in.IsPackage {
		seen := map[uuid.UUID]bool{}
		seq := 0
		for _, sid := range in.PackageServices {
			if seen[sid] || sid == id {
				continue
			}
			seen[sid] = true
			var pkg bool
			if err := tx.QueryRow(ctx, `SELECT is_package FROM app.services WHERE tenant_id = $1 AND id = $2`, p.TenantID, sid).Scan(&pkg); err != nil {
				return httpx.NotFound("not_found", "A service in the package was not found")
			}
			if pkg {
				return httpx.BadRequest("invalid_body", "A package cannot contain another package")
			}
			seq++
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.service_package_items (tenant_id, package_service_id, component_service_id, seq)
				VALUES ($1,$2,$3,$4)`, p.TenantID, id, sid, seq); err != nil {
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
	cat := "business_registration"
	if in.Category != "" {
		cat = mapServiceCategory(in.Category)
	}
	id := ids.New()
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var dupName bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.services WHERE tenant_id = $1 AND lower(name::text) = lower($2))`, p.TenantID, clean(in.Name)).Scan(&dupName); err != nil {
			return err
		}
		if dupName {
			return httpx.Conflict("duplicate", "A service with that name already exists")
		}
		code, err := s.nextServiceCode(ctx, tx, p.TenantID, in.Code, in.Name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.services (id, tenant_id, code, name, category, description, standard_price, government_fee, revisions_included, is_package)
			VALUES ($1,$2,$3,$4,$5::app.service_category,$6,$7,$8,$9,$10)`,
			id, p.TenantID, code, clean(in.Name), cat, nz(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions, in.IsPackage); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A service with that code or name already exists")
			}
			return err
		}
		if err := s.saveCatalogParts(ctx, tx, p, id, in); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.created", "service", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}

// UpdateCatalog changes a service. Projects already started keep their own copy of stages and documents.
func (s *Service) UpdateCatalog(ctx context.Context, p *auth.Principal, id uuid.UUID, in CatalogIn, replaceParts bool) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var isPkg bool
		if err := tx.QueryRow(ctx, `SELECT is_package FROM app.services WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).Scan(&isPkg); err != nil {
			return httpx.NotFound("not_found", "Service not found")
		}
		in.IsPackage = isPkg
		var cat any
		if in.Category != "" {
			cat = mapServiceCategory(in.Category)
		}
		if clean(in.Name) != "" {
			var dupName bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.services WHERE tenant_id = $1 AND id <> $2 AND lower(name::text) = lower($3))`, p.TenantID, id, clean(in.Name)).Scan(&dupName); err != nil {
				return err
			}
			if dupName {
				return httpx.Conflict("duplicate", "A service with that name already exists")
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.services SET
			  name = COALESCE(NULLIF($3,''), name),
			  category = COALESCE($4::app.service_category, category),
			  description = COALESCE(NULLIF($5,''), description),
			  standard_price = $6,
			  government_fee = $7,
			  revisions_included = $8
			WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, id, clean(in.Name), cat, clean(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A service with that code already exists")
			}
			return err
		}
		if replaceParts {
			if err := s.saveCatalogParts(ctx, tx, p, id, in); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.updated", "service", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}

func (s *Service) SetCatalogActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) (map[string]any, error) {
	status := "inactive"
	if active {
		status = "active"
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.services SET status = $3::app.record_status WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, status)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Service not found")
		}
		return s.Audit.Record(ctx, tx, p, "service_catalog.active", "service", id, nil, map[string]any{"is_active": active})
	})
	if err != nil {
		return nil, err
	}
	return s.GetCatalog(ctx, p, id)
}
