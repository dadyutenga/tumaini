package approval

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
)

type Request struct {
	ID          uuid.UUID
	BusinessID  uuid.UUID
	BranchID    *uuid.UUID
	Kind        string
	SubjectType string
	SubjectID   uuid.UUID
	Amount      decimal.NullDecimal
	Summary     string
	Payload     json.RawMessage
	Status      string
	RequestedBy uuid.UUID
	// Note is the decision note, set while hooks run.
	Note string
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

func (s *Service) Register(kind string, h Hooks) { s.hooks[kind] = h }

type Input struct {
	BranchID    *uuid.UUID
	Kind        string
	SubjectType string
	SubjectID   uuid.UUID
	Amount      decimal.NullDecimal
	Summary     string
	Payload     any
}

func (s *Service) Request(ctx context.Context, tx pgx.Tx, p *auth.Principal, in Input) (uuid.UUID, error) {
	id := ids.New()
	var payload []byte
	var err error
	if in.Payload != nil {
		payload, err = json.Marshal(in.Payload)
		if err != nil {
			return uuid.Nil, err
		}
	}
	var branch any
	if in.BranchID != nil {
		branch = *in.BranchID
	}
	var amount any
	if in.Amount.Valid {
		amount = in.Amount.Decimal
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO approval_requests (id, business_id, branch_id, kind, subject_type, subject_id, amount, summary, payload, requested_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, p.BusinessID, branch, in.Kind, in.SubjectType, in.SubjectID, amount, in.Summary, payload, p.UserID); err != nil {
		return uuid.Nil, err
	}
	_ = s.notify.ToPermission(ctx, tx, p.BusinessID, in.BranchID, "approvals.decide", notify.Message{
		Type: "approval_request", Title: "Approval needed", Body: in.Summary,
		Link: "/approvals/" + id.String(), EntityType: in.SubjectType, EntityID: &in.SubjectID,
	})
	return id, nil
}

func (s *Service) Decide(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, approve bool, note string) error {
	req, err := s.lock(ctx, tx, id)
	if err != nil {
		return err
	}
	if req.Status != "pending" {
		return httpx.Conflict("invalid_status", "This request is no longer pending")
	}
	if req.RequestedBy == p.UserID && !p.IsOwner {
		return httpx.Forbidden("cannot_approve_own", "You cannot approve your own request")
	}
	g, ok := p.Can("approvals.decide")
	if !ok && !p.IsOwner {
		return httpx.Forbidden("permission_denied", "You cannot decide this request")
	}
	if req.BranchID != nil && !p.SeesBranch("approvals.decide", *req.BranchID) {
		return httpx.NotFound("not_found", "Approval request not found")
	}
	if approve && !p.IsOwner && req.Amount.Valid && !auth.WithinLimit(g, ok, req.Amount.Decimal) {
		return httpx.Forbidden("over_limit", "This amount is above your approval limit")
	}
	hooks, ok := s.hooks[req.Kind]
	if !ok {
		return httpx.Conflict("invalid_status", "This approval type is not configured")
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE approval_requests SET status = $2, decided_by = $3, decided_at = now(), decision_note = NULLIF($4,'')
		WHERE id = $1
	`, id, status, p.UserID, note); err != nil {
		return err
	}
	req.Status = status
	req.Note = note
	if approve {
		if hooks.OnApproved != nil {
			if err := hooks.OnApproved(ctx, tx, p, req); err != nil {
				return err
			}
		}
	} else if hooks.OnRejected != nil {
		if err := hooks.OnRejected(ctx, tx, p, req); err != nil {
			return err
		}
	}
	return s.notify.ToUser(ctx, tx, p.BusinessID, req.RequestedBy, notify.Message{
		Type: "approval_decision", Title: "Request " + status, Body: req.Summary,
		Link: "/approvals/" + id.String(), EntityType: req.SubjectType, EntityID: &req.SubjectID,
	})
}

// Cancel lets the requester withdraw a pending request. The subject is
// returned to its previous state through the OnRejected hook.
func (s *Service) Cancel(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) error {
	req, err := s.lock(ctx, tx, id)
	if err != nil {
		return err
	}
	if req.RequestedBy != p.UserID {
		return httpx.Forbidden("permission_denied", "Only the requester can cancel this request")
	}
	if req.Status != "pending" {
		return httpx.Conflict("invalid_status", "This request is no longer pending")
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_requests SET status='cancelled', decided_by=$2, decided_at=now() WHERE id=$1`, id, p.UserID); err != nil {
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
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM approval_requests WHERE subject_type=$1 AND subject_id=$2 AND status='pending' LIMIT 1`, subjectType, subjectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Service) lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Request, error) {
	return s.read(ctx, tx, id, " FOR UPDATE")
}

func (s *Service) load(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Request, error) {
	return s.read(ctx, tx, id, "")
}

func (s *Service) read(ctx context.Context, tx pgx.Tx, id uuid.UUID, suffix string) (Request, error) {
	var r Request
	var branch *uuid.UUID
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT id, business_id, branch_id, kind, subject_type, subject_id, amount, summary, payload, status, requested_by
		FROM approval_requests WHERE id = $1`+suffix, id).Scan(&r.ID, &r.BusinessID, &branch, &r.Kind, &r.SubjectType, &r.SubjectID, &r.Amount, &r.Summary, &payload, &r.Status, &r.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, httpx.NotFound("not_found", "Approval request not found")
	}
	if err != nil {
		return Request{}, err
	}
	r.BranchID = branch
	r.Payload = payload
	return r, nil
}

func (s *Service) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Request, error) {
	return s.load(ctx, tx, id)
}
