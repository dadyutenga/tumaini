package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
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

var designStatuses = []string{"new_brief", "in_design", "internal_review", "with_client", "changes_requested", "approved", "printing", "delivered"}

// designNext lists the statuses a job may move to from each status.
var designNext = map[string][]string{
	"new_brief":         {"in_design", "cancelled"},
	"in_design":         {"internal_review", "with_client", "cancelled"},
	"internal_review":   {"in_design", "with_client", "cancelled"},
	"with_client":       {"in_design", "changes_requested", "approved", "cancelled"},
	"changes_requested": {"in_design", "cancelled"},
	"approved":          {"printing", "delivered"},
	"printing":          {"delivered"},
}

func canMove(from, to string) bool {
	for _, n := range designNext[from] {
		if n == to {
			return true
		}
	}
	return false
}

type DesignJobIn struct {
	ClientID          uuid.UUID        `json:"client_id" validate:"required"`
	ProjectID         *uuid.UUID       `json:"project_id"`
	StageID           *uuid.UUID       `json:"stage_id"`
	ServiceID         *uuid.UUID       `json:"service_id"`
	JobType           string           `json:"job_type" validate:"required,oneof=logo business_card poster flyer banner signage social_media packaging_label branding_package other"`
	Title             string           `json:"title" validate:"required,max=200"`
	Brief             string           `json:"brief" validate:"max=4000"`
	SizeFormat        string           `json:"size_format" validate:"max=200"`
	StyleNotes        string           `json:"style_notes" validate:"max=2000"`
	TextToInclude     string           `json:"text_to_include" validate:"max=2000"`
	Deadline          string           `json:"deadline" validate:"omitempty,date"`
	Priority          string           `json:"priority" validate:"omitempty,oneof=normal urgent"`
	DesignerID        *uuid.UUID       `json:"designer_id"`
	Price             *decimal.Decimal `json:"price" validate:"omitempty,dgte0"`
	Deposit           decimal.Decimal  `json:"deposit" validate:"dgte0"`
	RevisionsIncluded *int             `json:"revisions_included" validate:"omitempty,gte=0,lte=50"`
	PrintingNeeded    bool             `json:"printing_needed"`
	ReferenceFileIDs  []uuid.UUID      `json:"reference_file_ids"`
}

type DesignJobPatch struct {
	Title             string           `json:"title" validate:"max=200"`
	Brief             *string          `json:"brief" validate:"omitempty,max=4000"`
	SizeFormat        *string          `json:"size_format" validate:"omitempty,max=200"`
	StyleNotes        *string          `json:"style_notes" validate:"omitempty,max=2000"`
	TextToInclude     *string          `json:"text_to_include" validate:"omitempty,max=2000"`
	Deadline          string           `json:"deadline" validate:"omitempty,date"`
	Priority          string           `json:"priority" validate:"omitempty,oneof=normal urgent"`
	DesignerID        *uuid.UUID       `json:"designer_id"`
	Price             *decimal.Decimal `json:"price" validate:"omitempty,dgte0"`
	Deposit           *decimal.Decimal `json:"deposit" validate:"omitempty,dgte0"`
	RevisionsIncluded *int             `json:"revisions_included" validate:"omitempty,gte=0,lte=50"`
	PrintingNeeded    *bool            `json:"printing_needed"`
}

const jobCols = `j.id, j.business_id, j.number, j.title, j.job_type, j.status, j.priority, j.deadline, j.client_id, c.name AS client_name, j.project_id, pr.number AS project_number,
	j.stage_id, j.designer_id, u.name AS designer_name, j.price, j.deposit, j.revisions_included, j.revisions_used, j.printing_needed, j.created_at, j.approved_at, j.delivered_at,
	(j.deadline IS NOT NULL AND j.deadline < svc_today() AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS overdue,
	COALESCE((SELECT SUM(sp.amount) FROM svc_payments sp WHERE sp.design_job_id = j.id AND sp.status = 'posted'), 0) AS paid,
	COALESCE((SELECT SUM(k.amount) FROM svc_costs k WHERE k.design_job_id = j.id AND k.status <> 'voided'), 0) AS costs`
const jobFrom = ` FROM design_jobs j JOIN svc_clients c ON c.id = j.client_id LEFT JOIN svc_projects pr ON pr.id = j.project_id LEFT JOIN users u ON u.id = j.designer_id`

func ownJob(q *sqlq.Q, own *uuid.UUID) {
	if own != nil {
		q.Add("(j.designer_id = ? OR j.created_by = ?)", *own, *own)
	}
}

func jobMoney(list []map[string]any) {
	for _, r := range list {
		r["balance"] = num(r, "price").Sub(num(r, "paid"))
		r["profit"] = num(r, "paid").Sub(num(r, "costs"))
		inc, _ := r["revisions_included"].(int32)
		used, _ := r["revisions_used"].(int32)
		r["revisions_over"] = used > inc
	}
}

type JobFilter struct {
	Q          string
	Status     string
	DesignerID *uuid.UUID
	ClientID   *uuid.UUID
	ProjectID  *uuid.UUID
	Overdue    bool
}

func (s *Service) ListJobs(ctx context.Context, p *auth.Principal, f JobFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := jobQuery(p, f)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+jobFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+jobCols+jobFrom+q.Where()+` ORDER BY j.deadline NULLS LAST, j.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err == nil {
			jobMoney(out)
		}
		return err
	})
	return out, total, err
}

func jobQuery(p *auth.Principal, f JobFilter) *sqlq.Q {
	q := sqlq.New()
	if t := clean(f.Q); t != "" {
		q.Add("(j.title ILIKE '%' || ? || '%' OR j.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", t, t, t)
	}
	q.If(f.Status != "", "j.status = ?", f.Status)
	q.If(f.DesignerID != nil, "j.designer_id = ?", f.DesignerID)
	q.If(f.ClientID != nil, "j.client_id = ?", f.ClientID)
	q.If(f.ProjectID != nil, "j.project_id = ?", f.ProjectID)
	q.If(f.Overdue, "j.deadline < svc_today() AND j.status NOT IN ('approved','printing','delivered','cancelled')")
	ownJob(q, scopeOwn(p, "design.view"))
	return q
}

// JobBoard returns one column per status, in workflow order.
func (s *Service) JobBoard(ctx context.Context, p *auth.Principal, designerID *uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := jobQuery(p, JobFilter{DesignerID: designerID})
		q.Add("j.status <> 'cancelled'")
		list, err := rows(ctx, tx, `SELECT `+jobCols+jobFrom+q.Where()+` ORDER BY j.deadline NULLS LAST, j.created_at`, q.Args...)
		if err != nil {
			return err
		}
		jobMoney(list)
		out = make([]map[string]any, 0, len(designStatuses))
		idx := map[string]int{}
		for _, st := range designStatuses {
			idx[st] = len(out)
			out = append(out, map[string]any{"status": st, "jobs": []map[string]any{}})
		}
		for _, r := range list {
			i := idx[r["status"].(string)]
			out[i]["jobs"] = append(out[i]["jobs"].([]map[string]any), r)
		}
		return nil
	})
	return out, err
}

func (s *Service) jobRow(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, lock bool) (map[string]any, error) {
	q := sqlq.New(id)
	q.Add("j.id = $1")
	ownJob(q, scopeOwn(p, "design.view"))
	sql := `SELECT ` + jobCols + `, j.brief, j.size_format, j.style_notes, j.text_to_include, j.service_id` + jobFrom + q.Where()
	if lock {
		sql += ` FOR UPDATE OF j`
	}
	r, err := one(ctx, tx, "Design job", sql, q.Args...)
	if err == nil {
		jobMoney([]map[string]any{r})
	}
	return r, err
}

func (s *Service) GetJob(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		if j["versions"], err = rows(ctx, tx, `
			SELECT v.id, v.version_no, v.kind, v.file_id, f.original_name AS file_name, f.mime_type, v.link, v.notes, v.status, v.is_final, v.token_expires_at, v.decided_at, v.created_at, u.name AS uploaded_by
			FROM design_versions v LEFT JOIN files f ON f.id = v.file_id LEFT JOIN users u ON u.id = v.uploaded_by
			WHERE v.job_id=$1 ORDER BY v.created_at DESC`, id); err != nil {
			return err
		}
		if j["feedback"], err = rows(ctx, tx, `
			SELECT fb.id, fb.version_id, v.version_no, fb.author_type, COALESCE(u.name, 'Client') AS author, fb.comment, fb.created_at
			FROM design_feedback fb LEFT JOIN design_versions v ON v.id = fb.version_id LEFT JOIN users u ON u.id = fb.author_id
			WHERE fb.job_id=$1 ORDER BY fb.created_at`, id); err != nil {
			return err
		}
		if j["references"], err = rows(ctx, tx, `SELECT r.id, r.file_id, f.original_name AS file_name, f.mime_type, r.note FROM design_references r JOIN files f ON f.id = r.file_id WHERE r.job_id=$1 ORDER BY r.created_at`, id); err != nil {
			return err
		}
		if j["costs_list"], err = rows(ctx, tx, costSQL+` WHERE k.design_job_id=$1 ORDER BY k.cost_date, k.created_at`, id); err != nil {
			return err
		}
		if j["payments"], err = rows(ctx, tx, `SELECT sp.id, pa.number, pa.payment_date AS date, sp.amount, sp.applied, pa.method, sp.status FROM svc_payments sp JOIN payments pa ON pa.id = sp.id WHERE sp.design_job_id=$1 ORDER BY pa.payment_date`, id); err != nil {
			return err
		}
		if j["print_orders"], err = rows(ctx, tx, `
			SELECT o.id, o.contractor_id, ct.name AS contractor_name, o.quantity, o.cost, o.due_date, o.status, o.note, o.created_at
			FROM design_print_orders o LEFT JOIN contractors ct ON ct.id = o.contractor_id WHERE o.job_id=$1 ORDER BY o.created_at`, id); err != nil {
			return err
		}
		if j["time"], err = rows(ctx, tx, `
			SELECT t.id, t.user_id, u.name AS user_name, t.started_at, t.stopped_at, t.minutes FROM design_time_entries t JOIN users u ON u.id = t.user_id WHERE t.job_id=$1 ORDER BY t.started_at DESC`, id); err != nil {
			return err
		}
		if j["invoices"], err = rows(ctx, tx, `SELECT id, number, status, total, paid_total, balance FROM svc_invoices WHERE design_job_id=$1 ORDER BY created_at`, id); err != nil {
			return err
		}
		out = j
		return nil
	})
	return out, err
}

func (s *Service) CreateJob(ctx context.Context, p *auth.Principal, in DesignJobIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	var id uuid.UUID
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.createJobTx(ctx, tx, p, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, p, id)
}

func (s *Service) createJobTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in DesignJobIn) (uuid.UUID, error) {
	id := ids.New()
	if err := require404(ctx, tx, "svc_clients", "Client", in.ClientID); err != nil {
		return id, err
	}
	if in.ProjectID != nil {
		var c uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT client_id FROM svc_projects WHERE id=$1`, *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
			return id, httpx.NotFound("not_found", "Project not found for this client")
		}
	}
	if in.StageID != nil {
		if in.ProjectID == nil {
			return id, httpx.BadRequest("invalid_body", "A stage link needs a project")
		}
		if ok, _ := exists(ctx, tx, "svc_project_stages", *in.StageID); !ok {
			return id, httpx.NotFound("not_found", "Stage not found")
		}
	}
	price := money.Zero
	revisions := 3
	if in.ServiceID != nil {
		var std decimal.Decimal
		var rev int
		if err := tx.QueryRow(ctx, `SELECT standard_price, included_revisions FROM svc_catalog WHERE id=$1 AND is_active`, *in.ServiceID).Scan(&std, &rev); err != nil {
			return id, httpx.NotFound("not_found", "Service not found or inactive")
		}
		price, revisions = std, rev
	}
	if in.Price != nil {
		price = money.Money(*in.Price)
	}
	if in.RevisionsIncluded != nil {
		revisions = *in.RevisionsIncluded
	}
	if money.Money(in.Deposit).GreaterThan(price) {
		return id, httpx.BadRequest("invalid_body", "The deposit cannot be more than the price")
	}
	prio := in.Priority
	if prio == "" {
		prio = "normal"
	}
	number, err := sequence.Next(ctx, tx, *p.BranchID, "design_job")
	if err != nil {
		return id, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO design_jobs (id, business_id, branch_id, number, client_id, project_id, stage_id, service_id, job_type, title, brief, size_format, style_notes, text_to_include,
		  deadline, priority, designer_id, price, deposit, revisions_included, printing_needed, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		id, p.BusinessID, *p.BranchID, number, in.ClientID, uuidOrNil(in.ProjectID), uuidOrNil(in.StageID), uuidOrNil(in.ServiceID), in.JobType, clean(in.Title), nz(in.Brief), nz(in.SizeFormat), nz(in.StyleNotes), nz(in.TextToInclude),
		dateOrNil(in.Deadline), prio, uuidOrNil(in.DesignerID), price, money.Money(in.Deposit), revisions, in.PrintingNeeded, p.UserID); err != nil {
		return id, err
	}
	for _, f := range in.ReferenceFileIDs {
		if ok, _ := exists(ctx, tx, "files", f); !ok {
			return id, httpx.NotFound("not_found", "A reference file was not found")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO design_references (id, business_id, job_id, file_id) VALUES ($1,$2,$3,$4)`, ids.New(), p.BusinessID, id, f); err != nil {
			return id, err
		}
	}
	if in.DesignerID != nil && *in.DesignerID != p.UserID {
		eid := id
		if err := s.Notify.ToUser(ctx, tx, p.BusinessID, *in.DesignerID, notify.Message{Type: "design_assigned", Title: "New design brief", Body: number + ": " + clean(in.Title), Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &eid}); err != nil {
			return id, err
		}
	}
	return id, s.Audit.Record(ctx, tx, p, "design_job.created", "design_job", id, nil, map[string]any{"number": number, "title": in.Title, "price": price})
}

func (s *Service) UpdateJob(ctx context.Context, p *auth.Principal, id uuid.UUID, in DesignJobPatch) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.jobRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		if before["status"] == "cancelled" || before["status"] == "delivered" {
			return httpx.Conflict("invalid_status", "A "+before["status"].(string)+" job cannot be edited")
		}
		var price, deposit any
		if in.Price != nil {
			price = money.Money(*in.Price)
		}
		if in.Deposit != nil {
			deposit = money.Money(*in.Deposit)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE design_jobs SET title=COALESCE(NULLIF($2,''), title), brief=COALESCE($3, brief), size_format=COALESCE($4, size_format), style_notes=COALESCE($5, style_notes),
			  text_to_include=COALESCE($6, text_to_include), deadline=COALESCE(NULLIF($7,'')::date, deadline), priority=COALESCE(NULLIF($8,''), priority),
			  designer_id=COALESCE($9, designer_id), price=COALESCE($10, price), deposit=COALESCE($11, deposit), revisions_included=COALESCE($12, revisions_included),
			  printing_needed=COALESCE($13, printing_needed)
			WHERE id=$1`, id, clean(in.Title), in.Brief, in.SizeFormat, in.StyleNotes, in.TextToInclude, in.Deadline, in.Priority, uuidOrNil(in.DesignerID), price, deposit, in.RevisionsIncluded, in.PrintingNeeded); err != nil {
			return err
		}
		var pr, dp decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT price, deposit FROM design_jobs WHERE id=$1`, id).Scan(&pr, &dp); err != nil {
			return err
		}
		if dp.GreaterThan(pr) {
			return httpx.BadRequest("invalid_body", "The deposit cannot be more than the price")
		}
		if in.DesignerID != nil && before["designer_id"] != *in.DesignerID && *in.DesignerID != p.UserID {
			eid := id
			if err := s.Notify.ToUser(ctx, tx, p.BusinessID, *in.DesignerID, notify.Message{Type: "design_assigned", Title: "New design brief", Body: before["number"].(string) + ": " + before["title"].(string), Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &eid}); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "design_job.updated", "design_job", id, map[string]any{"price": before["price"], "designer_id": before["designer_id"]}, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, p, id)
}

// MoveJob changes the job status along the workflow.
func (s *Service) MoveJob(ctx context.Context, p *auth.Principal, id uuid.UUID, to, note string) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		return s.moveJobTx(ctx, tx, p, id, j, to, note)
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, p, id)
}

func (s *Service) moveJobTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, j map[string]any, to, note string) error {
	from, _ := j["status"].(string)
	if !canMove(from, to) {
		return httpx.Conflict("invalid_status", "A job in "+from+" cannot move to "+to)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE design_jobs SET status=$2,
		  revisions_used = revisions_used + CASE WHEN $2 = 'changes_requested' THEN 1 ELSE 0 END,
		  approved_at = CASE WHEN $2 = 'approved' THEN now() ELSE approved_at END,
		  delivered_at = CASE WHEN $2 = 'delivered' THEN now() ELSE delivered_at END
		WHERE id=$1`, id, to); err != nil {
		return err
	}
	if note != "" && p != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO design_feedback (id, business_id, job_id, author_type, author_id, comment) VALUES ($1,$2,$3,'staff',$4,$5)`, ids.New(), p.BusinessID, id, p.UserID, note); err != nil {
			return err
		}
	}
	if to == "approved" {
		var latest *uuid.UUID
		_ = tx.QueryRow(ctx, `SELECT id FROM design_versions WHERE job_id=$1 AND kind='design' ORDER BY version_no DESC LIMIT 1`, id).Scan(&latest)
		if latest != nil {
			if _, err := tx.Exec(ctx, `UPDATE design_versions SET is_final = (id = $2) WHERE job_id=$1 AND kind='design'`, id, *latest); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE design_versions SET status='approved' WHERE id=$1`, *latest); err != nil {
				return err
			}
		}
	}
	if to == "approved" || to == "delivered" || to == "cancelled" {
		if err := s.syncStageFromJobs(ctx, tx, j); err != nil {
			return err
		}
	}
	return nil
}

// syncStageFromJobs marks a project stage done when every design job linked to it is approved, delivered or cancelled
// and at least one is approved or delivered (the branding stage link).
func (s *Service) syncStageFromJobs(ctx context.Context, tx pgx.Tx, j map[string]any) error {
	stage, ok := j["stage_id"].(uuid.UUID)
	if !ok {
		return nil
	}
	var open, good int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status NOT IN ('approved','printing','delivered','cancelled')), count(*) FILTER (WHERE status IN ('approved','printing','delivered')) FROM design_jobs WHERE stage_id=$1`, stage).Scan(&open, &good); err != nil {
		return err
	}
	if open > 0 || good == 0 {
		return nil
	}
	var project uuid.UUID
	var cur string
	if err := tx.QueryRow(ctx, `SELECT project_id, status FROM svc_project_stages WHERE id=$1 FOR UPDATE`, stage).Scan(&project, &cur); err != nil || cur == "done" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE svc_project_stages SET status='done', completed_at=now(), updated_at=now() WHERE id=$1`, stage); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO svc_stage_events (id, business_id, project_id, stage_id, from_status, to_status, note) SELECT $1, business_id, project_id, id, $3, 'done', 'All design jobs approved' FROM svc_project_stages WHERE id=$2`, ids.New(), stage, cur); err != nil {
		return err
	}
	var pr map[string]any
	rowsList, err := rows(ctx, tx, `SELECT id, number, status, owner_id FROM svc_projects WHERE id=$1`, project)
	if err != nil || len(rowsList) == 0 {
		return err
	}
	pr = rowsList[0]
	biz, _ := j["business_id"].(uuid.UUID)
	return s.syncProjectProgress(ctx, tx, biz, project, pr)
}

// ---------- versions, feedback, approval ----------

type VersionIn struct {
	FileID *uuid.UUID `json:"file_id"`
	Link   string     `json:"link" validate:"omitempty,url,max=500"`
	Kind   string     `json:"kind" validate:"omitempty,oneof=design print_ready source"`
	Notes  string     `json:"notes" validate:"max=1000"`
}

func (s *Service) AddVersion(ctx context.Context, p *auth.Principal, jobID uuid.UUID, in VersionIn) (map[string]any, error) {
	if in.FileID == nil && clean(in.Link) == "" {
		return nil, httpx.BadRequest("invalid_body", "Attach a file or give a link")
	}
	kind := in.Kind
	if kind == "" {
		kind = "design"
	}
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status == "cancelled" {
			return httpx.Conflict("invalid_status", "A cancelled job cannot take files")
		}
		if kind != "design" && status != "approved" && status != "printing" && status != "delivered" {
			return httpx.Conflict("invalid_status", "Print-ready and source files are added once the design is approved")
		}
		if in.FileID != nil {
			if ok, _ := exists(ctx, tx, "files", *in.FileID); !ok {
				return httpx.NotFound("not_found", "File not found")
			}
		}
		var no int
		if kind == "design" {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no),0)+1 FROM design_versions WHERE job_id=$1 AND kind='design'`, jobID).Scan(&no); err != nil {
				return err
			}
		}
		id := ids.New()
		vstatus := "draft"
		if kind != "design" {
			vstatus = "approved"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO design_versions (id, business_id, job_id, version_no, file_id, link, kind, notes, status, uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			id, p.BusinessID, jobID, no, uuidOrNil(in.FileID), nz(in.Link), kind, nz(in.Notes), vstatus, p.UserID); err != nil {
			return err
		}
		if kind == "design" && (status == "new_brief" || status == "changes_requested") {
			if err := s.moveJobTx(ctx, tx, p, jobID, j, "in_design", ""); err != nil {
				return err
			}
		}
		out, err = one(ctx, tx, "Version", `SELECT v.id, v.version_no, v.kind, v.file_id, v.link, v.notes, v.status, v.is_final, v.created_at FROM design_versions v WHERE v.id=$1`, id)
		return err
	})
	return out, err
}

type FeedbackIn struct {
	VersionID *uuid.UUID `json:"version_id"`
	Comment   string     `json:"comment" validate:"required,max=2000"`
}

func (s *Service) AddFeedback(ctx context.Context, p *auth.Principal, jobID uuid.UUID, in FeedbackIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, false)
		if err != nil {
			return err
		}
		if in.VersionID != nil {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM design_versions WHERE id=$1 AND job_id=$2`, *in.VersionID, jobID).Scan(&n); err != nil || n == 0 {
				return httpx.NotFound("not_found", "Version not found")
			}
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO design_feedback (id, business_id, job_id, version_id, author_type, author_id, comment) VALUES ($1,$2,$3,$4,'staff',$5,$6)`,
			id, p.BusinessID, jobID, uuidOrNil(in.VersionID), p.UserID, clean(in.Comment)); err != nil {
			return err
		}
		if d, ok := j["designer_id"].(uuid.UUID); ok && d != p.UserID {
			eid := jobID
			if err := s.Notify.ToUser(ctx, tx, p.BusinessID, d, notify.Message{Type: "design_feedback", Title: "Feedback on a design job", Body: j["number"].(string) + ": " + clean(in.Comment), Link: "/design-jobs/" + jobID.String(), EntityType: "design_job", EntityID: &eid}); err != nil {
				return err
			}
		}
		out, err = one(ctx, tx, "Feedback", `SELECT id, version_id, author_type, comment, created_at FROM design_feedback WHERE id=$1`, id)
		return err
	})
	return out, err
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// SendForApproval gives the client a private link to one version. The link works for 14 days.
func (s *Service) SendForApproval(ctx context.Context, p *auth.Principal, jobID, versionID uuid.UUID, baseURL string) (map[string]any, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.Clock.Now().Add(14 * 24 * time.Hour)
	var phone, title, number string
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status != "in_design" && status != "internal_review" && status != "with_client" {
			return httpx.Conflict("invalid_status", "A job in "+status+" cannot be sent for approval")
		}
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM design_versions WHERE id=$1 AND job_id=$2 FOR UPDATE`, versionID, jobID).Scan(&kind); err != nil || kind != "design" {
			return httpx.NotFound("not_found", "Design version not found")
		}
		if _, err := tx.Exec(ctx, `UPDATE design_versions SET token_hash=NULL WHERE job_id=$1 AND status='sent'`, jobID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE design_versions SET status='draft' WHERE job_id=$1 AND status='sent' AND id <> $2`, jobID, versionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE design_versions SET status='sent', token_hash=$2, token_expires_at=$3, decided_at=NULL WHERE id=$1`, versionID, hashToken(token), expires); err != nil {
			return err
		}
		if status != "with_client" {
			if err := s.moveJobTx(ctx, tx, p, jobID, j, "with_client", ""); err != nil {
				return err
			}
		}
		number, _ = j["number"].(string)
		title, _ = j["title"].(string)
		var ph *string
		if err := tx.QueryRow(ctx, `SELECT phone FROM svc_clients WHERE id=$1`, j["client_id"]).Scan(&ph); err != nil {
			return err
		}
		if ph != nil {
			phone = *ph
		}
		return s.Audit.Record(ctx, tx, p, "design_version.sent", "design_job", jobID, nil, map[string]any{"version_id": versionID, "expires_at": expires})
	})
	if err != nil {
		return nil, err
	}
	path := "/approve/" + token
	delivered := false
	if s.SMS != nil && phone != "" && baseURL != "" {
		delivered = s.SMS.Send(ctx, sender.Message{To: phone, Body: "Please review your design \"" + title + "\": " + strings.TrimRight(baseURL, "/") + path}) == nil
	}
	return map[string]any{"job": number, "approval_path": path, "expires_at": expires, "delivered": delivered}, nil
}

// ---------- public approval page ----------

func (s *Service) tokenBusiness(ctx context.Context, token string) (uuid.UUID, error) {
	var biz *uuid.UUID
	if err := s.DB.Pool.QueryRow(ctx, `SELECT design_token_business($1)`, hashToken(token)).Scan(&biz); err != nil || biz == nil {
		return uuid.Nil, httpx.NotFound("not_found", "This approval link is not valid")
	}
	return *biz, nil
}

type approvalRow struct {
	versionID, jobID uuid.UUID
	versionNo        int
	status           string
	expires          time.Time
	decided          *time.Time
	fileID           *uuid.UUID
}

func (s *Service) loadApproval(ctx context.Context, tx pgx.Tx, token string, lock bool) (approvalRow, error) {
	var a approvalRow
	sql := `SELECT id, job_id, version_no, status, token_expires_at, decided_at, file_id FROM design_versions WHERE token_hash=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	if err := tx.QueryRow(ctx, sql, hashToken(token)).Scan(&a.versionID, &a.jobID, &a.versionNo, &a.status, &a.expires, &a.decided, &a.fileID); err != nil {
		return a, httpx.NotFound("not_found", "This approval link is not valid")
	}
	if s.Clock.Now().After(a.expires) {
		return a, httpx.New(410, "link_expired", "This approval link has expired. Ask us for a new one")
	}
	return a, nil
}

// PublicApproval returns what the client sees on the approval page.
func (s *Service) PublicApproval(ctx context.Context, token string) (map[string]any, error) {
	biz, err := s.tokenBusiness(ctx, token)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.tx(ctx, biz, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, token, false)
		if err != nil {
			return err
		}
		out, err = one(ctx, tx, "Design", `
			SELECT j.title, j.job_type, c.name AS client_name, (SELECT name FROM businesses WHERE id = j.business_id) AS business_name, j.revisions_included, j.revisions_used
			FROM design_jobs j JOIN svc_clients c ON c.id = j.client_id WHERE j.id=$1`, a.jobID)
		if err != nil {
			return err
		}
		out["version_no"], out["status"], out["expires_at"], out["decided_at"] = a.versionNo, a.status, a.expires, a.decided
		out["has_file"] = a.fileID != nil
		if a.fileID != nil {
			var name, mime string
			if err := tx.QueryRow(ctx, `SELECT original_name, mime_type FROM files WHERE id=$1`, *a.fileID).Scan(&name, &mime); err == nil {
				out["file_name"], out["mime_type"] = name, mime
			}
		}
		var link *string
		_ = tx.QueryRow(ctx, `SELECT link FROM design_versions WHERE id=$1`, a.versionID).Scan(&link)
		out["link"] = link
		out["comments"], err = rows(ctx, tx, `SELECT author_type, comment, created_at FROM design_feedback WHERE version_id=$1 ORDER BY created_at`, a.versionID)
		return err
	})
	return out, err
}

// PublicApprovalFile streams the version's file.
func (s *Service) PublicApprovalFile(ctx context.Context, token string) (string, string, io.ReadCloser, error) {
	biz, err := s.tokenBusiness(ctx, token)
	if err != nil {
		return "", "", nil, err
	}
	var path, name, mime string
	err = s.tx(ctx, biz, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, token, false)
		if err != nil {
			return err
		}
		if a.fileID == nil {
			return httpx.NotFound("not_found", "This version has no file")
		}
		return tx.QueryRow(ctx, `SELECT path, original_name, mime_type FROM files WHERE id=$1`, *a.fileID).Scan(&path, &name, &mime)
	})
	if err != nil {
		return "", "", nil, err
	}
	rc, err := s.Store.Open(ctx, path)
	return name, mime, rc, err
}

type DecisionIn struct {
	Decision string `json:"decision" validate:"required,oneof=approve changes"`
	Comment  string `json:"comment" validate:"max=2000"`
}

// PublicDecide records the client's choice. Approve marks the version final; changes sends the comment to the designer.
func (s *Service) PublicDecide(ctx context.Context, token string, in DecisionIn) (map[string]any, error) {
	if in.Decision == "changes" && clean(in.Comment) == "" {
		return nil, httpx.BadRequest("invalid_body", "Tell us what to change")
	}
	biz, err := s.tokenBusiness(ctx, token)
	if err != nil {
		return nil, err
	}
	var result string
	err = s.tx(ctx, biz, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, token, true)
		if err != nil {
			return err
		}
		if a.decided != nil || a.status != "sent" {
			return httpx.Conflict("already_decided", "A choice has already been recorded for this design")
		}
		rowsList, err := rows(ctx, tx, `SELECT id, business_id, number, title, status, designer_id, stage_id, project_id FROM design_jobs WHERE id=$1 FOR UPDATE`, a.jobID)
		if err != nil || len(rowsList) == 0 {
			return err
		}
		j := rowsList[0]
		if j["status"] != "with_client" {
			return httpx.Conflict("invalid_status", "This design is no longer waiting for the client")
		}
		if clean(in.Comment) != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO design_feedback (id, business_id, job_id, version_id, author_type, comment) VALUES ($1,$2,$3,$4,'client',$5)`, ids.New(), biz, a.jobID, a.versionID, clean(in.Comment)); err != nil {
				return err
			}
		}
		to := "approved"
		vstatus := "approved"
		if in.Decision == "changes" {
			to, vstatus = "changes_requested", "changes_requested"
		}
		if _, err := tx.Exec(ctx, `UPDATE design_versions SET status=$2, decided_at=now() WHERE id=$1`, a.versionID, vstatus); err != nil {
			return err
		}
		if err := s.moveJobTx(ctx, tx, nil, a.jobID, j, to, ""); err != nil {
			return err
		}
		title := "Client requested changes"
		if in.Decision == "approve" {
			title = "Design approved by the client"
		}
		eid := a.jobID
		msg := notify.Message{Type: "design_client_decision", Title: title, Body: j["number"].(string) + ": " + j["title"].(string), Link: "/design-jobs/" + a.jobID.String(), EntityType: "design_job", EntityID: &eid}
		if d, ok := j["designer_id"].(uuid.UUID); ok {
			if err := s.Notify.ToUser(ctx, tx, biz, d, msg); err != nil {
				return err
			}
		}
		if err := s.Notify.ToPermission(ctx, tx, biz, nil, "design.manage", msg); err != nil {
			return err
		}
		result = to
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": result}, nil
}

// ---------- timer ----------

func (s *Service) StartTimer(ctx context.Context, p *auth.Principal, jobID uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.jobRow(ctx, tx, p, jobID, false); err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO design_time_entries (id, business_id, job_id, user_id, started_at) VALUES ($1,$2,$3,$4,$5)`, id, p.BusinessID, jobID, p.UserID, s.Clock.Now()); err != nil {
			if isDup(err) {
				return httpx.Conflict("timer_running", "You already have a timer running. Stop it first")
			}
			return err
		}
		var err error
		out, err = one(ctx, tx, "Timer", `SELECT id, job_id, started_at FROM design_time_entries WHERE id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) StopTimer(ctx context.Context, p *auth.Principal, jobID uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var started time.Time
		if err := tx.QueryRow(ctx, `SELECT id, started_at FROM design_time_entries WHERE job_id=$1 AND user_id=$2 AND stopped_at IS NULL FOR UPDATE`, jobID, p.UserID).Scan(&id, &started); err != nil {
			return httpx.Conflict("no_timer", "You have no timer running on this job")
		}
		now := s.Clock.Now()
		minutes := int(now.Sub(started).Minutes() + 0.5)
		if minutes < 1 {
			minutes = 1
		}
		if _, err := tx.Exec(ctx, `UPDATE design_time_entries SET stopped_at=$2, minutes=$3 WHERE id=$1`, id, now, minutes); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Timer", `SELECT id, job_id, started_at, stopped_at, minutes FROM design_time_entries WHERE id=$1`, id)
		return err
	})
	return out, err
}

// ---------- print orders ----------

type PrintOrderIn struct {
	ContractorID *uuid.UUID      `json:"contractor_id"`
	Quantity     int             `json:"quantity" validate:"gt=0,lte=1000000"`
	Cost         decimal.Decimal `json:"cost" validate:"dgte0"`
	DueDate      string          `json:"due_date" validate:"omitempty,date"`
	Note         string          `json:"note" validate:"max=500"`
}

// CreatePrintOrder sends an approved design to a printer. The cost is owed to the contractor and counted against the job.
func (s *Service) CreatePrintOrder(ctx context.Context, p *auth.Principal, jobID uuid.UUID, in PrintOrderIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	if in.Cost.IsPositive() && in.ContractorID == nil {
		return nil, httpx.BadRequest("invalid_body", "Choose the contractor who prints it, so the cost can be recorded")
	}
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status != "approved" && status != "printing" {
			return httpx.Conflict("invalid_status", "A print order needs an approved design")
		}
		var costID any
		if in.Cost.IsPositive() {
			c, err := s.addCostTx(ctx, tx, p, CostIn{DesignJobID: &jobID, ContractorID: in.ContractorID, Kind: "printing", Description: "Printing for " + j["number"].(string), Amount: in.Cost})
			if err != nil {
				return err
			}
			costID = c["id"]
		} else if in.ContractorID != nil {
			if ok, _ := exists(ctx, tx, "contractors", *in.ContractorID); !ok {
				return httpx.NotFound("not_found", "Contractor not found")
			}
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO design_print_orders (id, business_id, job_id, contractor_id, quantity, cost, due_date, note, cost_id, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			id, p.BusinessID, jobID, uuidOrNil(in.ContractorID), in.Quantity, money.Money(in.Cost), dateOrNil(in.DueDate), nz(in.Note), costID, p.UserID); err != nil {
			return err
		}
		if status == "approved" {
			if err := s.moveJobTx(ctx, tx, p, jobID, j, "printing", ""); err != nil {
				return err
			}
		}
		out, err = one(ctx, tx, "Print order", `SELECT id, job_id, contractor_id, quantity, cost, due_date, status, note, created_at FROM design_print_orders WHERE id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdatePrintOrder(ctx context.Context, p *auth.Principal, id uuid.UUID, status string) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var cur string
		var jobID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, job_id FROM design_print_orders WHERE id=$1 FOR UPDATE`, id).Scan(&cur, &jobID); err != nil {
			return httpx.NotFound("not_found", "Print order not found")
		}
		order := map[string]int{"sent": 0, "ready": 1, "delivered": 2}
		if cur == "cancelled" || cur == "delivered" || (status != "cancelled" && order[status] <= order[cur]) {
			return httpx.Conflict("invalid_status", "A "+cur+" print order cannot move to "+status)
		}
		if _, err := tx.Exec(ctx, `UPDATE design_print_orders SET status=$2 WHERE id=$1`, id, status); err != nil {
			return err
		}
		if status == "ready" {
			j, err := rows(ctx, tx, `SELECT number, title, designer_id FROM design_jobs WHERE id=$1`, jobID)
			if err != nil {
				return err
			}
			if len(j) > 0 {
				if d, ok := j[0]["designer_id"].(uuid.UUID); ok {
					eid := jobID
					if err := s.Notify.ToUser(ctx, tx, p.BusinessID, d, notify.Message{Type: "design_print_ready", Title: "Print ready", Body: j[0]["number"].(string) + ": " + j[0]["title"].(string), Link: "/design-jobs/" + jobID.String(), EntityType: "design_job", EntityID: &eid}); err != nil {
						return err
					}
				}
			}
		}
		var err error
		out, err = one(ctx, tx, "Print order", `SELECT id, job_id, contractor_id, quantity, cost, due_date, status, note FROM design_print_orders WHERE id=$1`, id)
		return err
	})
	return out, err
}

// ---------- branding stage link ----------

type BrandingIn struct {
	Jobs []struct {
		JobType    string           `json:"job_type" validate:"required,oneof=logo business_card poster flyer banner signage social_media packaging_label branding_package other"`
		Title      string           `json:"title" validate:"required,max=200"`
		DesignerID *uuid.UUID       `json:"designer_id"`
		Deadline   string           `json:"deadline" validate:"omitempty,date"`
		Price      *decimal.Decimal `json:"price" validate:"omitempty,dgte0"`
	} `json:"jobs" validate:"required,min=1,max=20,dive"`
}

// CreateStageJobs creates several design jobs at once for a project's branding stage.
func (s *Service) CreateStageJobs(ctx context.Context, p *auth.Principal, projectID, stageID uuid.UUID, in BrandingIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	created := []uuid.UUID{}
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_project_stages WHERE id=$1 AND project_id=$2`, stageID, projectID).Scan(&n); err != nil || n == 0 {
			return httpx.NotFound("not_found", "Stage not found")
		}
		for _, jb := range in.Jobs {
			id, err := s.createJobTx(ctx, tx, p, DesignJobIn{ClientID: pr["client_id"].(uuid.UUID), ProjectID: &projectID, StageID: &stageID, JobType: jb.JobType, Title: jb.Title, DesignerID: jb.DesignerID, Deadline: jb.Deadline, Price: jb.Price})
			if err != nil {
				return err
			}
			created = append(created, id)
		}
		_, err = tx.Exec(ctx, `UPDATE svc_project_stages SET status='in_progress', updated_at=now() WHERE id=$1 AND status='not_started'`, stageID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"job_ids": created}, nil
}
