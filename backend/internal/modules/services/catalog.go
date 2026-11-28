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

func upsertDocumentType(ctx context.Context, tx pgx.Tx, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tINSERT INTO app.document_types (name) VALUES ($1)\n\t\tON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name\n\t\tRETURNING id", clean(name)).Scan(&id)
	return id, err
}

func (s *Service) nextServiceCode(ctx context.Context, tx pgx.Tx, requested, name string) (string, error) {
	base := clean(requested)
	if base != "" {
		base = serviceCodeFromName(base)
	} else {
		base = serviceCodeFromName(name)
	}
	code := base
	for i := 0; i < 100; i++ {
		var taken bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.services WHERE code = $1)", code).Scan(&taken); err != nil {
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
		q := sqlq.New()
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
			       (SELECT count(*) FROM app.service_stages st WHERE st.service_id = c.id) AS stage_count,
			       (SELECT count(*) FROM app.service_package_items pi WHERE pi.package_service_id = c.id) AS service_count
			FROM app.services c`+q.Where()+` ORDER BY c.category, c.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetCatalog(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		return s.loadCatalog(ctx, tx, id, &out)
	})
	return out, err
}

func (s *Service) loadCatalog(ctx context.Context, tx pgx.Tx, id uuid.UUID, out *map[string]any) error {
	c, err := one(ctx, tx, "Service", `
		SELECT id, code, name::text AS name,
		       CASE WHEN category = 'business_registration' THEN 'business' ELSE category::text END AS category,
		       description, standard_price, government_fee AS government_fees,
		       revisions_included AS included_revisions, is_package, (status = 'active') AS is_active, created_at
		FROM app.services WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if c["stages"], err = rows(ctx, tx, `
		SELECT seq AS position, name::text AS name, estimated_days AS est_days, is_branding_stage
		FROM app.service_stages WHERE service_id = $1 ORDER BY seq`, id); err != nil {
		return err
	}
	if c["documents"], err = rows(ctx, tx, `
		SELECT dt.id, dt.name::text AS name, st.seq AS stage_position
		FROM app.service_stage_documents sd
		JOIN app.service_stages st ON st.id = sd.service_stage_id
		JOIN app.document_types dt ON dt.id = sd.document_type_id
		WHERE st.service_id = $1
		ORDER BY st.seq, dt.name`, id); err != nil {
		return err
	}
	if c["package_services"], err = rows(ctx, tx, `
		SELECT s.id, s.name::text AS name, s.standard_price
		FROM app.service_package_items pi
		JOIN app.services s ON s.id = pi.component_service_id
		WHERE pi.package_service_id = $1
		ORDER BY pi.seq, s.name`, id); err != nil {
		return err
	}
	*out = c
	return nil
}

func (s *Service) saveCatalogParts(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, in CatalogIn) error {
	if _, err := tx.Exec(ctx, "DELETE FROM app.service_stages WHERE service_id = $1", id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM app.service_package_items WHERE package_service_id = $1", id); err != nil {
		return err
	}
	stageIDs := make([]uuid.UUID, 0, len(in.Stages))
	for i, st := range in.Stages {
		sid := ids.New()
		isBranding := st.IsBrandingStage || strings.Contains(strings.ToLower(st.Name), "brand")
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.service_stages (id, service_id, seq, name, estimated_days, is_branding_stage)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6)",
			sid, id, i+1, clean(st.Name), st.EstDays, isBranding); err != nil {
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
		dtID, err := upsertDocumentType(ctx, tx, d.Name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.service_stage_documents (service_stage_id, document_type_id, is_mandatory)\n\t\t\tVALUES ($1, $2, true) ON CONFLICT DO NOTHING", stageIDs[pos-1], dtID); err != nil {
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
			if err := tx.QueryRow(ctx, "SELECT is_package FROM app.services WHERE id = $1", sid).Scan(&pkg); err != nil {
				return httpx.NotFound("not_found", "A service in the package was not found")
			}
			if pkg {
				return httpx.BadRequest("invalid_body", "A package cannot contain another package")
			}
			seq++
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.service_package_items (package_service_id, component_service_id, seq)\n\t\t\t\tVALUES ($1, $2, $3)", id, sid, seq); err != nil {
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
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.services WHERE lower(name::text) = lower($1))", clean(in.Name)).Scan(&dupName); err != nil {
			return err
		}
		if dupName {
			return httpx.Conflict("duplicate", "A service with that name already exists")
		}
		code, err := s.nextServiceCode(ctx, tx, in.Code, in.Name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.services (id, code, name, category, description, standard_price, government_fee, revisions_included, is_package)\n\t\t\tVALUES ($1, $2, $3, $4::app.service_category, $5, $6, $7, $8, $9)",
			id, code, clean(in.Name), cat, nz(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions, in.IsPackage); err != nil {
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
		if err := tx.QueryRow(ctx, "SELECT is_package FROM app.services WHERE id = $1 FOR UPDATE", id).Scan(&isPkg); err != nil {
			return httpx.NotFound("not_found", "Service not found")
		}
		in.IsPackage = isPkg
		var cat any
		if in.Category != "" {
			cat = mapServiceCategory(in.Category)
		}
		if clean(in.Name) != "" {
			var dupName bool
			if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.services WHERE id <> $1 AND lower(name::text) = lower($2))", id, clean(in.Name)).Scan(&dupName); err != nil {
				return err
			}
			if dupName {
				return httpx.Conflict("duplicate", "A service with that name already exists")
			}
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.services SET\n\t\t\t  name = COALESCE(NULLIF($2,''), name),\n\t\t\t  category = COALESCE($3::app.service_category, category),\n\t\t\t  description = COALESCE(NULLIF($4,''), description),\n\t\t\t  standard_price = $5,\n\t\t\t  government_fee = $6,\n\t\t\t  revisions_included = $7\n\t\t\tWHERE id = $1", id, clean(in.Name), cat, clean(in.Description), money.Money(in.StandardPrice), money.Money(in.GovernmentFees), in.IncludedRevisions); err != nil {
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
		tag, err := tx.Exec(ctx, "UPDATE app.services SET status = $2::app.record_status WHERE id = $1", id, status)
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
