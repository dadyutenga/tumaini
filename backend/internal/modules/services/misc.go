package services

import (
	"context"
	"encoding/json"
	"fmt"

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
	Status string     `json:"status" validate:"omitempty,oneof=missing received"`
	FileID *uuid.UUID `json:"file_id"`
}

const docCols = `d.id, d.client_id, c.name AS client_name, d.project_id, pr.number AS project_number, d.stage_id, d.design_job_id, d.name, d.doc_type, d.status, d.file_id,
	f.original_name AS file_name, f.mime_type, d.received_at, d.created_at`
const docFrom = ` FROM svc_documents d JOIN svc_clients c ON c.id = d.client_id LEFT JOIN svc_projects pr ON pr.id = d.project_id LEFT JOIN files f ON f.id = d.file_id`

func (s *Service) ListDocuments(ctx context.Context, p *auth.Principal, clientID, projectID *uuid.UUID, status, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(clientID != nil, "d.client_id = ?", clientID)
		q.If(projectID != nil, "d.project_id = ?", projectID)
		q.If(status != "", "d.status = ?", status)
		if t := clean(q0); t != "" {
			q.Add("(d.name ILIKE '%' || ? || '%' OR d.doc_type ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", t, t, t)
		}
		if own := scopeOwn(p, "projects.view"); own != nil {
			q.Add("(d.project_id IS NULL OR EXISTS (SELECT 1 FROM svc_projects x WHERE x.id = d.project_id AND (x.owner_id = ? OR x.created_by = ?)))", *own, *own)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+docFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+docCols+docFrom+q.Where()+` ORDER BY c.name, d.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateDocument(ctx context.Context, p *auth.Principal, in DocumentIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "svc_clients", "Client", in.ClientID); err != nil {
			return err
		}
		if in.ProjectID != nil {
			var c uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT client_id FROM svc_projects WHERE id=$1`, *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
				return httpx.NotFound("not_found", "Project not found for this client")
			}
		}
		status := "missing"
		var received any
		if in.FileID != nil {
			if err := require404(ctx, tx, "files", "File", *in.FileID); err != nil {
				return err
			}
			status, received = "received", s.Clock.Now()
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO svc_documents (id, business_id, client_id, project_id, stage_id, design_job_id, name, doc_type, status, file_id, received_at, uploaded_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			id, p.BusinessID, in.ClientID, uuidOrNil(in.ProjectID), uuidOrNil(in.StageID), uuidOrNil(in.DesignJobID), clean(in.Name), nz(in.DocType), status, uuidOrNil(in.FileID), received, p.UserID); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Document", `SELECT `+docCols+docFrom+` WHERE d.id=$1`, id)
		return err
	})
	return out, err
}

// UpdateDocument renames a document, attaches a file or marks it received or missing.
func (s *Service) UpdateDocument(ctx context.Context, p *auth.Principal, id uuid.UUID, in DocumentPatch) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_documents WHERE id=$1 FOR UPDATE`, id).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Document not found")
		}
		if in.FileID != nil {
			if err := require404(ctx, tx, "files", "File", *in.FileID); err != nil {
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
			UPDATE svc_documents SET name=COALESCE(NULLIF($2,''), name), status=$3, file_id=COALESCE($4, file_id), uploaded_by = CASE WHEN $4::uuid IS NULL THEN uploaded_by ELSE $5 END,
			  received_at = CASE WHEN $3 = 'received' THEN COALESCE(received_at, now()) ELSE NULL END
			WHERE id=$1`, id, clean(in.Name), status, uuidOrNil(in.FileID), p.UserID); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Document", `SELECT `+docCols+docFrom+` WHERE d.id=$1`, id)
		return err
	})
	return out, err
}

// ---------- tasks ----------

type TaskIn struct {
	Title       string     `json:"title" validate:"max=200"`
	Description string     `json:"description" validate:"max=2000"`
	ProjectID   *uuid.UUID `json:"project_id"`
	DesignJobID *uuid.UUID `json:"design_job_id"`
	AssigneeID  *uuid.UUID `json:"assignee_id"`
	DueDate     string     `json:"due_date" validate:"omitempty,date"`
	Priority    string     `json:"priority" validate:"omitempty,oneof=normal urgent"`
	Status      string     `json:"status" validate:"omitempty,oneof=todo in_progress done cancelled"`
}

const taskCols = `t.id, t.title, t.description, t.project_id, pr.number AS project_number, t.design_job_id, t.assignee_id, u.name AS assignee_name, t.due_date, t.priority, t.status, t.completed_at, t.created_at,
	(t.due_date IS NOT NULL AND t.due_date < svc_today() AND t.status IN ('todo','in_progress')) AS overdue`
const taskFrom = ` FROM svc_tasks t LEFT JOIN svc_projects pr ON pr.id = t.project_id LEFT JOIN users u ON u.id = t.assignee_id`

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
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(f.Status != "", "t.status = ?", f.Status)
		q.If(f.AssigneeID != nil, "t.assignee_id = ?", f.AssigneeID)
		q.If(f.ProjectID != nil, "t.project_id = ?", f.ProjectID)
		q.If(f.From != "", "t.due_date >= ?::date", f.From)
		q.If(f.To != "", "t.due_date <= ?::date", f.To)
		q.If(f.Overdue, "t.due_date < svc_today() AND t.status IN ('todo','in_progress')")
		q.If(f.Open, "t.status IN ('todo','in_progress')")
		if own := scopeOwn(p, "tasks.view"); own != nil {
			q.Add("(t.assignee_id = ? OR t.created_by = ?)", *own, *own)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+taskFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+taskCols+taskFrom+q.Where()+` ORDER BY (t.status IN ('done','cancelled')), t.due_date NULLS LAST, t.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateTask(ctx context.Context, p *auth.Principal, in TaskIn) (map[string]any, error) {
	if clean(in.Title) == "" {
		return nil, httpx.BadRequest("invalid_body", "Task title is required")
	}
	id := ids.New()
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if in.ProjectID != nil {
			if err := require404(ctx, tx, "svc_projects", "Project", *in.ProjectID); err != nil {
				return err
			}
		}
		if in.DesignJobID != nil {
			if err := require404(ctx, tx, "design_jobs", "Design job", *in.DesignJobID); err != nil {
				return err
			}
		}
		prio := in.Priority
		if prio == "" {
			prio = "normal"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO svc_tasks (id, business_id, title, description, project_id, design_job_id, assignee_id, due_date, priority, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			id, p.BusinessID, clean(in.Title), nz(in.Description), uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), uuidOrNil(in.AssigneeID), dateOrNil(in.DueDate), prio, p.UserID); err != nil {
			return err
		}
		if in.AssigneeID != nil && *in.AssigneeID != p.UserID {
			eid := id
			if err := s.Notify.ToUser(ctx, tx, p.BusinessID, *in.AssigneeID, notify.Message{Type: "task_assigned", Title: "New task", Body: clean(in.Title), Link: "/tasks", EntityType: "svc_task", EntityID: &eid}); err != nil {
				return err
			}
		}
		var err error
		out, err = one(ctx, tx, "Task", `SELECT `+taskCols+taskFrom+` WHERE t.id=$1`, id)
		return err
	})
	return out, err
}

// UpdateTask edits a task. A person limited to own tasks may only change the status of a task assigned to them.
func (s *Service) UpdateTask(ctx context.Context, p *auth.Principal, id uuid.UUID, in TaskIn, statusOnly bool) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var assignee *uuid.UUID
		var cur string
		if err := tx.QueryRow(ctx, `SELECT assignee_id, status FROM svc_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&assignee, &cur); err != nil {
			return httpx.NotFound("not_found", "Task not found")
		}
		if statusOnly {
			if assignee == nil || *assignee != p.UserID {
				return httpx.NotFound("not_found", "Task not found")
			}
			in = TaskIn{Status: in.Status}
		}
		status := in.Status
		if status == "" {
			status = cur
		}
		if _, err := tx.Exec(ctx, `
			UPDATE svc_tasks SET title=COALESCE(NULLIF($2,''), title), description=COALESCE(NULLIF($3,''), description), assignee_id=COALESCE($4, assignee_id),
			  due_date=COALESCE(NULLIF($5,'')::date, due_date), priority=COALESCE(NULLIF($6,''), priority), status=$7,
			  completed_at = CASE WHEN $7 = 'done' THEN COALESCE(completed_at, now()) ELSE NULL END
			WHERE id=$1`, id, clean(in.Title), clean(in.Description), uuidOrNil(in.AssigneeID), in.DueDate, in.Priority, status); err != nil {
			return err
		}
		if in.AssigneeID != nil && (assignee == nil || *assignee != *in.AssigneeID) && *in.AssigneeID != p.UserID {
			eid := id
			if err := s.Notify.ToUser(ctx, tx, p.BusinessID, *in.AssigneeID, notify.Message{Type: "task_assigned", Title: "New task", Body: clean(in.Title), Link: "/tasks", EntityType: "svc_task", EntityID: &eid}); err != nil {
				return err
			}
		}
		var err error
		out, err = one(ctx, tx, "Task", `SELECT `+taskCols+taskFrom+` WHERE t.id=$1`, id)
		return err
	})
	return out, err
}

// ---------- renewals ----------

type RenewalIn struct {
	ClientID  uuid.UUID        `json:"client_id" validate:"required"`
	ProjectID *uuid.UUID       `json:"project_id"`
	Item      string           `json:"item" validate:"required,max=200"`
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

const renewalCols = `r.id, r.client_id, c.name AS client_name, c.phone AS client_phone, r.project_id, r.item, r.due_date, r.amount, r.status, r.renewed_at, r.next_id, r.notes,
	(r.due_date - svc_today()) AS days_left, r.reminded_30_at, r.reminded_7_at, r.created_at`
const renewalFrom = ` FROM svc_renewals r JOIN svc_clients c ON c.id = r.client_id`

func (s *Service) ListRenewals(ctx context.Context, p *auth.Principal, status string, clientID *uuid.UUID, within int, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "r.status = ?", status)
		q.If(clientID != nil, "r.client_id = ?", clientID)
		q.If(within > 0, "r.status = 'pending' AND r.due_date <= svc_today() + ?::int", within)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+renewalFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+renewalCols+renewalFrom+q.Where()+` ORDER BY (r.status <> 'pending'), r.due_date, r.created_at`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateRenewal(ctx context.Context, p *auth.Principal, in RenewalIn) (map[string]any, error) {
	id := ids.New()
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "svc_clients", "Client", in.ClientID); err != nil {
			return err
		}
		amount := money.Zero
		if in.Amount != nil {
			amount = money.Money(*in.Amount)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO svc_renewals (id, business_id, client_id, project_id, item, due_date, amount, notes, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			id, p.BusinessID, in.ClientID, uuidOrNil(in.ProjectID), clean(in.Item), in.DueDate, amount, nz(in.Notes), p.UserID); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols+renewalFrom+` WHERE r.id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateRenewal(ctx context.Context, p *auth.Principal, id uuid.UUID, in RenewalPatch) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_renewals WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
			return httpx.NotFound("not_found", "Renewal not found")
		}
		if status != "pending" {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be edited")
		}
		var amount any
		if in.Amount != nil {
			amount = money.Money(*in.Amount)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE svc_renewals SET item=COALESCE(NULLIF($2,''), item), due_date=COALESCE(NULLIF($3,'')::date, due_date), amount=COALESCE($4, amount), notes=COALESCE($5, notes),
			  reminded_30_at = CASE WHEN NULLIF($3,'') IS NOT NULL THEN NULL ELSE reminded_30_at END, reminded_7_at = CASE WHEN NULLIF($3,'') IS NOT NULL THEN NULL ELSE reminded_7_at END
			WHERE id=$1`, id, clean(in.Item), in.DueDate, amount, in.Notes); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols+renewalFrom+` WHERE r.id=$1`, id)
		return err
	})
	return out, err
}

// Renew marks a renewal done and, when a next date is given, schedules the next one.
func (s *Service) Renew(ctx context.Context, p *auth.Principal, id uuid.UUID, in RenewIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status, item string
		var client uuid.UUID
		var project *uuid.UUID
		var amount decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT status, client_id, project_id, item, amount FROM svc_renewals WHERE id=$1 FOR UPDATE`, id).Scan(&status, &client, &project, &item, &amount); err != nil {
			return httpx.NotFound("not_found", "Renewal not found")
		}
		if status != "pending" {
			return httpx.Conflict("invalid_status", "This renewal is already "+status)
		}
		var next any
		if in.NextDueDate != "" {
			if in.Amount != nil {
				amount = money.Money(*in.Amount)
			}
			nid := ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO svc_renewals (id, business_id, client_id, project_id, item, due_date, amount, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				nid, p.BusinessID, client, uuidOrNil(project), item, in.NextDueDate, amount, p.UserID); err != nil {
				return err
			}
			next = nid
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_renewals SET status='renewed', renewed_at=now(), next_id=$2 WHERE id=$1`, id, next); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols+renewalFrom+` WHERE r.id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) CancelRenewal(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE svc_renewals SET status='cancelled' WHERE id=$1 AND status='pending'`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be cancelled")
		}
		out, err = one(ctx, tx, "Renewal", `SELECT `+renewalCols+renewalFrom+` WHERE r.id=$1`, id)
		return err
	})
	return out, err
}

// RemindClient sends the renewal reminder to the client now.
func (s *Service) RemindClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var phone, item, due string
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		r, err := one(ctx, tx, "Renewal", `SELECT `+renewalCols+renewalFrom+` WHERE r.id=$1`, id)
		if err != nil {
			return err
		}
		if r["status"] != "pending" {
			return httpx.Conflict("invalid_status", "Only a pending renewal can be reminded")
		}
		phone, _ = r["client_phone"].(string)
		item, _ = r["item"].(string)
		due, _ = r["due_date"].(string)
		return nil
	})
	if err != nil {
		return nil, err
	}
	delivered := false
	if s.SMS != nil && phone != "" {
		delivered = s.SMS.Send(ctx, sender.Message{To: phone, Body: fmt.Sprintf("Reminder: %s is due on %s.", item, due)}) == nil
	}
	return map[string]any{"delivered": delivered, "has_phone": phone != ""}, nil
}

// ---------- daily reminders (job) ----------

// RunReminders sends renewal reminders at 30 and 7 days, warns about design deadlines in 2 days
// and about tasks due today. It is safe to run twice: each reminder is stamped.
func (s *Service) RunReminders(ctx context.Context, businessID uuid.UUID, _ json.RawMessage) error {
	type sms struct{ phone, body string }
	var texts []sms
	err := s.tx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		list, err := rows(ctx, tx, `
			SELECT r.id, r.item, r.due_date, c.name AS client_name, c.phone AS client_phone, (r.due_date - $1::date) AS days,
			       (r.reminded_30_at IS NULL) AS need30, (r.reminded_7_at IS NULL) AS need7
			FROM svc_renewals r JOIN svc_clients c ON c.id = r.client_id
			WHERE r.status = 'pending' AND r.due_date - $1::date <= 30 FOR UPDATE OF r`, date)
		if err != nil {
			return err
		}
		for _, r := range list {
			days, _ := r["days"].(int32)
			id := r["id"].(uuid.UUID)
			var stamp string
			switch {
			case days <= 7 && r["need7"] == true:
				stamp = `reminded_7_at = now(), reminded_30_at = COALESCE(reminded_30_at, now())`
			case days > 7 && days <= 30 && r["need30"] == true:
				stamp = `reminded_30_at = now()`
			default:
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE svc_renewals SET `+stamp+` WHERE id=$1`, id); err != nil {
				return err
			}
			eid := id
			body := fmt.Sprintf("%s for %s is due on %v (%d days)", r["item"], r["client_name"], r["due_date"], days)
			if err := s.Notify.ToPermission(ctx, tx, businessID, nil, "renewals.manage", notify.Message{Type: "renewal_due", Title: "Renewal due", Body: body, Link: "/renewals", EntityType: "svc_renewal", EntityID: &eid}); err != nil {
				return err
			}
			if ph, _ := r["client_phone"].(string); ph != "" {
				texts = append(texts, sms{ph, fmt.Sprintf("Reminder: %s is due on %v.", r["item"], r["due_date"])})
			}
		}
		jobs, err := rows(ctx, tx, `SELECT id, number, title, designer_id FROM design_jobs WHERE status IN ('new_brief','in_design','internal_review','changes_requested') AND deadline = $1::date + 2 AND designer_id IS NOT NULL`, date)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			id := j["id"].(uuid.UUID)
			if err := s.Notify.ToUser(ctx, tx, businessID, j["designer_id"].(uuid.UUID), notify.Message{Type: "design_deadline", Title: "Design deadline in 2 days", Body: fmt.Sprintf("%s: %s", j["number"], j["title"]), Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &id}); err != nil {
				return err
			}
		}
		tasks, err := rows(ctx, tx, `SELECT id, title, assignee_id FROM svc_tasks WHERE status IN ('todo','in_progress') AND due_date = $1::date AND assignee_id IS NOT NULL`, date)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			id := t["id"].(uuid.UUID)
			if err := s.Notify.ToUser(ctx, tx, businessID, t["assignee_id"].(uuid.UUID), notify.Message{Type: "task_due", Title: "Task due today", Body: fmt.Sprint(t["title"]), Link: "/tasks", EntityType: "svc_task", EntityID: &id}); err != nil {
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

// Dashboard collects what the services team looks at first. Each part needs its own permission.
func (s *Service) Dashboard(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	has := func(k string) bool { _, ok := p.Can(k); return ok || p.IsOwner }
	out := map[string]any{}
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if has("projects.view") {
			if out["projects_by_stage"], err = s.projectsByStage(ctx, tx, p); err != nil {
				return err
			}
			q := sqlq.New()
			ownProject(q, scopeOwn(p, "projects.view"))
			q.Add(`pr.status = 'in_progress' AND EXISTS (SELECT 1 FROM svc_project_stages st WHERE st.project_id = pr.id AND st.status <> 'done' AND st.deadline < svc_today())`)
			late, err := rows(ctx, tx, `SELECT count(*) AS late FROM svc_projects pr`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			out["late_projects"] = late[0]["late"]
		}
		if has("tasks.view") {
			q := sqlq.New()
			if own := scopeOwn(p, "tasks.view"); own != nil {
				q.Add("(t.assignee_id = ? OR t.created_by = ?)", *own, *own)
			}
			q.Add("t.due_date < svc_today() AND t.status IN ('todo','in_progress')")
			if out["overdue_tasks"], err = rows(ctx, tx, `SELECT `+taskCols+taskFrom+q.Where()+` ORDER BY t.due_date LIMIT 10`, q.Args...); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_tasks t`+q.Where(), q.Args...).Scan(&n); err != nil {
				return err
			}
			out["overdue_task_count"] = n
		}
		if has("renewals.view") {
			if out["renewals_due"], err = rows(ctx, tx, `SELECT `+renewalCols+renewalFrom+` WHERE r.status='pending' AND r.due_date <= svc_today() + 30 ORDER BY r.due_date LIMIT 10`); err != nil {
				return err
			}
		}
		if has("service_billing.view") {
			dr, err := rows(ctx, tx, `
				SELECT COALESCE(SUM(balance),0) AS owed_to_us, COALESCE(SUM(balance) FILTER (WHERE due_date < svc_today()),0) AS overdue, count(*) FILTER (WHERE due_date < svc_today()) AS overdue_invoices
				FROM svc_invoices WHERE status IN ('issued','partial')`)
			if err != nil {
				return err
			}
			out["debts"] = dr[0]
		}
		if has("contractors.view") {
			var owed decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount - settled),0) FROM svc_costs WHERE status='owed'`).Scan(&owed); err != nil {
				return err
			}
			out["we_owe_contractors"] = owed
		}
		if has("design.view") {
			q := sqlq.New()
			ownJob(q, scopeOwn(p, "design.view"))
			mine, err := rows(ctx, tx, `
				SELECT count(*) FILTER (WHERE j.status IN ('new_brief','in_design','internal_review','changes_requested')) AS in_progress,
				       count(*) FILTER (WHERE j.deadline = svc_today() AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS due_today,
				       count(*) FILTER (WHERE j.status = 'with_client') AS waiting_client,
				       count(*) FILTER (WHERE j.status = 'changes_requested') AS changes_requested,
				       count(*) FILTER (WHERE j.deadline < svc_today() AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS overdue
				FROM design_jobs j`+q.Where(), q.Args...)
			if err != nil {
				return err
			}
			out["design"] = mine[0]
			var running *uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT job_id FROM design_time_entries WHERE user_id=$1 AND stopped_at IS NULL`, p.UserID).Scan(&running)
			out["timer_job_id"] = running
		}
		return nil
	})
	return out, err
}

// ---------- reports ----------

func (s *Service) ProjectsReport(ctx context.Context, p *auth.Principal, from, to string) (export.Table, error) {
	var list []map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(from != "", "pr.start_date >= ?::date", from)
		q.If(to != "", "pr.start_date <= ?::date", to)
		q.Add("pr.status <> 'cancelled'")
		var err error
		list, err = rows(ctx, tx, `SELECT `+projectCols+projectFrom+q.Where()+` ORDER BY pr.start_date DESC, pr.number`, q.Args...)
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
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(from != "", "j.created_at::date >= ?::date", from)
		q.If(to != "", "j.created_at::date <= ?::date", to)
		q.Add("j.status <> 'cancelled'")
		var err error
		list, err = rows(ctx, tx, `
			SELECT COALESCE(u.name, 'Unassigned') AS designer, count(*) AS jobs,
			       count(*) FILTER (WHERE j.status IN ('approved','printing','delivered')) AS approved,
			       CASE WHEN count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL) = 0 THEN NULL
			            ELSE round(100.0 * count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL AND j.approved_at::date <= j.deadline)
			                 / count(*) FILTER (WHERE j.approved_at IS NOT NULL AND j.deadline IS NOT NULL), 1) END AS on_time_pct,
			       round(avg(EXTRACT(EPOCH FROM (j.approved_at - j.created_at)) / 86400) FILTER (WHERE j.approved_at IS NOT NULL)::numeric, 1) AS avg_days_to_approval,
			       round(avg(j.revisions_used)::numeric, 1) AS avg_revisions,
			       COALESCE(SUM((SELECT SUM(sp.amount) FROM svc_payments sp WHERE sp.design_job_id = j.id AND sp.status = 'posted')), 0) AS income,
			       COALESCE(SUM((SELECT SUM(k.amount) FROM svc_costs k WHERE k.design_job_id = j.id AND k.status <> 'voided')), 0) AS costs
			FROM design_jobs j LEFT JOIN users u ON u.id = j.designer_id`+q.Where()+` GROUP BY u.name ORDER BY jobs DESC`, q.Args...)
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
