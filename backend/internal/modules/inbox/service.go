package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/outbound"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/sender"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Approval *approval.Service
	Audit    *audit.Service
	Outbound *outbound.Service
	Clock    clock.Clock
}

func New(database *db.DB, ap *approval.Service, aud *audit.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Approval: ap, Audit: aud, Clock: clk}
}

// ---------- approvals ----------

// subjectLinks maps a subject type to the API path of the record.
var subjectLinks = map[string]string{
	"sale": "/sales/", "project": "/projects/", "design_job": "/design-jobs/",
	"transaction": "/finance/transactions/", "payment_received": "/payments/",
	"payment_made": "/payments/", "day_closing": "/day-closings/", "product": "/products/",
}

var legacyApprovalKinds = map[string]string{
	"discount":     "sale_discount",
	"price_change": "job_price_change",
}

func normalizeKind(k string) string {
	if mapped, ok := legacyApprovalKinds[k]; ok {
		return mapped
	}
	return k
}

// allowedKinds returns the app.approval_type values the caller holds permission to decide.
func allowedKinds(p *auth.Principal) []string {
	var kinds []string
	for kind, perm := range approval.KindPermission {
		if p.Has(perm) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// decidable returns a SQL predicate expressing when the caller may decide an
// app.approval_requests row (alias ar): pending, not their own request
// (enforced by approval_no_self_approval even for Admin), holding the matching
// permission, and within their authority limits.
func decidable(p *auth.Principal, q *sqlq.Q) string {
	kinds := allowedKinds(p)
	if len(kinds) == 0 {
		return "false"
	}
	cond := "ar.status = 'pending' AND ar.requested_by <> " + q.Arg(p.MembershipID) +
		" AND ar.approval_type::text = ANY(" + q.Arg(kinds) + ")"
	if lim, ok := p.Limits["discount_approve_pct"]; ok {
		cond += " AND (ar.approval_type <> 'sale_discount' OR ar.requested_percent IS NULL OR ar.requested_percent <= " + q.Arg(lim) + ")"
	}
	if lim, ok := p.Limits["price_change_approve_pct"]; ok {
		cond += " AND (ar.approval_type <> 'job_price_change' OR ar.current_amount IS NULL OR ar.current_amount <= 0 OR ar.requested_amount IS NULL OR round(abs(ar.requested_amount - ar.current_amount) * 100 / ar.current_amount, 2) <= " + q.Arg(lim) + ")"
	}
	return cond
}

const approvalSubjectTypeExpr = `CASE
	WHEN ar.sale_id IS NOT NULL THEN 'sale'
	WHEN ar.project_id IS NOT NULL THEN 'project'
	WHEN ar.design_job_id IS NOT NULL THEN 'design_job'
	WHEN ar.financial_transaction_id IS NOT NULL THEN 'transaction'
	WHEN ar.payment_received_id IS NOT NULL THEN 'payment_received'
	WHEN ar.payment_made_id IS NOT NULL THEN 'payment_made'
	WHEN ar.day_closing_id IS NOT NULL THEN 'day_closing'
	ELSE 'product'
END`

const approvalSubjectIDExpr = `COALESCE(
	ar.sale_id, ar.project_id, ar.design_job_id, ar.financial_transaction_id,
	ar.payment_received_id, ar.payment_made_id, ar.day_closing_id, ar.product_id
)`

type ApprovalFilter struct {
	Status string
	Kind   string
	Mine   bool
}

func (s *Service) List(ctx context.Context, p *auth.Principal, f ApprovalFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		can := decidable(p, q)
		mine := "ar.requested_by = " + q.Arg(p.MembershipID)
		// can is always referenced so every bound parameter appears in the query.
		visible := "(" + mine + " OR ((" + can + ") AND false))"
		if !f.Mine {
			hist := "false"
			if p.IsOwner {
				hist = "true"
			} else if kinds := allowedKinds(p); len(kinds) > 0 {
				hist = "ar.approval_type::text = ANY(" + q.Arg(kinds) + ")"
			}
			visible = "(" + mine + " OR (" + can + ") OR (ar.status <> 'pending' AND " + hist + "))"
		}
		q.Add(visible)
		q.If(f.Status != "", "ar.status = ?::app.approval_status", f.Status)
		q.If(f.Kind != "", "ar.approval_type = ?::app.approval_type", normalizeKind(f.Kind))
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.approval_requests ar`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		sql := `
			SELECT ar.id, ar.approval_type::text, ` + approvalSubjectTypeExpr + `, ` + approvalSubjectIDExpr + `,
			       ar.requested_amount, ar.requested_percent, ar.current_amount, ar.reason, ar.status::text,
			       rm.user_id, u.full_name::text, dm.user_id, d.full_name::text,
			       ar.decided_at, ar.decision_note, ar.requested_at, (` + can + `)
			FROM app.approval_requests ar
			JOIN app.memberships rm ON rm.tenant_id = ar.tenant_id AND rm.id = ar.requested_by
			JOIN iam.users u ON u.id = rm.user_id
			LEFT JOIN app.memberships dm ON dm.tenant_id = ar.tenant_id AND dm.id = ar.decided_by
			LEFT JOIN iam.users d ON d.id = dm.user_id` + q.Where() + ` ORDER BY ar.requested_at DESC` + q.Page(page.PerPage, page.Offset)
		rows, err := tx.Query(ctx, sql, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanApproval(rows)
			if err != nil {
				return err
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	return out, total, err
}

func scanApproval(rows pgx.Row) (map[string]any, error) {
	var id, subject, by uuid.UUID
	var decidedBy *uuid.UUID
	var kind, subjectType, summary, status, byName string
	var decidedName, note *string
	var amount, pct, current decimal.NullDecimal
	var decidedAt *time.Time
	var created time.Time
	var canDecide bool
	if err := rows.Scan(&id, &kind, &subjectType, &subject, &amount, &pct, &current, &summary, &status,
		&by, &byName, &decidedBy, &decidedName, &decidedAt, &note, &created, &canDecide); err != nil {
		return nil, err
	}
	row := map[string]any{
		"id": id, "kind": kind, "subject_type": subjectType, "subject_id": subject, "summary": summary, "status": status,
		"branch_id": nil, "requested_by": by, "requested_by_name": byName, "decided_by": decidedBy, "decided_by_name": decidedName,
		"decided_at": decidedAt, "decision_note": note, "created_at": created, "can_decide": canDecide, "amount": nil,
	}
	if amount.Valid {
		row["amount"] = amount.Decimal
	}
	if pct.Valid {
		row["requested_percent"] = pct.Decimal
	}
	if current.Valid {
		row["current_amount"] = current.Decimal
	}
	if base, ok := subjectLinks[subjectType]; ok {
		row["subject_link"] = "/api/v1" + base + subject.String()
	}
	return row, nil
}

// Count returns pending requests the caller may decide, for the badge.
func (s *Service) Count(ctx context.Context, p *auth.Principal) (int, error) {
	n := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		return tx.QueryRow(ctx, `SELECT count(*) FROM app.approval_requests ar WHERE `+decidable(p, q), q.Args...).Scan(&n)
	})
	return n, err
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		can := decidable(p, q)
		row, err := scanApproval(tx.QueryRow(ctx, `
			SELECT ar.id, ar.approval_type::text, `+approvalSubjectTypeExpr+`, `+approvalSubjectIDExpr+`,
			       ar.requested_amount, ar.requested_percent, ar.current_amount, ar.reason, ar.status::text,
			       rm.user_id, u.full_name::text, dm.user_id, d.full_name::text,
			       ar.decided_at, ar.decision_note, ar.requested_at, (`+can+`)
			FROM app.approval_requests ar
			JOIN app.memberships rm ON rm.tenant_id = ar.tenant_id AND rm.id = ar.requested_by
			JOIN iam.users u ON u.id = rm.user_id
			LEFT JOIN app.memberships dm ON dm.tenant_id = ar.tenant_id AND dm.id = ar.decided_by
			LEFT JOIN iam.users d ON d.id = dm.user_id
			WHERE ar.id = $1`, q.Args...))
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Approval request not found")
		}
		if err != nil {
			return err
		}
		if !s.visible(p, row) {
			return httpx.NotFound("not_found", "Approval request not found")
		}
		out = row
		return nil
	})
	return out, err
}

func (s *Service) visible(p *auth.Principal, row map[string]any) bool {
	if p.IsOwner || row["requested_by"] == p.UserID || row["can_decide"] == true {
		return true
	}
	if kind, ok := row["kind"].(string); ok {
		if perm, exists := approval.KindPermission[kind]; exists && p.Has(perm) {
			return true
		}
	}
	return false
}

// Decide approves or rejects inside one transaction with the subject's hook.
// Approve is idempotent: a retry with the same key replays the first response.
func (s *Service) Decide(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if err := s.Approval.Decide(ctx, tx, p, id, approve, note); err != nil {
			return err
		}
		state := "rejected"
		if approve {
			state = "approved"
		}
		if err := s.Audit.Record(ctx, tx, p, "approval."+state, "approval_requests", id, nil, map[string]any{"note": note}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": state}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) Cancel(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := s.Approval.Cancel(ctx, tx, p, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "approval.cancelled", "approval_requests", id, nil, nil)
	})
}

// ApproveWithPIN is no longer supported: the approved schema has no PIN credentials
// (see docs/database/database-transition.md decision #2).
func (s *Service) ApproveWithPIN(_ context.Context, _ *auth.Principal, _ uuid.UUID, _, _, _ string) (int, []byte, error) {
	return 0, nil, httpx.New(fiber.StatusGone, "pin_not_supported", "Approval PIN is not supported; approve from the approver's signed-in session")
}

// ---------- notifications ----------

func (s *Service) Notifications(ctx context.Context, p *auth.Principal, unread bool, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New().Add("recipient_id = ?", p.MembershipID).If(unread, "read_at IS NULL")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.notifications`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, notification_type::text, title, COALESCE(body, ''), link_path, read_at, created_at
			FROM app.notifications`+q.Where()+` ORDER BY created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var typ, title, body string
			var link *string
			var read *time.Time
			var created time.Time
			if err := rows.Scan(&id, &typ, &title, &body, &link, &read, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{
				"id": id, "type": typ, "title": title, "body": body, "link": link,
				"entity_type": nil, "entity_id": nil, "read_at": read, "created_at": created,
			})
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) UnreadCount(ctx context.Context, p *auth.Principal) (int, error) {
	n := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM app.notifications WHERE recipient_id = $1 AND read_at IS NULL`, p.MembershipID).Scan(&n)
	})
	return n, err
}

func (s *Service) MarkRead(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.notifications SET read_at = COALESCE(read_at, $3) WHERE id = $1 AND recipient_id = $2`, id, p.MembershipID, s.Clock.Now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Notification not found")
		}
		return nil
	})
}

func (s *Service) ReadAll(ctx context.Context, p *auth.Principal) (int64, error) {
	var n int64
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.notifications SET read_at = $2 WHERE recipient_id = $1 AND read_at IS NULL`, p.MembershipID, s.Clock.Now())
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

func (s *Service) SendTestNotification(ctx context.Context, p *auth.Principal, channel, recipient, message string) (map[string]any, error) {
	channel = strings.TrimSpace(strings.ToLower(channel))
	if channel == "" {
		channel = "in_app"
	}
	switch channel {
	case "in_app", "email", "sms", "whatsapp":
	default:
		return nil, httpx.BadRequest("invalid_body", "channel must be in_app, email, sms, or whatsapp")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "This is a test notification from your business settings."
	}
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	rec := strings.TrimSpace(recipient)
	id := ids.New()
	if channel != "in_app" {
		if s.Outbound == nil {
			return nil, httpx.ServiceUnavailable("channel_unavailable", "This channel is not set up. Ask an administrator to configure it.")
		}
		if _, err := s.Outbound.CheckAvailable(channel); err != nil {
			return nil, err
		}
		if len(rec) < 5 {
			return nil, httpx.BadRequest("no_recipient", "Enter a valid phone number or email address for this test message")
		}
		snd, _ := s.Outbound.Channels.For(channel)
		if err := snd.Send(ctx, sender.Message{To: rec, Subject: "Test notification", Body: msg}); err != nil {
			return nil, httpx.ServiceUnavailable("delivery_failed", "Test delivery failed: "+err.Error())
		}
		err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
			return s.Audit.Record(ctx, tx, p, "notification.test_sent", "notifications", id, nil, map[string]any{
				"channel": channel, "recipient": rec,
			})
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "channel": channel, "recipient": rec, "message": msg, "ok": true}, nil
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.notifications (id, tenant_id, recipient_id, notification_type, title, body, link_path, dedupe_key, created_at)
			VALUES ($1,$2,$3,'day_closed'::app.notification_type,$4,$5,'/settings',$6,$7)
		`, id, p.TenantID, p.MembershipID, "Test notification ("+channel+")", msg, "test:"+id.String(), s.Clock.Now()); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "notification.test_sent", "notifications", id, nil, map[string]any{
			"channel": channel, "recipient": rec,
		})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "channel": channel, "recipient": rec, "message": msg, "ok": true}, nil
}

// ---------- audit ----------

type AuditFilter struct {
	UserID     *uuid.UUID
	Action     string
	EntityType string
	EntityID   *uuid.UUID
	From, To   string
	BranchID   *uuid.UUID
}

func (s *Service) AuditLogs(ctx context.Context, p *auth.Principal, f AuditFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(f.UserID != nil, "a.actor_user_id = ?", f.UserID)
		q.If(f.Action != "", "a.action LIKE ?", strings.ReplaceAll(strings.ReplaceAll(f.Action, "%", ""), "_", "\\_")+"%")
		q.If(f.EntityType != "", "a.entity_table = ?", f.EntityType)
		q.If(f.EntityID != nil, "a.entity_id = ?", f.EntityID)
		q.If(f.From != "", "a.occurred_at >= ?::date", f.From)
		q.If(f.To != "", "a.occurred_at < ?::date + 1", f.To)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit.audit_log a`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.action, COALESCE(a.entity_table, ''), a.entity_id, a.actor_user_id, u.full_name::text,
			       a.old_values, a.new_values, host(a.client_ip), a.request_id, a.occurred_at
			FROM audit.audit_log a LEFT JOIN iam.users u ON u.id = a.actor_user_id`+q.Where()+` ORDER BY a.occurred_at DESC, a.seq DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var entity, user *uuid.UUID
			var action, typ string
			var userName, ip, reqID *string
			var before, after []byte
			var created time.Time
			if err := rows.Scan(&id, &action, &typ, &entity, &user, &userName, &before, &after, &ip, &reqID, &created); err != nil {
				return err
			}
			row := map[string]any{
				"id": id, "action": action, "entity_type": typ, "entity_id": entity, "user_id": user, "user_name": userName,
				"branch_id": nil, "ip": ip, "request_id": reqID, "created_at": created, "before": nil, "after": nil,
			}
			if len(before) > 0 {
				row["before"] = json.RawMessage(before)
			}
			if len(after) > 0 {
				row["after"] = json.RawMessage(after)
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	return out, total, err
}

// ---------- backups and exports ----------

func (s *Service) StartExport(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	id := ids.New()
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var running int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.backup_runs WHERE tenant_id = $1 AND backup_kind = 'tenant_export' AND status = 'running'`, p.TenantID).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return httpx.Conflict("invalid_status", "A data export is already in progress")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.backup_runs (id, backup_kind, tenant_id, status, started_at)
			VALUES ($1, 'tenant_export', $2, 'running', $3)
		`, id, p.TenantID, s.Clock.Now()); err != nil {
			return err
		}
		if err := jobs.EnqueueTx(ctx, tx, p.TenantID, jobs.BusinessExport, map[string]any{
			"export_id":     id,
			"user_id":       p.UserID,
			"membership_id": p.MembershipID,
		}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "backup.export_requested", "backup_runs", id, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "queued", "status_url": "/api/v1/exports/" + id.String()}, nil
}

func mapBackupStatus(dbStatus string) string {
	if dbStatus == "succeeded" {
		return "done"
	}
	return dbStatus
}

func (s *Service) Backups(ctx context.Context, p *auth.Principal, page httpx.Page) (map[string]any, int, error) {
	list := []map[string]any{}
	total := 0
	var last *time.Time
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.backup_runs WHERE tenant_id = $1 AND backup_kind = 'tenant_export'`, p.TenantID).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT br.id, br.status::text, m.user_id, u.full_name::text, f.id, br.error, br.started_at, br.finished_at
			FROM app.backup_runs br
			LEFT JOIN app.files f ON f.tenant_id = br.tenant_id AND f.storage_key = br.storage_uri
			LEFT JOIN app.memberships m ON m.tenant_id = f.tenant_id AND m.id = f.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE br.tenant_id = $1 AND br.backup_kind = 'tenant_export'
			ORDER BY br.started_at DESC LIMIT $2 OFFSET $3
		`, p.TenantID, page.PerPage, page.Offset)
		if err != nil {
			return err
		}
		for rows.Next() {
			row, err := scanExport(rows, true)
			if err != nil {
				rows.Close()
				return err
			}
			list = append(list, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if raw := setting.String(ctx, tx, "backup.last_automatic", ""); raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				last = &t
			}
		}
		return nil
	})
	return map[string]any{"exports": list, "last_automatic_backup_at": last}, total, err
}

func scanExport(rows pgx.Row, withUser bool) (map[string]any, error) {
	var id uuid.UUID
	var status string
	var user, file *uuid.UUID
	var userName, errText *string
	var created time.Time
	var finished *time.Time
	if err := rows.Scan(&id, &status, &user, &userName, &file, &errText, &created, &finished); err != nil {
		return nil, err
	}
	apiStatus := mapBackupStatus(status)
	row := map[string]any{
		"id": id, "status": apiStatus, "file_id": file, "error": errText,
		"created_at": created, "finished_at": finished, "download_url": nil,
	}
	if withUser {
		row["user_id"], row["user_name"] = user, userName
	}
	if file != nil && apiStatus == "done" {
		row["download_url"] = "/api/v1/files/" + file.String()
	}
	return row, nil
}

// Export returns an export job's status. Only callers who manage backups may see it.
func (s *Service) Export(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	if !p.Has("security.backup.manage") && !p.IsOwner {
		return nil, httpx.NotFound("not_found", "Export not found")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		row, err := scanExport(tx.QueryRow(ctx, `
			SELECT br.id, br.status::text, m.user_id, u.full_name::text, f.id, br.error, br.started_at, br.finished_at
			FROM app.backup_runs br
			LEFT JOIN app.files f ON f.tenant_id = br.tenant_id AND f.storage_key = br.storage_uri
			LEFT JOIN app.memberships m ON m.tenant_id = f.tenant_id AND m.id = f.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE br.tenant_id = $1 AND br.id = $2
		`, p.TenantID, id), false)
		if err != nil {
			return httpx.NotFound("not_found", "Export not found")
		}
		row["kind"] = "business_export"
		out = row
		return nil
	})
	return out, err
}
