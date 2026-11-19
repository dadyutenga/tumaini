package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Approval *approval.Service
	Audit    *audit.Service
	Clock    clock.Clock
}

func New(database *db.DB, ap *approval.Service, aud *audit.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Approval: ap, Audit: aud, Clock: clk}
}

// ---------- approvals ----------

// subjectLinks maps a subject type to the API path of the record, for the "link to the record" (FR-APR-02).
var subjectLinks = map[string]string{
	"sale": "/sales/", "sale_return": "/sale-returns/", "expense": "/expenses/", "stock_adjustment": "/stock-adjustments/",
	"purchase_order": "/purchase-orders/", "loan": "/loans/", "leave_request": "/leave-requests/", "attendance_day": "/attendance/days/",
	"payroll_run": "/payroll-runs/", "payment": "/payments/", "customer": "/customers/",
}

// decidable returns the condition under which the caller may decide a request:
// pending, not their own (unless Owner), in a branch inside their approvals.decide scope.
func decidable(p *auth.Principal, q *sqlq.Q) string {
	if p.IsOwner {
		return "ar.status = 'pending'"
	}
	if _, ok := p.Can("approvals.decide"); !ok {
		return "false"
	}
	cond := "ar.status = 'pending' AND ar.requested_by <> " + q.Arg(p.UserID)
	if b := p.ScopeBranches("approvals.decide"); b != nil {
		cond += " AND (ar.branch_id IS NULL OR ar.branch_id = ANY(" + q.Arg(b) + "))"
	}
	// The same rules Decide applies: the amount is within approvals.decide's limit, and for kinds that
	// have their own permission (payroll, loans, expenses, ...) the caller holds it with a limit that covers it.
	if g, ok := p.Can("approvals.decide"); ok && g.Limit != nil {
		cond += " AND (ar.amount IS NULL OR ar.amount <= " + q.Arg(*g.Limit) + ")"
	}
	kinds := make([]string, 0, len(approval.KindPermission))
	perms := make([]string, 0, len(approval.KindPermission))
	for k, perm := range approval.KindPermission {
		kinds, perms = append(kinds, k), append(perms, perm)
	}
	ks, ps, role := q.Arg(kinds), q.Arg(perms), q.Arg(p.RoleID)
	cond += ` AND (NOT EXISTS (SELECT 1 FROM unnest(` + ks + `::text[], ` + ps + `::text[]) m(k, pm) WHERE m.k = ar.kind)
		OR EXISTS (SELECT 1 FROM unnest(` + ks + `::text[], ` + ps + `::text[]) m(k, pm)
		           JOIN role_permissions rp ON rp.permission_key = m.pm AND rp.role_id = ` + role + `
		           WHERE m.k = ar.kind AND (rp.approval_limit IS NULL OR ar.amount IS NULL OR rp.approval_limit >= ar.amount)))`
	return cond
}

type ApprovalFilter struct {
	Status string
	Kind   string
	Mine   bool
}

func (s *Service) List(ctx context.Context, p *auth.Principal, f ApprovalFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		can := decidable(p, q)
		mine := "ar.requested_by = " + q.Arg(p.UserID)
		// can is always referenced so every bound parameter appears in the query.
		visible := "(" + mine + " OR ((" + can + ") AND false))"
		if !f.Mine {
			// Decided history stays visible to approvers in scope, so the inbox history is searchable (FR-APR-05).
			hist := "false"
			if p.IsOwner {
				hist = "true"
			} else if _, ok := p.Can("approvals.decide"); ok {
				hist = "true"
				if b := p.ScopeBranches("approvals.decide"); b != nil {
					hist = "(ar.branch_id IS NULL OR ar.branch_id = ANY(" + q.Arg(b) + "))"
				}
			}
			visible = "(" + mine + " OR (" + can + ") OR (ar.status <> 'pending' AND " + hist + "))"
		}
		q.Add(visible)
		q.If(f.Status != "", "ar.status = ?", f.Status)
		q.If(f.Kind != "", "ar.kind = ?", f.Kind)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM approval_requests ar`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		sql := `
			SELECT ar.id, ar.kind, ar.subject_type, ar.subject_id, ar.amount, ar.summary, ar.status, ar.branch_id,
			       ar.requested_by, u.name, ar.decided_by, d.name, ar.decided_at, ar.decision_note, ar.created_at, (` + can + `)
			FROM approval_requests ar
			JOIN users u ON u.id = ar.requested_by
			LEFT JOIN users d ON d.id = ar.decided_by` + q.Where() + ` ORDER BY ar.created_at DESC` + q.Page(page.PerPage, page.Offset)
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
	var branch, decidedBy *uuid.UUID
	var kind, subjectType, summary, status, byName string
	var decidedName, note *string
	var amount decimal.NullDecimal
	var decidedAt *time.Time
	var created time.Time
	var canDecide bool
	if err := rows.Scan(&id, &kind, &subjectType, &subject, &amount, &summary, &status, &branch, &by, &byName, &decidedBy, &decidedName, &decidedAt, &note, &created, &canDecide); err != nil {
		return nil, err
	}
	row := map[string]any{
		"id": id, "kind": kind, "subject_type": subjectType, "subject_id": subject, "summary": summary, "status": status,
		"branch_id": branch, "requested_by": by, "requested_by_name": byName, "decided_by": decidedBy, "decided_by_name": decidedName,
		"decided_at": decidedAt, "decision_note": note, "created_at": created, "can_decide": canDecide, "amount": nil,
	}
	if amount.Valid {
		row["amount"] = amount.Decimal
	}
	if base, ok := subjectLinks[subjectType]; ok {
		row["subject_link"] = "/api/v1" + base + subject.String()
	}
	return row, nil
}

// Count returns pending requests the caller may decide, for the badge.
func (s *Service) Count(ctx context.Context, p *auth.Principal) (int, error) {
	n := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		return tx.QueryRow(ctx, `SELECT count(*) FROM approval_requests ar WHERE `+decidable(p, q), q.Args...).Scan(&n)
	})
	return n, err
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		can := decidable(p, q)
		row, err := scanApproval(tx.QueryRow(ctx, `
			SELECT ar.id, ar.kind, ar.subject_type, ar.subject_id, ar.amount, ar.summary, ar.status, ar.branch_id,
			       ar.requested_by, u.name, ar.decided_by, d.name, ar.decided_at, ar.decision_note, ar.created_at, (`+can+`)
			FROM approval_requests ar
			JOIN users u ON u.id = ar.requested_by
			LEFT JOIN users d ON d.id = ar.decided_by
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
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT payload FROM approval_requests WHERE id=$1`, id).Scan(&payload); err != nil {
			return err
		}
		if len(payload) > 0 {
			row["payload"] = json.RawMessage(payload)
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
	if _, ok := p.Can("approvals.decide"); !ok {
		return false
	}
	if b, ok := row["branch_id"].(*uuid.UUID); ok && b != nil {
		return p.SeesBranch("approvals.decide", *b)
	}
	return true
}

// Decide approves or rejects inside one transaction with the subject's hook.
// Approve is idempotent: a retry with the same key replays the first response.
func (s *Service) Decide(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool, note string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
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
		if err := s.Audit.Record(ctx, tx, p, "approval."+state, "approval_request", id, nil, map[string]any{"note": note}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "status": state}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) Cancel(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.Approval.Cancel(ctx, tx, p, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "approval.cancelled", "approval_request", id, nil, nil)
	})
}

const pinMaxFailures = 5

// ApproveWithPIN lets a manager approve on the cashier's screen (FR-APR-07).
// The approver is identified by phone or email and proves presence with a PIN.
// The decision is recorded as the approver's, never the cashier's.
func (s *Service) ApproveWithPIN(ctx context.Context, p *auth.Principal, id uuid.UUID, identifier, pin, note string) (int, []byte, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || pin == "" {
		return 0, nil, httpx.BadRequest("invalid_body", "Approver and PIN are required")
	}
	var approverID uuid.UUID
	var hash *string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT id, pin_hash FROM users
			WHERE is_active AND (phone = $1 OR email = $1::citext) AND (locked_until IS NULL OR locked_until < $2)
		`, identifier, s.Clock.Now()).Scan(&approverID, &hash)
		if err != nil {
			return httpx.Unauthorized("invalid_credentials", "Approver or PIN is not correct")
		}
		var failures int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM login_history
			WHERE user_id = $1 AND reason = 'approval_pin' AND NOT success AND created_at > $2
		`, approverID, s.Clock.Now().Add(-15*time.Minute)).Scan(&failures); err != nil {
			return err
		}
		if failures >= pinMaxFailures {
			return httpx.TooMany("too_many_requests", "Too many wrong PINs for this approver. Wait 15 minutes.")
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	ok := hash != nil && auth.VerifyPassword(*hash, pin)
	// Record the attempt in its own transaction so a failure is counted even though the request fails.
	if err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var ip any
		if p.IP != "" {
			ip = p.IP
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO login_history (id, business_id, user_id, identifier, success, reason, ip, user_agent)
			VALUES ($1,$2,$3,$4,$5,'approval_pin',$6::inet,$7)
		`, ids.New(), p.BusinessID, approverID, identifier, ok, ip, p.UserAgent)
		return err
	}); err != nil {
		return 0, nil, err
	}
	if !ok {
		return 0, nil, httpx.Unauthorized("invalid_credentials", "Approver or PIN is not correct")
	}
	approver, err := s.principal(ctx, p, approverID)
	if err != nil {
		return 0, nil, err
	}
	if _, ok := approver.Can("approvals.decide"); !ok && !approver.IsOwner {
		return 0, nil, httpx.Forbidden("permission_denied", "That user cannot approve requests")
	}
	return s.Decide(ctx, approver, id, true, note)
}

// principal loads another user of the same business as an acting principal,
// keeping the request's IP and request ID for the audit trail.
func (s *Service) principal(ctx context.Context, caller *auth.Principal, userID uuid.UUID) (*auth.Principal, error) {
	a := &auth.Principal{UserID: userID, BusinessID: caller.BusinessID, Perms: map[string]auth.Grant{}, IP: caller.IP, UserAgent: caller.UserAgent, RequestID: caller.RequestID, BranchID: caller.BranchID}
	err := s.DB.InTx(ctx, caller.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var slug string
		if err := tx.QueryRow(ctx, `SELECT u.role_id, u.name, r.slug FROM users u JOIN roles r ON r.id=u.role_id WHERE u.id=$1`, userID).Scan(&a.RoleID, &a.Name, &slug); err != nil {
			return err
		}
		a.IsOwner, a.RoleSlug = slug == "owner", slug
		rows, err := tx.Query(ctx, `SELECT permission_key, scope, approval_limit FROM role_permissions WHERE role_id=$1`, a.RoleID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k, scope string
			var limit *decimal.Decimal
			if err := rows.Scan(&k, &scope, &limit); err != nil {
				rows.Close()
				return err
			}
			a.Perms[k] = auth.Grant{Scope: scope, Limit: limit}
		}
		rows.Close()
		brows, err := tx.Query(ctx, `SELECT branch_id FROM user_branches WHERE user_id=$1`, userID)
		if err != nil {
			return err
		}
		defer brows.Close()
		for brows.Next() {
			var b uuid.UUID
			if err := brows.Scan(&b); err != nil {
				return err
			}
			a.BranchIDs = append(a.BranchIDs, b)
		}
		return brows.Err()
	})
	if err != nil {
		return nil, err
	}
	if a.BranchID != nil && !a.AllowsBranch(*a.BranchID) {
		return nil, httpx.Forbidden("branch_forbidden", "That approver is not assigned to this branch")
	}
	return a, nil
}

// ---------- notifications ----------

func (s *Service) Notifications(ctx context.Context, p *auth.Principal, unread bool, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New().Add("user_id = ? AND in_app", p.UserID).If(unread, "read_at IS NULL")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notifications`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, type, title, body, link, entity_type, entity_id, read_at, created_at
			FROM notifications`+q.Where()+` ORDER BY created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var typ, title, body string
			var link, entityType *string
			var entity *uuid.UUID
			var read *time.Time
			var created time.Time
			if err := rows.Scan(&id, &typ, &title, &body, &link, &entityType, &entity, &read, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "type": typ, "title": title, "body": body, "link": link, "entity_type": entityType, "entity_id": entity, "read_at": read, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) UnreadCount(ctx context.Context, p *auth.Principal) (int, error) {
	n := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL AND in_app`, p.UserID).Scan(&n)
	})
	return n, err
}

func (s *Service) MarkRead(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, $3) WHERE id=$1 AND user_id=$2`, id, p.UserID, s.Clock.Now())
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE notifications SET read_at=$2 WHERE user_id=$1 AND read_at IS NULL`, p.UserID, s.Clock.Now())
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
	case "in_app", "email", "sms":
	default:
		return nil, httpx.BadRequest("invalid_body", "channel must be in_app, email, or sms")
	}
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "This is a test notification from your business settings."
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO notifications (id, business_id, user_id, type, title, body, link, in_app, created_at)
			VALUES ($1,$2,$3,'system_test',$4,$5,'/settings',true,$6)
		`, id, p.BusinessID, p.UserID, "Test notification ("+channel+")", msg, s.Clock.Now()); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "notification.test_sent", "notification", id, nil, map[string]any{
			"channel": channel, "recipient": strings.TrimSpace(recipient),
		})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "channel": channel, "recipient": strings.TrimSpace(recipient), "message": msg, "ok": true}, nil
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(f.UserID != nil, "a.user_id = ?", f.UserID)
		q.If(f.Action != "", "a.action LIKE ?", strings.ReplaceAll(strings.ReplaceAll(f.Action, "%", ""), "_", "\\_")+"%")
		q.If(f.EntityType != "", "a.entity_type = ?", f.EntityType)
		q.If(f.EntityID != nil, "a.entity_id = ?", f.EntityID)
		q.If(f.From != "", "a.created_at >= ?::date", f.From)
		q.If(f.To != "", "a.created_at < ?::date + 1", f.To)
		q.If(f.BranchID != nil, "a.branch_id = ?", f.BranchID)
		if b := p.ScopeBranches("audit.view"); b != nil && f.BranchID == nil {
			q.Add("(a.branch_id IS NULL OR a.branch_id = ANY(?))", b)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs a`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.action, a.entity_type, a.entity_id, a.user_id, u.name, a.branch_id, a.before, a.after, host(a.ip), a.request_id, a.created_at
			FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id`+q.Where()+` ORDER BY a.created_at DESC, a.id DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var entity, user, branch *uuid.UUID
			var action, typ string
			var userName, ip, reqID *string
			var before, after []byte
			var created time.Time
			if err := rows.Scan(&id, &action, &typ, &entity, &user, &userName, &branch, &before, &after, &ip, &reqID, &created); err != nil {
				return err
			}
			row := map[string]any{"id": id, "action": action, "entity_type": typ, "entity_id": entity, "user_id": user, "user_name": userName,
				"branch_id": branch, "ip": ip, "request_id": reqID, "created_at": created, "before": nil, "after": nil}
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var running int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM export_jobs WHERE kind='business_export' AND status IN ('queued','running')`).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return httpx.Conflict("invalid_status", "A data export is already in progress")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO export_jobs (id, business_id, user_id, kind, params) VALUES ($1,$2,$3,'business_export','{}')`, id, p.BusinessID, p.UserID); err != nil {
			return err
		}
		if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.BusinessExport, map[string]any{"export_id": id}); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "backup.export_requested", "export_job", id, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "queued", "status_url": "/api/v1/exports/" + id.String()}, nil
}

func (s *Service) Backups(ctx context.Context, p *auth.Principal, page httpx.Page) (map[string]any, int, error) {
	list := []map[string]any{}
	total := 0
	var last *time.Time
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM export_jobs WHERE kind='business_export'`).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.status, e.user_id, u.name, e.file_id, e.error, e.created_at, e.finished_at
			FROM export_jobs e JOIN users u ON u.id=e.user_id
			WHERE e.kind='business_export' ORDER BY e.created_at DESC LIMIT $1 OFFSET $2
		`, page.PerPage, page.Offset)
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
		// The nightly pg_dump runs on the host (FR-SEC-07). The host job records
		// its completion through the backup.last_automatic setting; absent means never.
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key='backup.last_automatic'`).Scan(&raw); err == nil {
			var t time.Time
			if json.Unmarshal(raw, &t) == nil {
				last = &t
			}
		}
		return nil
	})
	return map[string]any{"exports": list, "last_automatic_backup_at": last}, total, err
}

func scanExport(rows pgx.Row, withUser bool) (map[string]any, error) {
	var id, user uuid.UUID
	var status, userName string
	var file *uuid.UUID
	var errText *string
	var created time.Time
	var finished *time.Time
	if err := rows.Scan(&id, &status, &user, &userName, &file, &errText, &created, &finished); err != nil {
		return nil, err
	}
	row := map[string]any{"id": id, "status": status, "file_id": file, "error": errText, "created_at": created, "finished_at": finished, "download_url": nil}
	if withUser {
		row["user_id"], row["user_name"] = user, userName
	}
	if file != nil && status == "done" {
		row["download_url"] = "/api/v1/files/" + file.String()
	}
	return row, nil
}

// Export returns an export job's status. Only the requester or the Owner may see it.
func (s *Service) Export(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := scanExport(tx.QueryRow(ctx, `
			SELECT e.id, e.status, e.user_id, u.name, e.file_id, e.error, e.created_at, e.finished_at
			FROM export_jobs e JOIN users u ON u.id=e.user_id WHERE e.id=$1 AND (e.user_id=$2 OR $3)
		`, id, p.UserID, p.IsOwner), false)
		if err != nil {
			return httpx.NotFound("not_found", "Export not found")
		}
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM export_jobs WHERE id=$1`, id).Scan(&kind); err != nil {
			return err
		}
		row["kind"] = kind
		out = row
		return nil
	})
	return out, err
}
