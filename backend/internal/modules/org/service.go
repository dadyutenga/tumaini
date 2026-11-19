package org

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/chart"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
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

func (s *Service) GetBusiness(ctx context.Context, p *auth.Principal) (Business, error) {
	var b Business
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, name, legal_name, tax_number, reg_number, phone, email::text, address, logo_file, currency, timezone, is_active
			FROM businesses WHERE id = $1
		`, p.BusinessID).Scan(&b.ID, &b.Name, &b.LegalName, &b.TaxNumber, &b.RegNumber, &b.Phone, &b.Email, &b.Address, &b.LogoFile, &b.Currency, &b.Timezone, &b.IsActive)
	})
	return b, err
}

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
	if len(fields) > 0 {
		return Business{}, invalid(fields)
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, _ := s.scanBusiness(ctx, tx, p.BusinessID)
		if _, err := tx.Exec(ctx, `
			UPDATE businesses SET name=COALESCE(NULLIF($2,''), name), legal_name=COALESCE($3, legal_name),
			tax_number=COALESCE($4, tax_number), reg_number=COALESCE($5, reg_number), phone=COALESCE($6, phone),
			email=COALESCE($7::citext, email), address=COALESCE($8, address), currency=COALESCE(NULLIF($9,''), currency),
			timezone=COALESCE(NULLIF($10,''), timezone)
			WHERE id=$1
		`, p.BusinessID, in.Name, in.LegalName, in.TaxNumber, in.RegNumber, in.Phone, in.Email, in.Address, in.Currency, in.Timezone); err != nil {
			return err
		}
		// The rest of the system reads these two from settings, so keep them in step.
		for key, val := range map[string]string{"system.timezone": in.Timezone, "currency.code": in.Currency} {
			if val == "" {
				continue
			}
			raw, _ := json.Marshal(val)
			if _, err := tx.Exec(ctx, `
				INSERT INTO settings (business_id, key, value, updated_by) VALUES ($1,$2,$3,$4)
				ON CONFLICT (business_id, key) DO UPDATE SET value=EXCLUDED.value, updated_by=EXCLUDED.updated_by
			`, p.BusinessID, key, raw, p.UserID); err != nil {
				return err
			}
		}
		after, err := s.scanBusiness(ctx, tx, p.BusinessID)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "business.updated", "business", p.BusinessID, before, after)
	})
	if err != nil {
		return Business{}, err
	}
	return s.GetBusiness(ctx, p)
}

func (s *Service) scanBusiness(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Business, error) {
	var b Business
	err := tx.QueryRow(ctx, `
		SELECT id, name, legal_name, tax_number, reg_number, phone, email::text, address, logo_file, currency, timezone, is_active
		FROM businesses WHERE id=$1
	`, id).Scan(&b.ID, &b.Name, &b.LegalName, &b.TaxNumber, &b.RegNumber, &b.Phone, &b.Email, &b.Address, &b.LogoFile, &b.Currency, &b.Timezone, &b.IsActive)
	return b, err
}

func (s *Service) SaveLogo(ctx context.Context, p *auth.Principal, name string, r io.Reader) (string, error) {
	buf, mime, err := upload.Read(r, 2<<20, true)
	if err != nil {
		return "", err
	}
	file, err := s.Store.Save(ctx, p.BusinessID.String(), name, upload.Reader(buf), 2<<20)
	if err != nil {
		return "", httpx.BadRequest("invalid_body", err.Error())
	}
	id := ids.New()
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO files (id, business_id, path, original_name, mime_type, size_bytes, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, file.Path, name, mime, file.Size, p.UserID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE businesses SET logo_file=$2 WHERE id=$1`, p.BusinessID, id.String())
		return err
	})
	return id.String(), err
}

type Branch struct {
	ID       uuid.UUID  `json:"id"`
	Name     string     `json:"name"`
	Code     string     `json:"code"`
	Address  *string    `json:"address"`
	Phone    *string    `json:"phone"`
	Manager  *uuid.UUID `json:"manager_id"`
	IsMain   bool       `json:"is_main"`
	IsActive bool       `json:"is_active"`
	// InvoicePrefix is the prefix of this branch's invoice numbers (FR-SET-05); it can be set on update.
	InvoicePrefix *string `json:"invoice_prefix"`
}

func (s *Service) ListBranches(ctx context.Context, p *auth.Principal) ([]Branch, error) {
	var out []Branch
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := `SELECT id, name, code, address, phone, manager_id, is_main, is_active, ` + invoicePrefixSQL("branches.id") + ` FROM branches WHERE is_active OR $1`
		args := []any{p.IsOwner}
		if !p.IsOwner {
			q = `SELECT b.id, b.name, b.code, b.address, b.phone, b.manager_id, b.is_main, b.is_active, ` + invoicePrefixSQL("b.id") + `
				FROM branches b JOIN user_branches ub ON ub.branch_id=b.id AND ub.user_id=$1 ORDER BY b.name`
			args = []any{p.UserID}
		} else {
			q += ` ORDER BY name`
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b Branch
			if err := rows.Scan(&b.ID, &b.Name, &b.Code, &b.Address, &b.Phone, &b.Manager, &b.IsMain, &b.IsActive, &b.InvoicePrefix); err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	if out == nil {
		out = []Branch{}
	}
	return out, err
}

func (s *Service) CreateBranch(ctx context.Context, p *auth.Principal, in Branch) (Branch, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || strings.TrimSpace(in.Code) == "" {
		return Branch{}, httpx.BadRequest("invalid_body", "Branch name and code are required")
	}
	id := ids.New()
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	// The code is part of every document number, so keep it short and plain.
	if !branchCode.MatchString(code) {
		return Branch{}, invalid(map[string]string{"code": "use 1 to 8 letters or digits"})
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkManager(ctx, tx, in.Manager); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO branches (id, business_id, name, code, address, phone, manager_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, name, code, in.Address, in.Phone, in.Manager); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "Branch name or code already exists")
			}
			return err
		}
		if err := sequence.Ensure(ctx, tx, p.BusinessID, id); err != nil {
			return err
		}
		if _, err := chart.CreateMoneyAccount(ctx, tx, s.Ledger, chart.MoneyInput{
			BusinessID: p.BusinessID, BranchID: &id, Name: name + " till", Type: "cash",
			UserID: p.UserID, EntryDate: s.Clock.Now().Format("2006-01-02"),
		}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_branches (user_id, branch_id, business_id, is_default)
			SELECT u.id, $1, $2, false FROM users u JOIN roles r ON r.id=u.role_id WHERE r.slug='owner'
			ON CONFLICT DO NOTHING
		`, id, p.BusinessID)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "branch.created", "branch", id, nil, map[string]any{"name": name, "code": code})
	})
	if err != nil {
		return Branch{}, err
	}
	return s.GetBranch(ctx, p, id)
}

// invoicePrefixSQL is a scalar subquery for a branch's invoice prefix; col is a fixed column name.
func invoicePrefixSQL(col string) string {
	return `(SELECT ds.prefix FROM document_sequences ds WHERE ds.branch_id = ` + col + ` AND ds.doc_type = 'invoice')`
}

var branchCode = regexp.MustCompile(`^[A-Z0-9]{1,8}$`)

// checkManager makes sure a branch manager is an employee of this business.
func (s *Service) checkManager(ctx context.Context, tx pgx.Tx, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM employees WHERE id=$1)`, *id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return invalid(map[string]string{"manager_id": "employee not found"})
	}
	return nil
}

func (s *Service) GetBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) (Branch, error) {
	if !p.AllowsBranch(id) && !p.IsOwner {
		return Branch{}, httpx.NotFound("not_found", "Branch not found")
	}
	var b Branch
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, name, code, address, phone, manager_id, is_main, is_active, `+invoicePrefixSQL("branches.id")+` FROM branches WHERE id=$1`, id).
			Scan(&b.ID, &b.Name, &b.Code, &b.Address, &b.Phone, &b.Manager, &b.IsMain, &b.IsActive, &b.InvoicePrefix)
		if err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		return nil
	})
	return b, err
}

func (s *Service) UpdateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID, in Branch) (Branch, error) {
	if !p.SeesBranch("branches.manage", id) {
		return Branch{}, httpx.NotFound("not_found", "Branch not found")
	}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkManager(ctx, tx, in.Manager); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE branches SET name=COALESCE(NULLIF($2,''), name), address=COALESCE($3, address), phone=COALESCE($4, phone), manager_id=COALESCE($5, manager_id)
			WHERE id=$1
		`, id, strings.TrimSpace(in.Name), in.Address, in.Phone, in.Manager)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Branch not found")
		}
		if in.InvoicePrefix != nil {
			prefix := strings.ToUpper(strings.TrimSpace(*in.InvoicePrefix))
			if !invoicePrefix.MatchString(prefix) {
				return invalid(map[string]string{"invoice_prefix": "use 1 to 10 capital letters or digits"})
			}
			if _, err := tx.Exec(ctx, `UPDATE document_sequences SET prefix=$2 WHERE branch_id=$1 AND doc_type='invoice'`, id, prefix); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "branch.updated", "branch", id, nil, in)
	})
	if err != nil {
		return Branch{}, err
	}
	return s.GetBranch(ctx, p, id)
}

func (s *Service) DeactivateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	if !p.SeesBranch("branches.manage", id) {
		return httpx.NotFound("not_found", "Branch not found")
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var isMain bool
		if err := tx.QueryRow(ctx, `SELECT is_main FROM branches WHERE id=$1 FOR UPDATE`, id).Scan(&isMain); err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		if isMain {
			return httpx.Conflict("invalid_status", "The main branch cannot be deactivated")
		}
		var stock decimal.Decimal
		var tills, docs int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(qty_on_hand),0) FROM stock_levels WHERE branch_id=$1`, id).Scan(&stock); err != nil {
			return err
		}
		if stock.GreaterThan(decimal.Zero) {
			return httpx.Conflict("invalid_status", "This branch still has stock")
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM till_sessions WHERE branch_id=$1 AND status='open'`, id).Scan(&tills); err != nil {
			return err
		}
		if tills > 0 {
			return httpx.Conflict("invalid_status", "Close the open tills before deactivating this branch")
		}
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM sales WHERE branch_id=$1 AND status IN ('draft','held','pending_approval'))
			     + (SELECT count(*) FROM stock_transfers WHERE (from_branch_id=$1 OR to_branch_id=$1) AND status IN ('draft','dispatched'))
			     + (SELECT count(*) FROM stock_adjustments WHERE branch_id=$1 AND status IN ('draft','pending_approval'))
			     + (SELECT count(*) FROM purchase_orders WHERE branch_id=$1 AND status IN ('draft','pending_approval'))
			     + (SELECT count(*) FROM expenses WHERE branch_id=$1 AND status IN ('submitted','approved'))
		`, id).Scan(&docs); err != nil {
			return err
		}
		if docs > 0 {
			return httpx.Conflict("invalid_status", "This branch has open documents (drafts, transfers in transit, pending approvals or unpaid expenses)")
		}
		tag, err := tx.Exec(ctx, `UPDATE branches SET is_active=false WHERE id=$1 AND NOT is_main`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("invalid_status", "The main branch cannot be deactivated")
		}
		return s.Audit.Record(ctx, tx, p, "branch.deactivated", "branch", id, nil, map[string]any{"is_active": false})
	})
}

func (s *Service) BranchStaff(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	if _, err := s.GetBranch(ctx, p, id); err != nil {
		return nil, err
	}
	var users, employees []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT u.id, u.name, r.name FROM users u
			JOIN user_branches ub ON ub.user_id=u.id AND ub.branch_id=$1
			JOIN roles r ON r.id=u.role_id WHERE u.is_active ORDER BY u.name
		`, id)
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
		rows, err = tx.Query(ctx, `SELECT id, name, employee_no, status FROM employees WHERE branch_id=$1 ORDER BY name`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var eid uuid.UUID
			var name, no, status string
			if err := rows.Scan(&eid, &name, &no, &status); err != nil {
				return err
			}
			employees = append(employees, map[string]any{"id": eid, "name": name, "employee_no": no, "status": status})
		}
		return rows.Err()
	})
	if users == nil {
		users = []map[string]any{}
	}
	if employees == nil {
		employees = []map[string]any{}
	}
	return map[string]any{"users": users, "employees": employees}, err
}

type TaxRate struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Rate      decimal.Decimal `json:"rate"`
	IsDefault bool            `json:"is_default"`
	IsActive  bool            `json:"is_active"`
}

func (s *Service) ListTax(ctx context.Context, p *auth.Principal) ([]TaxRate, error) {
	var out []TaxRate
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, rate, is_default, is_active FROM tax_rates ORDER BY name`)
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
	if out == nil {
		out = []TaxRate{}
	}
	return out, err
}

func (s *Service) PaymentMethods(ctx context.Context, p *auth.Principal) (any, error) {
	var raw []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT value FROM settings WHERE key='payment.methods'`).Scan(&raw)
	})
	if err != nil {
		return map[string]any{}, nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v, nil
}

// Upload stores a file after checking its content. The client's Content-Type is ignored.
func (s *Service) Upload(ctx context.Context, p *auth.Principal, name, _ string, r io.Reader) (uuid.UUID, error) {
	buf, mime, err := upload.Read(r, 10<<20, false)
	if err != nil {
		return uuid.Nil, err
	}
	file, err := s.Store.Save(ctx, p.BusinessID.String(), name, upload.Reader(buf), 10<<20)
	if err != nil {
		return uuid.Nil, httpx.BadRequest("invalid_body", err.Error())
	}
	id := ids.New()
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO files (id, business_id, path, original_name, mime_type, size_bytes, uploaded_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, file.Path, name, mime, file.Size, p.UserID)
		return err
	})
	return id, err
}

func (s *Service) OpenFile(ctx context.Context, p *auth.Principal, id uuid.UUID) (string, string, io.ReadCloser, error) {
	var path, name, mime string
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT path, original_name, mime_type FROM files WHERE id=$1`, id).Scan(&path, &name, &mime)
		if err != nil {
			return httpx.NotFound("not_found", "File not found")
		}
		// A data export holds the whole business; only its requester or an Owner may download it.
		var exportOwner *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT user_id FROM export_jobs WHERE file_id=$1 LIMIT 1`, id).Scan(&exportOwner); err == nil {
			if exportOwner != nil && *exportOwner != p.UserID && !p.IsOwner {
				return httpx.NotFound("not_found", "File not found")
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Employee documents (contracts, ID copies) are for the people who manage employees and the employee.
		var docEmployee, docBranch uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT d.employee_id, e.branch_id FROM employee_documents d JOIN employees e ON e.id = d.employee_id WHERE d.file_id=$1`, id).Scan(&docEmployee, &docBranch); err == nil {
			own := p.EmployeeID != nil && *p.EmployeeID == docEmployee
			if !own && !p.SeesBranch("employees.manage", docBranch) {
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
