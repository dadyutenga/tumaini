package hr

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/modules/identity"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/phone"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
	"bms/internal/platform/storage"
)

type Service struct {
	DB       *db.DB
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
	Hash     auth.Params
	Store    storage.Store
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock, hash auth.Params, store storage.Store) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Notify: n, Clock: clk, Hash: hash, Store: store}
}

func removed(what string) error {
	return &httpx.Error{Status: 410, Code: "feature_removed", Message: what + " is not part of the approved model"}
}

func (s *Service) today(ctx context.Context, tx pgx.Tx) string {
	return hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
}

// ---------- employees ----------

type EmployeeIn struct {
	Name                  string           `json:"name"`
	Phone                 *string          `json:"phone"`
	Email                 *string          `json:"email" validate:"omitempty,email"`
	Position              string           `json:"position"`
	BranchID              *uuid.UUID       `json:"branch_id"`
	HireDate              string           `json:"hire_date" validate:"date"`
	BasePay               *decimal.Decimal `json:"base_pay"`
	PayMethod             *string          `json:"pay_method" validate:"omitempty,oneof=cash bank mobile_money"`
	PayAccount            *string          `json:"pay_account"`
	PayProvider           *string          `json:"pay_provider"`
	CompensationReason    string           `json:"compensation_reason"`
	EffectiveFrom         string           `json:"effective_from" validate:"date"`
	EmergencyContact      *string          `json:"emergency_contact"`
	EmergencyContactPhone *string          `json:"emergency_contact_phone"`
	PhotoFileID           *uuid.UUID       `json:"photo_file_id"`
	Status                string           `json:"status" validate:"omitempty,oneof=active suspended"`
}

const employeeCols = `e.id, e.employee_no, e.full_name::text AS name, e.phone::text AS phone, e.email::text AS email,
	e.position::text AS position, e.location_id AS branch_id, l.name::text AS branch_name,
	e.start_date AS hire_date, e.end_date AS terminated_on, e.status::text AS status,
	e.photo_file_id, e.emergency_contact_name AS emergency_contact, e.emergency_contact_phone::text AS emergency_contact_phone,
	e.membership_id, m.user_id, e.created_at, e.updated_at,
	comp.basic_salary AS base_pay, comp.pay_method::text AS pay_method,
	comp.payout_account_last4 AS pay_account_last4, comp.payout_provider AS pay_provider`

const employeeFrom = ` FROM app.employees e
	LEFT JOIN app.locations l ON l.tenant_id = e.tenant_id AND l.id = e.location_id
	LEFT JOIN app.memberships m ON m.tenant_id = e.tenant_id AND m.id = e.membership_id
	LEFT JOIN LATERAL (
		SELECT c.basic_salary, c.pay_method, c.payout_account_last4, c.payout_provider
		FROM app.employee_compensation c
		WHERE c.tenant_id = e.tenant_id AND c.employee_id = e.id AND c.effective_to IS NULL
		ORDER BY c.effective_from DESC LIMIT 1
	) comp ON true`

var salaryFields = []string{"base_pay", "pay_method", "pay_account_last4", "pay_provider"}

func canSeeSalary(p *auth.Principal, employeeID string) bool {
	if p.Has("hr.compensation.view") || p.Has("hr.compensation.manage") || p.Has("hr.payroll.view") {
		return true
	}
	return p.EmployeeID != nil && p.EmployeeID.String() == employeeID
}

func stripSalary(p *auth.Principal, m map[string]any) map[string]any {
	if !canSeeSalary(p, asString(m["id"])) {
		for _, k := range salaryFields {
			delete(m, k)
		}
	}
	return m
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type EmployeeFilter struct {
	Q        string
	Status   string
	BranchID *uuid.UUID
	Sort     string
}

func (s *Service) ListEmployees(ctx context.Context, p *auth.Principal, f EmployeeFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("e.tenant_id = $1")
		branch := f.BranchID
		if branch == nil {
			branch = p.BranchID
		}
		q.If(branch != nil, "e.location_id = ?", branch)
		q.If(f.Status != "", "e.status = ?::app.employee_status", f.Status)
		q.If(f.Q != "", "(e.full_name ILIKE ? OR e.employee_no ILIKE ? OR e.phone ILIKE ? OR e.position ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%", "%"+f.Q+"%", "%"+f.Q+"%")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.employees e`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		order := f.Sort
		if order == "" {
			order = "e.full_name ASC"
		}
		var err error
		out, err = hrkit.All(ctx, tx, `SELECT `+employeeCols+employeeFrom+q.Where()+` ORDER BY `+order+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	for i := range out {
		out[i] = stripSalary(p, out[i])
	}
	return out, total, err
}

func (s *Service) loadEmployee(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return hrkit.One(ctx, tx, "Employee not found", `SELECT `+employeeCols+employeeFrom+` WHERE e.tenant_id = $1 AND e.id = $2`, p.TenantID, id)
}

func (s *Service) GetEmployee(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		summary, err := hrkit.One(ctx, tx, "Employee not found", `
			SELECT count(*) FILTER (WHERE status IN ('present','late')) AS days_present,
			       count(*) FILTER (WHERE status = 'late') AS days_late,
			       count(*) FILTER (WHERE status = 'absent') AS days_absent,
			       count(*) FILTER (WHERE status = 'on_leave') AS days_leave
			FROM app.attendance_records WHERE tenant_id = $1 AND employee_id = $2 AND work_date >= date_trunc('month', $3::date)`,
			p.TenantID, id, s.today(ctx, tx))
		if err != nil {
			return err
		}
		m["attendance_this_month"] = summary
		out = stripSalary(p, m)
		return nil
	})
	return out, err
}

var onlyDigits = regexp.MustCompile(`[^0-9]`)

func last4Digits(raw string) *string {
	d := onlyDigits.ReplaceAllString(raw, "")
	if len(d) < 4 {
		return nil
	}
	s := d[len(d)-4:]
	return &s
}

func insertCompensation(ctx context.Context, tx pgx.Tx, tenantID, empID uuid.UUID, basic decimal.Decimal, method, account, provider, effectiveFrom, reason string) error {
	if method == "" {
		method = "cash"
	}
	if reason = strings.TrimSpace(reason); len(reason) < 3 {
		reason = "Compensation set"
	}
	var cipher []byte
	var keyID, last4 *string
	if method != "cash" {
		acc := strings.TrimSpace(account)
		if acc == "" {
			return httpx.BadRequest("invalid_body", "pay_account is required for bank and mobile money payouts")
		}
		cipher = []byte(acc)
		k := "local-v1"
		keyID = &k
		last4 = last4Digits(acc)
	}
	// Close any open compensation row before appending a new one.
	var prevID uuid.UUID
	var prevFrom string
	err := tx.QueryRow(ctx, `
		SELECT id, effective_from::text FROM app.employee_compensation
		WHERE tenant_id = $1 AND employee_id = $2 AND effective_to IS NULL
		ORDER BY effective_from DESC LIMIT 1 FOR UPDATE`, tenantID, empID).Scan(&prevID, &prevFrom)
	if err == nil {
		if effectiveFrom <= prevFrom {
			return httpx.Conflict("invalid_body", "effective_from must be after the current compensation effective date ("+prevFrom+")")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.employee_compensation SET effective_to = $3::date WHERE tenant_id = $1 AND id = $2`, tenantID, prevID, effectiveFrom); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app.employee_compensation
			(tenant_id, employee_id, effective_from, basic_salary, pay_method, payout_account_ciphertext, payout_key_id, payout_account_last4, payout_provider, reason)
		VALUES ($1,$2,$3::date,$4,$5::app.pay_method,$6,$7,$8,NULLIF($9,''),$10)`,
		tenantID, empID, effectiveFrom, money.Money(basic), method, cipher, keyID, last4, strings.TrimSpace(provider), reason)
	return err
}

func (s *Service) CreateEmployee(ctx context.Context, p *auth.Principal, in EmployeeIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("invalid_body", "Name is required")
	}
	if in.BasePay != nil && in.BasePay.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Base pay cannot be negative")
	}
	if in.BasePay != nil && !p.Has("hr.compensation.manage") {
		return nil, httpx.Forbidden("permission_denied", "You cannot set employee salaries")
	}
	ph, err := phone.Optional(deref(in.Phone))
	if err != nil {
		return nil, err
	}
	eph, err := phone.Optional(deref(in.EmergencyContactPhone))
	if err != nil {
		return nil, err
	}
	loc := in.BranchID
	if loc == nil {
		loc = p.BranchID
	}
	pos := strings.TrimSpace(in.Position)
	if pos == "" {
		pos = "Staff"
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if loc != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM app.locations WHERE tenant_id = $1 AND id = $2`, p.TenantID, *loc).Scan(&ok); err != nil || !ok {
				return httpx.BadRequest("invalid_branch", "Location not found")
			}
		}
		startDate := strings.TrimSpace(in.HireDate)
		if startDate == "" {
			t := hrkit.MustDate(s.today(ctx, tx))
			startDate = t.AddDate(0, 0, 1-t.Day()).Format(hrkit.DateFmt)
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "employee")
		if err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.employees
				(tenant_id, employee_no, full_name, phone, email, position, location_id, start_date, photo_file_id, emergency_contact_name, emergency_contact_phone)
			VALUES ($1,$2,$3,NULLIF($4,'')::app.phone_e164,NULLIF($5,'')::app.email,$6,$7,$8::date,$9,NULLIF($10,''),NULLIF($11,'')::app.phone_e164)
			RETURNING id`,
			p.TenantID, number, in.Name, ph, deref(in.Email), pos, loc, startDate, in.PhotoFileID, deref(in.EmergencyContact), eph).Scan(&id); err != nil {
			return err
		}
		if in.BasePay != nil {
			eff := strings.TrimSpace(in.EffectiveFrom)
			if eff == "" {
				eff = startDate
			}
			if err := insertCompensation(ctx, tx, p.TenantID, id, *in.BasePay, deref(in.PayMethod), deref(in.PayAccount), deref(in.PayProvider), eff, in.CompensationReason); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "employee.created", "employees", id, nil, map[string]any{"employee_no": number, "name": in.Name}); err != nil {
			return err
		}
		out, err = s.loadEmployee(ctx, tx, p, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return stripSalary(p, out), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func (s *Service) UpdateEmployee(ctx context.Context, p *auth.Principal, id uuid.UUID, in EmployeeIn) (map[string]any, error) {
	if in.BasePay != nil && in.BasePay.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Base pay cannot be negative")
	}
	if in.BasePay != nil && !p.Has("hr.compensation.manage") {
		return nil, httpx.Forbidden("permission_denied", "You cannot change salary fields")
	}
	var ph string
	if in.Phone != nil {
		var err error
		if ph, err = phone.Optional(*in.Phone); err != nil {
			return nil, err
		}
	}
	var eph string
	if in.EmergencyContactPhone != nil {
		var err error
		if eph, err = phone.Optional(*in.EmergencyContactPhone); err != nil {
			return nil, err
		}
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if before["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "A terminated employee cannot be edited")
		}
		if in.BranchID != nil {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM app.locations WHERE tenant_id = $1 AND id = $2`, p.TenantID, *in.BranchID).Scan(&ok); err != nil || !ok {
				return httpx.BadRequest("invalid_branch", "Location not found")
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.employees SET
			  full_name = COALESCE(NULLIF($3,''), full_name),
			  phone = CASE WHEN $4 THEN NULLIF($5,'')::app.phone_e164 ELSE phone END,
			  email = CASE WHEN $6::text IS NULL THEN email ELSE NULLIF($6,'')::app.email END,
			  position = COALESCE(NULLIF($7,''), position),
			  location_id = COALESCE($8, location_id),
			  start_date = COALESCE(NULLIF($9,'')::date, start_date),
			  photo_file_id = COALESCE($10, photo_file_id),
			  emergency_contact_name = CASE WHEN $11::text IS NULL THEN emergency_contact_name ELSE NULLIF($11,'') END,
			  emergency_contact_phone = CASE WHEN $12 THEN NULLIF($13,'')::app.phone_e164 ELSE emergency_contact_phone END,
			  status = COALESCE(NULLIF($14,'')::app.employee_status, status)
			WHERE tenant_id = $1 AND id = $2`,
			p.TenantID, id, strings.TrimSpace(in.Name), in.Phone != nil, ph, in.Email, strings.TrimSpace(in.Position),
			in.BranchID, strings.TrimSpace(in.HireDate), in.PhotoFileID, in.EmergencyContact, in.EmergencyContactPhone != nil, eph, strings.TrimSpace(in.Status)); err != nil {
			return err
		}
		if in.BasePay != nil {
			eff := strings.TrimSpace(in.EffectiveFrom)
			if eff == "" {
				eff = s.today(ctx, tx)
			}
			if err := insertCompensation(ctx, tx, p.TenantID, id, *in.BasePay, deref(in.PayMethod), deref(in.PayAccount), deref(in.PayProvider), eff, in.CompensationReason); err != nil {
				return err
			}
		}
		after, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "employee.updated", "employees", id, before, after); err != nil {
			return err
		}
		out = after
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stripSalary(p, out), nil
}

type TerminateIn struct {
	Date   string `json:"date" validate:"required,date"`
	Reason string `json:"reason" validate:"required"`
}

func (s *Service) Terminate(ctx context.Context, p *auth.Principal, id uuid.UUID, in TerminateIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if before["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "This employee is already terminated")
		}
		if in.Date < asString(before["hire_date"]) {
			return httpx.BadRequest("invalid_body", "Termination date cannot be before the start date")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.employees SET status = 'terminated', end_date = $3::date WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, in.Date); err != nil {
			return err
		}
		// Revoke linked seat (unless doing so would remove the last active Admin, enforced by tg_keep_one_admin).
		var mid *uuid.UUID
		if tx.QueryRow(ctx, `SELECT membership_id FROM app.employees WHERE tenant_id = $1 AND id = $2`, p.TenantID, id).Scan(&mid) == nil && mid != nil {
			var isOnlyAdmin bool
			_ = tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM app.membership_roles mr JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id WHERE mr.tenant_id = $1 AND mr.membership_id = $2 AND r.code = 'admin')
				   AND (SELECT count(*) FROM app.membership_roles mr JOIN app.roles r ON r.tenant_id = mr.tenant_id AND r.id = mr.role_id JOIN app.memberships m ON m.tenant_id = mr.tenant_id AND m.id = mr.membership_id WHERE mr.tenant_id = $1 AND r.code = 'admin' AND m.status = 'active') <= 1`,
				p.TenantID, *mid).Scan(&isOnlyAdmin)
			if !isOnlyAdmin {
				if _, err := tx.Exec(ctx, `UPDATE app.memberships SET status = 'revoked' WHERE tenant_id = $1 AND id = $2 AND status IN ('active','suspended','invited')`, p.TenantID, *mid); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at = now() WHERE membership_id = $1 AND revoked_at IS NULL`, *mid); err != nil {
					return err
				}
			}
		}
		after, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		out = after
		return s.Audit.Record(ctx, tx, p, "employee.terminated", "employees", id, before, map[string]any{"terminated_on": in.Date, "reason": in.Reason})
	})
	if err != nil {
		return nil, err
	}
	return stripSalary(p, out), nil
}

type UserIn struct {
	Name      string      `json:"name"`
	Phone     string      `json:"phone"`
	Email     string      `json:"email" validate:"omitempty,email"`
	Password  string      `json:"password" validate:"required"`
	RoleID    uuid.UUID   `json:"role_id" validate:"required"`
	BranchIDs []uuid.UUID `json:"branch_ids"`
	Language  string      `json:"language" validate:"omitempty,oneof=en sw"`
}

// CreateLogin creates an iam.users account and app.memberships seat linked to the employee.
func (s *Service) CreateLogin(ctx context.Context, p *auth.Principal, id uuid.UUID, in UserIn) (map[string]any, error) {
	if err := auth.PasswordOK(in.Password); err != nil {
		return nil, httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		emp, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if emp["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "A terminated employee cannot get a login")
		}
		if emp["membership_id"] != nil {
			return httpx.Conflict("duplicate", "This employee already has a login")
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = asString(emp["name"])
		}
		rawPhone := strings.TrimSpace(in.Phone)
		email := strings.TrimSpace(in.Email)
		if rawPhone == "" && email == "" {
			rawPhone = asString(emp["phone"])
			email = asString(emp["email"])
		}
		if rawPhone == "" && email == "" {
			return httpx.BadRequest("invalid_body", "A phone or email is required to log in")
		}
		ph, err := phone.Optional(rawPhone)
		if err != nil {
			return err
		}
		if err := identity.GuardAssignRole(ctx, tx, p, in.RoleID); err != nil {
			return err
		}
		var loc *uuid.UUID
		if len(in.BranchIDs) > 0 {
			l := in.BranchIDs[0]
			loc = &l
		} else if b, err := uuid.Parse(asString(emp["branch_id"])); err == nil {
			loc = &b
		} else {
			_ = tx.QueryRow(ctx, `SELECT id FROM app.locations WHERE tenant_id = $1 AND is_default`, p.TenantID).Scan(&loc)
		}
		lang := in.Language
		if lang == "" {
			lang = "en"
		}
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO iam.users (full_name, email, phone, preferred_locale)
			VALUES ($1, NULLIF($2,'')::app.email, NULLIF($3,'')::app.phone_e164, $4) RETURNING id`,
			name, email, ph, lang).Scan(&userID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already in use")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.user_credentials (user_id, password_hash, must_change_password) VALUES ($1,$2,true)`, userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash) VALUES ($1,$2)`, userID, hash); err != nil {
			return err
		}
		var membershipID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.memberships (tenant_id, user_id, default_location_id, status)
			VALUES ($1,$2,$3,'active') RETURNING id`, p.TenantID, userID, loc).Scan(&membershipID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.membership_roles (tenant_id, membership_id, role_id) VALUES ($1,$2,$3)`, p.TenantID, membershipID, in.RoleID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.employees SET membership_id = $3 WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, membershipID); err != nil {
			return err
		}
		out = map[string]any{"id": userID, "membership_id": membershipID, "employee_id": id, "name": name, "phone": ph, "email": email, "role_id": in.RoleID}
		return s.Audit.Record(ctx, tx, p, "user.created", "memberships", membershipID, nil, map[string]any{"name": name, "role_id": in.RoleID, "employee_id": id})
	})
	return out, err
}

// Statement lists payroll lines, payments and advances for an employee over a period.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		emp, err := s.loadEmployee(ctx, tx, p, id)
		if err != nil {
			return err
		}
		out, err = statement(ctx, tx, p.TenantID, id, from, to)
		if err != nil {
			return err
		}
		out["employee"] = map[string]any{"id": emp["id"], "employee_no": emp["employee_no"], "name": emp["name"]}
		return nil
	})
	return out, err
}

func statement(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, from, to string) (map[string]any, error) {
	if from == "" {
		from = "0001-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	slips, err := hrkit.All(ctx, tx, `
		SELECT COALESCE(ps.id, pl.id) AS id, COALESCE(ps.payslip_no, '') AS number, r.id AS run_id,
		       r.period_year, r.period_month, r.status::text AS run_status,
		       pl.gross_pay AS gross, (pl.deductions + pl.advance_repayment + pl.statutory_deductions) AS total_deductions,
		       pl.net_pay AS net, CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END AS paid_total, pl.paid_at
		FROM app.payroll_lines pl
		JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id
		LEFT JOIN app.payslips ps ON ps.tenant_id = pl.tenant_id AND ps.payroll_line_id = pl.id
		WHERE pl.tenant_id = $1 AND pl.employee_id = $2 AND r.status IN ('approved','paid')
		  AND make_date(r.period_year, r.period_month, 1) BETWEEN $3::date AND $4::date
		ORDER BY r.period_year, r.period_month`, tenantID, id, from, to)
	if err != nil {
		return nil, err
	}
	pays, err := hrkit.All(ctx, tx, `
		SELECT id, payment_no AS number, 'out' AS direction, method::text AS method, amount, business_date AS payment_date, status::text AS status, reference
		FROM app.payments_made
		WHERE tenant_id = $1 AND employee_id = $2 AND business_date BETWEEN $3::date AND $4::date
		ORDER BY business_date, created_at`, tenantID, id, from, to)
	if err != nil {
		return nil, err
	}
	loans, err := hrkit.All(ctx, tx, `
		SELECT id, kind::text AS kind, principal, monthly_deduction, amount_repaid, balance, status::text AS status, issued_on AS start_period
		FROM app.employee_advances WHERE tenant_id = $1 AND employee_id = $2 ORDER BY created_at`, tenantID, id)
	if err != nil {
		return nil, err
	}
	var gross, ded, net, paid decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(pl.gross_pay), 0),
		       COALESCE(SUM(pl.deductions + pl.advance_repayment + pl.statutory_deductions), 0),
		       COALESCE(SUM(pl.net_pay), 0),
		       COALESCE(SUM(CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END), 0)
		FROM app.payroll_lines pl JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id
		WHERE pl.tenant_id = $1 AND pl.employee_id = $2 AND r.status IN ('approved','paid')
		  AND make_date(r.period_year, r.period_month, 1) BETWEEN $3::date AND $4::date`, tenantID, id, from, to).
		Scan(&gross, &ded, &net, &paid); err != nil {
		return nil, err
	}
	var loanBal decimal.Decimal
	_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(balance), 0) FROM app.employee_advances WHERE tenant_id = $1 AND employee_id = $2 AND status = 'active'`, tenantID, id).Scan(&loanBal)
	return map[string]any{
		"payslips": slips, "payments": pays, "loans": loans,
		"totals": map[string]any{"gross": gross, "deductions": ded, "net": net, "paid": paid, "unpaid": net.Sub(paid), "loan_balance": loanBal},
	}, nil
}

// ---------- employee allowances (replaces salary components) ----------

func (s *Service) Components(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, id); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT id, name::text AS name, amount, effective_from, effective_to
			FROM app.employee_allowances
			WHERE tenant_id = $1 AND employee_id = $2 AND effective_to IS NULL
			ORDER BY name`, p.TenantID, id)
		return err
	})
	return out, err
}

type ComponentAssign struct {
	Name          string          `json:"name" validate:"required,max=120"`
	Amount        decimal.Decimal `json:"amount" validate:"dpos"`
	EffectiveFrom string          `json:"effective_from" validate:"date"`
}

func (s *Service) PutComponents(ctx context.Context, p *auth.Principal, id uuid.UUID, in []ComponentAssign) ([]map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, id); err != nil {
			return err
		}
		today := s.today(ctx, tx)
		// End currently active allowances (only those whose effective_from < today; same-day ones are deleted).
		if _, err := tx.Exec(ctx, `
			UPDATE app.employee_allowances SET effective_to = $3::date
			WHERE tenant_id = $1 AND employee_id = $2 AND effective_to IS NULL AND effective_from < $3::date`, p.TenantID, id, today); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.employee_allowances SET effective_to = effective_from + 1
			WHERE tenant_id = $1 AND employee_id = $2 AND effective_to IS NULL`, p.TenantID, id); err != nil {
			return err
		}
		for _, a := range in {
			eff := strings.TrimSpace(a.EffectiveFrom)
			if eff == "" {
				eff = today
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO app.employee_allowances (tenant_id, employee_id, name, amount, effective_from)
				VALUES ($1,$2,$3,$4,$5::date)`, p.TenantID, id, strings.TrimSpace(a.Name), money.Money(a.Amount), eff); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "employee.allowances_set", "employees", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.Components(ctx, p, id)
}

// ---------- leave balance ----------

func (s *Service) LeaveBalance(ctx context.Context, p *auth.Principal, id uuid.UUID, year int) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, id); err != nil {
			return err
		}
		var err error
		out, err = leaveBalance(ctx, tx, p.TenantID, id, s.yearOf(ctx, tx, year))
		return err
	})
	return out, err
}

func (s *Service) yearOf(ctx context.Context, tx pgx.Tx, year int) int {
	if year > 0 {
		return year
	}
	return hrkit.MustDate(s.today(ctx, tx)).Year()
}

func leaveBalance(ctx context.Context, tx pgx.Tx, tenantID, employee uuid.UUID, year int) ([]map[string]any, error) {
	return hrkit.All(ctx, tx, `
		SELECT lt.id AS leave_type_id, lt.name::text AS name, lt.is_paid, COALESCE(lt.annual_days, 0) AS allowed,
		       COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'approved'), 0) AS taken,
		       COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'requested'), 0) AS pending,
		       GREATEST(COALESCE(lt.annual_days, 0) - COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'approved'), 0), 0) AS remaining
		FROM app.leave_types lt
		LEFT JOIN app.leave_requests lr ON lr.tenant_id = lt.tenant_id AND lr.leave_type_id = lt.id
		     AND lr.employee_id = $2 AND EXTRACT(YEAR FROM lr.start_date) = $3
		WHERE lt.tenant_id = $1 AND lt.status = 'active'
		GROUP BY lt.id ORDER BY lt.name`, tenantID, employee, year)
}
