package hr

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

// ---------- departments ----------

type DepartmentIn struct {
	Name   string     `json:"name"`
	HeadID *uuid.UUID `json:"head_id"`
}

const deptSQL = `SELECT d.id, d.name, d.head_id, h.name AS head_name, d.created_at,
	(SELECT count(*) FROM employees e WHERE e.department_id = d.id AND e.status <> 'terminated') AS employee_count
	FROM departments d LEFT JOIN employees h ON h.id = d.head_id`

func (s *Service) Departments(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, deptSQL+` ORDER BY d.name`)
		return err
	})
	return out, err
}

func (s *Service) checkHead(ctx context.Context, tx pgx.Tx, head *uuid.UUID) error {
	if head == nil {
		return nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM employees WHERE id=$1 AND status <> 'terminated'`, *head).Scan(&n); err != nil || n == 0 {
		return httpx.BadRequest("invalid_body", "Head of department must be an active employee")
	}
	return nil
}

func (s *Service) SaveDepartment(ctx context.Context, p *auth.Principal, id *uuid.UUID, in DepartmentIn) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if id == nil && name == "" {
		return nil, httpx.BadRequest("invalid_body", "Department name is required")
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkHead(ctx, tx, in.HeadID); err != nil {
			return err
		}
		var before map[string]any
		rid := ids.New()
		if id == nil {
			if _, err := tx.Exec(ctx, `INSERT INTO departments (id, business_id, name, head_id) VALUES ($1,$2,$3,$4)`, rid, p.BusinessID, name, in.HeadID); err != nil {
				return dupe(err, "A department with that name already exists")
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Department not found", deptSQL+` WHERE d.id=$1`, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE departments SET name=COALESCE(NULLIF($2,''),name), head_id=COALESCE($3, head_id) WHERE id=$1`, rid, name, in.HeadID); err != nil {
				return dupe(err, "A department with that name already exists")
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Department not found", deptSQL+` WHERE d.id=$1`, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("department", id), "department", rid, before, out)
	})
	return out, err
}

func (s *Service) DeleteDepartment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Department not found", deptSQL+` WHERE d.id=$1`, id)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM employees WHERE department_id=$1`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("in_use", "Move the employees out of this department before deleting it")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM departments WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "department.deleted", "department", id, before, nil)
	})
}

// ---------- positions ----------

type PositionIn struct {
	Name       string           `json:"name"`
	DefaultPay *decimal.Decimal `json:"default_pay"`
}

const posSQL = `SELECT ps.id, ps.name, ps.default_pay, ps.created_at,
	(SELECT count(*) FROM employees e WHERE e.position_id = ps.id AND e.status <> 'terminated') AS employee_count
	FROM positions ps`

func (s *Service) Positions(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, posSQL+` ORDER BY ps.name`)
		return err
	})
	return out, err
}

func (s *Service) SavePosition(ctx context.Context, p *auth.Principal, id *uuid.UUID, in PositionIn) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if id == nil && name == "" {
		return nil, httpx.BadRequest("invalid_body", "Position name is required")
	}
	if in.DefaultPay != nil && in.DefaultPay.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Default pay cannot be negative")
	}
	var pay any
	if in.DefaultPay != nil {
		pay = money.Money(*in.DefaultPay)
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var before map[string]any
		rid := ids.New()
		if id == nil {
			if _, err := tx.Exec(ctx, `INSERT INTO positions (id, business_id, name, default_pay) VALUES ($1,$2,$3,$4)`, rid, p.BusinessID, name, pay); err != nil {
				return dupe(err, "A position with that name already exists")
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Position not found", posSQL+` WHERE ps.id=$1`, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE positions SET name=COALESCE(NULLIF($2,''),name), default_pay=COALESCE($3, default_pay) WHERE id=$1`, rid, name, pay); err != nil {
				return dupe(err, "A position with that name already exists")
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Position not found", posSQL+` WHERE ps.id=$1`, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("position", id), "position", rid, before, out)
	})
	return out, err
}

func (s *Service) DeletePosition(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Position not found", posSQL+` WHERE ps.id=$1`, id)
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM employees WHERE position_id=$1) + (SELECT count(*) FROM commission_rules WHERE position_id=$1)`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return httpx.Conflict("in_use", "This position is used by employees or commission rules")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM positions WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "position.deleted", "position", id, before, nil)
	})
}

// ---------- leave types ----------

type LeaveTypeIn struct {
	Name        string           `json:"name"`
	DaysPerYear *decimal.Decimal `json:"days_per_year"`
	IsPaid      *bool            `json:"is_paid"`
	IsActive    *bool            `json:"is_active"`
}

const ltSQL = `SELECT id, name, days_per_year, is_paid, is_active FROM leave_types`

func (s *Service) LeaveTypes(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, ltSQL+` ORDER BY name`)
		return err
	})
	return out, err
}

func (s *Service) SaveLeaveType(ctx context.Context, p *auth.Principal, id *uuid.UUID, in LeaveTypeIn) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if id == nil && name == "" {
		return nil, httpx.BadRequest("invalid_body", "Leave type name is required")
	}
	if in.DaysPerYear != nil && (in.DaysPerYear.IsNegative() || in.DaysPerYear.GreaterThan(decimal.NewFromInt(366))) {
		return nil, httpx.BadRequest("invalid_body", "Days per year must be between 0 and 366")
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var before map[string]any
		rid := ids.New()
		if id == nil {
			days := decimal.Zero
			if in.DaysPerYear != nil {
				days = *in.DaysPerYear
			}
			paid := true
			if in.IsPaid != nil {
				paid = *in.IsPaid
			}
			if _, err := tx.Exec(ctx, `INSERT INTO leave_types (id, business_id, name, days_per_year, is_paid) VALUES ($1,$2,$3,$4,$5)`, rid, p.BusinessID, name, days, paid); err != nil {
				return dupe(err, "A leave type with that name already exists")
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Leave type not found", ltSQL+` WHERE id=$1`, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE leave_types SET name=COALESCE(NULLIF($2,''),name), days_per_year=COALESCE($3,days_per_year), is_paid=COALESCE($4,is_paid), is_active=COALESCE($5,is_active) WHERE id=$1`,
				rid, name, in.DaysPerYear, in.IsPaid, in.IsActive); err != nil {
				return dupe(err, "A leave type with that name already exists")
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Leave type not found", ltSQL+` WHERE id=$1`, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("leave_type", id), "leave_type", rid, before, out)
	})
	return out, err
}

// ---------- commission rules ----------

type CommissionIn struct {
	EmployeeID *uuid.UUID       `json:"employee_id"`
	PositionID *uuid.UUID       `json:"position_id"`
	Calc       string           `json:"calc" validate:"omitempty,oneof=percent_of_sales fixed_per_sale"`
	Value      *decimal.Decimal `json:"value"`
	MinTarget  *decimal.Decimal `json:"min_target"`
	IsActive   *bool            `json:"is_active"`
}

const crSQL = `SELECT c.id, c.employee_id, e.name AS employee_name, c.position_id, ps.name AS position_name, c.calc, c.value, c.min_target, c.is_active
	FROM commission_rules c LEFT JOIN employees e ON e.id = c.employee_id LEFT JOIN positions ps ON ps.id = c.position_id`

func (s *Service) CommissionRules(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, crSQL+` ORDER BY e.name NULLS LAST, ps.name`)
		return err
	})
	return out, err
}

func (s *Service) SaveCommissionRule(ctx context.Context, p *auth.Principal, id *uuid.UUID, in CommissionIn) (map[string]any, error) {
	if in.Value != nil && in.Value.IsNegative() || in.MinTarget != nil && in.MinTarget.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Value and target cannot be negative")
	}
	if in.Calc == "percent_of_sales" && in.Value != nil && in.Value.GreaterThan(decimal.NewFromInt(100)) {
		return nil, httpx.BadRequest("invalid_body", "A percentage cannot be above 100")
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if in.EmployeeID != nil {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM employees WHERE id=$1`, *in.EmployeeID).Scan(&n); err != nil || n == 0 {
				return httpx.BadRequest("invalid_body", "Employee not found")
			}
		}
		if in.PositionID != nil {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM positions WHERE id=$1`, *in.PositionID).Scan(&n); err != nil || n == 0 {
				return httpx.BadRequest("invalid_body", "Position not found")
			}
		}
		var before map[string]any
		rid := ids.New()
		if id == nil {
			if in.EmployeeID == nil && in.PositionID == nil {
				return httpx.BadRequest("invalid_body", "Choose an employee or a position")
			}
			if in.Calc == "" || in.Value == nil {
				return httpx.BadRequest("invalid_body", "calc and value are required")
			}
			target := decimal.Zero
			if in.MinTarget != nil {
				target = *in.MinTarget
			}
			if _, err := tx.Exec(ctx, `INSERT INTO commission_rules (id, business_id, employee_id, position_id, calc, value, min_target) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				rid, p.BusinessID, in.EmployeeID, in.PositionID, in.Calc, *in.Value, target); err != nil {
				return err
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Commission rule not found", crSQL+` WHERE c.id=$1`, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE commission_rules SET employee_id=COALESCE($2,employee_id), position_id=COALESCE($3,position_id), calc=COALESCE(NULLIF($4,''),calc),
				value=COALESCE($5,value), min_target=COALESCE($6,min_target), is_active=COALESCE($7,is_active) WHERE id=$1`,
				rid, in.EmployeeID, in.PositionID, in.Calc, in.Value, in.MinTarget, in.IsActive); err != nil {
				return err
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Commission rule not found", crSQL+` WHERE c.id=$1`, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("commission_rule", id), "commission_rule", rid, before, out)
	})
	return out, err
}

// ---------- pay adjustments ----------

type AdjustmentIn struct {
	EmployeeID uuid.UUID       `json:"employee_id" validate:"required"`
	Period     string          `json:"period" validate:"required,date"`
	Kind       string          `json:"kind" validate:"required,oneof=bonus deduction"`
	Amount     decimal.Decimal `json:"amount" validate:"dpos"`
	Reason     string          `json:"reason" validate:"required"`
}

const adjSQL = `SELECT a.id, a.employee_id, e.name AS employee_name, a.period, a.kind, a.amount, a.reason, a.payslip_id, a.created_by, a.created_at
	FROM pay_adjustments a JOIN employees e ON e.id = a.employee_id`

func (s *Service) Adjustments(ctx context.Context, p *auth.Principal, employee *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("adjustments.manage"); br != nil {
			q.Add("e.branch_id = ANY(?)", br)
		}
		q.If(employee != nil, "a.employee_id = ?", employee)
		q.If(from != "", "a.period >= ?", from)
		q.If(to != "", "a.period <= ?", to)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM pay_adjustments a JOIN employees e ON e.id = a.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, adjSQL+q.Where()+` ORDER BY a.period DESC, a.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateAdjustment(ctx context.Context, p *auth.Principal, in AdjustmentIn) (map[string]any, error) {
	var out map[string]any
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		emp, err := s.loadEmployee(ctx, tx, p, "adjustments.manage", in.EmployeeID)
		if err != nil {
			return err
		}
		if emp["status"] == "terminated" && asString(emp["terminated_on"]) < in.Period {
			return httpx.Conflict("invalid_status", "The employee was terminated before that period")
		}
		if locked, err := periodLocked(ctx, tx, in.EmployeeID, in.Period); err != nil {
			return err
		} else if locked {
			return httpx.Conflict("period_locked", "Payroll for that period is already approved")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pay_adjustments (id, business_id, employee_id, period, kind, amount, reason, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, p.BusinessID, in.EmployeeID, in.Period, in.Kind, money.Money(in.Amount), strings.TrimSpace(in.Reason), p.UserID); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Adjustment not found", adjSQL+` WHERE a.id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "pay_adjustment.created", "pay_adjustment", id, nil, out)
	})
	return out, err
}

// periodLocked reports whether an approved payroll run covers the date for the employee.
func periodLocked(ctx context.Context, tx pgx.Tx, employee uuid.UUID, date string) (bool, error) {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id
		WHERE ps.employee_id = $1 AND r.status IN ('approved','paid','locked') AND $2::date BETWEEN r.period_start AND r.period_end
	`, employee, date).Scan(&n)
	return n > 0, err
}

func (s *Service) DeleteAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Adjustment not found", adjSQL+` WHERE a.id=$1`, id)
		if err != nil {
			return err
		}
		emp, _ := uuid.Parse(asString(before["employee_id"]))
		if _, err := s.loadEmployee(ctx, tx, p, "adjustments.manage", emp); err != nil {
			return httpx.NotFound("not_found", "Adjustment not found")
		}
		if before["payslip_id"] != nil {
			var st string
			_ = tx.QueryRow(ctx, `SELECT r.status FROM payslips ps JOIN payroll_runs r ON r.id=ps.run_id WHERE ps.id=$1`, before["payslip_id"]).Scan(&st)
			if st != "draft" && st != "calculated" && st != "reversed" {
				return httpx.Conflict("invalid_status", "This adjustment is on an approved payslip")
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM pay_adjustments WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "pay_adjustment.deleted", "pay_adjustment", id, before, nil)
	})
}

func dupe(err error, msg string) error {
	if db.IsUnique(err) {
		return httpx.Conflict("duplicate", msg)
	}
	return err
}

func auditVerb(entity string, id *uuid.UUID) string {
	if id == nil {
		return entity + ".created"
	}
	return entity + ".updated"
}
