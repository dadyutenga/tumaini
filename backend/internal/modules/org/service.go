package org

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	file, err := s.Store.Save(ctx, p.BusinessID.String(), name, r, 2<<20)
	if err != nil {
		return "", httpx.BadRequest("invalid_body", err.Error())
	}
	id := ids.New()
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO files (id, business_id, path, original_name, mime_type, size_bytes, uploaded_by)
			VALUES ($1,$2,$3,$4,'image', $5,$6)
		`, id, p.BusinessID, file.Path, name, file.Size, p.UserID); err != nil {
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
}

func (s *Service) ListBranches(ctx context.Context, p *auth.Principal) ([]Branch, error) {
	var out []Branch
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := `SELECT id, name, code, address, phone, manager_id, is_main, is_active FROM branches WHERE is_active OR $1`
		args := []any{p.IsOwner}
		if !p.IsOwner {
			q = `SELECT b.id, b.name, b.code, b.address, b.phone, b.manager_id, b.is_main, b.is_active
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
			if err := rows.Scan(&b.ID, &b.Name, &b.Code, &b.Address, &b.Phone, &b.Manager, &b.IsMain, &b.IsActive); err != nil {
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
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Code) == "" {
		return Branch{}, httpx.BadRequest("invalid_body", "Branch name and code are required")
	}
	id := ids.New()
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO branches (id, business_id, name, code, address, phone, manager_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, strings.TrimSpace(in.Name), code, in.Address, in.Phone, in.Manager); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "Branch name or code already exists")
			}
			return err
		}
		if err := sequence.Ensure(ctx, tx, p.BusinessID, id); err != nil {
			return err
		}
		if _, err := chart.CreateMoneyAccount(ctx, tx, s.Ledger, chart.MoneyInput{
			BusinessID: p.BusinessID, BranchID: &id, Name: in.Name + " till", Type: "cash",
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
		return s.Audit.Record(ctx, tx, p, "branch.created", "branch", id, nil, map[string]any{"name": in.Name, "code": code})
	})
	if err != nil {
		return Branch{}, err
	}
	return s.GetBranch(ctx, p, id)
}

func (s *Service) GetBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) (Branch, error) {
	if !p.AllowsBranch(id) && !p.IsOwner {
		return Branch{}, httpx.NotFound("not_found", "Branch not found")
	}
	var b Branch
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, name, code, address, phone, manager_id, is_main, is_active FROM branches WHERE id=$1`, id).
			Scan(&b.ID, &b.Name, &b.Code, &b.Address, &b.Phone, &b.Manager, &b.IsMain, &b.IsActive)
		if err != nil {
			return httpx.NotFound("not_found", "Branch not found")
		}
		return nil
	})
	return b, err
}

func (s *Service) UpdateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID, in Branch) (Branch, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
		return s.Audit.Record(ctx, tx, p, "branch.updated", "branch", id, nil, in)
	})
	if err != nil {
		return Branch{}, err
	}
	return s.GetBranch(ctx, p, id)
}

func (s *Service) DeactivateBranch(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
			SELECT count(*) FROM sales WHERE branch_id=$1 AND status IN ('draft','held','pending_approval')
		`, id).Scan(&docs); err != nil {
			return err
		}
		if docs > 0 {
			return httpx.Conflict("invalid_status", "This branch has unposted documents")
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

func (s *Service) Settings(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	grouped := map[string]map[string]any{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT key, value FROM settings ORDER BY key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				return err
			}
			group, name, ok := strings.Cut(key, ".")
			if !ok {
				group, name = "system", key
			}
			if grouped[group] == nil {
				grouped[group] = map[string]any{}
			}
			var v any
			_ = json.Unmarshal(raw, &v)
			grouped[group][name] = v
		}
		return rows.Err()
	})
	out := map[string]any{}
	for k, v := range grouped {
		out[k] = v
	}
	return out, err
}

func (s *Service) UpdateSettings(ctx context.Context, p *auth.Principal, group string, body map[string]any) error {
	allowed := map[string]bool{"tax": true, "currency": true, "invoice": true, "receipt": true, "payroll": true, "notifications": true, "system": true}
	if !allowed[group] {
		return httpx.BadRequest("invalid_body", "Unknown settings group")
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		for name, val := range body {
			key := group + "." + name
			raw, err := json.Marshal(val)
			if err != nil {
				return err
			}
			var old []byte
			_ = tx.QueryRow(ctx, `SELECT value FROM settings WHERE key=$1`, key).Scan(&old)
			if _, err := tx.Exec(ctx, `
				INSERT INTO settings (business_id, key, value, updated_by) VALUES ($1,$2,$3,$4)
				ON CONFLICT (business_id, key) DO UPDATE SET value=EXCLUDED.value, updated_by=EXCLUDED.updated_by
			`, p.BusinessID, key, raw, p.UserID); err != nil {
				return err
			}
			if group == "currency" && name == "code" {
				if code, ok := val.(string); ok && code != "" {
					_, _ = tx.Exec(ctx, `UPDATE businesses SET currency=$2 WHERE id=$1`, p.BusinessID, code)
				}
			}
			if group == "system" && name == "timezone" {
				if tz, ok := val.(string); ok && tz != "" {
					_, _ = tx.Exec(ctx, `UPDATE businesses SET timezone=$2 WHERE id=$1`, p.BusinessID, tz)
				}
			}
			_ = s.Audit.Record(ctx, tx, p, "settings.updated", "settings", p.BusinessID, map[string]any{"key": key, "value": json.RawMessage(old)}, map[string]any{"key": key, "value": val})
		}
		return nil
	})
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

func (s *Service) SaveTax(ctx context.Context, p *auth.Principal, id *uuid.UUID, in TaxRate) (TaxRate, error) {
	var outID uuid.UUID
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if in.IsDefault {
			_, _ = tx.Exec(ctx, `UPDATE tax_rates SET is_default=false`)
		}
		if id == nil {
			outID = ids.New()
			_, err := tx.Exec(ctx, `INSERT INTO tax_rates (id, business_id, name, rate, is_default, is_active) VALUES ($1,$2,$3,$4,$5,true)`,
				outID, p.BusinessID, in.Name, in.Rate, in.IsDefault)
			return err
		}
		outID = *id
		tag, err := tx.Exec(ctx, `UPDATE tax_rates SET name=COALESCE(NULLIF($2,''), name), rate=$3, is_default=$4, is_active=$5 WHERE id=$1`,
			outID, in.Name, in.Rate, in.IsDefault, in.IsActive)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Tax rate not found")
		}
		return s.Audit.Record(ctx, tx, p, "tax_rate.updated", "tax_rate", outID, nil, in)
	})
	if err != nil {
		return TaxRate{}, err
	}
	var t TaxRate
	_ = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, rate, is_default, is_active FROM tax_rates WHERE id=$1`, outID).
			Scan(&t.ID, &t.Name, &t.Rate, &t.IsDefault, &t.IsActive)
	})
	return t, nil
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

func (s *Service) UpdatePaymentMethods(ctx context.Context, p *auth.Principal, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO settings (business_id, key, value, updated_by) VALUES ($1,'payment.methods',$2,$3)
			ON CONFLICT (business_id, key) DO UPDATE SET value=EXCLUDED.value, updated_by=EXCLUDED.updated_by
		`, p.BusinessID, raw, p.UserID)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payment_methods.updated", "settings", p.BusinessID, nil, body)
	})
}

func (s *Service) Upload(ctx context.Context, p *auth.Principal, name, mime string, r io.Reader) (uuid.UUID, error) {
	file, err := s.Store.Save(ctx, p.BusinessID.String(), name, r, 10<<20)
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
		return nil
	})
	if err != nil {
		return "", "", nil, err
	}
	rc, err := s.Store.Open(ctx, path)
	return name, mime, rc, err
}
