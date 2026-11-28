package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

var designStatuses = []string{"new_brief", "in_design", "internal_review", "with_client", "approved", "printing", "delivered"}

func mapDesignJobType(t string) string {
	if t == "social_media" {
		return "social_media_post"
	}
	return t
}

func mapPrintOrderStatus(s string) string {
	if s == "sent" {
		return "sent_to_print"
	}
	return s
}

func httpsOrNil(link string) any {
	s := clean(link)
	if strings.HasPrefix(s, "https://") && len(s) >= 11 && len(s) <= 2008 {
		return s
	}
	return nil
}

type DesignJobIn struct {
	ClientID          uuid.UUID        `json:"client_id" validate:"required"`
	ProjectID         *uuid.UUID       `json:"project_id"`
	StageID           *uuid.UUID       `json:"stage_id"`
	ServiceID         *uuid.UUID       `json:"service_id"`
	JobType           string           `json:"job_type" validate:"required,oneof=logo business_card poster flyer banner signage social_media social_media_post packaging_label branding_package other"`
	Title             string           `json:"title" validate:"required,max=200"`
	Brief             string           `json:"brief" validate:"max=10000"`
	SizeFormat        string           `json:"size_format" validate:"max=100"`
	StyleNotes        string           `json:"style_notes" validate:"max=4000"`
	TextToInclude     string           `json:"text_to_include" validate:"max=4000"`
	Deadline          string           `json:"deadline" validate:"omitempty,date"`
	Priority          string           `json:"priority" validate:"omitempty,oneof=normal high urgent"`
	DesignerID        *uuid.UUID       `json:"designer_id"`
	Price             *decimal.Decimal `json:"price" validate:"omitempty,dgte0"`
	Deposit           decimal.Decimal  `json:"deposit" validate:"dgte0"`
	RevisionsIncluded *int             `json:"revisions_included" validate:"omitempty,gte=0,lte=50"`
	PrintingNeeded    bool             `json:"printing_needed"`
	ReferenceFileIDs  []uuid.UUID      `json:"reference_file_ids"`
}

type DesignJobPatch struct {
	Title             string           `json:"title" validate:"max=200"`
	Brief             *string          `json:"brief" validate:"omitempty,max=10000"`
	SizeFormat        *string          `json:"size_format" validate:"omitempty,max=100"`
	StyleNotes        *string          `json:"style_notes" validate:"omitempty,max=4000"`
	TextToInclude     *string          `json:"text_to_include" validate:"omitempty,max=4000"`
	Deadline          string           `json:"deadline" validate:"omitempty,date"`
	Priority          string           `json:"priority" validate:"omitempty,oneof=normal high urgent"`
	DesignerID        *uuid.UUID       `json:"designer_id"`
	Price             *decimal.Decimal `json:"price" validate:"omitempty,dgte0"`
	Deposit           *decimal.Decimal `json:"deposit" validate:"omitempty,dgte0"`
	RevisionsIncluded *int             `json:"revisions_included" validate:"omitempty,gte=0,lte=50"`
	PrintingNeeded    *bool            `json:"printing_needed"`
}

func jobCols(todayExpr string) string {
	return `j.id, j.job_no AS number, j.title::text AS title,
	CASE WHEN j.job_type = 'social_media_post' THEN 'social_media' ELSE j.job_type::text END AS job_type,
	j.status::text AS status, j.priority::text AS priority, j.deadline,
	j.customer_id AS client_id, c.full_name::text AS client_name,
	j.project_id, pr.project_no AS project_number, j.project_stage_id AS stage_id,
	dm.user_id AS designer_id, du.full_name::text AS designer_name,
	j.price, 0::numeric AS deposit, j.revisions_included, j.revisions_used, j.extra_revision_fee,
	j.printing_needed, j.final_version_id, j.created_at, j.approved_at, j.delivered_at,
	(j.deadline IS NOT NULL AND j.deadline < ` + todayExpr + ` AND j.status NOT IN ('approved','printing','delivered','cancelled')) AS overdue,
	(COALESCE((SELECT SUM(i.amount_paid) FROM app.invoices i WHERE i.design_job_id = j.id AND i.status <> 'void'), 0)
	 + COALESCE((SELECT SUM(pay.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pay.id), 0))
	             FROM app.payments_received pay WHERE pay.customer_id = j.customer_id AND pay.status = 'posted' AND pay.notes LIKE '%[design_job:' || j.id::text || ']%'), 0)) AS paid,
	(COALESCE((SELECT SUM(e.amount) FROM app.expenses e WHERE e.design_job_id = j.id AND e.status IN ('approved','paid')), 0)
	 + COALESCE((SELECT SUM(b.amount) FROM app.contractor_bills b WHERE b.design_job_id = j.id AND b.status <> 'void'), 0)) AS costs`
}

const jobFrom = ` FROM app.design_jobs j
	JOIN app.customers c ON c.id = j.customer_id
	LEFT JOIN app.projects pr ON pr.id = j.project_id
	LEFT JOIN app.memberships dm ON dm.id = j.designer_id
	LEFT JOIN iam.users du ON du.id = dm.user_id`

func ownJob(q *sqlq.Q, p *auth.Principal) {
	if !p.Has("services.design.view_all") {
		mid := p.MembershipID
		q.Add("(j.designer_id = ? OR j.created_by = ?)", mid, mid)
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func jobMoney(list []map[string]any) {
	for _, r := range list {
		r["balance"] = num(r, "price").Sub(num(r, "paid"))
		r["profit"] = num(r, "paid").Sub(num(r, "costs"))
		r["revisions_over"] = asInt(r["revisions_used"]) > asInt(r["revisions_included"])
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := s.jobQuery(ctx, tx, p, f, today)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+jobFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		out, err = rows(ctx, tx, `SELECT `+jobCols("$1::date")+jobFrom+q.Where()+` ORDER BY j.deadline NULLS LAST, j.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err == nil {
			jobMoney(out)
		}
		return err
	})
	return out, total, err
}

func (s *Service) jobQuery(ctx context.Context, tx pgx.Tx, p *auth.Principal, f JobFilter, today string) *sqlq.Q {
	q := sqlq.New(today)
	// Every query built from this binds today as $1, so one condition must always use it.
	q.Add("$1::date IS NOT NULL")
	if t := clean(f.Q); t != "" {
		q.Add("(j.title ILIKE '%' || ? || '%' OR j.job_no ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%')", t, t, t)
	}
	if f.Status != "" {
		st := f.Status
		if st == "changes_requested" {
			st = "in_design"
		}
		q.Add("j.status = ?::app.design_job_status", st)
	}
	if f.DesignerID != nil {
		if mid, err := s.resolveMembership(ctx, tx, f.DesignerID); err == nil && mid != nil {
			q.Add("j.designer_id = ?", *mid)
		} else {
			q.Add("j.designer_id = ?", *f.DesignerID)
		}
	}
	q.If(f.ClientID != nil, "j.customer_id = ?", f.ClientID)
	q.If(f.ProjectID != nil, "j.project_id = ?", f.ProjectID)
	q.If(f.Overdue, "j.deadline < $1::date AND j.status NOT IN ('approved','printing','delivered','cancelled')")
	ownJob(q, p)
	return q
}

// JobBoard returns one column per status, in workflow order.
func (s *Service) JobBoard(ctx context.Context, p *auth.Principal, designerID *uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := s.jobQuery(ctx, tx, p, JobFilter{DesignerID: designerID}, today)
		q.Add("j.status <> 'cancelled'")
		list, err := rows(ctx, tx, `SELECT `+jobCols("$1::date")+jobFrom+q.Where()+` ORDER BY j.deadline NULLS LAST, j.created_at`, q.Args...)
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
			st, _ := r["status"].(string)
			if i, ok := idx[st]; ok {
				out[i]["jobs"] = append(out[i]["jobs"].([]map[string]any), r)
			}
		}
		return nil
	})
	return out, err
}

func (s *Service) jobRow(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, lock bool) (map[string]any, error) {
	today, err := s.bizDate(ctx, tx)
	if err != nil {
		return nil, err
	}
	q := sqlq.New(today, id)
	q.Add("j.id = $2")
	ownJob(q, p)
	sql := `SELECT ` + jobCols("$1::date") + `, j.brief, j.size_format, j.style_notes, j.text_to_include, j.service_id` + jobFrom + q.Where()
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		versions, err := rows(ctx, tx, `
			SELECT v.id, v.version_no, 'design'::text AS kind, v.preview_file_id AS file_id,
			       f.original_name::text AS file_name, f.mime_type, v.working_file_url::text AS link, NULL::text AS notes,
			       CASE WHEN v.status = 'sent_to_client' THEN 'sent' ELSE v.status::text END AS status,
			       (j.final_version_id = v.id) AS is_final,
			       (SELECT l.expires_at FROM app.design_approval_links l WHERE l.design_version_id = v.id AND l.revoked_at IS NULL ORDER BY l.created_at DESC LIMIT 1) AS token_expires_at,
			       v.decided_at, v.created_at, u.full_name::text AS uploaded_by
			FROM app.design_versions v
			JOIN app.design_jobs j ON j.id = v.design_job_id
			LEFT JOIN app.files f ON f.id = v.preview_file_id
			LEFT JOIN app.memberships m ON m.id = v.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE v.design_job_id = $1
			ORDER BY v.version_no DESC, v.created_at DESC`, id)
		if err != nil {
			return err
		}
		assets, err := rows(ctx, tx, `
			SELECT a.id, 0 AS version_no,
			       CASE WHEN a.asset_kind IN ('print_pdf','png') THEN 'print_ready' ELSE 'source' END AS kind,
			       a.file_id, f.original_name::text AS file_name, f.mime_type, a.url::text AS link, NULL::text AS notes,
			       'approved'::text AS status, false AS is_final, NULL::timestamptz AS token_expires_at, NULL::timestamptz AS decided_at,
			       a.created_at, u.full_name::text AS uploaded_by
			FROM app.design_final_assets a
			JOIN app.design_versions v ON v.id = a.design_version_id
			LEFT JOIN app.files f ON f.id = a.file_id
			LEFT JOIN app.memberships m ON m.id = a.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE v.design_job_id = $1
			ORDER BY a.created_at DESC`, id)
		if err != nil {
			return err
		}
		j["versions"] = append(versions, assets...)

		if j["feedback"], err = rows(ctx, tx, `
			SELECT fb.id, fb.design_version_id AS version_id, v.version_no, fb.author_type::text AS author_type,
			       COALESCE(u.full_name::text, 'Client') AS author, fb.comment, fb.created_at
			FROM app.design_feedback fb
			LEFT JOIN app.design_versions v ON v.id = fb.design_version_id
			LEFT JOIN app.memberships m ON m.id = fb.author_id
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE fb.design_job_id = $1 ORDER BY fb.created_at`, id); err != nil {
			return err
		}
		if j["references"], err = rows(ctx, tx, `
			SELECT a.id, a.file_id, f.original_name::text AS file_name, f.mime_type, a.caption AS note
			FROM app.attachments a
			JOIN app.files f ON f.id = a.file_id
			WHERE a.design_job_id = $1 ORDER BY a.created_at`, id); err != nil {
			return err
		}
		if j["costs_list"], err = rows(ctx, tx, costUnionSQL+` WHERE k.design_job_id = $1 ORDER BY k.date, k.created_at`, id); err != nil {
			return err
		}
		if j["payments"], err = rows(ctx, tx, `
			SELECT DISTINCT pr.id, pr.receipt_no AS number, pr.business_date AS date, pr.amount,
			       COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS applied,
			       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
			       pr.status::text AS status, pr.created_at
			FROM app.payments_received pr
			LEFT JOIN app.payment_allocations a ON a.payment_received_id = pr.id
			LEFT JOIN app.invoices i ON i.id = a.invoice_id
			WHERE (i.design_job_id = $1 OR pr.notes LIKE '%[design_job:' || $1::text || ']%')
			ORDER BY pr.business_date, pr.created_at`, id); err != nil {
			return err
		}
		if j["print_orders"], err = rows(ctx, tx, `
			SELECT o.id, o.contractor_id, ct.name::text AS contractor_name, o.quantity, o.cost, o.due_date,
			       CASE WHEN o.status = 'sent_to_print' THEN 'sent' ELSE o.status::text END AS status,
			       o.notes AS note, o.created_at
			FROM app.print_orders o
			LEFT JOIN app.contractors ct ON ct.id = o.contractor_id
			WHERE o.design_job_id = $1 ORDER BY o.created_at`, id); err != nil {
			return err
		}
		if j["time"], err = rows(ctx, tx, `
			SELECT t.id, m.user_id, u.full_name::text AS user_name, t.started_at, t.ended_at AS stopped_at,
			       CASE WHEN t.ended_at IS NOT NULL THEN GREATEST(1, ROUND(EXTRACT(EPOCH FROM (t.ended_at - t.started_at)) / 60)::int) END AS minutes
			FROM app.design_time_logs t
			JOIN app.memberships m ON m.id = t.membership_id
			JOIN iam.users u ON u.id = m.user_id
			WHERE t.design_job_id = $1 ORDER BY t.started_at DESC`, id); err != nil {
			return err
		}
		if j["invoices"], err = rows(ctx, tx, `
			SELECT id, invoice_no AS number,
			       CASE WHEN status = 'partially_paid' THEN 'partial' ELSE status::text END AS status,
			       total, amount_paid AS paid_total, balance_due AS balance
			FROM app.invoices WHERE design_job_id = $1 ORDER BY created_at`, id); err != nil {
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
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
	if err := require404(ctx, tx, "app.customers", "Client", in.ClientID); err != nil {
		return id, err
	}
	if in.ProjectID != nil {
		var c uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT customer_id FROM app.projects WHERE id = $1", *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
			return id, httpx.NotFound("not_found", "Project not found for this client")
		}
	}
	if in.StageID != nil {
		if in.ProjectID == nil {
			return id, httpx.BadRequest("invalid_body", "A stage link needs a project")
		}
		if err := require404(ctx, tx, "app.project_stages", "Stage", *in.StageID); err != nil {
			return id, err
		}
	}
	price := money.Zero
	revisions := 3
	if in.ServiceID != nil {
		var std decimal.Decimal
		var rev int
		if err := tx.QueryRow(ctx, "SELECT standard_price, revisions_included FROM app.services WHERE id = $1 AND status = 'active'", *in.ServiceID).Scan(&std, &rev); err != nil {
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
	brief := clean(in.Brief)
	if brief == "" {
		brief = clean(in.Title)
	}
	designerMid, err := s.resolveMembership(ctx, tx, in.DesignerID)
	if err != nil {
		return id, err
	}
	buID, err := s.servicesBU(ctx, tx)
	if err != nil {
		return id, err
	}
	number, err := sequence.Next(ctx, tx, uuid.Nil, "design_job")
	if err != nil {
		return id, err
	}
	if _, err := tx.Exec(ctx, "\n\t\tINSERT INTO app.design_jobs (id, job_no, customer_id, business_unit_id, project_id, project_stage_id, service_id, job_type, title, brief, size_format, style_notes, text_to_include, deadline, priority, designer_id, price, revisions_included, printing_needed)\n\t\tVALUES ($1, $2, $3, $4, $5, $6, $7, $8::app.design_job_type, $9, $10, $11, $12, $13, $14, $15::app.priority, $16, $17, $18, $19)",
		id, number, in.ClientID, buID, uuidOrNil(in.ProjectID), uuidOrNil(in.StageID), uuidOrNil(in.ServiceID),
		mapDesignJobType(in.JobType), clean(in.Title), brief, nz(in.SizeFormat), nz(in.StyleNotes), nz(in.TextToInclude),
		dateOrNil(in.Deadline), prio, uuidOrNil(designerMid), price, revisions, in.PrintingNeeded); err != nil {
		return id, err
	}
	for _, f := range in.ReferenceFileIDs {
		if err := require404(ctx, tx, "app.files", "Reference file", f); err != nil {
			return id, err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO app.attachments (id, file_id, design_job_id) VALUES ($1, $2, $3)", ids.New(), f, id); err != nil {
			return id, err
		}
	}
	if designerMid != nil && *designerMid != p.MembershipID {
		uid := s.membershipUserID(ctx, tx, *designerMid)
		if uid != uuid.Nil {
			eid := id
			_ = s.Notify.ToUser(ctx, tx, uid, notify.Message{
				Type: "design_brief_assigned", Title: "New design brief",
				Body: number + ": " + clean(in.Title), Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &eid,
			})
		}
	}
	return id, s.Audit.Record(ctx, tx, p, "design_job.created", "design_job", id, nil, map[string]any{"number": number, "title": in.Title, "price": price})
}

func (s *Service) UpdateJob(ctx context.Context, p *auth.Principal, id uuid.UUID, in DesignJobPatch) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.jobRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		if before["status"] == "cancelled" || before["status"] == "delivered" {
			return httpx.Conflict("invalid_status", "A "+before["status"].(string)+" job cannot be edited")
		}
		var price any
		if in.Price != nil {
			price = money.Money(*in.Price)
		}
		var brief any
		if in.Brief != nil && clean(*in.Brief) != "" {
			brief = clean(*in.Brief)
		}
		designerMid, err := s.resolveMembership(ctx, tx, in.DesignerID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.design_jobs SET\n\t\t\t  title = COALESCE(NULLIF($2,''), title),\n\t\t\t  brief = COALESCE($3, brief),\n\t\t\t  size_format = COALESCE($4, size_format),\n\t\t\t  style_notes = COALESCE($5, style_notes),\n\t\t\t  text_to_include = COALESCE($6, text_to_include),\n\t\t\t  deadline = COALESCE(NULLIF($7,'')::date, deadline),\n\t\t\t  priority = COALESCE(NULLIF($8,'')::app.priority, priority),\n\t\t\t  designer_id = COALESCE($9, designer_id),\n\t\t\t  price = COALESCE($10, price),\n\t\t\t  revisions_included = COALESCE($11, revisions_included),\n\t\t\t  printing_needed = COALESCE($12, printing_needed)\n\t\t\tWHERE id = $1", id, clean(in.Title), brief, in.SizeFormat, in.StyleNotes, in.TextToInclude,
			in.Deadline, in.Priority, uuidOrNil(designerMid), price, in.RevisionsIncluded, in.PrintingNeeded); err != nil {
			return err
		}
		if designerMid != nil && *designerMid != p.MembershipID {
			uid := s.membershipUserID(ctx, tx, *designerMid)
			if uid != uuid.Nil && before["designer_id"] != uid {
				eid := id
				_ = s.Notify.ToUser(ctx, tx, uid, notify.Message{
					Type: "design_brief_assigned", Title: "New design brief",
					Body: before["number"].(string) + ": " + before["title"].(string), Link: "/design-jobs/" + id.String(), EntityType: "design_job", EntityID: &eid,
				})
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
// Approving or requesting changes drives app.design_versions so DB triggers maintain final_version_id and revisions_used.
func (s *Service) MoveJob(ctx context.Context, p *auth.Principal, id uuid.UUID, to, note string) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		var latestVer *uuid.UUID
		_ = tx.QueryRow(ctx, "SELECT id FROM app.design_versions WHERE design_job_id = $1 ORDER BY version_no DESC LIMIT 1", id).Scan(&latestVer)

		switch to {
		case "approved":
			if latestVer == nil {
				return httpx.Conflict("invalid_status", "Upload a design version before approving")
			}
			if _, err := tx.Exec(ctx, "UPDATE app.design_versions SET status = 'approved' WHERE id = $1", *latestVer); err != nil {
				return err
			}
		case "changes_requested":
			if latestVer == nil {
				return httpx.Conflict("invalid_status", "Upload a design version first")
			}
			// Must be sent_to_client to transition to changes_requested; otherwise move job directly to in_design.
			var vstat string
			_ = tx.QueryRow(ctx, "SELECT status::text FROM app.design_versions WHERE id = $1", *latestVer).Scan(&vstat)
			if vstat == "sent_to_client" {
				if _, err := tx.Exec(ctx, "UPDATE app.design_versions SET status = 'changes_requested' WHERE id = $1", *latestVer); err != nil {
					return err
				}
			} else {
				if _, err := tx.Exec(ctx, "UPDATE app.design_jobs SET status = 'in_design', designer_id = COALESCE(designer_id, $2) WHERE id = $1", id, p.MembershipID); err != nil {
					return err
				}
			}
		case "in_design", "internal_review", "with_client":
			if _, err := tx.Exec(ctx, "UPDATE app.design_jobs SET status = $2::app.design_job_status, designer_id = COALESCE(designer_id, $3) WHERE id = $1", id, to, p.MembershipID); err != nil {
				return err
			}
		default:
			if _, err := tx.Exec(ctx, "UPDATE app.design_jobs SET status = $2::app.design_job_status WHERE id = $1", id, to); err != nil {
				return err
			}
		}
		if note != "" && latestVer != nil {
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.design_feedback (id, design_job_id, design_version_id, author_type, author_id, comment)\n\t\t\t\tVALUES ($1, $2, $3, 'staff', $4, $5)", ids.New(), id, *latestVer, p.MembershipID, note); err != nil {
				return err
			}
		}
		if to == "approved" || to == "delivered" || to == "cancelled" {
			if err := s.checkBrandingStageComplete(ctx, tx, j); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, p, id)
}

// checkBrandingStageComplete completes the linked project if tg_branding_stage_rollup finished the last stage.
func (s *Service) checkBrandingStageComplete(ctx context.Context, tx pgx.Tx, j map[string]any) error {
	stage, ok := j["stage_id"].(uuid.UUID)
	if !ok || stage == uuid.Nil {
		return nil
	}
	var open, good int
	if err := tx.QueryRow(ctx, "\n\t\tSELECT count(*) FILTER (WHERE status NOT IN ('approved','printing','delivered','cancelled')),\n\t\t       count(*) FILTER (WHERE status IN ('approved','printing','delivered'))\n\t\tFROM app.design_jobs WHERE project_stage_id = $1", stage).Scan(&open, &good); err != nil {
		return err
	}
	if open > 0 || good == 0 {
		return nil
	}
	var project uuid.UUID
	var cur string
	if err := tx.QueryRow(ctx, "SELECT project_id, status::text FROM app.project_stages WHERE id = $1 FOR UPDATE", stage).Scan(&project, &cur); err != nil {
		return nil
	}
	if cur != "done" {
		_, _ = tx.Exec(ctx, `SELECT set_config('app.change_note', 'All design jobs approved', true)`)
		if _, err := tx.Exec(ctx, "UPDATE app.project_stages SET status = 'done' WHERE id = $1", stage); err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `SELECT set_config('app.change_note', '', true)`)
	}
	prRows, err := rows(ctx, tx, `
		SELECT pr.id, pr.project_no AS number, pr.status::text AS status, om.user_id AS owner_id
		FROM app.projects pr
		LEFT JOIN app.memberships om ON om.id = pr.owner_id
		WHERE pr.id = $1`, project)
	if err != nil || len(prRows) == 0 {
		return err
	}
	return s.syncProjectProgress(ctx, tx, project, prRows[0])
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status == "cancelled" {
			return httpx.Conflict("invalid_status", "A cancelled job cannot take files")
		}
		if in.FileID != nil {
			if err := require404(ctx, tx, "app.files", "File", *in.FileID); err != nil {
				return err
			}
		}
		if kind != "design" {
			if status != "approved" && status != "printing" && status != "delivered" {
				return httpx.Conflict("invalid_status", "Print-ready and source files are added once the design is approved")
			}
			finalVer, ok := j["final_version_id"].(uuid.UUID)
			if !ok || finalVer == uuid.Nil {
				return httpx.Conflict("invalid_status", "An approved design version is required first")
			}
			assetKind := "print_pdf"
			var fileArg, urlArg any
			if kind == "source" && in.FileID == nil {
				assetKind = "source_link"
				urlArg = httpsOrNil(in.Link)
				if urlArg == nil {
					return httpx.BadRequest("invalid_body", "Source link must be a valid https:// URL")
				}
			} else if in.FileID != nil {
				fileArg = *in.FileID
				if kind == "source" {
					assetKind = "other"
				}
			} else {
				assetKind = "other"
				urlArg = httpsOrNil(in.Link)
				if urlArg == nil {
					return httpx.BadRequest("invalid_body", "Link must be a valid https:// URL")
				}
			}
			id := ids.New()
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.design_final_assets (id, design_version_id, asset_kind, file_id, url)\n\t\t\t\tVALUES ($1, $2, $3::app.design_asset_kind, $4, $5)",
				id, finalVer, assetKind, fileArg, urlArg); err != nil {
				return err
			}
			out, err = one(ctx, tx, "Asset", `
				SELECT id, 0 AS version_no, $2::text AS kind, file_id, url::text AS link, NULL::text AS notes, 'approved'::text AS status, false AS is_final, created_at
				FROM app.design_final_assets WHERE id = $1`, id, kind)
			return err
		}

		if in.FileID == nil {
			return httpx.BadRequest("invalid_body", "A design version requires a preview file")
		}
		var no int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no), 0) + 1 FROM app.design_versions WHERE design_job_id = $1`, jobID).Scan(&no); err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.design_versions (id, design_job_id, version_no, preview_file_id, working_file_url)\n\t\t\tVALUES ($1, $2, $3, $4, $5)",
			id, jobID, no, *in.FileID, httpsOrNil(in.Link)); err != nil {
			return err
		}
		if status == "new_brief" {
			if _, err := tx.Exec(ctx, "UPDATE app.design_jobs SET status = 'in_design', designer_id = COALESCE(designer_id, $2) WHERE id = $1", jobID, p.MembershipID); err != nil {
				return err
			}
		}
		out, err = one(ctx, tx, "Version", `
			SELECT v.id, v.version_no, 'design'::text AS kind, v.preview_file_id AS file_id, v.working_file_url::text AS link,
			       NULL::text AS notes, v.status::text AS status, false AS is_final, v.created_at
			FROM app.design_versions v WHERE v.id = $1`, id)
		return err
	})
	return out, err
}

type FeedbackIn struct {
	VersionID *uuid.UUID `json:"version_id"`
	Comment   string     `json:"comment" validate:"required,max=5000"`
}

func (s *Service) AddFeedback(ctx context.Context, p *auth.Principal, jobID uuid.UUID, in FeedbackIn) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, false)
		if err != nil {
			return err
		}
		var verID uuid.UUID
		if in.VersionID != nil {
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.design_versions WHERE id = $1 AND design_job_id = $2", *in.VersionID, jobID).Scan(&n); err != nil || n == 0 {
				return httpx.NotFound("not_found", "Version not found")
			}
			verID = *in.VersionID
		} else {
			if err := tx.QueryRow(ctx, "SELECT id FROM app.design_versions WHERE design_job_id = $1 ORDER BY version_no DESC LIMIT 1", jobID).Scan(&verID); err != nil {
				return httpx.BadRequest("invalid_status", "Upload a design version before adding feedback")
			}
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.design_feedback (id, design_job_id, design_version_id, author_type, author_id, comment)\n\t\t\tVALUES ($1, $2, $3, 'staff', $4, $5)",
			id, jobID, verID, p.MembershipID, clean(in.Comment)); err != nil {
			return err
		}
		if d, ok := j["designer_id"].(uuid.UUID); ok && d != uuid.Nil && d != p.UserID {
			eid := jobID
			_ = s.Notify.ToUser(ctx, tx, d, notify.Message{
				Type: "design_feedback", Title: "Feedback on a design job",
				Body: j["number"].(string) + ": " + clean(in.Comment), Link: "/design-jobs/" + jobID.String(), EntityType: "design_job", EntityID: &eid,
			})
		}
		out, err = one(ctx, tx, "Feedback", `SELECT id, design_version_id AS version_id, author_type::text AS author_type, comment, created_at FROM app.design_feedback WHERE id = $1`, id)
		return err
	})
	return out, err
}

func tokenHashBytes(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

// SendForApproval creates a single-use approval link valid for 14 days, registers it in iam.public_tokens,
// and writes an outbound_messages row in the same transaction when the chosen channel is configured.
// If the caller explicitly requested a channel that is not configured, the link is still committed
// and channelUnavailable is returned true so the handler can respond 503 with approval_path intact.
func (s *Service) SendForApproval(ctx context.Context, p *auth.Principal, jobID, versionID uuid.UUID, baseURL, channel string) (map[string]any, bool, error) {
	requested := clean(strings.ToLower(channel))
	sentVia := requested
	if sentVia == "" {
		sentVia = "sms"
	}
	var err error
	if sentVia, err = outbound.ValidateChannel(sentVia); err != nil {
		return nil, false, err
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, false, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := tokenHashBytes(token)
	expires := s.Clock.Now().Add(14 * 24 * time.Hour)
	path := "/approve/" + token
	fullURL := path
	if baseURL != "" {
		fullURL = strings.TrimRight(baseURL, "/") + path
	}
	var number string
	queued := false
	channelUnavailable := false
	var outboundID *uuid.UUID
	err = s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status != "in_design" && status != "internal_review" && status != "with_client" {
			return httpx.Conflict("invalid_status", "A job in "+status+" cannot be sent for approval")
		}
		var vstat string
		if err := tx.QueryRow(ctx, "SELECT status::text FROM app.design_versions WHERE id = $1 AND design_job_id = $2 FOR UPDATE", versionID, jobID).Scan(&vstat); err != nil {
			return httpx.NotFound("not_found", "Design version not found")
		}
		// Revoke any open approval links for this job.
		if _, err := tx.Exec(ctx, "\n\t\t\tWITH rev AS (\n\t\t\t  UPDATE app.design_approval_links SET revoked_at = now()\n\t\t\t  WHERE design_job_id = $1 AND decided_at IS NULL AND revoked_at IS NULL\n\t\t\t  RETURNING token_hash\n\t\t\t)\n\t\t\tUPDATE iam.public_tokens pt SET revoked_at = now()\n\t\t\tFROM rev WHERE pt.token_hash = rev.token_hash", jobID); err != nil {
			return err
		}
		// Supersede any other version still marked sent_to_client.
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.design_versions SET status = 'superseded'\n\t\t\tWHERE design_job_id = $1 AND status = 'sent_to_client' AND id <> $2", jobID, versionID); err != nil {
			return err
		}
		if vstat != "sent_to_client" {
			if _, err := tx.Exec(ctx, "UPDATE app.design_versions SET status = 'sent_to_client' WHERE id = $1", versionID); err != nil {
				return err
			}
		}
		linkID := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.design_approval_links (id, design_job_id, design_version_id, token_hash, expires_at, sent_via)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6::app.message_channel)",
			linkID, jobID, versionID, hash, expires, sentVia); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO iam.public_tokens (token_hash, purpose, target_id, expires_at)\n\t\t\tVALUES ($1, 'design_approval', $2, $3)",
			hash, linkID, expires); err != nil {
			return err
		}
		number, _ = j["number"].(string)
		title, _ := j["title"].(string)
		clientID := j["client_id"].(uuid.UUID)
		var ph, email *string
		if err := tx.QueryRow(ctx, "SELECT phone::text, email::text FROM app.customers WHERE id = $1", clientID).Scan(&ph, &email); err != nil {
			return err
		}
		recipient := ""
		if sentVia == "email" {
			if email != nil {
				recipient = *email
			}
		} else if ph != nil {
			recipient = *ph
		}
		if s.Outbound != nil && s.Outbound.Channels.Available(sentVia) {
			if len(strings.TrimSpace(recipient)) >= 5 {
				row, err := s.Outbound.Enqueue(ctx, tx, outbound.Message{
					Channel:        sentVia,
					Recipient:      recipient,
					Subject:        "Design approval: " + title,
					Body:           `Please review your design "` + title + `": ` + fullURL,
					IdempotencyKey: "design-approval:" + linkID.String(),
					DesignJobID:    &jobID,
					CustomerID:     &clientID,
				})
				if err != nil {
					return err
				}
				queued = true
				idCopy := row.ID
				outboundID = &idCopy
			} else if requested != "" {
				return httpx.BadRequest("no_recipient", "Client has no phone number or email for this channel")
			}
		} else if requested != "" {
			channelUnavailable = true
		}
		return s.Audit.Record(ctx, tx, p, "design_version.sent", "design_job", jobID, nil, map[string]any{"version_id": versionID, "expires_at": expires, "sent_via": sentVia})
	})
	if err != nil {
		return nil, false, err
	}
	res := map[string]any{
		"job":           number,
		"approval_path": path,
		"expires_at":    expires,
		"delivered":     queued,
		"queued":        queued,
		"sent_via":      sentVia,
	}
	if outboundID != nil {
		res["id"] = *outboundID
		res["status"] = "queued"
	}
	return res, channelUnavailable, nil
}

// ---------- public approval page ----------

type publicTokenRow struct {
	linkID    uuid.UUID
	expiresAt time.Time
	revokedAt *time.Time
}

func (s *Service) resolvePublicToken(ctx context.Context, token string) (publicTokenRow, error) {
	var r publicTokenRow
	err := s.DB.Pool.QueryRow(ctx, `
		SELECT target_id, expires_at, revoked_at
		FROM iam.public_tokens WHERE token_hash = $1 AND purpose = 'design_approval'`,
		tokenHashBytes(token)).Scan(&r.linkID, &r.expiresAt, &r.revokedAt)
	if err != nil {
		return r, httpx.NotFound("not_found", "This approval link is not valid")
	}
	if r.revokedAt != nil || s.Clock.Now().After(r.expiresAt) {
		return r, httpx.New(410, "link_expired", "This approval link has expired. Ask us for a new one")
	}
	return r, nil
}

type approvalRow struct {
	linkID, versionID, jobID uuid.UUID
	versionNo                int
	status                   string
	expires                  time.Time
	decided                  *time.Time
	fileID                   uuid.UUID
	workingURL               *string
}

func (s *Service) loadApproval(ctx context.Context, tx pgx.Tx, linkID uuid.UUID, lock bool) (approvalRow, error) {
	var a approvalRow
	sql := `
		SELECT l.id, l.design_version_id, l.design_job_id, v.version_no,
		       CASE WHEN v.status = 'sent_to_client' THEN 'sent' ELSE v.status::text END,
		       l.expires_at, l.decided_at, v.preview_file_id, v.working_file_url::text
		FROM app.design_approval_links l
		JOIN app.design_versions v ON v.id = l.design_version_id
		WHERE l.id = $1`
	if lock {
		sql += ` FOR UPDATE OF l`
	}
	if err := tx.QueryRow(ctx, sql, linkID).Scan(&a.linkID, &a.versionID, &a.jobID, &a.versionNo, &a.status, &a.expires, &a.decided, &a.fileID, &a.workingURL); err != nil {
		return a, httpx.NotFound("not_found", "This approval link is not valid")
	}
	if s.Clock.Now().After(a.expires) {
		return a, httpx.New(410, "link_expired", "This approval link has expired. Ask us for a new one")
	}
	return a, nil
}

// PublicApproval returns what the client sees on the approval page.
func (s *Service) PublicApproval(ctx context.Context, token string) (map[string]any, error) {
	pt, err := s.resolvePublicToken(ctx, token)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.globalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, pt.linkID, false)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE app.design_approval_links SET first_opened_at = COALESCE(first_opened_at, now()) WHERE id = $1 AND decision IS NULL", pt.linkID); err != nil {
			return err
		}
		out, err = one(ctx, tx, "Design", `
			SELECT j.title::text AS title,
			       CASE WHEN j.job_type = 'social_media_post' THEN 'social_media' ELSE j.job_type::text END AS job_type,
			       c.full_name::text AS client_name,
			       COALESCE(bu.display_name::text, NULLIF(bs.business_name::text,''), bp.name::text) AS business_name,
			       j.revisions_included, j.revisions_used
			FROM app.design_jobs j
			JOIN app.customers c ON c.id = j.customer_id
			CROSS JOIN app.business_profile bp
			CROSS JOIN app.business_settings bs
			LEFT JOIN app.business_units bu ON bu.code = 'B'
			WHERE j.id = $1`, a.jobID)
		if err != nil {
			return err
		}
		out["version_no"], out["status"], out["expires_at"], out["decided_at"] = a.versionNo, a.status, a.expires, a.decided
		out["has_file"] = a.fileID != uuid.Nil
		if a.fileID != uuid.Nil {
			var name, mime string
			if err := tx.QueryRow(ctx, "SELECT original_name::text, mime_type FROM app.files WHERE id = $1", a.fileID).Scan(&name, &mime); err == nil {
				out["file_name"], out["mime_type"] = name, mime
			}
		}
		out["link"] = a.workingURL
		out["comments"], err = rows(ctx, tx, `SELECT author_type::text AS author_type, comment, created_at FROM app.design_feedback WHERE design_version_id = $1 ORDER BY created_at`, a.versionID)
		return err
	})
	return out, err
}

// PublicApprovalFile streams the version's preview file.
func (s *Service) PublicApprovalFile(ctx context.Context, token string) (string, string, io.ReadCloser, error) {
	pt, err := s.resolvePublicToken(ctx, token)
	if err != nil {
		return "", "", nil, err
	}
	var storageKey, name, mime string
	err = s.globalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, pt.linkID, false)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT storage_key, original_name::text, mime_type FROM app.files WHERE id = $1", a.fileID).Scan(&storageKey, &name, &mime)
	})
	if err != nil {
		return "", "", nil, err
	}
	rc, err := s.Store.Open(ctx, storageKey)
	return name, mime, rc, err
}

type DecisionIn struct {
	Decision string `json:"decision" validate:"required,oneof=approve changes"`
	Comment  string `json:"comment" validate:"max=5000"`
}

// PublicDecide records the client's choice. DB triggers update version, job, revisions_used, and branding stage rollup.
func (s *Service) PublicDecide(ctx context.Context, token string, in DecisionIn, ip, userAgent string) (map[string]any, error) {
	if in.Decision == "changes" && clean(in.Comment) == "" {
		return nil, httpx.BadRequest("invalid_body", "Tell us what to change")
	}
	pt, err := s.resolvePublicToken(ctx, token)
	if err != nil {
		return nil, err
	}
	var result string
	err = s.globalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.loadApproval(ctx, tx, pt.linkID, true)
		if err != nil {
			return err
		}
		if a.decided != nil || a.status != "sent" {
			return httpx.Conflict("already_decided", "A choice has already been recorded for this design")
		}
		jRows, err := rows(ctx, tx, `
			SELECT j.id, j.job_no AS number, j.title::text AS title, j.status::text AS status,
			       dm.user_id AS designer_id, j.project_stage_id AS stage_id, j.project_id
			FROM app.design_jobs j
			LEFT JOIN app.memberships dm ON dm.id = j.designer_id
			WHERE j.id = $1 FOR UPDATE OF j`, a.jobID)
		if err != nil || len(jRows) == 0 {
			return err
		}
		j := jRows[0]
		decision := "approved"
		result = "approved"
		if in.Decision == "changes" {
			decision = "changes_requested"
			result = "changes_requested"
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.design_approval_links\n\t\t\tSET decision = $2::app.client_decision, decision_ip = NULLIF($3,'')::inet, decision_user_agent = left(NULLIF($4,''), 512)\n\t\t\tWHERE id = $1", pt.linkID, decision, clean(ip), clean(userAgent)); err != nil {
			return err
		}
		if clean(in.Comment) != "" {
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.design_feedback (id, design_job_id, design_version_id, author_type, approval_link_id, comment)\n\t\t\t\tVALUES ($1, $2, $3, 'client', $4, $5)",
				ids.New(), a.jobID, a.versionID, pt.linkID, clean(in.Comment)); err != nil {
				return err
			}
		}
		if decision == "approved" {
			if err := s.checkBrandingStageComplete(ctx, tx, j); err != nil {
				return err
			}
		}
		title := "Client requested changes"
		if decision == "approved" {
			title = "Design approved by the client"
		}
		eid := a.jobID
		msg := notify.Message{
			Type: "design_approved", Title: title,
			Body: j["number"].(string) + ": " + j["title"].(string), Link: "/design-jobs/" + a.jobID.String(), EntityType: "design_job", EntityID: &eid,
		}
		if d, ok := j["designer_id"].(uuid.UUID); ok && d != uuid.Nil {
			_ = s.Notify.ToUser(ctx, tx, d, msg)
		}
		_ = s.Notify.ToPermission(ctx, tx, nil, "services.design.manage", msg)
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.jobRow(ctx, tx, p, jobID, false); err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.design_time_logs (id, design_job_id, membership_id, started_at)\n\t\t\tVALUES ($1, $2, $3, $4)", id, jobID, p.MembershipID, s.Clock.Now()); err != nil {
			if isDup(err) {
				return httpx.Conflict("timer_running", "You already have a timer running. Stop it first")
			}
			return err
		}
		var err error
		out, err = one(ctx, tx, "Timer", `SELECT id, design_job_id AS job_id, started_at FROM app.design_time_logs WHERE id = $1`, id)
		return err
	})
	return out, err
}

func (s *Service) StopTimer(ctx context.Context, p *auth.Principal, jobID uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var started time.Time
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT id, started_at FROM app.design_time_logs\n\t\t\tWHERE design_job_id = $1 AND membership_id = $2 AND ended_at IS NULL FOR UPDATE", jobID, p.MembershipID).Scan(&id, &started); err != nil {
			return httpx.Conflict("no_timer", "You have no timer running on this job")
		}
		now := s.Clock.Now()
		if !now.After(started) {
			now = started.Add(time.Minute)
		}
		if _, err := tx.Exec(ctx, "UPDATE app.design_time_logs SET ended_at = $2 WHERE id = $1", id, now); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Timer", `
			SELECT id, design_job_id AS job_id, started_at, ended_at AS stopped_at,
			       GREATEST(1, ROUND(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60)::int) AS minutes
			FROM app.design_time_logs WHERE id = $1`, id)
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
	Note         string          `json:"note" validate:"max=1000"`
}

// CreatePrintOrder sends an approved design to a printer contractor. Any positive cost creates a contractor bill.
func (s *Service) CreatePrintOrder(ctx context.Context, p *auth.Principal, jobID uuid.UUID, in PrintOrderIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	if in.ContractorID == nil {
		return nil, httpx.BadRequest("invalid_body", "Choose the contractor who prints it")
	}
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		j, err := s.jobRow(ctx, tx, p, jobID, true)
		if err != nil {
			return err
		}
		status := j["status"].(string)
		if status != "approved" && status != "printing" {
			return httpx.Conflict("invalid_status", "A print order needs an approved design")
		}
		if err := require404(ctx, tx, "app.contractors", "Contractor", *in.ContractorID); err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.print_orders (id, design_job_id, contractor_id, quantity, cost, due_date, notes)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7)",
			id, jobID, *in.ContractorID, in.Quantity, money.Money(in.Cost), dateOrNil(in.DueDate), nz(in.Note)); err != nil {
			return err
		}
		if in.Cost.IsPositive() {
			if _, err := s.addCostTx(ctx, tx, p, CostIn{
				DesignJobID: &jobID, ContractorID: in.ContractorID, Kind: "printing",
				Description: "Printing for " + j["number"].(string), Amount: in.Cost,
			}, &id); err != nil {
				return err
			}
		}
		if status == "approved" {
			if _, err := tx.Exec(ctx, "UPDATE app.design_jobs SET status = 'printing' WHERE id = $1", jobID); err != nil {
				return err
			}
		}
		out, err = one(ctx, tx, "Print order", `
			SELECT id, design_job_id AS job_id, contractor_id, quantity, cost, due_date,
			       CASE WHEN status = 'sent_to_print' THEN 'sent' ELSE status::text END AS status,
			       notes AS note, created_at
			FROM app.print_orders WHERE id = $1`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdatePrintOrder(ctx context.Context, p *auth.Principal, id uuid.UUID, status string) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var cur string
		var jobID uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT status::text, design_job_id FROM app.print_orders WHERE id = $1 FOR UPDATE", id).Scan(&cur, &jobID); err != nil {
			return httpx.NotFound("not_found", "Print order not found")
		}
		if status == "cancelled" {
			var billID uuid.UUID
			if err := tx.QueryRow(ctx, "SELECT id FROM app.contractor_bills WHERE print_order_id = $1", id).Scan(&billID); err == nil {
				if err := s.voidCostTx(ctx, tx, p, billID, "Print order cancelled"); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, "UPDATE app.print_orders SET status = $2::app.print_order_status WHERE id = $1", id, status); err != nil {
			return err
		}
		if status == "ready" {
			jRows, err := rows(ctx, tx, `
				SELECT j.job_no AS number, j.title::text AS title, dm.user_id AS designer_id
				FROM app.design_jobs j LEFT JOIN app.memberships dm ON dm.id = j.designer_id
				WHERE j.id = $1`, jobID)
			if err == nil && len(jRows) > 0 {
				if d, ok := jRows[0]["designer_id"].(uuid.UUID); ok && d != uuid.Nil {
					eid := jobID
					_ = s.Notify.ToUser(ctx, tx, d, notify.Message{
						Type: "print_ready", Title: "Print ready",
						Body: jRows[0]["number"].(string) + ": " + jRows[0]["title"].(string), Link: "/design-jobs/" + jobID.String(), EntityType: "design_job", EntityID: &eid,
					})
				}
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "print_order.updated", "print_order", id, map[string]any{"status": cur}, map[string]any{"status": status}); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Print order", `
			SELECT id, design_job_id AS job_id, contractor_id, quantity, cost, due_date,
			       CASE WHEN status = 'sent_to_print' THEN 'sent' ELSE status::text END AS status,
			       notes AS note
			FROM app.print_orders WHERE id = $1`, id)
		return err
	})
	return out, err
}

// ListPrintOrders lists print orders across design jobs with optional status, contractor, or job filters.
func (s *Service) ListPrintOrders(ctx context.Context, p *auth.Principal, status string, contractorID, jobID *uuid.UUID, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "po.status = ?::app.print_order_status", mapPrintOrderStatus(status))
		q.If(contractorID != nil, "po.contractor_id = ?", contractorID)
		q.If(jobID != nil, "po.design_job_id = ?", jobID)
		ownJob(q, p)
		from := ` FROM app.print_orders po
			JOIN app.design_jobs j ON j.id = po.design_job_id
			JOIN app.customers c ON c.id = j.customer_id
			LEFT JOIN app.contractors ct ON ct.id = po.contractor_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `
			SELECT po.id, po.design_job_id AS job_id, j.job_no AS job_number, j.title::text AS job_title,
			       j.customer_id AS client_id, c.full_name::text AS client_name,
			       po.contractor_id, ct.name::text AS contractor_name, po.quantity, po.cost, po.due_date,
			       CASE WHEN po.status = 'sent_to_print' THEN 'sent' ELSE po.status::text END AS status,
			       po.notes AS note, po.created_at`+
			from+q.Where()+` ORDER BY po.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

type ExtraRevisionIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	ExtraRevisions int             `json:"extra_revisions" validate:"gte=0,lte=50"`
	Note           string          `json:"note" validate:"max=500"`
}

// AddExtraRevisionCharge adds an extra revision fee to a design job's price and included revisions.
func (s *Service) AddExtraRevisionCharge(ctx context.Context, p *auth.Principal, id uuid.UUID, in ExtraRevisionIn) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.jobRow(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		if before["status"] == "cancelled" || before["status"] == "delivered" {
			return httpx.Conflict("invalid_status", "A "+before["status"].(string)+" job cannot be charged extra revisions")
		}
		fee := money.Money(in.Amount)
		extra := in.ExtraRevisions
		if extra == 0 {
			extra = 1
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.design_jobs SET price = price + $2, extra_revision_fee = extra_revision_fee + $2, revisions_included = revisions_included + $3
			WHERE id = $1`, id, fee, extra); err != nil {
			return err
		}
		var latestVer *uuid.UUID
		_ = tx.QueryRow(ctx, "SELECT id FROM app.design_versions WHERE design_job_id = $1 ORDER BY version_no DESC LIMIT 1", id).Scan(&latestVer)
		if latestVer != nil {
			msg := "Extra revision charge added: " + fee.StringFixed(2)
			if note := clean(in.Note); note != "" {
				msg += " — " + note
			}
			_, _ = tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.design_feedback (id, design_job_id, design_version_id, author_type, author_id, comment)\n\t\t\t\tVALUES ($1, $2, $3, 'staff', $4, $5)", ids.New(), id, *latestVer, p.MembershipID, msg)
		}
		return s.Audit.Record(ctx, tx, p, "design_job.extra_revision_charged", "design_job", id,
			map[string]any{"price": before["price"], "revisions_included": before["revisions_included"]},
			map[string]any{"added_amount": fee, "extra_revisions": extra, "note": clean(in.Note)})
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(ctx, p, id)
}

// ---------- branding stage link ----------

type BrandingIn struct {
	Jobs []struct {
		JobType    string           `json:"job_type" validate:"required,oneof=logo business_card poster flyer banner signage social_media social_media_post packaging_label branding_package other"`
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		pr, err := s.projectRow(ctx, tx, p, projectID, true)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.project_stages WHERE id = $1 AND project_id = $2", stageID, projectID).Scan(&n); err != nil || n == 0 {
			return httpx.NotFound("not_found", "Stage not found")
		}
		for _, jb := range in.Jobs {
			id, err := s.createJobTx(ctx, tx, p, DesignJobIn{
				ClientID: pr["client_id"].(uuid.UUID), ProjectID: &projectID, StageID: &stageID,
				JobType: jb.JobType, Title: jb.Title, DesignerID: jb.DesignerID, Deadline: jb.Deadline, Price: jb.Price,
			})
			if err != nil {
				return err
			}
			created = append(created, id)
		}
		_, err = tx.Exec(ctx, "UPDATE app.project_stages SET status = 'in_progress', is_branding_stage = true WHERE id = $1 AND status = 'not_started'", stageID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"job_ids": created}, nil
}
