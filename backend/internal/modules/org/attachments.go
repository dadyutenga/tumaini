package org

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
)

type Attachment struct {
	ID             uuid.UUID  `json:"id"`
	EntityType     string     `json:"entity_type"`
	EntityID       uuid.UUID  `json:"entity_id"`
	FileID         uuid.UUID  `json:"file_id"`
	FileName       string     `json:"file_name"`
	MimeType       string     `json:"mime_type"`
	SizeBytes      int64      `json:"size_bytes"`
	Label          *string    `json:"label"`
	UploadedBy     *uuid.UUID `json:"uploaded_by"`
	UploadedByName *string    `json:"uploaded_by_name"`
	CreatedAt      time.Time  `json:"created_at"`
}

type AttachmentIn struct {
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	FileID     uuid.UUID `json:"file_id"`
	Label      string    `json:"label"`
}

// attachmentColumn is the app.attachments column each entity type points through. The table has one
// typed foreign key per owner (exactly one is set), so a file can only be attached to a real record of
// the same tenant. Values are fixed identifiers, never request input.
var attachmentColumn = map[string]string{
	"sale": "sale_id", "customer": "customer_id", "client": "customer_id", "purchase_order": "purchase_id",
	"purchase": "purchase_id", "supplier": "supplier_id", "expense": "expense_id", "employee": "employee_id",
	"project": "project_id", "design_job": "design_job_id", "contractor": "contractor_id",
	"payment": "payment_received_id", "payment_made": "payment_made_id", "task": "task_id",
	"contractor_bill": "contractor_bill_id", "stock_adjustment": "stock_adjustment_id",
}

func (s *Service) ListAttachments(ctx context.Context, p *auth.Principal, entityType string, entityID uuid.UUID) ([]Attachment, error) {
	entityType = strings.TrimSpace(entityType)
	col, ok := attachmentColumn[entityType]
	if !ok {
		return nil, httpx.BadRequest("invalid_query", "Unsupported entity_type")
	}
	out := []Attachment{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.file_id, f.original_name, f.mime_type, f.size_bytes, a.caption, m.user_id, u.full_name::text, a.created_at
			FROM app.attachments a
			JOIN app.files f ON f.tenant_id = a.tenant_id AND f.id = a.file_id
			LEFT JOIN app.memberships m ON m.tenant_id = a.tenant_id AND m.id = a.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE a.tenant_id = $1 AND a.`+col+` = $2
			ORDER BY a.created_at DESC`, p.TenantID, entityID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a := Attachment{EntityType: entityType, EntityID: entityID}
			if err := rows.Scan(&a.ID, &a.FileID, &a.FileName, &a.MimeType, &a.SizeBytes, &a.Label, &a.UploadedBy, &a.UploadedByName, &a.CreatedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) CreateAttachment(ctx context.Context, p *auth.Principal, in AttachmentIn) (Attachment, error) {
	in.EntityType = strings.TrimSpace(in.EntityType)
	col, ok := attachmentColumn[in.EntityType]
	if !ok {
		return Attachment{}, httpx.BadRequest("invalid_body", "Unsupported entity_type")
	}
	if in.EntityID == uuid.Nil || in.FileID == uuid.Nil {
		return Attachment{}, httpx.BadRequest("invalid_body", "entity_id and file_id are required")
	}
	var out Attachment
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var origName, mimeType string
		var size int64
		if err := tx.QueryRow(ctx, `SELECT original_name, mime_type, size_bytes FROM app.files WHERE id = $1 AND deleted_at IS NULL`, in.FileID).Scan(&origName, &mimeType, &size); err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		lbl := strings.TrimSpace(in.Label)
		var lblPtr *string
		if lbl != "" {
			lblPtr = &lbl
		}
		var id uuid.UUID
		var created time.Time
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.attachments (tenant_id, file_id, caption, `+col+`) VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
			p.TenantID, in.FileID, lblPtr, in.EntityID).Scan(&id, &created); err != nil {
			if db.PgCode(err) == "23503" {
				return httpx.NotFound("not_found", "The record to attach to was not found")
			}
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "attachment.created", "attachments", id, nil, map[string]any{"entity_type": in.EntityType, "entity_id": in.EntityID, "file_id": in.FileID, "file_name": origName}); err != nil {
			return err
		}
		uid, uname := p.UserID, p.Name
		out = Attachment{ID: id, EntityType: in.EntityType, EntityID: in.EntityID, FileID: in.FileID, FileName: origName,
			MimeType: mimeType, SizeBytes: size, Label: lblPtr, UploadedBy: &uid, UploadedByName: &uname, CreatedAt: created}
		return nil
	})
	return out, err
}

func (s *Service) DeleteAttachment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM app.attachments WHERE tenant_id = $1 AND id = $2`, p.TenantID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Attachment not found")
		}
		return s.Audit.Record(ctx, tx, p, "attachment.deleted", "attachments", id, map[string]any{"attachment_id": id}, nil)
	})
}
