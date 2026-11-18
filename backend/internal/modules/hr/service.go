package hr

import (
	"context"
	"encoding/json"
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
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Clock    clock.Clock
	Hash     auth.Params
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock, hash auth.Params) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Notify: n, Clock: clk, Hash: hash}
}

// today is the business-local date.
func (s *Service) today(ctx context.Context, tx pgx.Tx) string {
	return hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
}

// ---------- employees ----------

type EmployeeIn struct {
	Name             string           `json:"name"`
	Phone            *string          `json:"phone"`
	Email            *string          `json:"email" validate:"omitempty,email"`
	Address          *string          `json:"address"`
	IDReference      *string          `json:"id_reference"`
	PhotoFile        *string          `json:"photo_file"`
	BranchID         *uuid.UUID       `json:"branch_id"`
	DepartmentID     *uuid.UUID       `json:"department_id"`
	PositionID       *uuid.UUID       `json:"position_id"`
	HireDate         string           `json:"hire_date" validate:"date"`
	EmploymentType   string           `json:"employment_type" validate:"omitempty,oneof=permanent contract casual"`
	PayType          string           `json:"pay_type" validate:"omitempty,oneof=monthly daily hourly"`
	BasePay          *decimal.Decimal `json:"base_pay"`
	PayMethod        *string          `json:"pay_method" validate:"omitempty,oneof=cash bank mobile_money"`
	PayAccount       *string          `json:"pay_account"`
	EmergencyContact *string          `json:"emergency_contact"`
	Status           string           `json:"status" validate:"omitempty,oneof=active suspended"`
}

const employeeCols = `e.id, e.employee_no, e.name, e.phone, e.email::text AS email, e.address, e.id_reference, e.photo_file,
	e.branch_id, b.name AS branch_name, e.department_id, d.name AS department_name, e.position_id, ps.name AS position_name,
	e.hire_date, e.employment_type, e.pay_type, e.base_pay, e.pay_method, e.pay_account, e.emergency_contact,
	e.status, e.terminated_on, e.termination_reason, e.created_at, e.updated_at,
	(SELECT u.id FROM users u WHERE u.employee_id = e.id LIMIT 1) AS user_id`

const employeeFrom = ` FROM employees e
	JOIN branches b ON b.id = e.branch_id
	LEFT JOIN departments d ON d.id = e.department_id
	LEFT JOIN positions ps ON ps.id = e.position_id`

var salaryFields = []string{"base_pay", "pay_method", "pay_account"}

// canSeeSalary reports whether the caller may read salary fields of employee.
func canSeeSalary(p *auth.Principal, employeeID string) bool {
	if _, ok := p.Can("employees.view_salary"); ok || p.IsOwner {
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
	Q            string
	Status       string
	BranchID     *uuid.UUID
	DepartmentID *uuid.UUID
	PositionID   *uuid.UUID
	Sort         string
}

func (s *Service) ListEmployees(ctx context.Context, p *auth.Principal, f EmployeeFilter, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("employees.view"); br != nil {
			q.Add("e.branch_id = ANY(?)", br)
		}
		q.If(f.BranchID != nil, "e.branch_id = ?", f.BranchID)
		q.If(f.DepartmentID != nil, "e.department_id = ?", f.DepartmentID)
		q.If(f.PositionID != nil, "e.position_id = ?", f.PositionID)
		q.If(f.Status != "", "e.status = ?", f.Status)
		q.If(f.Q != "", "(e.name ILIKE ? OR e.employee_no ILIKE ? OR e.phone ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%", "%"+f.Q+"%")
		if err := tx.QueryRow(ctx, `SELECT count(*)`+employeeFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `SELECT `+employeeCols+employeeFrom+q.Where()+` ORDER BY `+f.Sort+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	for i := range out {
		out[i] = stripSalary(p, out[i])
	}
	return out, total, err
}

// loadEmployee reads one employee inside the caller's scope for key.
func (s *Service) loadEmployee(ctx context.Context, tx pgx.Tx, p *auth.Principal, key string, id uuid.UUID) (map[string]any, error) {
	m, err := hrkit.One(ctx, tx, "Employee not found", `SELECT `+employeeCols+employeeFrom+` WHERE e.id = $1`, id)
	if err != nil {
		return nil, err
	}
	branch, _ := uuid.Parse(asString(m["branch_id"]))
	own := p.EmployeeID != nil && *p.EmployeeID == id
	if !own && !p.SeesBranch(key, branch) {
		return nil, httpx.NotFound("not_found", "Employee not found")
	}
	return m, nil
}

func (s *Service) GetEmployee(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadEmployee(ctx, tx, p, "employees.view", id)
		if err != nil {
			return err
		}
		summary, err := hrkit.One(ctx, tx, "Employee not found", `
			SELECT count(*) FILTER (WHERE status IN ('present','late')) AS days_present,
			       count(*) FILTER (WHERE status = 'late') AS days_late,
			       count(*) FILTER (WHERE status = 'absent') AS days_absent,
			       count(*) FILTER (WHERE status = 'leave') AS days_leave
			FROM attendance_days WHERE employee_id = $1 AND day >= date_trunc('month', $2::date)
		`, id, s.today(ctx, tx))
		if err != nil {
			return err
		}
		m["attendance_this_month"] = summary
		out = stripSalary(p, m)
		return nil
	})
	return out, err
}

func (s *Service) validateRefs(ctx context.Context, tx pgx.Tx, p *auth.Principal, in EmployeeIn) error {
	if in.BranchID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM branches WHERE id=$1`, *in.BranchID).Scan(&ok); err != nil || !ok {
			return httpx.BadRequest("invalid_branch", "Branch not found")
		}
		if !p.SeesBranch("employees.manage", *in.BranchID) {
			return httpx.Forbidden("branch_forbidden", "You cannot manage employees of that branch")
		}
	}
	if in.DepartmentID != nil {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM departments WHERE id=$1`, *in.DepartmentID).Scan(&n); err != nil || n == 0 {
			return httpx.BadRequest("invalid_body", "Department not found")
		}
	}
	if in.PositionID != nil {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM positions WHERE id=$1`, *in.PositionID).Scan(&n); err != nil || n == 0 {
			return httpx.BadRequest("invalid_body", "Position not found")
		}
	}
	if in.BasePay != nil && in.BasePay.IsNegative() {
		return httpx.BadRequest("invalid_body", "Base pay cannot be negative")
	}
	return nil
}

func (s *Service) CreateEmployee(ctx context.Context, p *auth.Principal, in EmployeeIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, httpx.BadRequest("invalid_body", "Name is required")
	}
	if in.BranchID == nil {
		in.BranchID = p.BranchID
	}
	if in.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if in.EmploymentType == "" {
		in.EmploymentType = "permanent"
	}
	if in.PayType == "" {
		in.PayType = "monthly"
	}
	id := ids.New()
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.validateRefs(ctx, tx, p, in); err != nil {
			return err
		}
		if in.HireDate == "" {
			// Without a hire date the employee counts as employed from the start
			// of the current month, so the first payroll run is not prorated.
			t := hrkit.MustDate(s.today(ctx, tx))
			in.HireDate = t.AddDate(0, 0, 1-t.Day()).Format(hrkit.DateFmt)
		}
		base := money.Zero
		if in.BasePay != nil {
			base = money.Money(*in.BasePay)
		} else if in.PositionID != nil {
			_ = tx.QueryRow(ctx, `SELECT COALESCE(default_pay,0) FROM positions WHERE id=$1`, *in.PositionID).Scan(&base)
		}
		number, err := sequence.Next(ctx, tx, *in.BranchID, "employee")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO employees (id, business_id, branch_id, department_id, position_id, employee_no, name, phone, email, address,
				id_reference, photo_file, hire_date, employment_type, pay_type, base_pay, pay_method, pay_account, emergency_contact)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::citext,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,$14,$15,$16,$17,NULLIF($18,''),NULLIF($19,''))
		`, id, p.BusinessID, *in.BranchID, in.DepartmentID, in.PositionID, number, in.Name, deref(in.Phone), deref(in.Email), deref(in.Address),
			deref(in.IDReference), deref(in.PhotoFile), in.HireDate, in.EmploymentType, in.PayType, base, in.PayMethod, deref(in.PayAccount), deref(in.EmergencyContact)); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "employee.created", "employee", id, nil, map[string]any{"employee_no": number, "name": in.Name}); err != nil {
			return err
		}
		out, err = s.loadEmployee(ctx, tx, p, "employees.manage", id)
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
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.loadEmployee(ctx, tx, p, "employees.manage", id)
		if err != nil {
			return err
		}
		if before["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "A terminated employee cannot be edited")
		}
		if err := s.validateRefs(ctx, tx, p, in); err != nil {
			return err
		}
		_, salaryOK := p.Can("employees.view_salary")
		if in.BasePay != nil && !salaryOK && !p.IsOwner {
			return httpx.Forbidden("permission_denied", "You cannot change salary fields")
		}
		var base any
		if in.BasePay != nil {
			base = money.Money(*in.BasePay)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE employees SET
			  name = COALESCE(NULLIF($2,''), name),
			  phone = CASE WHEN $3::text IS NULL THEN phone ELSE NULLIF($3,'') END,
			  email = CASE WHEN $4::text IS NULL THEN email ELSE NULLIF($4,'')::citext END,
			  address = CASE WHEN $5::text IS NULL THEN address ELSE NULLIF($5,'') END,
			  id_reference = CASE WHEN $6::text IS NULL THEN id_reference ELSE NULLIF($6,'') END,
			  photo_file = CASE WHEN $7::text IS NULL THEN photo_file ELSE NULLIF($7,'') END,
			  branch_id = COALESCE($8, branch_id),
			  department_id = COALESCE($9, department_id),
			  position_id = COALESCE($10, position_id),
			  hire_date = COALESCE(NULLIF($11,'')::date, hire_date),
			  employment_type = COALESCE(NULLIF($12,''), employment_type),
			  pay_type = COALESCE(NULLIF($13,''), pay_type),
			  base_pay = COALESCE($14, base_pay),
			  pay_method = COALESCE($15, pay_method),
			  pay_account = CASE WHEN $16::text IS NULL THEN pay_account ELSE NULLIF($16,'') END,
			  emergency_contact = CASE WHEN $17::text IS NULL THEN emergency_contact ELSE NULLIF($17,'') END,
			  status = COALESCE(NULLIF($18,''), status)
			WHERE id = $1
		`, id, strings.TrimSpace(in.Name), in.Phone, in.Email, in.Address, in.IDReference, in.PhotoFile, in.BranchID, in.DepartmentID, in.PositionID,
			in.HireDate, in.EmploymentType, in.PayType, base, in.PayMethod, in.PayAccount, in.EmergencyContact, in.Status); err != nil {
			return err
		}
		after, err := s.loadEmployee(ctx, tx, p, "employees.manage", id)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "employee.updated", "employee", id, before, after); err != nil {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.loadEmployee(ctx, tx, p, "employees.manage", id)
		if err != nil {
			return err
		}
		if before["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "This employee is already terminated")
		}
		if in.Date < asString(before["hire_date"]) {
			return httpx.BadRequest("invalid_body", "Termination date cannot be before the hire date")
		}
		if _, err := tx.Exec(ctx, `UPDATE employees SET status='terminated', terminated_on=$2, termination_reason=$3 WHERE id=$1`, id, in.Date, strings.TrimSpace(in.Reason)); err != nil {
			return err
		}
		// A terminated employee must not keep access. The last Owner is never
		// deactivated here, so BR-20 holds.
		if _, err := tx.Exec(ctx, `
			UPDATE users u SET is_active = false
			WHERE u.employee_id = $1 AND u.is_active
			  AND NOT EXISTS (SELECT 1 FROM roles r WHERE r.id = u.role_id AND r.slug = 'owner')
		`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE employee_id = $1 AND NOT is_active)
		`, id); err != nil {
			return err
		}
		after, err := s.loadEmployee(ctx, tx, p, "employees.manage", id)
		if err != nil {
			return err
		}
		out = after
		return s.Audit.Record(ctx, tx, p, "employee.terminated", "employee", id, before, map[string]any{"terminated_on": in.Date, "reason": in.Reason})
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

// CreateLogin creates a user linked to the employee.
func (s *Service) CreateLogin(ctx context.Context, p *auth.Principal, id uuid.UUID, in UserIn) (map[string]any, error) {
	if err := auth.PasswordOK(in.Password); err != nil {
		return nil, httpx.BadRequest("weak_password", err.Error())
	}
	hash, err := auth.HashPassword(in.Password, s.Hash)
	if err != nil {
		return nil, err
	}
	userID := ids.New()
	var out map[string]any
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		emp, err := s.loadEmployee(ctx, tx, p, "employees.view", id)
		if err != nil {
			return err
		}
		if emp["status"] == "terminated" {
			return httpx.Conflict("invalid_status", "A terminated employee cannot get a login")
		}
		if emp["user_id"] != nil {
			return httpx.Conflict("duplicate", "This employee already has a login")
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = asString(emp["name"])
		}
		phone := strings.TrimSpace(in.Phone)
		email := strings.TrimSpace(in.Email)
		if phone == "" && email == "" {
			phone = asString(emp["phone"])
			email = asString(emp["email"])
		}
		if phone == "" && email == "" {
			return httpx.BadRequest("invalid_body", "A phone or email is required to log in")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM roles WHERE id=$1`, in.RoleID).Scan(&n); err != nil || n == 0 {
			return httpx.BadRequest("invalid_body", "Role not found")
		}
		branches := in.BranchIDs
		if len(branches) == 0 {
			b, _ := uuid.Parse(asString(emp["branch_id"]))
			branches = []uuid.UUID{b}
		}
		for _, b := range branches {
			if !p.SeesBranch("users.manage", b) {
				return httpx.Forbidden("branch_forbidden", "You cannot assign that branch")
			}
		}
		lang := in.Language
		if lang == "" {
			lang = "en"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, business_id, role_id, employee_id, name, phone, email, password_hash, language, must_change_password, created_by)
			VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,'')::citext,$8,$9,true,$10)
		`, userID, p.BusinessID, in.RoleID, id, name, phone, email, hash, lang, p.UserID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "That phone or email is already in use")
			}
			return err
		}
		for i, b := range branches {
			if _, err := tx.Exec(ctx, `INSERT INTO user_branches (user_id, branch_id, business_id, is_default) VALUES ($1,$2,$3,$4)`, userID, b, p.BusinessID, i == 0); err != nil {
				return httpx.BadRequest("invalid_branch", "One of the branches does not exist")
			}
		}
		out = map[string]any{"id": userID, "employee_id": id, "name": name, "phone": phone, "email": email, "role_id": in.RoleID, "branch_ids": branches}
		return s.Audit.Record(ctx, tx, p, "user.created", "user", userID, nil, map[string]any{"name": name, "role_id": in.RoleID, "employee_id": id})
	})
	return out, err
}

// Statement lists earnings, deductions, payments and loans for a period.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		emp, err := s.loadEmployee(ctx, tx, p, "employees.view_salary", id)
		if err != nil {
			return err
		}
		out, err = statement(ctx, tx, id, from, to)
		if err != nil {
			return err
		}
		out["employee"] = map[string]any{"id": emp["id"], "employee_no": emp["employee_no"], "name": emp["name"]}
		return nil
	})
	return out, err
}

func statement(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to string) (map[string]any, error) {
	if from == "" {
		from = "0001-01-01"
	}
	if to == "" {
		to = "9999-12-31"
	}
	slips, err := hrkit.All(ctx, tx, `
		SELECT ps.id, ps.number, r.id AS run_id, r.number AS run_number, r.period_start, r.period_end, r.status AS run_status,
		       ps.gross, ps.total_deductions, ps.net, ps.paid_total, ps.status
		FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id
		WHERE ps.employee_id = $1 AND r.status IN ('approved','paid','locked') AND r.period_end >= $2 AND r.period_start <= $3
		ORDER BY r.period_start
	`, id, from, to)
	if err != nil {
		return nil, err
	}
	pays, err := hrkit.All(ctx, tx, `
		SELECT id, number, kind, direction, amount, method, payment_date, status, reference
		FROM payments WHERE party_type = 'employee' AND party_id = $1 AND payment_date BETWEEN $2 AND $3
		ORDER BY payment_date, created_at
	`, id, from, to)
	if err != nil {
		return nil, err
	}
	loans, err := hrkit.All(ctx, tx, `
		SELECT id, number, kind, principal, monthly_deduction, balance, status, start_period
		FROM employee_loans WHERE employee_id = $1 ORDER BY created_at
	`, id)
	if err != nil {
		return nil, err
	}
	var gross, ded, net, paid decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(ps.gross),0), COALESCE(SUM(ps.total_deductions),0), COALESCE(SUM(ps.net),0), COALESCE(SUM(ps.paid_total),0)
		FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id
		WHERE ps.employee_id = $1 AND r.status IN ('approved','paid','locked') AND r.period_end >= $2 AND r.period_start <= $3
	`, id, from, to).Scan(&gross, &ded, &net, &paid); err != nil {
		return nil, err
	}
	var loanBal decimal.Decimal
	_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(balance),0) FROM employee_loans WHERE employee_id=$1 AND status='disbursed'`, id).Scan(&loanBal)
	return map[string]any{
		"payslips": slips, "payments": pays, "loans": loans,
		"totals": map[string]any{"gross": gross, "deductions": ded, "net": net, "paid": paid, "unpaid": net.Sub(paid), "loan_balance": loanBal},
	}, nil
}

// ---------- employee components ----------

func (s *Service) Components(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "components.manage", id); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT sc.id AS component_id, sc.name, sc.kind, sc.calc, sc.value AS default_value, ec.value AS value,
			       (ec.component_id IS NOT NULL) AS assigned, sc.applies_to_all, sc.is_active
			FROM salary_components sc
			LEFT JOIN employee_components ec ON ec.component_id = sc.id AND ec.employee_id = $1
			WHERE ec.component_id IS NOT NULL OR (sc.applies_to_all AND sc.is_active)
			ORDER BY sc.sort_order, sc.name
		`, id)
		return err
	})
	return out, err
}

type ComponentAssign struct {
	ComponentID uuid.UUID        `json:"component_id" validate:"required"`
	Value       *decimal.Decimal `json:"value"`
}

// PutComponents replaces the employee's component assignments.
func (s *Service) PutComponents(ctx context.Context, p *auth.Principal, id uuid.UUID, in []ComponentAssign) ([]map[string]any, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "components.manage", id); err != nil {
			return err
		}
		before, err := hrkit.All(ctx, tx, `SELECT component_id, value FROM employee_components WHERE employee_id=$1`, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM employee_components WHERE employee_id=$1`, id); err != nil {
			return err
		}
		seen := map[uuid.UUID]bool{}
		for _, a := range in {
			if seen[a.ComponentID] {
				return httpx.BadRequest("invalid_body", "A component is listed twice")
			}
			seen[a.ComponentID] = true
			if a.Value != nil && a.Value.IsNegative() {
				return httpx.BadRequest("invalid_body", "Component value cannot be negative")
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO employee_components (employee_id, component_id, business_id, value)
				SELECT $1, id, $3, $4 FROM salary_components WHERE id = $2
			`, id, a.ComponentID, p.BusinessID, a.Value); err != nil {
				return err
			}
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM employee_components WHERE employee_id=$1 AND component_id=$2`, id, a.ComponentID).Scan(&n); err != nil || n == 0 {
				return httpx.BadRequest("invalid_body", "Salary component not found")
			}
		}
		return s.Audit.Record(ctx, tx, p, "employee.components_set", "employee", id, before, in)
	})
	if err != nil {
		return nil, err
	}
	return s.Components(ctx, p, id)
}

// ---------- leave balance ----------

func (s *Service) LeaveBalance(ctx context.Context, p *auth.Principal, id uuid.UUID, year int) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, "employees.view", id); err != nil {
			return err
		}
		var err error
		out, err = leaveBalance(ctx, tx, id, s.yearOf(ctx, tx, year))
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

func leaveBalance(ctx context.Context, tx pgx.Tx, employee uuid.UUID, year int) ([]map[string]any, error) {
	return hrkit.All(ctx, tx, `
		SELECT lt.id AS leave_type_id, lt.name, lt.is_paid, lt.days_per_year AS allowed,
		       COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'approved'), 0) AS taken,
		       COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'pending'), 0) AS pending,
		       GREATEST(lt.days_per_year - COALESCE(SUM(lr.days) FILTER (WHERE lr.status = 'approved'), 0), 0) AS remaining
		FROM leave_types lt
		LEFT JOIN leave_requests lr ON lr.leave_type_id = lt.id AND lr.employee_id = $1
		     AND EXTRACT(YEAR FROM lr.start_date) = $2
		WHERE lt.is_active
		GROUP BY lt.id ORDER BY lt.name
	`, employee, year)
}

// decode is a small helper for approval payloads.
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
