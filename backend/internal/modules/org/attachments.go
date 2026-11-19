package org

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
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

var allowedAttachmentEntities = map[string]bool{
	"sale": true, "customer": true, "product": true, "purchase_order": true,
	"supplier": true, "expense": true, "employee": true, "client": true,
	"project": true, "design_job": true, "contractor": true, "payment": true,
}

func (s *Service) ListAttachments(ctx context.Context, p *auth.Principal, entityType string, entityID uuid.UUID) ([]Attachment, error) {
	entityType = strings.TrimSpace(entityType)
	if !allowedAttachmentEntities[entityType] {
		return nil, httpx.BadRequest("invalid_query", "Unsupported entity_type")
	}
	out := []Attachment{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.entity_type, a.entity_id, a.file_id, f.original_name, f.mime_type, f.size_bytes,
			       a.label, a.uploaded_by, u.name, a.created_at
			FROM attachments a
			JOIN files f ON f.id = a.file_id
			LEFT JOIN users u ON u.id = a.uploaded_by
			WHERE a.entity_type = $1 AND a.entity_id = $2
			ORDER BY a.created_at DESC
		`, entityType, entityID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Attachment
			if err := rows.Scan(&a.ID, &a.EntityType, &a.EntityID, &a.FileID, &a.FileName, &a.MimeType, &a.SizeBytes, &a.Label, &a.UploadedBy, &a.UploadedByName, &a.CreatedAt); err != nil {
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
	if !allowedAttachmentEntities[in.EntityType] {
		return Attachment{}, httpx.BadRequest("invalid_body", "Unsupported entity_type")
	}
	if in.EntityID == uuid.Nil || in.FileID == uuid.Nil {
		return Attachment{}, httpx.BadRequest("invalid_body", "entity_id and file_id are required")
	}
	var out Attachment
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var origName, mimeType string
		var size int64
		if err := tx.QueryRow(ctx, `SELECT original_name, mime_type, size_bytes FROM files WHERE id = $1`, in.FileID).Scan(&origName, &mimeType, &size); err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		id := ids.New()
		lbl := strings.TrimSpace(in.Label)
		var lblPtr *string
		if lbl != "" {
			lblPtr = &lbl
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO attachments (id, business_id, entity_type, entity_id, file_id, label, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, in.EntityType, in.EntityID, in.FileID, lblPtr, p.UserID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "attachment.created", in.EntityType, in.EntityID, nil, map[string]any{"attachment_id": id, "file_id": in.FileID, "file_name": origName}); err != nil {
			return err
		}
		uid := p.UserID
		uname := p.Name
		out = Attachment{
			ID: id, EntityType: in.EntityType, EntityID: in.EntityID, FileID: in.FileID,
			FileName: origName, MimeType: mimeType, SizeBytes: size, Label: lblPtr,
			UploadedBy: &uid, UploadedByName: &uname, CreatedAt: s.Clock.Now(),
		}
		return nil
	})
	return out, err
}

func (s *Service) DeleteAttachment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var entityType string
		var entityID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT entity_type, entity_id FROM attachments WHERE id = $1`, id).Scan(&entityType, &entityID); err != nil {
			return httpx.NotFound("not_found", "Attachment not found")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM attachments WHERE id = $1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attachment.deleted", entityType, entityID, map[string]any{"attachment_id": id}, nil)
	})
}
