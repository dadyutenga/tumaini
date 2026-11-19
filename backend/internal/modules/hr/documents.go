package hr

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/middleware"
	"bms/internal/platform/upload"
)

// FR-EMP-19: documents such as a contract or an ID copy attached to an employee.
// They are sensitive, so only people who manage employees, and the employee, can read them;
// GET /files/:id enforces the same rule.

const documentMax = 10 << 20

var documentKinds = map[string]bool{"contract": true, "id_copy": true, "certificate": true, "other": true}

func (s *Service) ListDocuments(ctx context.Context, p *auth.Principal, employeeID uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "employees.manage", employeeID); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT d.id, d.kind, d.name, d.file_id, f.mime_type, f.size_bytes, d.uploaded_by, d.created_at,
			       '/api/v1/files/' || d.file_id::text AS download_url
			FROM employee_documents d JOIN files f ON f.id = d.file_id
			WHERE d.employee_id = $1 ORDER BY d.created_at DESC, d.id`, employeeID)
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
	if len(name) > 200 {
		return nil, invalidFields(map[string]string{"name": "at most 200 characters"})
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "employees.manage", employeeID); err != nil {
			return err
		}
		file, err := s.Store.Save(ctx, p.BusinessID.String(), filename, upload.Reader(content), documentMax)
		if err != nil {
			return httpx.BadRequest("invalid_body", err.Error())
		}
		fileID, docID := ids.New(), ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO files (id, business_id, path, original_name, mime_type, size_bytes, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, fileID, p.BusinessID, file.Path, filename, mime, file.Size, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO employee_documents (id, business_id, employee_id, file_id, kind, name, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, docID, p.BusinessID, employeeID, fileID, kind, name, p.UserID); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Document not found", `
			SELECT d.id, d.kind, d.name, d.file_id, f.mime_type, f.size_bytes, d.uploaded_by, d.created_at,
			       '/api/v1/files/' || d.file_id::text AS download_url
			FROM employee_documents d JOIN files f ON f.id = d.file_id WHERE d.id = $1`, docID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "employee.document_added", "employee", employeeID, nil, map[string]any{"document_id": docID, "kind": kind, "name": name})
	})
	return out, err
}

func (s *Service) DeleteDocument(ctx context.Context, p *auth.Principal, employeeID, docID uuid.UUID) error {
	var path string
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "employees.manage", employeeID); err != nil {
			return err
		}
		var fileID uuid.UUID
		if err := tx.QueryRow(ctx, `
			DELETE FROM employee_documents WHERE id = $1 AND employee_id = $2 RETURNING file_id`, docID, employeeID).Scan(&fileID); err != nil {
			return httpx.NotFound("not_found", "Document not found")
		}
		if err := tx.QueryRow(ctx, `DELETE FROM files WHERE id = $1 RETURNING path`, fileID).Scan(&path); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "employee.document_removed", "employee", employeeID, map[string]any{"document_id": docID}, nil)
	})
	if err != nil {
		return err
	}
	// The record is gone; removing the bytes is best effort and never fails the request.
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
	// kind and name may be form fields beside the file, or query parameters.
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
