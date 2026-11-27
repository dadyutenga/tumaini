package org

import (
	"context"
	"errors"
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

func hasAnyPerm(p *auth.Principal, perms ...string) bool {
	for _, perm := range perms {
		if p.Has(perm) {
			return true
		}
	}
	return false
}

func canViewAllExpenses(p *auth.Principal) bool {
	return hasAnyPerm(p, "finance.expense.enter", "finance.expense.approve", "finance.expense.pay", "finance.transaction.correct", "reports.business.view")
}

// authorizeAttachmentEntity enforces role permissions and record-level ownership for an entity
// referenced by app.attachments. Returns 403 when the caller lacks module permission, and 404
// when an own-scoped caller tries to access another user's record (avoiding IDOR enumeration).
func authorizeAttachmentEntity(ctx context.Context, tx pgx.Tx, p *auth.Principal, entityType string, entityID uuid.UUID, write bool) error {
	if p.IsOwner {
		return nil
	}
	denied := httpx.Forbidden("permission_denied", "You do not have permission to access attachments for this record")
	notFound := httpx.NotFound("not_found", "Record not found")

	switch entityType {
	case "sale":
		if !hasAnyPerm(p, "shop.sale.view_all", "shop.sale.create") {
			return denied
		}
		if p.Has("shop.sale.view_all") {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.sales WHERE tenant_id = $1 AND id = $2 AND sold_by = $3)`,
			p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "customer":
		if write {
			if !hasAnyPerm(p, "shop.customer.manage", "services.client.manage", "shop.sale.create") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "shop.customer.view_all", "shop.customer.manage", "shop.sale.create", "shop.sale.view_all", "services.client.view_all", "services.client.manage") {
			return denied
		}
		return nil

	case "client":
		if write {
			if !p.Has("services.client.manage") {
				return denied
			}
		} else if !hasAnyPerm(p, "services.client.view_all", "services.client.manage") {
			return denied
		}
		if p.Has("services.client.view_all") {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM app.customers c
				WHERE c.tenant_id = $1 AND c.id = $2
				  AND (c.registered_by = $3 OR c.assigned_to = $3 OR EXISTS (
				    SELECT 1 FROM app.projects pr
				    WHERE pr.tenant_id = c.tenant_id AND pr.customer_id = c.id
				      AND (pr.owner_id = $3 OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.owner_id = $3))
				  ))
			)`, p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "purchase_order", "purchase":
		if !p.Has("shop.purchase.manage") {
			return denied
		}
		return nil

	case "supplier":
		if write {
			if !p.Has("shop.supplier.manage") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "shop.supplier.manage", "shop.purchase.manage") {
			return denied
		}
		return nil

	case "expense":
		if write {
			if !hasAnyPerm(p, "finance.expense.submit", "finance.expense.enter", "finance.expense.approve", "finance.expense.pay", "finance.transaction.correct") {
				return denied
			}
		} else if !canViewAllExpenses(p) && !p.Has("finance.expense.submit") {
			return denied
		}
		if canViewAllExpenses(p) {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.expenses WHERE tenant_id = $1 AND id = $2 AND requested_by = $3)`,
			p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "employee":
		if write {
			if !p.Has("hr.employee.manage") {
				return denied
			}
			return nil
		}
		if hasAnyPerm(p, "hr.employee.view", "hr.employee.manage") {
			return nil
		}
		if p.EmployeeID != nil && *p.EmployeeID == entityID {
			return nil
		}
		return denied

	case "project":
		if write {
			if !p.Has("services.project.manage") {
				return denied
			}
		} else if !hasAnyPerm(p, "services.project.view_all", "services.project.manage") {
			return denied
		}
		if p.Has("services.project.view_all") {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM app.projects pr
				WHERE pr.tenant_id = $1 AND pr.id = $2
				  AND (pr.owner_id = $3 OR pr.created_by = $3 OR EXISTS (
				    SELECT 1 FROM app.project_stages st WHERE st.tenant_id = pr.tenant_id AND st.project_id = pr.id AND st.owner_id = $3
				  ))
			)`, p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "design_job":
		if write {
			if !p.Has("services.design.manage") {
				return denied
			}
		} else if !hasAnyPerm(p, "services.design.view_all", "services.design.manage") {
			return denied
		}
		if p.Has("services.design.view_all") {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM app.design_jobs j
				WHERE j.tenant_id = $1 AND j.id = $2 AND (j.designer_id = $3 OR j.created_by = $3)
			)`, p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "contractor":
		if !p.Has("services.contractor.manage") {
			return denied
		}
		return nil

	case "contractor_bill":
		if write {
			if !p.Has("services.contractor.manage") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "services.contractor.manage", "finance.payment.make", "finance.account.view") {
			return denied
		}
		return nil

	case "payment":
		if write {
			if !hasAnyPerm(p, "finance.payment.receive", "finance.payment.view_all", "finance.transaction.correct") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "finance.payment.receive", "finance.payment.view_all", "finance.account.view", "shop.sale.view_all", "services.invoice.manage") {
			return denied
		}
		return nil

	case "payment_made":
		if write {
			if !hasAnyPerm(p, "finance.payment.make", "finance.transaction.correct") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "finance.payment.make", "finance.account.view") {
			return denied
		}
		return nil

	case "task":
		if write {
			if !p.Has("services.task.manage") {
				return denied
			}
		} else if !hasAnyPerm(p, "services.task.view_all", "services.task.manage") {
			return denied
		}
		if p.Has("services.task.view_all") {
			return nil
		}
		var ok bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM app.tasks t
				WHERE t.tenant_id = $1 AND t.id = $2 AND (t.assignee_id = $3 OR t.created_by = $3)
			)`, p.TenantID, entityID, p.MembershipID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return notFound
		}
		return nil

	case "stock_adjustment":
		if write {
			if !p.Has("shop.stock.manage") {
				return denied
			}
			return nil
		}
		if !hasAnyPerm(p, "shop.stock.view", "shop.stock.manage") {
			return denied
		}
		return nil

	default:
		return denied
	}
}

type attachmentTarget struct {
	entityType string
	entityID   uuid.UUID
	createdBy  *uuid.UUID
}

func scanAttachmentTarget(rows pgx.Row) (attachmentTarget, error) {
	var createdBy *uuid.UUID
	var saleID, purchaseID, expenseID, payRecID, payMadeID, customerID, supplierID, contractorID, projectID, designJobID, taskID, employeeID, billID, stockAdjID *uuid.UUID
	if err := rows.Scan(
		&createdBy,
		&saleID, &purchaseID, &expenseID, &payRecID, &payMadeID, &customerID,
		&supplierID, &contractorID, &projectID, &designJobID, &taskID, &employeeID,
		&billID, &stockAdjID,
	); err != nil {
		return attachmentTarget{}, err
	}
	t := attachmentTarget{createdBy: createdBy}
	switch {
	case saleID != nil:
		t.entityType, t.entityID = "sale", *saleID
	case purchaseID != nil:
		t.entityType, t.entityID = "purchase", *purchaseID
	case expenseID != nil:
		t.entityType, t.entityID = "expense", *expenseID
	case payRecID != nil:
		t.entityType, t.entityID = "payment", *payRecID
	case payMadeID != nil:
		t.entityType, t.entityID = "payment_made", *payMadeID
	case customerID != nil:
		t.entityType, t.entityID = "customer", *customerID
	case supplierID != nil:
		t.entityType, t.entityID = "supplier", *supplierID
	case contractorID != nil:
		t.entityType, t.entityID = "contractor", *contractorID
	case projectID != nil:
		t.entityType, t.entityID = "project", *projectID
	case designJobID != nil:
		t.entityType, t.entityID = "design_job", *designJobID
	case taskID != nil:
		t.entityType, t.entityID = "task", *taskID
	case employeeID != nil:
		t.entityType, t.entityID = "employee", *employeeID
	case billID != nil:
		t.entityType, t.entityID = "contractor_bill", *billID
	case stockAdjID != nil:
		t.entityType, t.entityID = "stock_adjustment", *stockAdjID
	}
	return t, nil
}

func (s *Service) ListAttachments(ctx context.Context, p *auth.Principal, entityType string, entityID uuid.UUID) ([]Attachment, error) {
	entityType = strings.TrimSpace(entityType)
	col, ok := attachmentColumn[entityType]
	if !ok {
		return nil, httpx.BadRequest("invalid_query", "Unsupported entity_type")
	}
	out := []Attachment{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := authorizeAttachmentEntity(ctx, tx, p, entityType, entityID, false); err != nil {
			return err
		}
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
		if err := authorizeAttachmentEntity(ctx, tx, p, in.EntityType, in.EntityID, true); err != nil {
			return err
		}
		var storageKey, origName, mimeType, class string
		var size int64
		var fileCreator *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT storage_key, original_name, mime_type, size_bytes, classification::text, created_by FROM app.files WHERE id = $1 AND deleted_at IS NULL`, in.FileID).
			Scan(&storageKey, &origName, &mimeType, &size, &class, &fileCreator); err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		if err := s.authorizeFileLoaded(ctx, tx, p, in.FileID, storageKey, class, fileCreator); err != nil {
			return err
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
		target, err := scanAttachmentTarget(tx.QueryRow(ctx, `
			SELECT created_by,
			       sale_id, purchase_id, expense_id, payment_received_id, payment_made_id, customer_id,
			       supplier_id, contractor_id, project_id, design_job_id, task_id, employee_id,
			       contractor_bill_id, stock_adjustment_id
			FROM app.attachments
			WHERE tenant_id = $1 AND id = $2
			FOR UPDATE`, p.TenantID, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Attachment not found")
		}
		if err != nil {
			return err
		}
		if err := authorizeAttachmentEntity(ctx, tx, p, target.entityType, target.entityID, true); err != nil {
			// Allow the uploader to remove their own attachment if they still have read access to the entity.
			if target.createdBy == nil || *target.createdBy != p.MembershipID ||
				authorizeAttachmentEntity(ctx, tx, p, target.entityType, target.entityID, false) != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.attachments WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "attachment.deleted", "attachments", id, map[string]any{"attachment_id": id, "entity_type": target.entityType, "entity_id": target.entityID}, nil)
	})
}
