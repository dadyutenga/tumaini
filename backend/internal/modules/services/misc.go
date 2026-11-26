package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sender"
	"bms/internal/platform/sqlq"
)

// ---------- documents ----------

type DocumentIn struct {
	ClientID    uuid.UUID  `json:"client_id" validate:"required"`
	ProjectID   *uuid.UUID `json:"project_id"`
	StageID     *uuid.UUID `json:"stage_id"`
	DesignJobID *uuid.UUID `json:"design_job_id"`
	Name        string     `json:"name" validate:"required,max=200"`
	DocType     string     `json:"doc_type" validate:"max=60"`
	FileID      *uuid.UUID `json:"file_id"`
}

type DocumentPatch struct {
	Name   string     `json:"name" validate:"max=200"`
	Status string     `json:"status" validate:"omitempty,oneof=missing received rejected returned"`
	FileID *uuid.UUID `json:"file_id"`
}

const docCols = `d.id, d.customer_id AS client_id, c.full_name::text AS client_name,
	d.project_id, pr.project_no AS project_number, d.project_stage_id AS stage_id, NULL::uuid AS design_job_id,
	d.title::text AS name,
	COALESCE(dt.name::text, CASE WHEN d.is_final_deliverable THEN 'deliverable' ELSE 'required' END) AS doc_type,
	d.status::text AS status, d.file_id, f.original_name::text AS file_name, f.mime_type, d.received_at, d.created_at`

const docFrom = ` FROM app.client_documents d
	JOIN app.customers c ON c.tenant_id = d.tenant_id AND c.id = d.customer_id
	LEFT JOIN app.projects pr ON pr.tenant_id = d.tenant_id AND pr.id = d.project_id
	LEFT JOIN app.document_types dt ON dt.tenant_id = d.tenant_id AND dt.id = d.document_type_id
	LEFT JOIN app.files f ON f.tenant_id = d.tenant_id AND f.id = d.file_id`

func (s *Service) ListDocuments(ctx context.Context, p *auth.Principal, clientID, projectID *uuid.UUID, status, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("d.tenant_id = $1")
		q.If(clientID != nil, "d.customer_id = ?", clientID)
		q.If(projectID != nil, "d.project_id = ?", projectID)
		q.If(status != "", "d.status = ?::app.client_document_status", status)
		if t := clean(q0); t != "" {
			q.Add("(d.title ILIKE '%' || ? || '%' OR COALESCE(dt.name::text,'') ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%')", t, t, t)
		}
		if !p.Has("services.project.view_all") && !p.Has("services.client.view_all") {
			mid := p.MembershipID
			q.Add("(d.project_id IS NULL OR EXISTS (SELECT 1 FROM app.projects x WHERE x.tenant_id = d.tenant_id AND x.id = d.project_id AND (x.owner_id = ? OR x.created_by = ?)))", mid, mid)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+docFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+docCols+docFrom+q.Where()+` ORDER BY c.full_name, d.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateDocument(ctx context.Context, p *auth.Principal, in DocumentIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "app.customers", "Client", p.TenantID, in.ClientID); err != nil {
			return err
		}
		if in.ProjectID != nil {
			var c uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT customer_id FROM app.projects WHERE tenant_id = $1 AND id = $2`, p.TenantID, *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
				return httpx.NotFound("not_found", "Project not found for this client")
			}
		}
		if in.StageID != nil && in.ProjectID == nil {
			return httpx.BadRequest("invalid_body", "A stage document needs a project")
		}
		status := "missing"
		var receivedAt, receivedBy any
		if in.FileID != nil {
			if err := require404(ctx, tx, "app.files", "File", p.TenantID, *in.FileID); err != nil {
				return err
			}
			status = "received"
			receivedAt = s.Clock.Now()
			receivedBy = p.MembershipID
		}
		var docTypeID any
		isDeliverable := strings.EqualFold(clean(in.DocType), "deliverable")
		if dt := clean(in.DocType); dt != "" && !strings.EqualFold(dt, "required") && !isDeliverable {
			dtID, err := upsertDocumentType(ctx, tx, p.TenantID, dt)
			if err != nil {
				return err
			}
			docTypeID = dtID
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.client_documents (id, tenant_id, customer_id, project_id, project_stage_id, document_type_id, title, status, file_id, is_final_deliverable, received_at, received_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::app.client_document_status,$9,$10,$11,$12)`,
			id, p.TenantID, in.ClientID, uuidOrNil(in.ProjectID), uuidOrNil(in.StageID), docTypeID, clean(in.Name), status, uuidOrNil(in.FileID), isDeliverable, receivedAt, receivedBy); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Document", `SELECT `+docCols+docFrom+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, id)
		return err
	})
	return out, err
}

// UpdateDocument renames a document, attaches a file, or changes its status.
func (s *Service) UpdateDocument(ctx context.Context, p *auth.Principal, id uuid.UUID, in DocumentPatch) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status::text FROM app.client_documents WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Document not found")
		}
		if in.FileID != nil {
			if err := require404(ctx, tx, "app.files", "File", p.TenantID, *in.FileID); err != nil {
				return err
			}
		}
		status := in.Status
		if status == "" {
			status = cur
			if in.FileID != nil {
				status = "received"
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.client_documents SET
			  title = COALESCE(NULLIF($3,''), title),
			  status = $4::app.client_document_status,
			  file_id = COALESCE($5, file_id),
			  received_at = CASE WHEN $4 = 'received' THEN COALESCE(received_at, now()) ELSE received_at END,
			  received_by = CASE WHEN $4 = 'received' THEN COALESCE(received_by, $6) ELSE received_by END
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, clean(in.Name), status, uuidOrNil(in.FileID), p.MembershipID); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Document", `SELECT `+docCols+docFrom+` WHERE d.tenant_id = $1 AND d.id = $2`, p.TenantID, id)
		return err
	})
	return out, err
}

// ---------- tasks ----------

type TaskIn struct {
	Title       string     `json:"title" validate:"max=200"`
	Description string     `json:"description" validate:"max=4000"`
	ProjectID   *uuid.UUID `json:"project_id"`
	DesignJobID *uuid.UUID `json:"design_job_id"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	DueDate     string     `json:"due_date" validate:"omitempty,date"`
	Priority    string     `json:"priority" validate:"omitempty,oneof=normal high urgent"`
	Status      string     `json:"status" validate:"omitempty,oneof=todo open in_progress done cancelled"`
}

func mapTaskStatus(st string) string {
	if st == "todo" {
		return "open"
	}
	return st
}

func taskCols(todayExpr string) string {
	return `t.id, t.title::text AS title, t.description, t.project_id, pr.project_no AS project_number, t.design_job_id,
	am.user_id AS assignee_id, au.full_name::text AS assignee_name,
	t.due_at::date AS due_date, t.priority::text AS priority,
	CASE WHEN t.status = 'open' THEN 'todo' ELSE t.status::text END AS status,
	t.completed_at, t.created_at,
	(t.due_at IS NOT NULL AND t.due_at::date < ` + todayExpr + ` AND t.status IN ('open','in_progress')) AS overdue`
}

const taskFrom = ` FROM app.tasks t
	LEFT JOIN app.projects pr ON pr.tenant_id = t.tenant_id AND pr.id = t.project_id
	LEFT JOIN app.memberships am ON am.tenant_id = t.tenant_id AND am.id = t.assignee_id
	LEFT JOIN iam.users au ON au.id = am.user_id`

type TaskFilter struct {
	Status     string
	AssigneeID *uuid.UUID
	ProjectID  *uuid.UUID
	From, To   string
	Overdue    bool
	Open       bool
}

func (s *Service) ListTasks(ctx context.Context, p *auth.Principal, f TaskFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := sqlq.New(p.TenantID, today)
		q.Add("t.tenant_id = $1")
		q.If(f.Status != "", "t.status = ?::app.task_status", mapTaskStatus(f.Status))
		if f.AssigneeID != nil {
			if mid, err := s.resolveMembership(ctx, tx, p.TenantID, f.AssigneeID); err == nil && mid != nil {
				q.Add("t.assignee_id = ?", *mid)
			} else {
				q.Add("t.assignee_id = ?", *f.AssigneeID)
			}
		}
		q.If(f.ProjectID != nil, "t.project_id = ?", f.ProjectID)
		q.If(f.From != "", "t.due_at::date >= ?::date", f.From)
		q.If(f.To != "", "t.due_at::date <= ?::date", f.To)
		q.If(f.Overdue, "t.due_at::date < $2::date AND t.status IN ('open','in_progress')")
		q.If(f.Open, "t.status IN ('open','in_progress')")
		if !p.Has("services.task.view_all") {
			mid := p.MembershipID
			q.Add("(t.assignee_id = ? OR t.created_by = ?)", mid, mid)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+taskFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		out, err = rows(ctx, tx, `SELECT `+taskCols("$2::date")+taskFrom+q.Where()+` ORDER BY (t.status IN ('done','cancelled')), t.due_at NULLS LAST, t.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func dueDateToTimestamptz(s string) any {
	s = clean(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Add(23*time.Hour + 59*time.Minute)
	}
	return nil
}

func (s *Service) CreateTask(ctx context.Context, p *auth.Principal, in TaskIn) (map[string]any, error) {
	if clean(in.Title) == "" {
		return nil, httpx.BadRequest("invalid_body", "Task title is required")
	}
	id := ids.New()
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if in.ProjectID != nil {
			if err := require404(ctx, tx, "app.projects", "Project", p.TenantID, *in.ProjectID); err != nil {
				return err
			}
		}
		if in.DesignJobID != nil {
			if err := require404(ctx, tx, "app.design_jobs", "Design job", p.TenantID, *in.DesignJobID); err != nil {
				return err
			}
		}
		assigneeMid := p.MembershipID
		if in.AssigneeID != nil {
			mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.AssigneeID)
			if err != nil {
				return err
			}
			if mid != nil {
				assigneeMid = *mid
			}
		}
		prio := in.Priority
		if prio == "" {
			prio = "normal"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.tasks (id, tenant_id, title, description, project_id, design_job_id, assignee_id, due_at, priority)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::app.priority)`,
			id, p.TenantID, clean(in.Title), nz(in.Description), uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), assigneeMid, dueDateToTimestamptz(in.DueDate), prio); err != nil {
			return err
		}
		if assigneeMid != p.MembershipID {
			if uid := s.membershipUserID(ctx, tx, p.TenantID, assigneeMid); uid != uuid.Nil {
				eid := id
				_ = s.Notify.ToUser(ctx, tx, p.TenantID, uid, notify.Message{
					Type: "task_assigned", Title: "New task", Body: clean(in.Title), Link: "/tasks", EntityType: "task", EntityID: &eid,
				})
			}
		}
		out, err = one(ctx, tx, "Task", `SELECT `+taskCols("$2::date")+taskFrom+` WHERE t.tenant_id = $1 AND t.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

// UpdateTask edits a task. Without services.task.manage, a member may only update the status of a task assigned to them.
func (s *Service) UpdateTask(ctx context.Context, p *auth.Principal, id uuid.UUID, in TaskIn, statusOnly bool) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		var assignee uuid.UUID
		var cur, title string
		if err := tx.QueryRow(ctx, `SELECT assignee_id, status::text, title::text FROM app.tasks WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).Scan(&assignee, &cur, &title); err != nil {
			return httpx.NotFound("not_found", "Task not found")
		}
		if statusOnly {
			if assignee != p.MembershipID {
				return httpx.NotFound("not_found", "Task not found")
			}
			in = TaskIn{Status: in.Status}
		}
		status := mapTaskStatus(in.Status)
		if status == "" {
			status = cur
		}
		var assigneeMid any
		var newAssignee *uuid.UUID
		if in.AssigneeID != nil {
			mid, err := s.resolveMembership(ctx, tx, p.TenantID, in.AssigneeID)
			if err != nil {
				return err
			}
			newAssignee = mid
			assigneeMid = uuidOrNil(mid)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.tasks SET
			  title = COALESCE(NULLIF($3,''), title),
			  description = COALESCE(NULLIF($4,''), description),
			  assignee_id = COALESCE($5, assignee_id),
			  due_at = COALESCE($6, due_at),
			  priority = COALESCE(NULLIF($7,'')::app.priority, priority),
			  status = $8::app.task_status,
			  completed_at = CASE WHEN $8 = 'done' THEN COALESCE(completed_at, now()) ELSE NULL END
			WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, id, clean(in.Title), clean(in.Description), assigneeMid, dueDateToTimestamptz(in.DueDate), in.Priority, status); err != nil {
			return err
		}
		if newAssignee != nil && *newAssignee != assignee && *newAssignee != p.MembershipID {
			if uid := s.membershipUserID(ctx, tx, p.TenantID, *newAssignee); uid != uuid.Nil {
				eid := id
				body := clean(in.Title)
				if body == "" {
					body = title
				}
				_ = s.Notify.ToUser(ctx, tx, p.TenantID, uid, notify.Message{
					Type: "task_assigned", Title: "New task", Body: body, Link: "/tasks", EntityType: "task", EntityID: &eid,
				})
			}
		}
		out, err = one(ctx, tx, "Task", `SELECT `+taskCols("$2::date")+taskFrom+` WHERE t.tenant_id = $1 AND t.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

// ---------- renewals ----------

type RenewalIn struct {
	ClientID  uuid.UUID        `json:"client_id" validate:"required"`
	ProjectID *uuid.UUID       `json:"project_id"`
	Item      string           `json:"item" validate:"required,max=200"`
	ItemType  string           `json:"item_type" validate:"omitempty,oneof=business_license annual_return annual_fee tax_filing permit other"`
	DueDate   string           `json:"due_date" validate:"required,date"`
	Amount    *decimal.Decimal `json:"amount" validate:"omitempty,dgte0"`
	Notes     string           `json:"notes" validate:"max=1000"`
}

type RenewalPatch struct {
	Item    string           `json:"item" validate:"max=200"`
	DueDate string           `json:"due_date" validate:"omitempty,date"`
	Amount  *decimal.Decimal `json:"amount" validate:"omitempty,dgte0"`
	Notes   *string          `json:"notes" validate:"omitempty,max=1000"`
}

type RenewIn struct {
	NextDueDate string           `json:"next_due_date" validate:"omitempty,date"`
	Amount      *decimal.Decimal `json:"amount" validate:"omitempty,dgte0"`
}

func mapRenewalStatus(st string) string {
	if st == "pending" {
		return "upcoming"
	}
	return st
}

func inferRenewalItemType(explicit, item string) string {
	if explicit != "" {
		return explicit
	}
	low := strings.ToLower(item)
	switch {
	case strings.Contains(low, "license") || strings.Contains(low, "licence"):
		return "business_license"
	case strings.Contains(low, "return"):
		return "annual_return"
	case strings.Contains(low, "tax") || strings.Contains(low, "filing"):
		return "tax_filing"
	case strings.Contains(low, "permit"):
		return "permit"
	case strings.Contains(low, "fee"):
		return "annual_fee"
	default:
		return "other"
	}
}

func renewalCols(todayExpr string) string {
	return `r.id, r.customer_id AS client_id, c.full_name::text AS client_name, c.phone::text AS client_phone,
	r.project_id, r.item_type::text AS item_type, r.description::text AS item, r.due_date, 0::numeric AS amount,
	CASE WHEN r.status = 'upcoming' THEN 'pending' ELSE r.status::text END AS status,
	r.renewed_at, r.next_renewal_id AS next_id, r.notes,
	(r.due_date - ` + todayExpr + `) AS days_left, NULL::timestamptz AS reminded_30_at, NULL::timestamptz AS reminded_7_at, r.created_at`
}

const renewalFrom = ` FROM app.renewals r JOIN app.customers c ON c.tenant_id = r.tenant_id AND c.id = r.customer_id`

func (s *Service) ListRenewals(ctx context.Context, p *auth.Principal, status string, clientID *uuid.UUID, within int, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := sqlq.New(p.TenantID, today)
		q.Add("r.tenant_id = $1")
		q.If(status != "", "r.status = ?::app.renewal_status", mapRenewalStatus(status))
		q.If(clientID != nil, "r.customer_id = ?", clientID)
		q.If(within > 0, "r.status = 'upcoming' AND r.due_date <= $2::date + ?::int", within)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+renewalFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		out, err = rows(ctx, tx, `SELECT `+renewalCols("$2::date")+renewalFrom+q.Where()+` ORDER BY (r.status <> 'upcoming'), r.due_date, r.created_at`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateRenewal(ctx context.Context, p *auth.Principal, in RenewalIn) (map[string]any, error) {
	id := ids.New()
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if err := require404(ctx, tx, "app.customers", "Client", p.TenantID, in.ClientID); err != nil {
			return err
		}
		itemType := inferRenewalItemType(in.ItemType, in.Item)
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.renewals (id, tenant_id, customer_id, project_id, item_type, description, due_date, notes)
			VALUES ($1,$2,$3,$4,$5::app.renewal_item,$6,$7::date,$8)`,
			id, p.TenantID, in.ClientID, uuidOrNil(in.ProjectID), itemType, clean(in.Item), in.DueDate, nz(in.Notes)); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A renewal for that project, type and date already exists")
			}
			return err
		}
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateRenewal(ctx context.Context, p *auth.Principal, id uuid.UUID, in RenewalPatch) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status::text FROM app.renewals WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).Scan(&status); err != nil {
			return httpx.NotFound("not_found", "Renewal not found")
		}
		if status != "upcoming" {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be edited")
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.renewals SET
			  description = COALESCE(NULLIF($3,''), description),
			  due_date = COALESCE(NULLIF($4,'')::date, due_date),
			  notes = COALESCE($5, notes)
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, clean(in.Item), in.DueDate, in.Notes); err != nil {
			return err
		}
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

// Renew marks a renewal done and, when a next date is given, schedules the next one.
func (s *Service) Renew(ctx context.Context, p *auth.Principal, id uuid.UUID, in RenewIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		var status, itemType, item string
		var client uuid.UUID
		var project *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT status::text, customer_id, project_id, item_type::text, description::text
			FROM app.renewals WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).
			Scan(&status, &client, &project, &itemType, &item); err != nil {
			return httpx.NotFound("not_found", "Renewal not found")
		}
		if status != "upcoming" && status != "lapsed" {
			return httpx.Conflict("invalid_status", "This renewal is already "+status)
		}
		var next any
		if in.NextDueDate != "" {
			nid := ids.New()
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.renewals (id, tenant_id, customer_id, project_id, item_type, description, due_date)
				VALUES ($1,$2,$3,$4,$5::app.renewal_item,$6,$7::date)`,
				nid, p.TenantID, client, uuidOrNil(project), itemType, item, in.NextDueDate); err != nil {
				return err
			}
			next = nid
		}
		if _, err := tx.Exec(ctx, `UPDATE app.renewals SET status = 'renewed', renewed_at = now(), next_renewal_id = $3 WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, next); err != nil {
			return err
		}
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

func (s *Service) CancelRenewal(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE app.renewals SET status = 'cancelled' WHERE tenant_id = $1 AND id = $2 AND status = 'upcoming'`, p.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be cancelled")
		}
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.id = $3`, p.TenantID, today, id)
		return err
	})
	return out, err
}

// RemindClient sends the renewal reminder to the client now.
func (s *Service) RemindClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var phoneStr, item, due string
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		r, err := one(ctx, tx, "Renewal", `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.id = $3`, p.TenantID, today, id)
		if err != nil {
			return err
		}
		if r["status"] != "pending" {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be reminded")
		}
		phoneStr, _ = r["client_phone"].(string)
		item, _ = r["item"].(string)
		due, _ = r["due_date"].(string)
		return nil
	})
	if err != nil {
		return nil, err
	}
	delivered := false
	if s.SMS != nil && phoneStr != "" {
		delivered = s.SMS.Send(ctx, sender.Message{To: phoneStr, Body: fmt.Sprintf("Reminder: %s is due on %s.", item, due)}) == nil
	}
	return map[string]any{"delivered": delivered, "has_phone": phoneStr != ""}, nil
}

// ---------- daily reminders (job) ----------

// RunReminders notifies staff about upcoming renewals, design deadlines in 2 days, and tasks due today.
func (s *Service) RunReminders(ctx context.Context, tenantID uuid.UUID, _ json.RawMessage) error {
	type sms struct{ phone, body string }
	var texts []sms
	err := s.tenantTx(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		lead := s.Notify.DaysBefore(ctx, tx, "renewal_due", 30)
		list, err := rows(ctx, tx, `
			SELECT r.id, r.description::text AS item, r.due_date, c.full_name::text AS client_name, c.phone::text AS client_phone, (r.due_date - $2::date) AS days
			FROM app.renewals r JOIN app.customers c ON c.tenant_id = r.tenant_id AND c.id = r.customer_id
			WHERE r.tenant_id = $1 AND r.status = 'upcoming' AND (r.due_date - $2::date) IN ($3, 7, 0)`, tenantID, date, lead)
		if err != nil {
			return err
		}
		for _, r := range list {
			days := asInt(r["days"])
			id := r["id"].(uuid.UUID)
			eid := id
			body := fmt.Sprintf("%s for %s is due on %v (%d days)", r["item"], r["client_name"], r["due_date"], days)
			if err := s.Notify.ToPermission(ctx, tx, tenantID, nil, "services.renewal.manage", notify.Message{
				Type: "renewal_due", Title: "Renewal due", Body: body, Link: "/renewals", EntityType: "renewal", EntityID: &eid,
			}); err != nil {
				return err
			}
			if ph, _ := r["client_phone"].(string); ph != "" {
				texts = append(texts, sms{ph, fmt.Sprintf("Reminder: %s is due on %v.", r["item"], r["due_date"])})
			}
		}
		jobs, err := rows(ctx, tx, `
			SELECT j.id, j.job_no AS number, j.title::text AS title, dm.user_id AS designer_id
			FROM app.design_jobs j
			JOIN app.memberships dm ON dm.tenant_id = j.tenant_id AND dm.id = j.designer_id
			WHERE j.tenant_id = $1 AND j.status IN ('new_brief','in_design','internal_review') AND j.deadline = $2::date + 2`, tenantID, date)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			id := j["id"].(uuid.UUID)
			if err := s.Notify.ToUser(ctx, tx, tenantID, j["designer_id"].(uuid.UUID), notify.Message{
				Type: "design_deadline", Title: "Design deadline in 2 days", Body: fmt.Sprintf("%s: %s", j["number"], j["title"]),
				Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		tasks, err := rows(ctx, tx, `
			SELECT t.id, t.title::text AS title, am.user_id AS assignee_id
			FROM app.tasks t
			JOIN app.memberships am ON am.tenant_id = t.tenant_id AND am.id = t.assignee_id
			WHERE t.tenant_id = $1 AND t.status IN ('open','in_progress') AND t.due_at::date = $2::date`, tenantID, date)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			id := t["id"].(uuid.UUID)
			if err := s.Notify.ToUser(ctx, tx, tenantID, t["assignee_id"].(uuid.UUID), notify.Message{
				Type: "task_overdue", Title: "Task due today", Body: fmt.Sprint(t["title"]),
				Link: "/tasks", EntityType: "task", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.SMS != nil {
		for _, t := range texts {
			_ = s.SMS.Send(ctx, sender.Message{To: t.phone, Body: t.body})
		}
	}
	return nil
}

// ---------- dashboard ----------

// Dashboard collects what the services team looks at first. Each part checks its approved permission.
func (s *Service) Dashboard(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	out := map[string]any{}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if p.Has("services.project.view_all") || p.Has("services.project.manage") {
			if out["projects_by_stage"], err = s.projectsByStage(ctx, tx, p); err != nil {
				return err
			}
			q := sqlq.New(p.TenantID, today)
			q.Add("pr.tenant_id = $1")
			ownProject(q, p)
			q.Add(`pr.status IN ('not_started','in_progress') AND EXISTS (SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.status <> 'done' AND st.deadline < $2::date)`)
			late, err := rows(ctx, tx, `SELECT count(*) AS late FROM app.projects pr`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			out["late_projects"] = late[0]["late"]
		}
		if p.Has("services.task.view_all") || p.Has("services.task.manage") {
			q := sqlq.New(p.TenantID, today)
			q.Add("t.tenant_id = $1")
			if !p.Has("services.task.view_all") {
				mid := p.MembershipID
				q.Add("(t.assignee_id = ? OR t.created_by = ?)", mid, mid)
			}
			q.Add("t.due_at::date < $2::date AND t.status IN ('open','in_progress')")
			if out["overdue_tasks"], err = rows(ctx, tx, `SELECT `+taskCols("$2::date")+taskFrom+q.Where()+` ORDER BY t.due_at LIMIT 10`, q.Args...); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.tasks t`+q.Where(), q.Args...).Scan(&n); err != nil {
				return err
			}
			out["overdue_task_count"] = n
		}
		if p.Has("services.renewal.manage") {
			if out["renewals_due"], err = rows(ctx, tx, `SELECT `+renewalCols("$2::date")+renewalFrom+` WHERE r.tenant_id = $1 AND r.status = 'upcoming' AND r.due_date <= $2::date + 30 ORDER BY r.due_date LIMIT 10`, p.TenantID, today); err != nil {
				return err
			}
		}
		if p.Has("services.invoice.manage") || p.Has("services.invoice.draft") || p.Has("finance.debt.view") {
			dr, err := rows(ctx, tx, `
				SELECT COALESCE(SUM(balance_due), 0) AS owed_to_us,
				       COALESCE(SUM(balance_due) FILTER (WHERE due_date < $2::date), 0) AS overdue,
				       count(*) FILTER (WHERE due_date < $2::date) AS overdue_invoices
				FROM app.invoices WHERE tenant_id = $1 AND status IN ('issued','partially_paid')`, p.TenantID, today)
			if err != nil {
				return err
			}
			out["debts"] = dr[0]
		}
		if p.Has("services.contractor.manage") {
			var owed decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(balance_due), 0) FROM app.contractor_bills WHERE tenant_id = $1 AND status IN ('open','partially_paid')`, p.TenantID).Scan(&owed); err != nil {
				return err
			}
			out["we_owe_contractors"] = owed
		}
		if p.Has("services.design.view_all") || p.Has("services.design.manage") {
			q := sqlq.New(p.TenantID, today)
			q.Add("j.tenant_id = $1")
			ownJob(q, p)
			mine, err := rows(ctx, tx, `
				SELECT count(*) FILTER (WHERE j.status IN ('new_brief','in_design','internal_review')) AS in_progress,
				       count(*) FILTER (WHERE j.deadline = $2::date AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS due_today,
				       count(*) FILTER (WHERE j.status = 'with_client') AS waiting_client,
				       0::bigint AS changes_requested,
				       count(*) FILTER (WHERE j.deadline < $2::date AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS overdue
				FROM app.design_jobs j`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			out["design"] = mine[0]
			var running *uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT design_job_id FROM app.design_time_logs WHERE tenant_id = $1 AND membership_id = $2 AND ended_at IS NULL`, p.TenantID, p.MembershipID).Scan(&running)
			out["timer_job_id"] = running
		}
		return nil
	})
	return out, err
}

// ---------- reports ----------

func (s *Service) ProjectsReport(ctx context.Context, p *auth.Principal, from, to string) (export.Table, error) {
	var list []map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("pr.tenant_id = $1 AND pr.status <> 'cancelled'")
		q.If(from != "", "pr.start_date >= ?::date", from)
		q.If(to != "", "pr.start_date <= ?::date", to)
		var err error
		list, err = rows(ctx, tx, `SELECT `+projectCols+projectFrom+q.Where()+` ORDER BY pr.start_date DESC, pr.project_no`, q.Args...)
		if err != nil {
			return err
		}
		addBalances(list)
		return nil
	})
	sum := func(k string) decimal.Decimal {
		t := money.Zero
		for _, r := range list {
			t = t.Add(num(r, k))
		}
		return t
	}
	return export.Table{Title: "Projects", Subtitle: rangeText(from, to),
		Columns: []export.Col{{Key: "number", Label: "Project"}, {Key: "client_name", Label: "Client"}, {Key: "status", Label: "Status"}, {Key: "start_date", Label: "Started"},
			{Key: "total_price", Label: "Price"}, {Key: "paid", Label: "Paid"}, {Key: "balance", Label: "Balance"}, {Key: "costs", Label: "Costs"}, {Key: "profit", Label: "Profit"}},
		Rows:   list,
		Totals: map[string]any{"label": "Total", "total_price": sum("total_price"), "paid": sum("paid"), "balance": sum("balance"), "costs": sum("costs"), "profit": sum("profit")}}, err
}

func (s *Service) DesignReport(ctx context.Context, p *auth.Principal, from, to string) (export.Table, error) {
	var list []map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("j.tenant_id = $1 AND j.status <> 'cancelled'")
		q.If(from != "", "j.created_at::date >= ?::date", from)
		q.If(to != "", "j.created_at::date <= ?::date", to)
		var err error
		list, err = rows(ctx, tx, `
			SELECT COALESCE(du.full_name::text, 'Unassigned') AS designer, count(*) AS jobs,
			       count(*) FILTER (WHERE j.status IN ('approved','printing','delivered')) AS approved,
			       CASE WHEN count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL) = 0 THEN NULL
			            ELSE round(100.0 * count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL AND j.approved_at::date <= j.deadline)
			                 / count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL), 1) END AS on_time_pct,
			       round(avg(EXTRACT(EPOCH FROM (j.approved_at - j.created_at)) / 86400) FILTER (WHERE j.approved_at IS NOT NULL)::numeric, 1) AS avg_days_to_approval,
			       round(avg(j.revisions_used)::numeric, 1) AS avg_revisions,
			       COALESCE(SUM((SELECT COALESCE(SUM(i.amount_paid), 0) FROM app.invoices i WHERE i.tenant_id = j.tenant_id AND i.design_job_id = j.id AND i.status <> 'void')), 0) AS income,
			       COALESCE(SUM((SELECT COALESCE(SUM(e.amount), 0) FROM app.expenses e WHERE e.tenant_id = j.tenant_id AND e.design_job_id = j.id AND e.status IN ('approved','paid'))
			                  + (SELECT COALESCE(SUM(b.amount), 0) FROM app.contractor_bills b WHERE b.tenant_id = j.tenant_id AND b.design_job_id = j.id AND b.status <> 'void')), 0) AS costs
			FROM app.design_jobs j
			LEFT JOIN app.memberships dm ON dm.tenant_id = j.tenant_id AND dm.id = j.designer_id
			LEFT JOIN iam.users du ON du.id = dm.user_id`+q.Where()+` GROUP BY du.full_name ORDER BY jobs DESC`, q.Args...)
		if err != nil {
			return err
		}
		for _, r := range list {
			r["profit"] = num(r, "income").Sub(num(r, "costs"))
		}
		return nil
	})
	sum := func(k string) decimal.Decimal {
		t := money.Zero
		for _, r := range list {
			t = t.Add(num(r, k))
		}
		return t
	}
	return export.Table{Title: "Design jobs", Subtitle: rangeText(from, to),
		Columns: []export.Col{{Key: "designer", Label: "Designer"}, {Key: "jobs", Label: "Jobs"}, {Key: "approved", Label: "Approved"}, {Key: "on_time_pct", Label: "On time %"},
			{Key: "avg_days_to_approval", Label: "Days to approval"}, {Key: "avg_revisions", Label: "Revisions per job"}, {Key: "income", Label: "Income"}, {Key: "costs", Label: "Costs"}, {Key: "profit", Label: "Profit"}},
		Rows:   list,
		Totals: map[string]any{"label": "Total", "income": sum("income"), "costs": sum("costs"), "profit": sum("profit")}}, err
}

func rangeText(from, to string) string {
	switch {
	case from != "" && to != "":
		return from + " to " + to
	case from != "":
		return "From " + from
	case to != "":
		return "Until " + to
	}
	return ""
}
