package services

import (
	"context"
	"encoding/json"
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
	Name       string           `json:"name" validate:"max=200"`
	Status     string           `json:"status" validate:"omitempty,oneof=in_progress on_hold completed cancelled"`
	StartDate  string           `json:"start_date" validate:"omitempty,date"`
	OwnerID    *uuid.UUID       `json:"owner_id"`
	TotalPrice *decimal.Decimal `json:"total_price" validate:"omitempty,dgte0"`
	Notes      *string          `json:"notes" validate:"omitempty,max=2000"`
}

// ownProject limits a list to projects the user owns or has a stage on.
func ownProject(q *sqlq.Q, own *uuid.UUID) {
	if own != nil {
		q.Add(`(pr.owner_id = ? OR pr.created_by = ? OR EXISTS (SELECT 1 FROM svc_project_stages st WHERE st.project_id = pr.id AND st.owner_id = ?))`, *own, *own, *own)
	}
}

const projectCols = `pr.id, pr.number, pr.name, pr.status, pr.start_date, pr.total_price, pr.notes, pr.owner_id, u.name AS owner_name,
	pr.client_id, c.name AS client_name, pr.service_id, pr.created_at, pr.completed_at,
	(SELECT count(*) FROM svc_project_stages st WHERE st.project_id = pr.id) AS stages_total,
	(SELECT count(*) FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status = 'done') AS stages_done,
	(SELECT st.name FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' ORDER BY st.position LIMIT 1) AS current_stage,
	COALESCE((SELECT SUM(sp.amount) FROM svc_payments sp WHERE sp.project_id = pr.id AND sp.status = 'posted'), 0) AS paid,
	COALESCE((SELECT SUM(k.amount) FROM svc_costs k WHERE k.project_id = pr.id AND k.status <> 'voided'), 0) AS costs`

const projectFrom = ` FROM svc_projects pr JOIN svc_clients c ON c.id = pr.client_id LEFT JOIN users u ON u.id = pr.owner_id`

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
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := projectQuery(p, f)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+projectFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+projectCols+projectFrom+q.Where()+` ORDER BY pr.created_at DESC, pr.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err == nil {
			addBalances(out)
		}
		return err
	})
	return out, total, err
}

func projectQuery(p *auth.Principal, f ProjectFilter) *sqlq.Q {
	q := sqlq.New()
	if t := clean(f.Q); t != "" {
		q.Add("(pr.name ILIKE '%' || ? || '%' OR pr.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", t, t, t)
	}
	q.If(f.Status != "", "pr.status = ?", f.Status)
	q.If(f.ClientID != nil, "pr.client_id = ?", f.ClientID)
	q.If(f.OwnerID != nil, "pr.owner_id = ?", f.OwnerID)
	q.If(f.Late, `pr.status = 'in_progress' AND EXISTS (SELECT 1 FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' AND st.deadline < svc_today())`)
	ownProject(q, scopeOwn(p, "projects.view"))
	return q
}

func addBalances(list []map[string]any) {
	for _, r := range list {
		r["balance"] = num(r, "total_price").Sub(num(r, "paid"))
		r["profit"] = num(r, "paid").Sub(num(r, "costs"))
	}
}

// Board groups active projects by their current stage.
func (s *Service) ProjectBoard(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("pr.status IN ('in_progress','on_hold')")
		ownProject(q, scopeOwn(p, "projects.view"))
		list, err := rows(ctx, tx, `SELECT `+projectCols+`,
			(SELECT st.deadline FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' ORDER BY st.position LIMIT 1) AS stage_deadline,
			(SELECT st.status FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' ORDER BY st.position LIMIT 1) AS stage_status`+
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
	name    string
	estDays int
	docs    []string
}

// stagesFor copies the stages and required documents of a service (or of every service in a package).
func (s *Service) stagesFor(ctx context.Context, tx pgx.Tx, serviceID uuid.UUID) (name string, price decimal.Decimal, seeds []stageSeed, err error) {
	var isPkg bool
	if err = tx.QueryRow(ctx, `SELECT name, standard_price, is_package FROM svc_catalog WHERE id=$1 AND is_active`, serviceID).Scan(&name, &price, &isPkg); err != nil {
		return "", price, nil, httpx.NotFound("not_found", "Service not found or inactive")
	}
	list := []uuid.UUID{serviceID}
	if isPkg {
		list = nil
		r, err := tx.Query(ctx, `SELECT pi.service_id FROM svc_package_items pi JOIN svc_catalog c ON c.id = pi.service_id WHERE pi.package_id=$1 ORDER BY c.name`, serviceID)
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
		stages, err := rows(ctx, tx, `SELECT position, name, est_days FROM svc_catalog_stages WHERE service_id=$1 ORDER BY position`, sid)
		if err != nil {
			return "", price, nil, err
		}
		docs, err := rows(ctx, tx, `SELECT name, stage_position FROM svc_catalog_documents WHERE service_id=$1`, sid)
		if err != nil {
			return "", price, nil, err
		}
		start := len(seeds)
		for _, st := range stages {
			seeds = append(seeds, stageSeed{name: st["name"].(string), estDays: int(st["est_days"].(int32))})
		}
		for _, d := range docs {
			if pos, ok := d["stage_position"].(int32); ok && int(pos) >= 1 && int(pos) <= len(stages) {
				seeds[start+int(pos)-1].docs = append(seeds[start+int(pos)-1].docs, d["name"].(string))
			} else if len(seeds) > start {
				seeds[start].docs = append(seeds[start].docs, d["name"].(string))
			}
		}
	}
	return name, price, seeds, nil
}

func (s *Service) CreateProject(ctx context.Context, p *auth.Principal, in ProjectIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.createProjectTx(ctx, tx, p, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, id)
}

func (s *Service) createProjectTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in ProjectIn) (uuid.UUID, error) {
	id := ids.New()
	var clientName string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT name, is_active FROM svc_clients WHERE id=$1 FOR UPDATE`, in.ClientID).Scan(&clientName, &active); err != nil {
		return id, httpx.NotFound("not_found", "Client not found")
	}
	if !active {
		return id, httpx.Conflict("client_inactive", "That client is not active")
	}
	name := clean(in.Name)
	price := money.Zero
	var seeds []stageSeed
	if in.ServiceID != nil {
		svcName, svcPrice, sd, err := s.stagesFor(ctx, tx, *in.ServiceID)
		if err != nil {
			return id, err
		}
		if name == "" {
			name = svcName + " - " + clientName
		}
		price, seeds = svcPrice, sd
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
	owner := in.OwnerID
	if owner == nil {
		u := p.UserID
		owner = &u
	}
	number, err := sequence.Next(ctx, tx, *p.BranchID, "svc_project")
	if err != nil {
		return id, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO svc_projects (id, business_id, branch_id, number, client_id, service_id, name, start_date, owner_id, total_price, notes, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, p.BusinessID, *p.BranchID, number, in.ClientID, uuidOrNil(in.ServiceID), name, start, uuidOrNil(owner), price, nz(in.Notes), p.UserID); err != nil {
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
		if _, err := tx.Exec(ctx, `INSERT INTO svc_project_stages (id, business_id, project_id, position, name, est_days, deadline, owner_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			stageID, p.BusinessID, id, i+1, sd.name, sd.estDays, deadline, uuidOrNil(owner)); err != nil {
			return id, err
		}
		for _, d := range sd.docs {
			if _, err := tx.Exec(ctx, `INSERT INTO svc_documents (id, business_id, client_id, project_id, stage_id, name, doc_type) VALUES ($1,$2,$3,$4,$5,$6,'required')`,
				ids.New(), p.BusinessID, in.ClientID, id, stageID, d); err != nil {
				return id, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE svc_clients SET status='active' WHERE id=$1 AND status='lead'`, in.ClientID); err != nil {
		return id, err
	}
	return id, s.Audit.Record(ctx, tx, p, "project.created", "svc_project", id, nil, map[string]any{"number": number, "client_id": in.ClientID, "service_id": in.ServiceID, "total_price": price})
}

func (s *Service) projectRow(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, lock bool) (map[string]any, error) {
	q := sqlq.New(id)
	q.Add("pr.id = $1")
	ownProject(q, scopeOwn(p, "projects.view"))
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
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		if pr["stages"], err = rows(ctx, tx, `
			SELECT st.id, st.position, st.name, st.status, st.owner_id, u.name AS owner_name, st.deadline, st.est_days, st.checklist, st.note, st.updated_at, st.completed_at,
			       (SELECT count(*) FROM svc_documents d WHERE d.stage_id = st.id AND d.status = 'missing') AS documents_missing
			FROM svc_project_stages st LEFT JOIN users u ON u.id = st.owner_id WHERE st.project_id=$1 ORDER BY st.position`, id); err != nil {
			return err
		}
		if pr["documents"], err = rows(ctx, tx, `
			SELECT id, stage_id, name, doc_type, status, file_id, received_at FROM svc_documents WHERE project_id=$1 ORDER BY created_at, name`, id); err != nil {
			return err
		}
		if pr["payments"], err = rows(ctx, tx, `
			SELECT sp.id, pa.number, pa.payment_date AS date, sp.amount, sp.applied, pa.method, pa.reference, sp.status
			FROM svc_payments sp JOIN payments pa ON pa.id = sp.id WHERE sp.project_id=$1 ORDER BY pa.payment_date, pa.created_at`, id); err != nil {
			return err
		}
		if pr["costs_list"], err = rows(ctx, tx, `
			SELECT k.id, k.number, k.kind, k.description, k.amount, k.cost_date, k.status, k.contractor_id, ct.name AS contractor_name
			FROM svc_costs k LEFT JOIN contractors ct ON ct.id = k.contractor_id WHERE k.project_id=$1 ORDER BY k.cost_date, k.created_at`, id); err != nil {
			return err
		}
		if pr["notes_list"], err = rows(ctx, tx, `
			SELECT n.id, n.kind, n.body, n.noted_on, n.created_at, u.name AS author FROM svc_project_notes n LEFT JOIN users u ON u.id = n.created_by
			WHERE n.project_id=$1 ORDER BY n.noted_on DESC, n.created_at DESC`, id); err != nil {
			return err
		}
		if pr["invoices"], err = rows(ctx, tx, `SELECT id, number, status, issue_date, due_date, total, paid_total, balance FROM svc_invoices WHERE project_id=$1 ORDER BY created_at`, id); err != nil {
			return err
		}
		if pr["design_jobs"], err = rows(ctx, tx, `SELECT id, number, title, job_type, status, deadline, stage_id FROM design_jobs WHERE project_id=$1 ORDER BY created_at`, id); err != nil {
			return err
		}
		out = pr
		return nil
	})
	return out, err
}

func (s *Service) UpdateProject(ctx context.Context, p *auth.Principal, id uuid.UUID, in ProjectPatch) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.projectRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		var total any
		if in.TotalPrice != nil {
			total = money.Money(*in.TotalPrice)
		}
		var notes any
		if in.Notes != nil {
			notes = clean(*in.Notes)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE svc_projects SET name=COALESCE(NULLIF($2,''), name), status=COALESCE(NULLIF($3,''), status), start_date=COALESCE(NULLIF($4,'')::date, start_date),
			  owner_id=COALESCE($5, owner_id), total_price=COALESCE($6, total_price), notes=COALESCE($7, notes),
			  completed_at = CASE WHEN COALESCE(NULLIF($3,''), status) = 'completed' THEN COALESCE(completed_at, now()) ELSE NULL END
			WHERE id=$1`, id, clean(in.Name), in.Status, in.StartDate, uuidOrNil(in.OwnerID), total, notes); err != nil {
			return err
		}
		if in.Status == "completed" {
			if _, err := tx.Exec(ctx, `UPDATE svc_clients SET status = CASE WHEN EXISTS (SELECT 1 FROM svc_projects x WHERE x.client_id = $1 AND x.status IN ('in_progress','on_hold')) THEN status ELSE 'completed' END WHERE id=$1 AND status='active'`, before["client_id"]); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "project.updated", "svc_project", id, map[string]any{"status": before["status"], "total_price": before["total_price"]}, in)
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

// UpdateStage changes a stage. A status change records who and when, with an optional note.
func (s *Service) UpdateStage(ctx context.Context, p *auth.Principal, projectID, stageID uuid.UUID, in StagePatch) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		if pr["status"] == "cancelled" {
			return httpx.Conflict("invalid_status", "A cancelled project cannot be changed")
		}
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_project_stages WHERE id=$1 AND project_id=$2 FOR UPDATE`, stageID, projectID).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Stage not found")
		}
		var checklist any
		if in.Checklist != nil {
			raw, _ := json.Marshal(in.Checklist)
			checklist = string(raw)
		}
		status := in.Status
		if status == "" {
			status = cur
		}
		if _, err := tx.Exec(ctx, `
			UPDATE svc_project_stages SET name=COALESCE(NULLIF($3,''), name), status=$4, owner_id=COALESCE($5, owner_id), deadline=COALESCE(NULLIF($6,'')::date, deadline),
			  checklist=COALESCE($7::jsonb, checklist), note=COALESCE(NULLIF($8,''), note), updated_by=$9, updated_at=now(),
			  completed_at = CASE WHEN $4 = 'done' THEN COALESCE(completed_at, now()) ELSE NULL END
			WHERE id=$1 AND project_id=$2`,
			stageID, projectID, clean(in.Name), status, uuidOrNil(in.OwnerID), in.Deadline, checklist, clean(in.Note), p.UserID); err != nil {
			return err
		}
		if status != cur {
			if _, err := tx.Exec(ctx, `INSERT INTO svc_stage_events (id, business_id, project_id, stage_id, from_status, to_status, note, changed_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				ids.New(), p.BusinessID, projectID, stageID, cur, status, nz(in.Note), p.UserID); err != nil {
				return err
			}
		}
		return s.syncProjectProgress(ctx, tx, p.BusinessID, projectID, pr)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, projectID)
}

// syncProjectProgress completes the project when every stage is done and reopens it when one is not.
func (s *Service) syncProjectProgress(ctx context.Context, tx pgx.Tx, businessID, projectID uuid.UUID, pr map[string]any) error {
	var total, done int
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status='done') FROM svc_project_stages WHERE project_id=$1`, projectID).Scan(&total, &done); err != nil {
		return err
	}
	status, _ := pr["status"].(string)
	switch {
	case total > 0 && done == total && status == "in_progress":
		if _, err := tx.Exec(ctx, `UPDATE svc_projects SET status='completed', completed_at=now() WHERE id=$1`, projectID); err != nil {
			return err
		}
		if owner, ok := pr["owner_id"].(uuid.UUID); ok {
			eid := projectID
			return s.Notify.ToUser(ctx, tx, businessID, owner, notify.Message{Type: "project_completed", Title: "Project completed", Body: "All stages of " + pr["number"].(string) + " are done", Link: "/projects/" + projectID.String(), EntityType: "svc_project", EntityID: &eid})
		}
	case done < total && status == "completed":
		_, err := tx.Exec(ctx, `UPDATE svc_projects SET status='in_progress', completed_at=NULL WHERE id=$1`, projectID)
		return err
	}
	return nil
}

type StageIn2 struct {
	Name     string     `json:"name" validate:"required,max=160"`
	OwnerID  *uuid.UUID `json:"owner_id"`
	Deadline string     `json:"deadline" validate:"omitempty,date"`
}

func (s *Service) AddStage(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in StageIn2) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_project_stages (id, business_id, project_id, position, name, owner_id, deadline, updated_by)
			VALUES ($1,$2,$3,(SELECT COALESCE(MAX(position),0)+1 FROM svc_project_stages WHERE project_id=$3),$4,$5,$6,$7)`,
			ids.New(), p.BusinessID, projectID, clean(in.Name), uuidOrNil(in.OwnerID), dateOrNil(in.Deadline), p.UserID); err != nil {
			return err
		}
		return s.syncProjectProgress(ctx, tx, p.BusinessID, projectID, pr)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, p, projectID)
}

type NoteIn struct {
	Kind    string `json:"kind" validate:"omitempty,oneof=call message meeting note"`
	Body    string `json:"body" validate:"required,max=2000"`
	NotedOn string `json:"noted_on" validate:"omitempty,date"`
}

func (s *Service) AddNote(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in NoteIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.projectRow(ctx, tx, p, projectID, false); err != nil {
			return err
		}
		date := in.NotedOn
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		kind := in.Kind
		if kind == "" {
			kind = "note"
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO svc_project_notes (id, business_id, project_id, kind, body, noted_on, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, p.BusinessID, projectID, kind, clean(in.Body), date, p.UserID); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Note", `SELECT id, kind, body, noted_on, created_at FROM svc_project_notes WHERE id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) ListNotes(ctx context.Context, p *auth.Principal, projectID uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.projectRow(ctx, tx, p, projectID, false); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT n.id, n.kind, n.body, n.noted_on, n.created_at, u.name AS author FROM svc_project_notes n LEFT JOIN users u ON u.id=n.created_by WHERE n.project_id=$1 ORDER BY n.noted_on DESC, n.created_at DESC`, projectID)
		return err
	})
	return out, err
}

type UpdateIn struct {
	Message string `json:"message" validate:"required,max=480"`
}

// SendUpdate sends an SMS to the client and records it as a note. When no SMS
// provider is configured the note is still recorded and delivered is false.
func (s *Service) SendUpdate(ctx context.Context, p *auth.Principal, projectID uuid.UUID, in UpdateIn) (map[string]any, error) {
	var phone, number string
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, false)
		if err != nil {
			return err
		}
		number, _ = pr["number"].(string)
		var ph *string
		if err := tx.QueryRow(ctx, `SELECT phone FROM svc_clients WHERE id=$1`, pr["client_id"]).Scan(&ph); err != nil {
			return err
		}
		if ph != nil {
			phone = *ph
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO svc_project_notes (id, business_id, project_id, kind, body, noted_on, created_by) VALUES ($1,$2,$3,'update',$4,$5,$6)`,
			ids.New(), p.BusinessID, projectID, clean(in.Message), date, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	delivered := false
	if s.SMS != nil && phone != "" {
		delivered = s.SMS.Send(ctx, sender.Message{To: phone, Body: clean(in.Message)}) == nil
	}
	return map[string]any{"project": number, "delivered": delivered, "has_phone": phone != ""}, nil
}

// ProjectSummary counts open projects by current stage, with money totals, for the dashboard.
func (s *Service) projectsByStage(ctx context.Context, tx pgx.Tx, p *auth.Principal) ([]map[string]any, error) {
	q := sqlq.New()
	q.Add("pr.status = 'in_progress'")
	ownProject(q, scopeOwn(p, "projects.view"))
	return rows(ctx, tx, `
		SELECT COALESCE((SELECT st.name FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' ORDER BY st.position LIMIT 1), 'No stages') AS stage,
		       count(*) AS projects
		FROM svc_projects pr`+q.Where()+` GROUP BY 1 ORDER BY 2 DESC, 1`, q.Args...)
}
