package services

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sender"
	"bms/internal/platform/sqlq"
)

type ProjectIn struct {
	ClientID   uuid.UUID        `json:"client_id" validate:"required"`
	ServiceID  *uuid.UUID       `json:"service_id"`
	Name       string           `json:"name" validate:"max=200"`
	StartDate  string           `json:"start_date" validate:"omitempty,date"`
	OwnerID    *uuid.UUID       `json:"owner_id"`
	TotalPrice *decimal.Decimal `json:"total_price" validate:"omitempty,dgte0"`
	Notes      string           `json:"notes" validate:"max=2000"`
}

type ProjectPatch struct {
	Name         string           `json:"name" validate:"max=200"`
	Status       string           `json:"status" validate:"omitempty,oneof=not_started in_progress on_hold completed cancelled"`
	StartDate    string           `json:"start_date" validate:"omitempty,date"`
	OwnerID      *uuid.UUID       `json:"owner_id"`
	TotalPrice   *decimal.Decimal `json:"total_price" validate:"omitempty,dgte0"`
	Notes        *string          `json:"notes" validate:"omitempty,max=2000"`
	CancelReason string           `json:"cancel_reason" validate:"max=1000"`
}

// ownProject limits a list to projects the member owns, created, or has a stage on.
func ownProject(q *sqlq.Q, p *auth.Principal) {
	if !p.Has("services.project.view_all") {
		mid := p.MembershipID
		q.Add(`(pr.owner_id = ? OR pr.created_by = ? OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.owner_id = ?))`, mid, mid, mid)
	}
}

const projectCols = `pr.id, pr.project_no AS number, pr.title::text AS name, pr.status::text AS status, pr.start_date, pr.target_end_date,
	pr.total_price, NULL::text AS notes, om.user_id AS owner_id, ou.full_name::text AS owner_name,
	pr.customer_id AS client_id, c.full_name::text AS client_name, pr.service_id, pr.created_at, pr.completed_at,
	(SELECT count(*) FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id) AS stages_total,
	(SELECT count(*) FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status = 'done') AS stages_done,
	(SELECT st.name::text FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' ORDER BY st.seq LIMIT 1) AS current_stage,
	(COALESCE((SELECT SUM(i.amount_paid) FROM app.invoices i WHERE i.tenant_id = pr.tenant_id AND i.project_id = pr.id AND i.status <> 'void'), 0)
	 + COALESCE((SELECT SUM(pay.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.tenant_id = pay.tenant_id AND a.payment_received_id = pay.id), 0))
	             FROM app.payments_received pay WHERE pay.tenant_id = pr.tenant_id AND pay.customer_id = pr.customer_id AND pay.status = 'posted' AND pay.notes LIKE '%[project:' || pr.id::text || ']%'), 0)) AS paid,
	(COALESCE((SELECT SUM(e.amount) FROM app.expenses e WHERE e.tenant_id = pr.tenant_id AND e.project_id = pr.id AND e.status IN ('approved','paid')), 0)
	 + COALESCE((SELECT SUM(b.amount) FROM app.contractor_bills b WHERE b.tenant_id = pr.tenant_id AND b.project_id = pr.id AND b.status <> 'void'), 0)) AS costs`

const projectFrom = ` FROM app.projects pr
	JOIN app.customers c ON c.tenant_id = pr.tenant_id AND c.id = pr.customer_id
	LEFT JOIN app.memberships om ON om.tenant_id = pr.tenant_id AND om.id = pr.owner_id
	LEFT JOIN iam.users ou ON ou.id = om.user_id`

type ProjectFilter struct {
	Q        string
	Status   string
	ClientID *uuid.UUID
	OwnerID  *uuid.UUID
	Late     bool
}

func (s *Service) ListProjects(ctx context.Context, p *auth.Principal, f ProjectFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := s.projectQuery(ctx, tx, p, f, today)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+projectFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		out, err = rows(ctx, tx, `SELECT `+projectCols+projectFrom+q.Where()+` ORDER BY pr.created_at DESC, pr.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err == nil {
			addBalances(out)
		}
		return err
	})
	return out, total, err
}

func (s *Service) projectQuery(ctx context.Context, tx pgx.Tx, p *auth.Principal, f ProjectFilter, today string) *sqlq.Q {
	q := sqlq.New(p.TenantID)
	q.Add("pr.tenant_id = $1")
	if t := clean(f.Q); t != "" {
		q.Add("(pr.title ILIKE '%' || ? || '%' OR pr.project_no ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%')", t, t, t)
	}
	q.If(f.Status != "", "pr.status = ?::app.project_status", f.Status)
	q.If(f.ClientID != nil, "pr.customer_id = ?", f.ClientID)
	if f.OwnerID != nil {
		if mid, err := s.resolveMembership(ctx, tx, p.TenantID, f.OwnerID); err == nil && mid != nil {
			q.Add("pr.owner_id = ?", *mid)
		} else {
			q.Add("pr.owner_id = ?", *f.OwnerID)
		}
	}
	q.If(f.Late, `pr.status IN ('not_started','in_progress') AND EXISTS (SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' AND st.deadline < ?::date)`, today)
	ownProject(q, p)
	return q
}

func addBalances(list []map[string]any) {
	for _, r := range list {
		r["balance"] = num(r, "total_price").Sub(num(r, "paid"))
		r["profit"] = num(r, "paid").Sub(num(r, "costs"))
	}
}

// ProjectBoard groups active projects by their current stage.
func (s *Service) ProjectBoard(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("pr.tenant_id = $1 AND pr.status IN ('not_started','in_progress','on_hold')")
		ownProject(q, p)
		list, err := rows(ctx, tx, `SELECT `+projectCols+`,
			(SELECT st.deadline FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' ORDER BY st.seq LIMIT 1) AS stage_deadline,
			(SELECT st.status::text FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' ORDER BY st.seq LIMIT 1) AS stage_status`+
			projectFrom+q.Where()+` ORDER BY pr.created_at`, q.Args...)
		if err != nil {
			return err
		}
		addBalances(list)
		idx := map[string]int{}
		out = []map[string]any{}
		for _, r := range list {
			name, _ := r["current_stage"].(string)
			if name == "" {
				name = "No stages"
			}
			i, ok := idx[name]
			if !ok {
				i = len(out)
				idx[name] = i
				out = append(out, map[string]any{"stage": name, "projects": []map[string]any{}})
			}
			out[i]["projects"] = append(out[i]["projects"].([]map[string]any), r)
		}
		return nil
	})
	return out, err
}

type stageSeed struct {
	serviceStageID uuid.UUID
	name           string
	estDays        int
	isBranding     bool
	checklist      []string
	docs           []struct {
		typeID uuid.UUID
		name   string
	}
}

// stagesFor copies the stages, checklist items, and required documents of a service (or package).
func (s *Service) stagesFor(ctx context.Context, tx pgx.Tx, tenantID, serviceID uuid.UUID) (name string, price decimal.Decimal, seeds []stageSeed, err error) {
	var isPkg bool
	if err = tx.QueryRow(ctx, `SELECT name::text, standard_price, is_package FROM app.services WHERE tenant_id = $1 AND id = $2 AND status = 'active'`, tenantID, serviceID).Scan(&name, &price, &isPkg); err != nil {
		return "", price, nil, httpx.NotFound("not_found", "Service not found or inactive")
	}
	list := []uuid.UUID{serviceID}
	if isPkg {
		list = nil
		r, err := tx.Query(ctx, `SELECT pi.component_service_id FROM app.service_package_items pi WHERE pi.tenant_id = $1 AND pi.package_service_id = $2 ORDER BY pi.seq`, tenantID, serviceID)
		if err != nil {
			return "", price, nil, err
		}
		for r.Next() {
			var id uuid.UUID
			if err := r.Scan(&id); err != nil {
				r.Close()
				return "", price, nil, err
			}
			list = append(list, id)
		}
		r.Close()
		if err := r.Err(); err != nil {
			return "", price, nil, err
		}
	}
	for _, sid := range list {
		stages, err := rows(ctx, tx, `SELECT id, name::text AS name, estimated_days, is_branding_stage FROM app.service_stages WHERE tenant_id = $1 AND service_id = $2 ORDER BY seq`, tenantID, sid)
		if err != nil {
			return "", price, nil, err
		}
		for _, st := range stages {
			stID := st["id"].(uuid.UUID)
			est := 0
			switch v := st["estimated_days"].(type) {
			case int16:
				est = int(v)
			case int32:
				est = int(v)
			case int64:
				est = int(v)
			}
			isBranding, _ := st["is_branding_stage"].(bool)
			seed := stageSeed{serviceStageID: stID, name: st["name"].(string), estDays: est, isBranding: isBranding}
			chk, err := rows(ctx, tx, `SELECT label::text AS label FROM app.service_stage_checklist_items WHERE tenant_id = $1 AND service_stage_id = $2 ORDER BY seq`, tenantID, stID)
			if err != nil {
				return "", price, nil, err
			}
			for _, c := range chk {
				seed.checklist = append(seed.checklist, c["label"].(string))
			}
			docs, err := rows(ctx, tx, `
				SELECT dt.id, dt.name::text AS name
				FROM app.service_stage_documents sd
				JOIN app.document_types dt ON dt.tenant_id = sd.tenant_id AND dt.id = sd.document_type_id
				WHERE sd.tenant_id = $1 AND sd.service_stage_id = $2 ORDER BY dt.name`, tenantID, stID)
			if err != nil {
				return "", price, nil, err
			}
			for _, d := range docs {
				seed.docs = append(seed.docs, struct {
					typeID uuid.UUID
					name   string
				}{typeID: d["id"].(uuid.UUID), name: d["name"].(string)})
			}
			seeds = append(seeds, seed)
		}
	}
	return name, price, seeds, nil
}

func (s *Service) CreateProject(ctx context.Context, p *auth.Principal, in ProjectIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.createProjectTx(ctx, tx, p, in, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, id)
}

func (s *Service) createProjectTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in ProjectIn, quotationID *uuid.UUID) (uuid.UUID, error) {
	id := ids.New()
	var clientName string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT full_name::text, (archived_at IS NULL AND client_status <> 'inactive') FROM app.customers WHERE tenant_id = $1 AND id = $2 AND client_status IS NOT NULL FOR UPDATE`, p.TenantID, in.ClientID).Scan(&clientName, &active); err != nil {
		return id, httpx.NotFound("not_found", "Client not found")
	}
	if !active {
		return id, httpx.Conflict("client_inactive", "That client is not active")
	}
	name := clean(in.Name)
	price := money.Zero
	var seeds []stageSeed
	var serviceID uuid.UUID
	if in.ServiceID != nil {
		svcName, svcPrice, sd, err := s.stagesFor(ctx, tx, p.TenantID, *in.ServiceID)
		if err != nil {
			return id, err
		}
		if name == "" {
			name = svcName + " - " + clientName
		}
		price, seeds = svcPrice, sd
		serviceID = *in.ServiceID
	} else {
		var err error
		if serviceID, err = s.defaultCustomService(ctx, tx, p.TenantID); err != nil {
			return id, err
		}
	}
	if name == "" {
		return id, httpx.BadRequest("invalid_body", "Project name or a service is required")
	}
	if in.TotalPrice != nil {
		price = money.Money(*in.TotalPrice)
	}
	start := in.StartDate
	if start == "" {
		var err error
		if start, err = s.bizDate(ctx, tx); err != nil {
			return id, err
		}
	}
	ownerMid := p.MembershipID
	if in.OwnerID != nil {
		mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.OwnerID)
		if err != nil {
			return id, err
		}
		if mid != nil {
			ownerMid = *mid
		}
	}
	buID, err := s.servicesBU(ctx, tx, p.TenantID)
	if err != nil {
		return id, err
	}
	number, err := sequence.Next(ctx, tx, uuid.Nil, "project")
	if err != nil {
		return id, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.projects (id, tenant_id, project_no, customer_id, service_id, quotation_id, business_unit_id, title, owner_id, start_date, total_price)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11)`,
		id, p.TenantID, number, in.ClientID, serviceID, uuidOrNil(quotationID), buID, name, ownerMid, start, price); err != nil {
		return id, err
	}
	// Transition immediately to in_progress so projects start active.
	if _, err := tx.Exec(ctx, `UPDATE app.projects SET status = 'in_progress' WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
		return id, err
	}
	t0, _ := time.Parse("2006-01-02", start)
	days := 0
	for i, sd := range seeds {
		days += sd.estDays
		var deadline any
		if sd.estDays > 0 {
			deadline = t0.AddDate(0, 0, days).Format("2006-01-02")
		}
		stageID := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.project_stages (id, tenant_id, project_id, service_stage_id, seq, name, owner_id, deadline, is_branding_stage)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			stageID, p.TenantID, id, sd.serviceStageID, i+1, sd.name, ownerMid, deadline, sd.isBranding); err != nil {
			return id, err
		}
		for ci, lbl := range sd.checklist {
			if _, err := tx.Exec(ctx, `INSERT INTO app.project_stage_checklist (tenant_id, project_stage_id, seq, label) VALUES ($1,$2,$3,$4)`,
				p.TenantID, stageID, ci+1, lbl); err != nil {
				return id, err
			}
		}
		for _, d := range sd.docs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.client_documents (id, tenant_id, customer_id, project_id, project_stage_id, document_type_id, title, status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,'missing')`,
				ids.New(), p.TenantID, in.ClientID, id, stageID, d.typeID, d.name); err != nil {
				return id, err
			}
		}
	}
	if clean(in.Notes) != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.client_interactions (tenant_id, customer_id, project_id, kind, summary)
			VALUES ($1,$2,$3,'note',$4)`, p.TenantID, in.ClientID, id, clean(in.Notes)); err != nil {
			return id, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE app.customers SET client_status = 'active' WHERE tenant_id = $1 AND id = $2 AND client_status = 'lead'`, p.TenantID, in.ClientID); err != nil {
		return id, err
	}
	return id, s.Audit.Record(ctx, tx, p, "project.created", "project", id, nil, map[string]any{"number": number, "client_id": in.ClientID, "service_id": serviceID, "total_price": price})
}

func (s *Service) projectRow(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, lock bool) (map[string]any, error) {
	q := sqlq.New(p.TenantID, id)
	q.Add("pr.tenant_id = $1 AND pr.id = $2")
	ownProject(q, p)
	sql := `SELECT ` + projectCols + projectFrom + q.Where()
	if lock {
		sql += ` FOR UPDATE OF pr`
	}
	r, err := one(ctx, tx, "Project", sql, q.Args...)
	if err == nil {
		addBalances([]map[string]any{r})
	}
	return r, err
}

func (s *Service) GetProject(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		stages, err := rows(ctx, tx, `
			SELECT st.id, st.seq AS position, st.name::text AS name, st.status::text AS status,
			       om.user_id AS owner_id, ou.full_name::text AS owner_name, st.deadline,
			       COALESCE(ss.estimated_days, 0) AS est_days, st.is_branding_stage,
			       (SELECT h.note FROM app.project_stage_history h WHERE h.tenant_id = st.tenant_id AND h.project_stage_id = st.id AND h.note IS NOT NULL ORDER BY h.created_at DESC LIMIT 1) AS note,
			       st.updated_at, st.completed_at,
			       (SELECT count(*) FROM app.client_documents d WHERE d.tenant_id = st.tenant_id AND d.project_stage_id = st.id AND d.status = 'missing') AS documents_missing
			FROM app.project_stages st
			LEFT JOIN app.service_stages ss ON ss.tenant_id = st.tenant_id AND ss.id = st.service_stage_id
			LEFT JOIN app.memberships om ON om.tenant_id = st.tenant_id AND om.id = st.owner_id
			LEFT JOIN iam.users ou ON ou.id = om.user_id
			WHERE st.tenant_id = $1 AND st.project_id = $2 ORDER BY st.seq`, p.TenantID, id)
		if err != nil {
			return err
		}
		chkRows, err := rows(ctx, tx, `
			SELECT c.project_stage_id, c.label::text AS text, c.is_done AS done
			FROM app.project_stage_checklist c
			JOIN app.project_stages st ON st.tenant_id = c.tenant_id AND st.id = c.project_stage_id
			WHERE c.tenant_id = $1 AND st.project_id = $2
			ORDER BY c.project_stage_id, c.seq`, p.TenantID, id)
		if err != nil {
			return err
		}
		chkByStage := map[uuid.UUID][]map[string]any{}
		for _, c := range chkRows {
			sid := c["project_stage_id"].(uuid.UUID)
			chkByStage[sid] = append(chkByStage[sid], map[string]any{"text": c["text"], "done": c["done"]})
		}
		for _, st := range stages {
			sid := st["id"].(uuid.UUID)
			list := chkByStage[sid]
			if list == nil {
				list = []map[string]any{}
			}
			st["checklist"] = list
		}
		pr["stages"] = stages

		if pr["documents"], err = rows(ctx, tx, `
			SELECT d.id, d.project_stage_id AS stage_id, d.title::text AS name,
			       COALESCE(dt.name::text, CASE WHEN d.is_final_deliverable THEN 'deliverable' ELSE 'required' END) AS doc_type,
			       d.status::text AS status, d.file_id, d.received_at
			FROM app.client_documents d
			LEFT JOIN app.document_types dt ON dt.tenant_id = d.tenant_id AND dt.id = d.document_type_id
			WHERE d.tenant_id = $1 AND d.project_id = $2 ORDER BY d.created_at, d.title`, p.TenantID, id); err != nil {
			return err
		}
		if pr["payments"], err = rows(ctx, tx, `
			SELECT DISTINCT pr.id, pr.receipt_no AS number, pr.business_date AS date, pr.amount,
			       COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.tenant_id = pr.tenant_id AND a.payment_received_id = pr.id), 0) AS applied,
			       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
			       pr.reference, pr.status::text AS status, pr.created_at
			FROM app.payments_received pr
			LEFT JOIN app.payment_allocations a ON a.tenant_id = pr.tenant_id AND a.payment_received_id = pr.id
			LEFT JOIN app.invoices i ON i.tenant_id = a.tenant_id AND i.id = a.invoice_id
			WHERE pr.tenant_id = $1 AND (i.project_id = $2 OR pr.notes LIKE '%[project:' || $2::text || ']%')
			ORDER BY pr.business_date, pr.created_at`, p.TenantID, id); err != nil {
			return err
		}
		if pr["costs_list"], err = rows(ctx, tx, costUnionSQL+` WHERE k.project_id = $2 ORDER BY k.date, k.created_at`, p.TenantID, id); err != nil {
			return err
		}
		if pr["notes_list"], err = rows(ctx, tx, `
			SELECT n.id, n.kind::text AS kind, n.summary AS body, n.occurred_at::date AS noted_on, n.created_at, u.full_name::text AS author
			FROM app.client_interactions n
			LEFT JOIN app.memberships m ON m.tenant_id = n.tenant_id AND m.id = n.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE n.tenant_id = $1 AND n.project_id = $2
			ORDER BY n.occurred_at DESC, n.created_at DESC`, p.TenantID, id); err != nil {
			return err
		}
		if pr["invoices"], err = rows(ctx, tx, `
			SELECT id, invoice_no AS number,
			       CASE WHEN status = 'partially_paid' THEN 'partial' ELSE status::text END AS status,
			       issue_date, due_date, total, amount_paid AS paid_total, balance_due AS balance
			FROM app.invoices WHERE tenant_id = $1 AND project_id = $2 ORDER BY created_at`, p.TenantID, id); err != nil {
			return err
		}
		if pr["design_jobs"], err = rows(ctx, tx, `
			SELECT id, job_no AS number, title::text AS title,
			       CASE WHEN job_type = 'social_media_post' THEN 'social_media' ELSE job_type::text END AS job_type,
			       status::text AS status, deadline, project_stage_id AS stage_id
			FROM app.design_jobs WHERE tenant_id = $1 AND project_id = $2 ORDER BY created_at`, p.TenantID, id); err != nil {
			return err
		}
		if pr["stage_events"], err = rows(ctx, tx, `
			SELECT e.id, e.project_stage_id AS stage_id, st.name::text AS stage_name,
			       e.from_status::text AS from_status, e.to_status::text AS to_status, e.note, e.created_at,
			       u.full_name::text AS actor_name
			FROM app.project_stage_history e
			JOIN app.project_stages st ON st.tenant_id = e.tenant_id AND st.id = e.project_stage_id
			LEFT JOIN app.memberships m ON m.tenant_id = e.tenant_id AND m.id = e.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE e.tenant_id = $1 AND st.project_id = $2
			ORDER BY e.created_at DESC`, p.TenantID, id); err != nil {
			return err
		}
		out = pr
		return nil
	})
	return out, err
}

func (s *Service) UpdateProject(ctx context.Context, p *auth.Principal, id uuid.UUID, in ProjectPatch) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.projectRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		var total any
		if in.TotalPrice != nil {
			total = money.Money(*in.TotalPrice)
		}
		var ownerMid any
		if in.OwnerID != nil {
			mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.OwnerID)
			if err != nil {
				return err
			}
			ownerMid = uuidOrNil(mid)
		}
		curStatus, _ := before["status"].(string)
		if in.Status == "completed" && curStatus == "not_started" {
			if _, err := tx.Exec(ctx, `UPDATE app.projects SET status = 'in_progress' WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
				return err
			}
		}
		cancelReason := clean(in.CancelReason)
		if cancelReason == "" && in.Notes != nil {
			cancelReason = clean(*in.Notes)
		}
		if in.Status == "cancelled" && cancelReason == "" {
			cancelReason = "Cancelled"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.projects SET
			  title = COALESCE(NULLIF($3,''), title),
			  status = COALESCE(NULLIF($4,'')::app.project_status, status),
			  start_date = COALESCE(NULLIF($5,'')::date, start_date),
			  owner_id = COALESCE($6, owner_id),
			  total_price = COALESCE($7, total_price),
			  cancel_reason = CASE WHEN $4 = 'cancelled' THEN $8 ELSE cancel_reason END
			WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, id, clean(in.Name), in.Status, in.StartDate, ownerMid, total, nz(cancelReason)); err != nil {
			return err
		}
		if in.Status == "completed" {
			if _, err := tx.Exec(ctx, `
				UPDATE app.customers SET client_status = CASE
				  WHEN EXISTS (SELECT 1 FROM app.projects x WHERE x.tenant_id = $1 AND x.customer_id = $2 AND x.status IN ('not_started','in_progress','on_hold')) THEN client_status
				  ELSE 'completed' END
				WHERE tenant_id = $1 AND id = $2 AND client_status = 'active'`, p.TenantID, before["client_id"]); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "project.updated", "project", id, map[string]any{"status": before["status"], "total_price": before["total_price"]}, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, id)
}

type StagePatch struct {
	Name      string     `json:"name" validate:"max=160"`
	Status    string     `json:"status" validate:"omitempty,oneof=not_started in_progress done stuck"`
	Note      string     `json:"note" validate:"max=1000"`
	OwnerID   *uuid.UUID `json:"owner_id"`
	Deadline  string     `json:"deadline" validate:"omitempty,date"`
	Checklist []struct {
		Text string `json:"text" validate:"required,max=200"`
		Done bool   `json:"done"`
	} `json:"checklist" validate:"dive"`
}

// UpdateStage changes a stage. Status changes automatically append to app.project_stage_history via trigger.
func (s *Service) UpdateStage(ctx context.Context, p *auth.Principal, projectID, stageID uuid.UUID, in StagePatch) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		if pr["status"] == "cancelled" {
			return httpx.Conflict("invalid_status", "A cancelled project cannot be changed")
		}
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status::text FROM app.project_stages WHERE tenant_id = $1 AND id = $2 AND project_id = $3 FOR UPDATE`, p.TenantID, stageID, projectID).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Stage not found")
		}
		var ownerMid any
		if in.OwnerID != nil {
			mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.OwnerID)
			if err != nil {
				return err
			}
			ownerMid = uuidOrNil(mid)
		}
		status := in.Status
		if status == "" {
			status = cur
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.change_note', $1, true)`, clean(in.Note)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.project_stages SET
			  name = COALESCE(NULLIF($4,''), name),
			  status = $5::app.stage_status,
			  owner_id = COALESCE($6, owner_id),
			  deadline = COALESCE(NULLIF($7,'')::date, deadline)
			WHERE tenant_id = $1 AND id = $2 AND project_id = $3`,
			p.TenantID, stageID, projectID, clean(in.Name), status, ownerMid, in.Deadline); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.change_note', '', true)`); err != nil {
			return err
		}
		if in.Checklist != nil {
			for i, item := range in.Checklist {
				var doneBy, doneAt any
				if item.Done {
					doneBy = p.MembershipID
					doneAt = s.Clock.Now()
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO app.project_stage_checklist (tenant_id, project_stage_id, seq, label, is_done, done_by, done_at)
					VALUES ($1,$2,$3,$4,$5,$6,$7)
					ON CONFLICT (tenant_id, project_stage_id, seq) DO UPDATE
					SET label = EXCLUDED.label, is_done = EXCLUDED.is_done,
					    done_by = CASE WHEN EXCLUDED.is_done THEN COALESCE(app.project_stage_checklist.done_by, EXCLUDED.done_by) ELSE NULL END,
					    done_at = CASE WHEN EXCLUDED.is_done THEN COALESCE(app.project_stage_checklist.done_at, EXCLUDED.done_at) ELSE NULL END`,
					p.TenantID, stageID, i+1, clean(item.Text), item.Done, doneBy, doneAt); err != nil {
					return err
				}
			}
		}
		return s.syncProjectProgress(ctx, tx, p.TenantID, projectID, pr)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, projectID)
}

// syncProjectProgress completes the project when every stage is done.
func (s *Service) syncProjectProgress(ctx context.Context, tx pgx.Tx, tenantID, projectID uuid.UUID, pr map[string]any) error {
	var total, done int
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'done') FROM app.project_stages WHERE tenant_id = $1 AND project_id = $2`, tenantID, projectID).Scan(&total, &done); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status::text FROM app.projects WHERE tenant_id = $1 AND id = $2`, tenantID, projectID).Scan(&status); err != nil {
		return err
	}
	if total > 0 && done == total && (status == "not_started" || status == "in_progress") {
		if status == "not_started" {
			if _, err := tx.Exec(ctx, `UPDATE app.projects SET status = 'in_progress' WHERE tenant_id = $1 AND id = $2`, tenantID, projectID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE app.projects SET status = 'completed', completed_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, projectID); err != nil {
			return err
		}
		if owner, ok := pr["owner_id"].(uuid.UUID); ok && owner != uuid.Nil {
			eid := projectID
			numStr, _ := pr["number"].(string)
			return s.Notify.ToUser(ctx, tx, tenantID, owner, notify.Message{
				Type: "project_stage_changed", Title: "Project completed",
				Body: "All stages of " + numStr + " are done",
				Link: "/projects/" + projectID.String(), EntityType: "project", EntityID: &eid,
			})
		}
	}
	return nil
}

type StageIn2 struct {
	Name            string     `json:"name" validate:"required,max=160"`
	OwnerID         *uuid.UUID `json:"owner_id"`
	Deadline        string     `json:"deadline" validate:"omitempty,date"`
	IsBrandingStage bool       `json:"is_branding_stage"`
}

func (s *Service) AddStage(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in StageIn2) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		var ownerMid any
		if in.OwnerID != nil {
			mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.OwnerID)
			if err != nil {
				return err
			}
			ownerMid = uuidOrNil(mid)
		}
		isBranding := in.IsBrandingStage || strings.Contains(strings.ToLower(in.Name), "brand")
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.project_stages (id, tenant_id, project_id, seq, name, owner_id, deadline, is_branding_stage)
			VALUES ($1,$2,$3,(SELECT COALESCE(MAX(seq),0)+1 FROM app.project_stages WHERE tenant_id = $2 AND project_id = $3),$4,$5,$6,$7)`,
			ids.New(), p.TenantID, projectID, clean(in.Name), ownerMid, dateOrNil(in.Deadline), isBranding); err != nil {
			return err
		}
		return s.syncProjectProgress(ctx, tx, p.TenantID, projectID, pr)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, projectID)
}

type NoteIn struct {
	Kind    string `json:"kind" validate:"omitempty,oneof=call message meeting note"`
	Body    string `json:"body" validate:"required,max=4000"`
	NotedOn string `json:"noted_on" validate:"omitempty,date"`
}

func (s *Service) AddNote(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in NoteIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, false)
		if err != nil {
			return err
		}
		occurred := s.Clock.Now()
		if in.NotedOn != "" {
			if t, err := time.Parse("2006-01-02", in.NotedOn); err == nil {
				occurred = t
			}
		}
		kind := in.Kind
		if kind == "" {
			kind = "note"
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.client_interactions (id, tenant_id, customer_id, project_id, kind, summary, occurred_at)
			VALUES ($1,$2,$3,$4,$5::app.interaction_kind,$6,$7)`,
			id, p.TenantID, pr["client_id"], projectID, kind, clean(in.Body), occurred); err != nil {
			return err
		}
		out, err = one(ctx, tx, "Note", `SELECT id, kind::text AS kind, summary AS body, occurred_at::date AS noted_on, created_at FROM app.client_interactions WHERE tenant_id = $1 AND id = $2`, p.TenantID, id)
		return err
	})
	return out, err
}

func (s *Service) ListNotes(ctx context.Context, p *auth.Principal, projectID uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.projectRow(ctx, tx, p, projectID, false); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `
			SELECT n.id, n.kind::text AS kind, n.summary AS body, n.occurred_at::date AS noted_on, n.created_at, u.full_name::text AS author
			FROM app.client_interactions n
			LEFT JOIN app.memberships m ON m.tenant_id = n.tenant_id AND m.id = n.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE n.tenant_id = $1 AND n.project_id = $2
			ORDER BY n.occurred_at DESC, n.created_at DESC`, p.TenantID, projectID)
		return err
	})
	return out, err
}

type UpdateIn struct {
	Message string `json:"message" validate:"required,max=480"`
}

// SendUpdate sends an SMS to the client and records it as a message interaction.
func (s *Service) SendUpdate(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in UpdateIn) (map[string]any, error) {
	var phoneStr, number string
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, false)
		if err != nil {
			return err
		}
		number, _ = pr["number"].(string)
		var ph *string
		if err := tx.QueryRow(ctx, `SELECT phone::text FROM app.customers WHERE tenant_id = $1 AND id = $2`, p.TenantID, pr["client_id"]).Scan(&ph); err != nil {
			return err
		}
		if ph != nil {
			phoneStr = *ph
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO app.client_interactions (id, tenant_id, customer_id, project_id, kind, summary, occurred_at)
			VALUES ($1,$2,$3,$4,'message',$5,$6)`,
			ids.New(), p.TenantID, pr["client_id"], projectID, clean(in.Message), s.Clock.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	delivered := false
	if s.SMS != nil && phoneStr != "" {
		delivered = s.SMS.Send(ctx, sender.Message{To: phoneStr, Body: clean(in.Message)}) == nil
	}
	return map[string]any{"project": number, "delivered": delivered, "has_phone": phoneStr != ""}, nil
}

// projectsByStage counts open projects by current stage, for the services dashboard.
func (s *Service) projectsByStage(ctx context.Context, tx pgx.Tx, p *auth.Principal) ([]map[string]any, error) {
	q := sqlq.New(p.TenantID)
	q.Add("pr.tenant_id = $1 AND pr.status IN ('not_started','in_progress')")
	ownProject(q, p)
	return rows(ctx, tx, `
		SELECT COALESCE((SELECT st.name::text FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' ORDER BY st.seq LIMIT 1), 'No stages') AS stage,
		       count(*) AS projects
		FROM app.projects pr`+q.Where()+` GROUP BY 1 ORDER BY 2 DESC, 1`, q.Args...)
}
