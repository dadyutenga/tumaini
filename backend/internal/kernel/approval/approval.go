// Package approval manages app.approval_requests.
//
// The approved model has seven approval types (discount, job price change, sale void, transaction
// correction, payment void, day reopen, product price change). Other decisions, such as approving an
// expense, a leave request or a payroll run, are state changes on the record itself and are made on
// that record's screen, not here.
//
// The database enforces who may approve (app.tg_approval_decide calls app.assert_can_approve, which
// checks the permission and the authority limit), that nobody approves their own request, and that
// the guarded action (voiding a sale, changing a price, reopening a day) is allowed only while an
// approved request exists. This package files requests, records decisions, runs the optional hook that
// performs the approved action, and tells the people involved.
package approval

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

type Request struct {
	ID         uuid.UUID
	BusinessID uuid.UUID
	BranchID   *uuid.UUID
	// Kind is the app.approval_type value.
	Kind        string
	SubjectType string
	SubjectID   uuid.UUID
	Amount      decimal.NullDecimal
	Percent     decimal.NullDecimal
	Summary     string
	Payload     []byte
	Status      string
	// RequestedBy is the requester's user id; RequestedByMembership is their seat.
	RequestedBy           uuid.UUID
	RequestedByMembership uuid.UUID
	// Note is the decision note, set while hooks run.
	Note string
}

// column is the app.approval_requests column each subject type points through.
var column = map[string]string{
	"sale": "sale_id", "project": "project_id", "design_job": "design_job_id", "transaction": "financial_transaction_id",
	"payment_received": "payment_received_id", "payment_made": "payment_made_id", "day_closing": "day_closing_id", "product": "product_id",
}

// KindPermission is the permission a person needs to decide a request of that type.
var KindPermission = map[string]string{
	"sale_discount": "shop.discount.approve", "job_price_change": "services.price.approve", "sale_void": "shop.sale.void",
	"transaction_correction": "finance.transaction.correct", "payment_void": "finance.payment.void",
	"day_reopen": "finance.day.reopen", "product_price_change": "shop.product.price_approve",
}

// legacyKind maps the kind names modules used before the schema replacement.
var legacyKind = map[string]string{
	"discount": "sale_discount", "price_change": "job_price_change", "sale_void": "sale_void", "payment_void": "payment_void",
}

type Hooks struct {
	OnApproved func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req Request) error
	OnRejected func(ctx context.Context, tx pgx.Tx, p *auth.Principal, req Request) error
}

type Service struct {
	hooks  map[string]Hooks
	notify *notify.Service
}

func New(n *notify.Service) *Service {
	return &Service{hooks: map[string]Hooks{}, notify: n}
}

// Register installs the hook for an approval type (app.approval_type value).
func (s *Service) Register(kind string, h Hooks) {
	if k, ok := legacyKind[kind]; ok {
		kind = k
	}
	s.hooks[kind] = h
}

type Input struct {
	BranchID *uuid.UUID
	// Kind is the approval type; SubjectType and SubjectID say what it is about.
	Kind        string
	SubjectType string
	SubjectID   uuid.UUID
	// Amount is the requested amount (a new price, a correction); Percent is a requested discount.
	Amount decimal.NullDecimal
	// Current is the value being replaced (the old price), used to size a price-change request.
	Current decimal.NullDecimal
	Percent decimal.NullDecimal
	Summary string
	Payload any
}

// Request files an approval request and notifies the people who can decide it.
func (s *Service) Request(ctx context.Context, tx pgx.Tx, p *auth.Principal, in Input) (uuid.UUID, error) {
	kind := in.Kind
	if k, ok := legacyKind[kind]; ok {
		kind = k
	}
	if _, ok := KindPermission[kind]; !ok {
		return uuid.Nil, httpx.BadRequest("approval_not_supported", "That action does not use an approval request")
	}
	col, ok := column[in.SubjectType]
	if !ok {
		return uuid.Nil, httpx.BadRequest("approval_not_supported", "That kind of record cannot have an approval request")
	}
	id := uuid.New()
	var amount, percent, current any
	if in.Amount.Valid {
		amount = in.Amount.Decimal
	}
	if in.Percent.Valid {
		percent = in.Percent.Decimal
	}
	if in.Current.Valid {
		current = in.Current.Decimal
	}
	reason := in.Summary
	if len(reason) < 3 {
		reason = "Approval requested"
	}
	// col comes from the fixed column map above, never from the request.
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.approval_requests (id, tenant_id, approval_type, reason, requested_amount, requested_percent, current_amount, `+col+`)
		VALUES ($1,$2,$3::app.approval_type,$4,$5,$6,$7,$8)`,
		id, p.TenantID, kind, reason, amount, percent, current, in.SubjectID); err != nil {
		return uuid.Nil, err
	}
	var amt *decimal.Decimal
	if in.Amount.Valid {
		amt = &in.Amount.Decimal
	}
	_ = s.notify.ToApprovers(ctx, tx, p.TenantID, in.BranchID, KindPermission[kind], amt, p.UserID, notify.Message{
		Type: "approval_waiting", Title: "Approval needed", Body: reason, Link: "/approvals/" + id.String(),
		EntityType: in.SubjectType, EntityID: &in.SubjectID,
	})
	return id, nil
}

// Decide records the decision. The database refuses (BM007) when the decider lacks the permission or
// the amount is above their authority limit, and refuses a self-approval.
func (s *Service) Decide(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, approve bool, note string) error {
	req, err := s.lock(ctx, tx, id)
	if err != nil {
		return err
	}
	if req.Status != "pending" {
		return httpx.Conflict("invalid_status", "This request is no longer pending")
	}
	if req.RequestedByMembership == p.MembershipID {
		return httpx.Forbidden("cannot_approve_own", "You cannot approve your own request")
	}
	status := "rejected"
	if approve {
		status = "approved"
	} else if len(note) < 1 {
		return httpx.BadRequest("note_required", "Say why the request is rejected")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app.approval_requests SET status = $2::app.approval_status, decision_note = NULLIF($3,'') WHERE id = $1`, id, status, note); err != nil {
		return err
	}
	req.Status, req.Note = status, note
	hooks := s.hooks[req.Kind]
	if approve && hooks.OnApproved != nil {
		if err := hooks.OnApproved(ctx, tx, p, req); err != nil {
			return err
		}
	} else if !approve && hooks.OnRejected != nil {
		if err := hooks.OnRejected(ctx, tx, p, req); err != nil {
			return err
		}
	}
	return s.notify.ToUser(ctx, tx, p.TenantID, req.RequestedBy, notify.Message{
		Type: "approval_decided", Title: "Request " + status, Body: req.Summary, Link: "/approvals/" + id.String(),
		EntityType: req.SubjectType, EntityID: &req.SubjectID,
	})
}

// Cancel lets the requester withdraw a pending request.
func (s *Service) Cancel(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) error {
	req, err := s.lock(ctx, tx, id)
	if err != nil {
		return err
	}
	if req.RequestedByMembership != p.MembershipID {
		return httpx.Forbidden("permission_denied", "Only the requester can cancel this request")
	}
	if req.Status != "pending" {
		return httpx.Conflict("invalid_status", "This request is no longer pending")
	}
	if _, err := tx.Exec(ctx, `UPDATE app.approval_requests SET status = 'cancelled' WHERE id = $1`, id); err != nil {
		return err
	}
	req.Status = "cancelled"
	if h, ok := s.hooks[req.Kind]; ok && h.OnRejected != nil {
		return h.OnRejected(ctx, tx, p, req)
	}
	return nil
}

// PendingFor returns the pending request for a subject, if any.
func (s *Service) PendingFor(ctx context.Context, tx pgx.Tx, subjectType string, subjectID uuid.UUID) (*uuid.UUID, error) {
	var cols string
	switch subjectType {
	case "payment":
		cols = "payment_received_id = $1 OR payment_made_id = $1"
	default:
		c, ok := column[subjectType]
		if !ok {
			return nil, nil
		}
		cols = c + " = $1"
	}
	var id uuid.UUID
	// cols comes from the fixed column map above.
	err := tx.QueryRow(ctx, `SELECT id FROM app.approval_requests WHERE (`+cols+`) AND status = 'pending' LIMIT 1`, subjectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Service) lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Request, error) {
	return s.read(ctx, tx, id, " FOR UPDATE OF a")
}

func (s *Service) read(ctx context.Context, tx pgx.Tx, id uuid.UUID, suffix string) (Request, error) {
	var r Request
	var sale, project, job, txn, pr, pm, day, product *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT a.id, a.tenant_id, a.approval_type::text, a.requested_amount, a.requested_percent, a.reason, a.status::text,
		       a.requested_by, m.user_id, a.sale_id, a.project_id, a.design_job_id, a.financial_transaction_id,
		       a.payment_received_id, a.payment_made_id, a.day_closing_id, a.product_id
		FROM app.approval_requests a JOIN app.memberships m ON m.tenant_id = a.tenant_id AND m.id = a.requested_by
		WHERE a.id = $1`+suffix, id).Scan(&r.ID, &r.BusinessID, &r.Kind, &r.Amount, &r.Percent, &r.Summary, &r.Status,
		&r.RequestedByMembership, &r.RequestedBy, &sale, &project, &job, &txn, &pr, &pm, &day, &product)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, httpx.NotFound("not_found", "Approval request not found")
	}
	if err != nil {
		return Request{}, err
	}
	for typ, v := range map[string]*uuid.UUID{"sale": sale, "project": project, "design_job": job, "transaction": txn,
		"payment_received": pr, "payment_made": pm, "day_closing": day, "product": product} {
		if v != nil {
			r.SubjectType, r.SubjectID = typ, *v
		}
	}
	return r, nil
}

func (s *Service) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Request, error) {
	return s.read(ctx, tx, id, "")
}
