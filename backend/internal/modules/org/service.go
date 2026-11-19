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
	"bms/internal/kernel/ledger"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
	"bms/internal/platform/storage"
	"bms/internal/platform/upload"
)

type Service struct {
	DB     *db.DB
	Audit  *audit.Service
	Ledger *ledger.Service
	Store  storage.Store
	Clock  clock.Clock
}

func New(database *db.DB, aud *audit.Service, led *ledger.Service, store storage.Store, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Ledger: led, Store: store, Clock: clk}
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

// The business profile is two rows: app.tenants (legal name, timezone, status) and
// app.tenant_settings (display name, contact details, tax identifiers, currency, logo).
const businessSelect = `
	SELECT t.id, ts.business_name::text, t.legal_name::text, ts.tin, ts.registration_no, ts.phone::text, ts.email::text,
	       ts.address, ts.logo_file_id::text, ts.currency::text, t.timezone, t.status = 'active'
	FROM app.tenants t JOIN app.tenant_settings ts ON ts.tenant_id = t.id WHERE t.id = $1`

func scanBusiness(row pgx.Row) (Business, error) {
	var b Business
	err := row.Scan(&b.ID, &b.Name, &b.LegalName, &b.TaxNumber, &b.RegNumber, &b.Phone, &b.Email, &b.Address, &b.LogoFile, &b.Currency, &b.Timezone, &b.IsActive)
	return b, err
}

func (s *Service) GetBusiness(ctx context.Context, p *auth.Principal) (Business, error) {
	var b Business
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		b, err = scanBusiness(tx.QueryRow(ctx, businessSelect, p.TenantID))
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
		before, _ := scanBusiness(tx.QueryRow(ctx, businessSelect, p.TenantID))
		if _, err := tx.Exec(ctx, `
			UPDATE app.tenant_settings SET
			  business_name   = coalesce(NULLIF($2,''), business_name),
			  tin             = CASE WHEN $3::text IS NULL THEN tin ELSE NULLIF($3,'') END,
			  registration_no = CASE WHEN $4::text IS NULL THEN registration_no ELSE NULLIF($4,'') END,
			  phone           = CASE WHEN $5::text IS NULL THEN phone ELSE NULLIF($5,'')::app.phone_e164 END,
			  email           = CASE WHEN $6::text IS NULL THEN email ELSE NULLIF($6,'')::app.email END,
			  address         = CASE WHEN $7::text IS NULL THEN address ELSE NULLIF($7,'') END,
			  currency        = coalesce(NULLIF($8,''), currency)::app.currency
			WHERE tenant_id = $1`,
			p.TenantID, in.Name, clear(in.TaxNumber), clear(in.RegNumber), clear(in.Phone), clear(in.Email), clear(in.Address), in.Currency); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.tenants SET legal_name = coalesce(NULLIF($2,''), legal_name), timezone = coalesce(NULLIF($3,''), timezone),
			       default_currency = coalesce(NULLIF($4,''), default_currency)::app.currency
			WHERE id = $1`, p.TenantID, deref(in.LegalName), in.Timezone, in.Currency); err != nil {
			return err
		}
		var err error
		if out, err = scanBusiness(tx.QueryRow(ctx, businessSelect, p.TenantID)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "business.updated", "tenants", p.TenantID, before, out)
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
		_, err := tx.Exec(ctx, `UPDATE app.tenant_settings SET logo_file_id = $2 WHERE tenant_id = $1`, p.TenantID, id)
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
	// IsMain is the tenant's default location (app.locations.is_default).
	IsMain   bool `json:"is_main"`
	IsActive bool `json:"is_active"`
}

const branchSelect = `
	SELECT id, name::text, kind::text, address, phone::text, business_unit_id, is_default, status = 'active'
	FROM app.locations WHERE tenant_id = $1`

func scanBranch(row pgx.Row) (Branch, error) {
	var b Branch
	err := row.Scan(&b.ID, &b.Name, &b.Kind, &b.Address, &b.Phone, &b.BusinessUnitID, &b.IsMain, &b.IsActive)
	return b, err
}

func (s *Service) ListBranches(ctx context.Context, p *auth.Principal) ([]Branch, error) {
	out := []Branch{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, branchSelect+` AND (status = 'active' OR $2) ORDER BY is_default DESC, name`, p.TenantID, p.Has("settings.manage"))
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
			if err := tx.QueryRow(ctx, `SELECT id FROM app.business_units WHERE tenant_id = $1 AND code = $2`, p.TenantID, code).Scan(&unit); err != nil {
				return err
			}
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.locations (tenant_id, business_unit_id, name, kind, address, phone)
			VALUES ($1,$2,$3,$4::app.location_kind,$5,NULLIF($6,'')::app.phone_e164) RETURNING id`,
			p.TenantID, unit, name, kind, in.Address, deref(in.Phone)).Scan(&id); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A branch with that name already exists")
			}
			return err
		}
		// Every shop or office keeps its own cash drawer (spec: cash accounts belong to a location).
		if kind != "warehouse" {
			if _, err := tx.Exec(ctx, `INSERT INTO app.financial_accounts (tenant_id, name, kind, location_id, business_unit_id) VALUES ($1,$2,'cash',$3,$4)`,
				p.TenantID, name+" till", id, unit); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "branch.created", "locations", id, nil, map[string]any{"name": name, "kind": kind}); err != nil {
			return err
		}
		var err error
		out, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $2`, p.TenantID, id))
		return err
	})
	return out, err
}

func (s *Service) GetBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) (Branch, error) {
	var b Branch
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) (err error) {
		if b, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $2`, p.TenantID, id)); err != nil {
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
		tag, err := tx.Exec(ctx, `
			UPDATE app.locations SET name = coalesce(NULLIF($3,''), name), address = coalesce($4, address),
			       phone = coalesce(NULLIF($5,'')::app.phone_e164, phone), kind = coalesce(NULLIF($6,'')::app.location_kind, kind)
			WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, strings.TrimSpace(in.Name), in.Address, deref(in.Phone), in.Kind)
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
		out, err = scanBranch(tx.QueryRow(ctx, branchSelect+` AND id = $2`, p.TenantID, id))
		return err
	})
	return out, err
}

func (s *Service) DeactivateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var isMain bool
		if err := tx.QueryRow(ctx, `SELECT is_default FROM app.locations WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, id).Scan(&isMain); err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		if isMain {
			return httpx.Conflict("invalid_status", "The main branch cannot be deactivated")
		}
		var stock decimal.Decimal
		var docs int
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(quantity_on_hand), 0) FROM app.stock_balances WHERE tenant_id = $1 AND location_id = $2`, p.TenantID, id).Scan(&stock); err != nil {
			return err
		}
		if stock.GreaterThan(decimal.Zero) {
			return httpx.Conflict("invalid_status", "This branch still has stock")
		}
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM app.sales WHERE tenant_id = $1 AND location_id = $2 AND status IN ('draft','held','pending_approval'))
			     + (SELECT count(*) FROM app.stock_transfers WHERE tenant_id = $1 AND (from_location_id = $2 OR to_location_id = $2) AND status IN ('draft','dispatched'))
			     + (SELECT count(*) FROM app.day_closings WHERE tenant_id = $1 AND location_id = $2 AND status = 'open')`, p.TenantID, id).Scan(&docs); err != nil {
			return err
		}
		if docs > 0 {
			return httpx.Conflict("invalid_status", "This branch has open documents (drafts, transfers in transit or an open day)")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.locations SET status = 'inactive' WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
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
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.full_name::text, coalesce(r.name::text, '') FROM app.memberships m
			JOIN iam.users u ON u.id = m.user_id
			LEFT JOIN app.membership_roles mr ON mr.tenant_id = m.tenant_id AND mr.membership_id = m.id
			LEFT JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id
			WHERE m.tenant_id = $1 AND m.default_location_id = $2 AND m.status = 'active' ORDER BY u.full_name`, p.TenantID, id)
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
		erows, err := tx.Query(ctx, `SELECT id, full_name::text, employee_no, status::text FROM app.employees WHERE tenant_id = $1 AND location_id = $2 ORDER BY full_name`, p.TenantID, id)
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
		rows, err := tx.Query(ctx, `SELECT id, name::text, rate_pct, is_default, status = 'active' FROM app.tax_rates WHERE tenant_id = $1 ORDER BY name`, p.TenantID)
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

// store checks the bytes (the client's Content-Type is ignored), saves them under the tenant's
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
	file, err := s.Store.Save(ctx, p.TenantID.String(), safe, upload.Reader(buf), max)
	if err != nil {
		return uuid.Nil, httpx.BadRequest("invalid_body", err.Error())
	}
	sum := sha256.Sum256(buf)
	var id uuid.UUID
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.files (tenant_id, storage_key, original_name, mime_type, size_bytes, sha256, classification)
			VALUES ($1,$2,$3,$4,$5,$6,$7::app.data_class) RETURNING id`,
			p.TenantID, file.Path, truncate(name, 255), mime, file.Size, sum[:], class).Scan(&id); err != nil {
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
	var path, name, mime string
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT storage_key, original_name, mime_type FROM app.files WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&path, &name, &mime); err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		// A data export holds the whole business; only someone who manages backups may download it.
		var isExport bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.backup_runs WHERE tenant_id = $1 AND storage_uri = $2)`, p.TenantID, path).Scan(&isExport); err != nil {
			return err
		}
		if isExport && !p.Has("security.backup.manage") {
			return httpx.NotFound("not_found", "File not found")
		}
		// Employee documents (contracts, ID copies) are for people who manage employees, and the employee.
		var docEmployee uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT employee_id FROM app.attachments WHERE file_id = $1 AND employee_id IS NOT NULL LIMIT 1`, id).Scan(&docEmployee); err == nil {
			own := p.EmployeeID != nil && *p.EmployeeID == docEmployee
			if !own && !p.Has("hr.employee.manage") && !p.Has("hr.employee.view") {
				return httpx.NotFound("not_found", "File not found")
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	})
	if err != nil {
		return "", "", nil, err
	}
	rc, err := s.Store.Open(ctx, path)
	return name, mime, rc, err
}
