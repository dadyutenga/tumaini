package hr

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
	"bms/internal/platform/upload"
)

const documentMax = 10 << 20

var documentKinds = map[string]bool{"contract": true, "id_copy": true, "certificate": true, "other": true}

// Employee documents are stored as app.files linked through app.attachments(employee_id).
func (s *Service) ListDocuments(ctx context.Context, p *auth.Principal, employeeID uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, employeeID); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT a.id, COALESCE(a.caption, f.original_name) AS name, a.file_id, f.mime_type, f.size_bytes,
			       a.created_by AS uploaded_by, a.created_at,
			       '/api/v1/files/' || a.file_id::text AS download_url
			FROM app.attachments a
			JOIN app.files f ON f.id = a.file_id
			WHERE a.employee_id = $1 AND f.deleted_at IS NULL
			ORDER BY a.created_at DESC, a.id`, employeeID)
		return err
	})
	return out, err
}

func (s *Service) AddDocument(ctx context.Context, p *auth.Principal, employeeID uuid.UUID, kind, name, filename string, content []byte, mime string) (map[string]any, error) {
	if !documentKinds[kind] {
		return nil, invalidFields(map[string]string{"kind": "must be contract, id_copy, certificate or other"})
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filename
	}
	caption := kind + ": " + name
	if len(caption) > 200 {
		caption = caption[:200]
	}
	sum := sha256.Sum256(content)
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, employeeID); err != nil {
			return err
		}
		file, err := s.Store.Save(ctx, "business", filename, upload.Reader(content), documentMax)
		if err != nil {
			return err
		}
		var fileID, docID uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.files (storage_key, original_name, mime_type, size_bytes, sha256, classification)\n\t\t\tVALUES ($1, $2, $3, $4, $5, 'restricted') RETURNING id", file.Path, filename, mime, file.Size, sum[:]).Scan(&fileID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.attachments (file_id, caption, employee_id)\n\t\t\tVALUES ($1, $2, $3) RETURNING id", fileID, caption, employeeID).Scan(&docID); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Document not found", `
			SELECT a.id, COALESCE(a.caption, f.original_name) AS name, a.file_id, f.mime_type, f.size_bytes,
			       a.created_by AS uploaded_by, a.created_at,
			       '/api/v1/files/' || a.file_id::text AS download_url
			FROM app.attachments a JOIN app.files f ON f.id = a.file_id
			WHERE a.id = $1`, docID); err != nil {
			return err
		}
		out["kind"] = kind
		return s.Audit.Record(ctx, tx, p, "employee.document_added", "employees", employeeID, nil, map[string]any{"document_id": docID, "kind": kind, "name": name})
	})
	return out, err
}

func (s *Service) DeleteDocument(ctx context.Context, p *auth.Principal, employeeID, docID uuid.UUID) error {
	var path string
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, employeeID); err != nil {
			return err
		}
		var fileID uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tDELETE FROM app.attachments WHERE id = $1 AND employee_id = $2 RETURNING file_id", docID, employeeID).Scan(&fileID); err != nil {
			return httpx.NotFound("not_found", "Document not found")
		}
		if err := tx.QueryRow(ctx, "UPDATE app.files SET deleted_at = now() WHERE id = $1 RETURNING storage_key", fileID).Scan(&path); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "employee.document_removed", "employees", employeeID, map[string]any{"document_id": docID}, nil)
	})
	if err != nil {
		return err
	}
	_ = s.Store.Delete(ctx, path)
	return nil
}

func (h *Handler) ListDocuments(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	out, err := h.Svc.ListDocuments(c.Context(), middleware.Principal(c), id)
	return httpx.OK(c, out, err)
}

func (h *Handler) AddDocument(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return httpx.BadRequest("invalid_body", "Upload a file field named file")
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()
	content, mime, err := upload.Read(f, documentMax, false)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(c.FormValue("kind"))
	if kind == "" {
		kind = strings.TrimSpace(c.Query("kind"))
	}
	if kind == "" {
		kind = "other"
	}
	name := c.FormValue("name")
	if name == "" {
		name = c.Query("name")
	}
	out, err := h.Svc.AddDocument(c.Context(), middleware.Principal(c), id, kind, name, fh.Filename, content, mime)
	return httpx.Created(c, out, err)
}

func (h *Handler) DeleteDocument(c fiber.Ctx) error {
	id, err := httpx.UUIDParam(c, "id")
	if err != nil {
		return err
	}
	docID, err := httpx.UUIDParam(c, "doc_id")
	if err != nil {
		return err
	}
	if err := h.Svc.DeleteDocument(c.Context(), middleware.Principal(c), id, docID); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func invalidFields(fields map[string]string) error {
	return &httpx.Error{Status: fiber.StatusUnprocessableEntity, Code: "validation_failed", Message: "Check the highlighted fields", Fields: fields}
}
