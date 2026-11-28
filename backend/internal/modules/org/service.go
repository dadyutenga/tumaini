package org

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
	"bms/internal/platform/storage"
	"bms/internal/platform/upload"
)

type Service struct {
	DB    *db.DB
	Audit *audit.Service
	Store storage.Store
	Clock clock.Clock
}

func New(database *db.DB, aud *audit.Service, store storage.Store, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Store: store, Clock: clk}
}

type Business struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	LegalName *string   `json:"legal_name"`
	TaxNumber *string   `json:"tax_number"`
	RegNumber *string   `json:"reg_number"`
	Phone     *string   `json:"phone"`
	Email     *string   `json:"email"`
	Address   *string   `json:"address"`
	LogoFile  *string   `json:"logo_file"`
	Currency  string    `json:"currency"`
	Timezone  string    `json:"timezone"`
	IsActive  bool      `json:"is_active"`
}

const businessSelect = `
	SELECT '00000000-0000-0000-0000-000000000001'::uuid, bs.business_name::text, bp.name::text, bs.tin,
	       bs.registration_no, bs.phone::text, bs.email::text, bs.address, bs.logo_file_id::text,
	       bs.currency::text, bp.timezone, bp.status = 'active'
	FROM app.business_profile bp CROSS JOIN app.business_settings bs WHERE bp.id`

func scanBusiness(row pgx.Row) (Business, error) {
	var b Business
	err := row.Scan(&b.ID, &b.Name, &b.LegalName, &b.TaxNumber, &b.RegNumber, &b.Phone, &b.Email, &b.Address, &b.LogoFile, &b.Currency, &b.Timezone, &b.IsActive)
	return b, err
}

func (s *Service) GetBusiness(ctx context.Context, p *auth.Principal) (Business, error) {
	var b Business
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		b, err = scanBusiness(tx.QueryRow(ctx, businessSelect))
		return err
	})
	return b, err
}

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

var tinPattern = regexp.MustCompile(`^[0-9]{3}-?[0-9]{3}-?[0-9]{3}$`)

func (s *Service) UpdateBusiness(ctx context.Context, p *auth.Principal, in Business) (Business, error) {
	fields := map[string]string{}
	if in.Timezone != "" {
		if _, err := timezone(in.Timezone); err != nil {
			fields["timezone"] = err.Error()
		}
	}
	if in.Currency != "" && !currencyCode.MatchString(in.Currency) {
		fields["currency"] = "must be a three-letter code such as TZS"
	}
	if in.Email != nil && *in.Email != "" && !strings.Contains(*in.Email, "@") {
		fields["email"] = "must be an email address"
	}
	if in.TaxNumber != nil && *in.TaxNumber != "" && !tinPattern.MatchString(*in.TaxNumber) {
		fields["tax_number"] = "must be a TIN such as 123-456-789"
	}
	if in.Phone != nil && *in.Phone != "" {
		if e164.MatchString(strings.ReplaceAll(*in.Phone, " ", "")) {
			v := strings.ReplaceAll(*in.Phone, " ", "")
			in.Phone = &v
		} else {
			fields["phone"] = "must be an international number such as +255712345678"
		}
	}
	if len(fields) > 0 {
		return Business{}, invalid(fields)
	}
	// An empty string clears an optional detail; nil leaves it alone.
	clear := func(v *string) any {
		if v == nil {
			return nil
		}
		return *v
	}
	var out Business
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, _ := scanBusiness(tx.QueryRow(ctx, businessSelect))
		// The name customers see and the contact details live in business_settings; the legal name,
		// time zone and currency of the books in business_profile.
		if _, err := tx.Exec(ctx, `
			UPDATE app.business_settings SET
			  business_name   = coalesce(NULLIF($1,''), business_name),
			  tin             = CASE WHEN $2::text IS NULL THEN tin ELSE NULLIF($2,'') END,
			  registration_no = CASE WHEN $3::text IS NULL THEN registration_no ELSE NULLIF($3,'') END,
			  phone           = CASE WHEN $4::text IS NULL THEN phone ELSE NULLIF($4,'')::app.phone_e164 END,
			  email           = CASE WHEN $5::text IS NULL THEN email ELSE NULLIF($5,'')::app.email END,
			  address         = CASE WHEN $6::text IS NULL THEN address ELSE NULLIF($6,'') END,
			  currency        = coalesce(NULLIF($7,''), currency)::app.currency`,
			in.Name, clear(in.TaxNumber), clear(in.RegNumber), clear(in.Phone), clear(in.Email), clear(in.Address), in.Currency); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.business_profile SET
			  name = coalesce(NULLIF($1,''), name), timezone = coalesce(NULLIF($2,''), timezone),
			  default_currency = coalesce(NULLIF($3,''), default_currency)::app.currency, updated_at = now()
			WHERE id`, deref(in.LegalName), in.Timezone, in.Currency); err != nil {
			return err
		}
		var err error
		if out, err = scanBusiness(tx.QueryRow(ctx, businessSelect)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "business.updated", "business_profile", before, out)
	})
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SaveLogo stores the logo as a file and points the business profile at it.
func (s *Service) SaveLogo(ctx context.Context, p *auth.Principal, name string, r io.Reader) (string, error) {
	id, err := s.store(ctx, p, name, r, 2<<20, true, "public", func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := tx.Exec(ctx, "UPDATE app.business_settings SET logo_file_id = $1", id)
		return err
	})
	return id.String(), err
}

// ---------------------------------------------------------------------------------------------
// Locations (the API still calls them branches)
// ---------------------------------------------------------------------------------------------

type Branch struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Address        *string   `json:"address"`
	Phone          *string   `json:"phone"`
	BusinessUnitID uuid.UUID `json:"business_unit_id"`
	// IsMain marks the business's default location (app.locations.is_default).
	IsMain   bool `json:"is_main"`
	IsActive bool `json:"is_active"`
}

const branchSelect = `
	SELECT id, name::text, kind::text, address, phone::text, business_unit_id, is_default, status = 'active'
	FROM app.locations WHERE true`

func scanBranch(row pgx.Row) (Branch, error) {
	var b Branch
	err := row.Scan(&b.ID, &b.Name, &b.Kind, &b.Address, &b.Phone, &b.BusinessUnitID, &b.IsMain, &b.IsActive)
	return b, err
}

func (s *Service) ListBranches(ctx context.Context, p *auth.Principal) ([]Branch, error) {
	out := []Branch{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		sql := branchSelect + ` AND (status = 'active' OR $1)`
		args := []any{p.Has("settings.manage")}
		if !p.CanAccessAllBranches() {
			sql += ` AND id = ANY($2)`
			args = append(args, p.BranchIDs)
		}
		sql += ` ORDER BY is_default DESC, name`
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBranch(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

var branchKinds = map[string]bool{"shop": true, "office": true, "warehouse": true}

func (s *Service) CreateBranch(ctx context.Context, p *auth.Principal, in Branch) (Branch, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Branch{}, httpx.BadRequest("invalid_body", "Branch name is required")
	}
	kind := in.Kind
	if kind == "" {
		kind = "shop"
	}
	if !branchKinds[kind] {
		return Branch{}, invalid(map[string]string{"kind": "must be shop, office or warehouse"})
	}
	var out Branch
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		unit := in.BusinessUnitID
		if unit == uuid.Nil {
			// Shops and warehouses belong to the retail unit, offices to the services unit.
			code := "A"
			if kind == "office" {
				code = "B"
			}
			if err := tx.QueryRow(ctx, "SELECT id FROM app.business_units WHERE code = $1", code).Scan(&unit); err != nil {
				return err
			}
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.locations ( business_unit_id, name, kind, address, phone)
			VALUES ($1,$2,$3::app.location_kind,$4,NULLIF($5,'')::app.phone_e164) RETURNING id`, unit, name, kind, in.Address, deref(in.Phone)).Scan(&id); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A branch with that name already exists")
			}
			return err
		}
		// Every shop or office keeps its own cash drawer (spec: cash accounts belong to a location).
		if kind != "warehouse" {
			if _, err := tx.Exec(ctx, "INSERT INTO app.financial_accounts (name, kind, location_id, business_unit_id) VALUES ($1, 'cash', $2, $3)", name+" till", id, unit); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "branch.created", "locations", id, nil, map[string]any{"name": name, "kind": kind}); err != nil {
			return err
		}
		var err error
		out, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $1`, id))
		return err
	})
	return out, err
}

func (s *Service) GetBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) (Branch, error) {
	if !p.AllowsBranch(id) {
		return Branch{}, httpx.Forbidden("branch_forbidden", "You do not have access to this branch")
	}
	var b Branch
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if b, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $1`, id)); err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		return nil
	})
	return b, err
}

func (s *Service) UpdateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID, in Branch) (Branch, error) {
	if in.Kind != "" && !branchKinds[in.Kind] {
		return Branch{}, invalid(map[string]string{"kind": "must be shop, office or warehouse"})
	}
	var out Branch
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.locations SET name = coalesce(NULLIF($2,''), name), address = coalesce($3, address),\n\t\t\t       phone = coalesce(NULLIF($4,'')::app.phone_e164, phone), kind = coalesce(NULLIF($5,'')::app.location_kind, kind)\n\t\t\tWHERE id = $1", id, strings.TrimSpace(in.Name), in.Address, deref(in.Phone), in.Kind)
		if err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A branch with that name already exists")
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Branch not found")
		}
		if err := s.Audit.Record(ctx, tx, p, "branch.updated", "locations", id, nil, in); err != nil {
			return err
		}
		out, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $1`, id))
		return err
	})
	return out, err
}

func (s *Service) DeactivateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var isMain bool
		if err := tx.QueryRow(ctx, "SELECT is_default FROM app.locations WHERE id = $1 FOR UPDATE", id).Scan(&isMain); err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		if isMain {
			return httpx.Conflict("invalid_status", "The main branch cannot be deactivated")
		}
		var stock decimal.Decimal
		var docs int
		if err := tx.QueryRow(ctx, "SELECT coalesce(sum(quantity_on_hand), 0) FROM app.stock_balances WHERE location_id = $1", id).Scan(&stock); err != nil {
			return err
		}
		if stock.GreaterThan(decimal.Zero) {
			return httpx.Conflict("invalid_status", "This branch still has stock")
		}
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM app.sales WHERE location_id = $1 AND status IN ('draft','held','pending_approval'))
			     + (SELECT count(*) FROM app.stock_transfers WHERE (from_location_id = $1 OR to_location_id = $1) AND status IN ('draft','dispatched'))
			     + (SELECT count(*) FROM app.day_closings WHERE location_id = $1 AND status = 'open')`, id).Scan(&docs); err != nil {
			return err
		}
		if docs > 0 {
			return httpx.Conflict("invalid_status", "This branch has open documents (drafts, transfers in transit or an open day)")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.locations SET status = 'inactive' WHERE id = $1", id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "branch.deactivated", "locations", id, nil, map[string]any{"is_active": false})
	})
}

// BranchStaff lists the people whose default location is this one, and the employees based there.
func (s *Service) BranchStaff(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	if _, err := s.GetBranch(ctx, p, id); err != nil {
		return nil, err
	}
	users, employees := []map[string]any{}, []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT u.id, u.full_name::text, coalesce(r.name::text, '') FROM app.memberships m\n\t\t\tJOIN iam.users u ON u.id = m.user_id\n\t\t\tLEFT JOIN app.membership_roles mr ON mr.membership_id = m.id\n\t\t\tLEFT JOIN app.roles r ON r.id = mr.role_id\n\t\t\tWHERE m.default_location_id = $1 AND m.status = 'active' ORDER BY u.full_name", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var uid uuid.UUID
			var name, role string
			if err := rows.Scan(&uid, &name, &role); err != nil {
				rows.Close()
				return err
			}
			users = append(users, map[string]any{"id": uid, "name": name, "role": role})
		}
		rows.Close()
		erows, err := tx.Query(ctx, "SELECT id, full_name::text, employee_no, status::text FROM app.employees WHERE location_id = $1 ORDER BY full_name", id)
		if err != nil {
			return err
		}
		defer erows.Close()
		for erows.Next() {
			var eid uuid.UUID
			var name, no, status string
			if err := erows.Scan(&eid, &name, &no, &status); err != nil {
				return err
			}
			employees = append(employees, map[string]any{"id": eid, "name": name, "employee_no": no, "status": status})
		}
		return erows.Err()
	})
	return map[string]any{"users": users, "employees": employees}, err
}

// ---------------------------------------------------------------------------------------------
// Tax rates
// ---------------------------------------------------------------------------------------------

type TaxRate struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Rate      decimal.Decimal `json:"rate"`
	IsDefault bool            `json:"is_default"`
	IsActive  bool            `json:"is_active"`
}

func (s *Service) ListTax(ctx context.Context, p *auth.Principal) ([]TaxRate, error) {
	out := []TaxRate{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name::text, rate_pct, is_default, status = 'active' FROM app.tax_rates ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t TaxRate
			if err := rows.Scan(&t.ID, &t.Name, &t.Rate, &t.IsDefault, &t.IsActive); err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) PaymentMethods(ctx context.Context, p *auth.Principal) (any, error) {
	var v any = setting.Defaults["payment.methods"]
	_ = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if raw, err := setting.Raw(ctx, tx, "payment.methods"); err == nil {
			var stored any
			if json.Unmarshal(raw, &stored) == nil {
				v = stored
			}
		}
		return nil
	})
	return v, nil
}

// ---------------------------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------------------------

var unsafeKey = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// store checks the bytes (the client's Content-Type is ignored), saves them under the business's
// prefix and records app.files, then runs link in the same transaction so a file is never recorded
// without whatever points at it.
func (s *Service) store(ctx context.Context, p *auth.Principal, name string, r io.Reader, max int64, imagesOnly bool, class string,
	link func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error) (uuid.UUID, error) {
	buf, mime, err := upload.Read(r, max, imagesOnly)
	if err != nil {
		return uuid.Nil, err
	}
	safe := unsafeKey.ReplaceAllString(name, "_")
	if safe == "" {
		safe = "file"
	}
	file, err := s.Store.Save(ctx, "business", safe, upload.Reader(buf), max)
	if err != nil {
		return uuid.Nil, err // an API error from the scanner, or logged and reported as a server error
	}
	sum := sha256.Sum256(buf)
	var id uuid.UUID
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.files (storage_key, original_name, mime_type, size_bytes, sha256, classification)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6::app.data_class) RETURNING id", file.Path, truncate(name, 255), mime, file.Size, sum[:], class).Scan(&id); err != nil {
			return err
		}
		if link != nil {
			return link(ctx, tx, id)
		}
		return nil
	})
	if err != nil {
		_ = s.Store.Delete(ctx, file.Path)
	}
	return id, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Upload stores a file after checking its content.
func (s *Service) Upload(ctx context.Context, p *auth.Principal, name, _ string, r io.Reader) (uuid.UUID, error) {
	return s.store(ctx, p, name, r, 10<<20, false, "confidential", nil)
}

// OpenFile returns a stored file after checking the caller may see it.
func (s *Service) OpenFile(ctx context.Context, p *auth.Principal, id uuid.UUID) (string, string, io.ReadCloser, error) {
	var path, name, mime, class string
	var createdBy *uuid.UUID
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT storage_key, original_name, mime_type, classification::text, created_by FROM app.files WHERE id = $1 AND deleted_at IS NULL`, id).
			Scan(&path, &name, &mime, &class, &createdBy); err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		return s.authorizeFileLoaded(ctx, tx, p, id, path, class, createdBy)
	})
	if err != nil {
		return "", "", nil, err
	}
	rc, err := s.Store.Open(ctx, path)
	return name, mime, rc, err
}

// authorizeFileLoaded verifies that principal p may read the stored file id.
// Always returns 404 on denial so callers cannot enumerate file UUIDs.
func (s *Service) authorizeFileLoaded(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, storageKey, class string, createdBy *uuid.UUID) error {
	notFound := httpx.NotFound("not_found", "File not found")

	// 1. A data export holds the whole business; only someone who manages backups may download it.
	var isExport bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.backup_runs WHERE storage_uri = $1)", storageKey).Scan(&isExport); err != nil {
		return err
	}
	if isExport {
		if !p.Has("security.backup.manage") {
			return notFound
		}
		return nil
	}

	// 2. Public assets (business/branch logos) and owner/uploader access.
	if class == "public" || p.IsOwner || (createdBy != nil && *createdBy == p.MembershipID) {
		return nil
	}

	// 3. Payslip PDFs: payroll managers or the payslip's own employee.
	var payslipEmp uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT pl.employee_id FROM app.payslips ps JOIN app.payroll_lines pl ON pl.id = ps.payroll_line_id WHERE ps.file_id = $1 LIMIT 1", id).Scan(&payslipEmp); err == nil {
		if hasAnyPerm(p, "hr.payroll.view", "hr.payroll.manage", "hr.payroll.approve") || (p.EmployeeID != nil && *p.EmployeeID == payslipEmp) {
			return nil
		}
		return notFound
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// 4. Expense receipts: expense viewers/approvers or the requester.
	var expRequester uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT requested_by FROM app.expenses WHERE receipt_file_id = $1 LIMIT 1", id).Scan(&expRequester); err == nil {
		if canViewAllExpenses(p) || expRequester == p.MembershipID {
			return nil
		}
		return notFound
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// 5. Client documents: services viewers or own client/project.
	var docCustomer uuid.UUID
	var docProject *uuid.UUID
	if err := tx.QueryRow(ctx, "SELECT customer_id, project_id FROM app.client_documents WHERE file_id = $1 LIMIT 1", id).Scan(&docCustomer, &docProject); err == nil {
		if hasAnyPerm(p, "services.client.view_all", "services.project.view_all") {
			return nil
		}
		if docProject != nil && authorizeAttachmentEntity(ctx, tx, p, "project", *docProject, false) == nil {
			return nil
		}
		if authorizeAttachmentEntity(ctx, tx, p, "client", docCustomer, false) == nil {
			return nil
		}
		return notFound
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// 6. Design previews and final assets: design viewers or the job's designer/creator.
	var designJobID uuid.UUID
	if err := tx.QueryRow(ctx, "\n\t\tSELECT design_job_id FROM app.design_versions WHERE preview_file_id = $1\n\t\tUNION ALL\n\t\tSELECT v.design_job_id FROM app.design_final_assets a JOIN app.design_versions v ON v.id = a.design_version_id WHERE a.file_id = $1\n\t\tLIMIT 1", id).Scan(&designJobID); err == nil {
		if authorizeAttachmentEntity(ctx, tx, p, "design_job", designJobID, false) == nil {
			return nil
		}
		return notFound
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// 7. Generic attachments: caller must be authorized to read at least one linked entity.
	attRows, err := tx.Query(ctx, "\n\t\tSELECT created_by,\n\t\t       sale_id, purchase_id, expense_id, payment_received_id, payment_made_id, customer_id,\n\t\t       supplier_id, contractor_id, project_id, design_job_id, task_id, employee_id,\n\t\t       contractor_bill_id, stock_adjustment_id\n\t\tFROM app.attachments\n\t\tWHERE file_id = $1", id)
	if err != nil {
		return err
	}
	var targets []attachmentTarget
	for attRows.Next() {
		t, err := scanAttachmentTarget(attRows)
		if err != nil {
			attRows.Close()
			return err
		}
		targets = append(targets, t)
	}
	attRows.Close()
	if err := attRows.Err(); err != nil {
		return err
	}
	if len(targets) > 0 {
		for _, t := range targets {
			if authorizeAttachmentEntity(ctx, tx, p, t.entityType, t.entityID, false) == nil {
				return nil
			}
		}
		return notFound
	}

	// 8. Product photos & employee avatars are readable by any member of staff.
	var isCatalogOrAvatar bool
	if err := tx.QueryRow(ctx, "\n\t\tSELECT EXISTS (SELECT 1 FROM app.products WHERE photo_file_id = $1)\n\t\t    OR EXISTS (SELECT 1 FROM app.employees WHERE photo_file_id = $1)", id).Scan(&isCatalogOrAvatar); err != nil {
		return err
	}
	if isCatalogOrAvatar {
		return nil
	}

	// Unattached confidential files are readable only by their uploader or the owner.
	return notFound
}
