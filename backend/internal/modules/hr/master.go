package hr

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

// ---------- departments & positions (not in the approved schema; position is text on app.employees) ----------

type DepartmentIn struct {
	Name   string     `json:"name"`
	HeadID *uuid.UUID `json:"head_id"`
}

type PositionIn struct {
	Name       string           `json:"name"`
	DefaultPay *decimal.Decimal `json:"default_pay"`
}

func (s *Service) Departments(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) SaveDepartment(ctx context.Context, p *auth.Principal, id *uuid.UUID, in DepartmentIn) (map[string]any, error) {
	return nil, removed("Departments table")
}

func (s *Service) DeleteDepartment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return removed("Departments table")
}

func (s *Service) Positions(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) SavePosition(ctx context.Context, p *auth.Principal, id *uuid.UUID, in PositionIn) (map[string]any, error) {
	return nil, removed("Positions table (set position directly on the employee)")
}

func (s *Service) DeletePosition(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return removed("Positions table")
}

// ---------- leave types ----------

type LeaveTypeIn struct {
	Name        string           `json:"name"`
	DaysPerYear *decimal.Decimal `json:"days_per_year"`
	IsPaid      *bool            `json:"is_paid"`
	IsActive    *bool            `json:"is_active"`
}

const ltSQL = `SELECT id, name::text AS name, COALESCE(annual_days, 0) AS days_per_year, is_paid, status = 'active' AS is_active FROM app.leave_types`

func (s *Service) LeaveTypes(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, ltSQL+` WHERE tenant_id = $1 ORDER BY name`, p.TenantID)
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var before map[string]any
		var rid uuid.UUID
		if id == nil {
			paid := true
			if in.IsPaid != nil {
				paid = *in.IsPaid
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO app.leave_types (tenant_id, name, annual_days, is_paid)
				VALUES ($1,$2,$3,$4) RETURNING id`, p.TenantID, name, in.DaysPerYear, paid).Scan(&rid); err != nil {
				return dupe(err, "A leave type with that name already exists")
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Leave type not found", ltSQL+` WHERE tenant_id = $1 AND id = $2`, p.TenantID, rid); err != nil {
				return err
			}
			var st *string
			if in.IsActive != nil {
				v := "active"
				if !*in.IsActive {
					v = "inactive"
				}
				st = &v
			}
			if _, err := tx.Exec(ctx, `
				UPDATE app.leave_types
				SET name = COALESCE(NULLIF($3,''), name), annual_days = COALESCE($4, annual_days),
				    is_paid = COALESCE($5, is_paid), status = COALESCE($6::app.record_status, status)
				WHERE tenant_id = $1 AND id = $2`, p.TenantID, rid, name, in.DaysPerYear, in.IsPaid, st); err != nil {
				return dupe(err, "A leave type with that name already exists")
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Leave type not found", ltSQL+` WHERE tenant_id = $1 AND id = $2`, p.TenantID, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("leave_type", id), "leave_types", rid, before, out)
	})
	return out, err
}

// ---------- commission rules ----------

type CommissionIn struct {
	Name              string           `json:"name"`
	Basis             string           `json:"basis"`
	Calc              string           `json:"calc"` // legacy alias: percent_of_sales, fixed_per_project
	Percent           *decimal.Decimal `json:"percent"`
	FixedAmount       *decimal.Decimal `json:"fixed_amount"`
	Value             *decimal.Decimal `json:"value"` // legacy alias for percent / fixed_amount
	ProductCategoryID *uuid.UUID       `json:"product_category_id"`
	ServiceID         *uuid.UUID       `json:"service_id"`
	EmployeeID        *uuid.UUID       `json:"employee_id"`
	EffectiveFrom     string           `json:"effective_from" validate:"date"`
	EffectiveTo       string           `json:"effective_to" validate:"date"`
	IsActive          *bool            `json:"is_active"`
}

const crSQL = `SELECT c.id, c.name::text AS name, c.basis::text AS basis, c.percent, c.fixed_amount,
	c.product_category_id, c.service_id, c.employee_id, e.full_name::text AS employee_name,
	c.effective_from, c.effective_to, c.active AS is_active
	FROM app.commission_rules c
	LEFT JOIN app.employees e ON e.tenant_id = c.tenant_id AND e.id = c.employee_id`

func (s *Service) CommissionRules(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, crSQL+` WHERE c.tenant_id = $1 ORDER BY c.effective_from DESC, c.name`, p.TenantID)
		return err
	})
	return out, err
}

func (s *Service) SaveCommissionRule(ctx context.Context, p *auth.Principal, id *uuid.UUID, in CommissionIn) (map[string]any, error) {
	basis := strings.TrimSpace(in.Basis)
	if basis == "" {
		switch in.Calc {
		case "percent_of_sales":
			basis = "sale_percent"
		case "fixed_per_project":
			basis = "project_fixed"
		}
	}
	pct := in.Percent
	fixed := in.FixedAmount
	if in.Value != nil {
		if basis == "sale_percent" && pct == nil {
			pct = in.Value
		} else if basis == "project_fixed" && fixed == nil {
			fixed = in.Value
		}
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var before map[string]any
		var rid uuid.UUID
		if id == nil {
			if basis != "sale_percent" && basis != "project_fixed" {
				return httpx.BadRequest("invalid_body", "basis must be sale_percent or project_fixed")
			}
			if basis == "sale_percent" && (pct == nil || pct.IsNegative() || pct.GreaterThan(decimal.NewFromInt(100))) {
				return httpx.BadRequest("invalid_body", "percent must be between 0 and 100")
			}
			if basis == "project_fixed" && (fixed == nil || !fixed.IsPositive()) {
				return httpx.BadRequest("invalid_body", "fixed_amount must be greater than zero")
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				name = "Commission rule (" + basis + ")"
			}
			effFrom := strings.TrimSpace(in.EffectiveFrom)
			if effFrom == "" {
				effFrom = s.today(ctx, tx)
			}
			if basis == "sale_percent" {
				fixed = nil
				in.ServiceID = nil
			} else {
				pct = nil
				in.ProductCategoryID = nil
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO app.commission_rules
					(tenant_id, name, basis, percent, fixed_amount, product_category_id, service_id, employee_id, effective_from, effective_to)
				VALUES ($1,$2,$3::app.commission_basis,$4,$5,$6,$7,$8,$9::date,NULLIF($10,'')::date)
				RETURNING id`,
				p.TenantID, name, basis, pct, fixed, in.ProductCategoryID, in.ServiceID, in.EmployeeID, effFrom, strings.TrimSpace(in.EffectiveTo)).Scan(&rid); err != nil {
				return err
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Commission rule not found", crSQL+` WHERE c.tenant_id = $1 AND c.id = $2`, p.TenantID, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE app.commission_rules
				SET name = COALESCE(NULLIF($3,''), name), percent = COALESCE($4, percent), fixed_amount = COALESCE($5, fixed_amount),
				    effective_to = COALESCE(NULLIF($6,'')::date, effective_to), active = COALESCE($7, active)
				WHERE tenant_id = $1 AND id = $2`,
				p.TenantID, rid, strings.TrimSpace(in.Name), pct, fixed, strings.TrimSpace(in.EffectiveTo), in.IsActive); err != nil {
				return err
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Commission rule not found", crSQL+` WHERE c.tenant_id = $1 AND c.id = $2`, p.TenantID, rid); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, auditVerb("commission_rule", id), "commission_rules", rid, before, out)
	})
	return out, err
}

// ---------- pay adjustments ----------

type AdjustmentIn struct {
	EmployeeID  uuid.UUID       `json:"employee_id" validate:"required"`
	PeriodYear  int             `json:"period_year"`
	PeriodMonth int             `json:"period_month"`
	Period      string          `json:"period"` // legacy YYYY-MM-DD or YYYY-MM
	Kind        string          `json:"kind" validate:"required,oneof=bonus deduction"`
	Amount      decimal.Decimal `json:"amount" validate:"dpos"`
	Reason      string          `json:"reason" validate:"required"`
}

func (a AdjustmentIn) yearMonth() (int, int, error) {
	if a.PeriodYear >= 2020 && a.PeriodYear <= 2100 && a.PeriodMonth >= 1 && a.PeriodMonth <= 12 {
		return a.PeriodYear, a.PeriodMonth, nil
	}
	s := strings.TrimSpace(a.Period)
	if len(s) == 7 {
		s += "-01"
	}
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return d.Year(), int(d.Month()), nil
	}
	return 0, 0, httpx.BadRequest("invalid_body", "Specify period_year and period_month (or period YYYY-MM)")
}

const adjSQL = `SELECT a.id, a.employee_id, e.full_name::text AS employee_name, a.period_year, a.period_month,
	make_date(a.period_year, a.period_month, 1) AS period, a.kind::text AS kind, a.amount, a.reason, a.payroll_line_id, a.created_by, a.created_at
	FROM app.employee_pay_adjustments a
	JOIN app.employees e ON e.tenant_id = a.tenant_id AND e.id = a.employee_id`

func (s *Service) Adjustments(ctx context.Context, p *auth.Principal, employee *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("a.tenant_id = $1")
		q.If(p.BranchID != nil, "e.location_id = ?", p.BranchID)
		q.If(employee != nil, "a.employee_id = ?", employee)
		q.If(from != "", "make_date(a.period_year, a.period_month, 1) >= date_trunc('month', ?::date)::date", from)
		q.If(to != "", "make_date(a.period_year, a.period_month, 1) <= date_trunc('month', ?::date)::date", to)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.employee_pay_adjustments a JOIN app.employees e ON e.tenant_id = a.tenant_id AND e.id = a.employee_id`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, adjSQL+q.Where()+` ORDER BY a.period_year DESC, a.period_month DESC, a.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateAdjustment(ctx context.Context, p *auth.Principal, in AdjustmentIn) (map[string]any, error) {
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 3 {
		return nil, httpx.BadRequest("invalid_body", "Reason must be at least 3 characters")
	}
	yr, mo, err := in.yearMonth()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.loadEmployee(ctx, tx, p, in.EmployeeID); err != nil {
			return err
		}
		firstDay := time.Date(yr, time.Month(mo), 1, 0, 0, 0, 0, time.UTC).Format(hrkit.DateFmt)
		if err := hrkit.PayrollLocked(ctx, tx, in.EmployeeID, firstDay); err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.employee_pay_adjustments (tenant_id, employee_id, kind, amount, reason, period_year, period_month)
			VALUES ($1,$2,$3::app.pay_adjustment_kind,$4,$5,$6,$7) RETURNING id`,
			p.TenantID, in.EmployeeID, in.Kind, money.Money(in.Amount), reason, yr, mo).Scan(&id); err != nil {
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Adjustment not found", adjSQL+` WHERE a.tenant_id = $1 AND a.id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "pay_adjustment.created", "employee_pay_adjustments", id, nil, out)
	})
	return out, err
}

func (s *Service) DeleteAdjustment(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := hrkit.One(ctx, tx, "Adjustment not found", adjSQL+` WHERE a.tenant_id = $1 AND a.id = $2`, p.TenantID, id)
		if err != nil {
			return err
		}
		if before["payroll_line_id"] != nil {
			var st string
			_ = tx.QueryRow(ctx, `
				SELECT r.status::text FROM app.payroll_lines pl
				JOIN app.payroll_runs r ON r.tenant_id = pl.tenant_id AND r.id = pl.payroll_run_id
				WHERE pl.tenant_id = $1 AND pl.id = $2`, p.TenantID, before["payroll_line_id"]).Scan(&st)
			if st != "draft" && st != "cancelled" {
				return httpx.Conflict("invalid_status", "This adjustment is already on an approved payroll")
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.employee_pay_adjustments WHERE tenant_id = $1 AND id = $2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "pay_adjustment.deleted", "employee_pay_adjustments", id, before, nil)
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
