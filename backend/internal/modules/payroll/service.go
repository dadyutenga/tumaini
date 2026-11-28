package payroll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/modules/hr/hrkit"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

// largeRun is the employee count above which calculation runs as a job.
const largeRun = 200

type Service struct {
	DB     *db.DB
	Audit  *audit.Service
	Notify *notify.Service
	Jobs   *jobs.Client
	Clock  clock.Clock
}

func New(database *db.DB, aud *audit.Service, n *notify.Service, j *jobs.Client, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Notify: n, Jobs: j, Clock: clk}
}

const runSQL = `SELECT r.id,
	format('PR-%s-%s', r.period_year, lpad(r.period_month::text, 2, '0')) AS number,
	r.period_year, r.period_month,
	make_date(r.period_year, r.period_month, 1)::text AS period_start,
	(make_date(r.period_year, r.period_month, 1) + interval '1 month' - interval '1 day')::date::text AS period_end,
	r.location_id, r.location_id AS branch_id, l.name::text AS location_name, l.name::text AS branch_name,
	r.business_unit_id, bu.name::text AS business_unit_name,
	r.account_id, fa.name::text AS account_name,
	r.status::text AS status,
	r.generated_by, r.generated_by AS created_by, r.generated_at,
	r.approved_by, r.approved_at, r.paid_by, r.paid_at, r.paid_business_date::text AS paid_business_date,
	r.notes, r.created_at, r.updated_at,
	COALESCE((SELECT SUM(pl.gross_pay) FROM app.payroll_lines pl WHERE pl.payroll_run_id = r.id), 0) AS gross_total,
	COALESCE((SELECT SUM(pl.deductions + pl.advance_repayment + pl.statutory_deductions) FROM app.payroll_lines pl WHERE pl.payroll_run_id = r.id), 0) AS deduction_total,
	COALESCE((SELECT SUM(pl.net_pay) FROM app.payroll_lines pl WHERE pl.payroll_run_id = r.id), 0) AS net_total,
	COALESCE((SELECT SUM(si.employer_amount) FROM app.payroll_statutory_items si JOIN app.payroll_lines pl ON pl.id = si.payroll_line_id WHERE pl.payroll_run_id = r.id), 0) AS employer_total,
	(SELECT count(*) FROM app.payroll_lines pl WHERE pl.payroll_run_id = r.id) AS payslip_count,
	COALESCE((SELECT SUM(pl.net_pay) FROM app.payroll_lines pl WHERE pl.payroll_run_id = r.id AND pl.paid_at IS NOT NULL), 0) AS paid_total
	FROM app.payroll_runs r
	LEFT JOIN app.locations l ON l.id = r.location_id
	LEFT JOIN app.business_units bu ON bu.id = r.business_unit_id
	LEFT JOIN app.financial_accounts fa ON fa.id = r.account_id`

// ---------- runs ----------

type RunIn struct {
	PeriodYear     int        `json:"period_year"`
	PeriodMonth    int        `json:"period_month"`
	Start          string     `json:"period_start" validate:"omitempty,date"`
	End            string     `json:"period_end" validate:"omitempty,date"`
	LocationID     *uuid.UUID `json:"location_id"`
	BranchID       *uuid.UUID `json:"branch_id"`
	BusinessUnitID *uuid.UUID `json:"business_unit_id"`
	Notes          string     `json:"notes"`
	Scope          string     `json:"scope"`
}

func resolveRunPeriod(in RunIn) (int, int, error) {
	y, m := in.PeriodYear, in.PeriodMonth
	if (y == 0 || m == 0) && in.Start != "" {
		d, err := hrkit.Date(in.Start)
		if err != nil {
			return 0, 0, httpx.BadRequest("invalid_body", "Invalid period_start date")
		}
		if in.End != "" && in.End < in.Start {
			return 0, 0, httpx.BadRequest("invalid_body", "The period end cannot be before its start")
		}
		y, m = d.Year(), int(d.Month())
	}
	if y < 2020 || y > 2100 || m < 1 || m > 12 {
		return 0, 0, httpx.BadRequest("invalid_body", "Valid period_year (2020-2100) and period_month (1-12) or period_start are required")
	}
	return y, m, nil
}

func (s *Service) resolveRunLocationAndBU(ctx context.Context, tx pgx.Tx, p *auth.Principal, in RunIn) (uuid.UUID, uuid.UUID, error) {
	locID := in.LocationID
	if locID == nil {
		locID = in.BranchID
	}
	if locID == nil {
		locID = p.BranchID
	}
	var loc, bu uuid.UUID
	if locID != nil {
		if err := tx.QueryRow(ctx, "SELECT id, business_unit_id FROM app.locations WHERE id = $1 AND status = 'active'", *locID).Scan(&loc, &bu); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, uuid.Nil, httpx.NotFound("not_found", "Location not found")
			}
			return uuid.Nil, uuid.Nil, err
		}
	} else {
		if err := tx.QueryRow(ctx, "SELECT id, business_unit_id FROM app.locations WHERE status = 'active' ORDER BY is_default DESC, created_at ASC LIMIT 1").Scan(&loc, &bu); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, uuid.Nil, httpx.BadRequest("branch_required", "Configure an active location before creating a payroll run")
			}
			return uuid.Nil, uuid.Nil, err
		}
	}
	if in.BusinessUnitID != nil {
		var ok bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.business_units WHERE id = $1 AND status = 'active')", *in.BusinessUnitID).Scan(&ok); err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		if !ok {
			return uuid.Nil, uuid.Nil, httpx.NotFound("not_found", "Business unit not found")
		}
		bu = *in.BusinessUnitID
	}
	return loc, bu, nil
}

func (s *Service) CreateRun(ctx context.Context, p *auth.Principal, in RunIn) (map[string]any, error) {
	year, month, err := resolveRunPeriod(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		locID, buID, err := s.resolveRunLocationAndBU(ctx, tx, p, in)
		if err != nil {
			return err
		}
		id := ids.New()
		notes := strings.TrimSpace(in.Notes)
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.payroll_runs (id, period_year, period_month, status, location_id, business_unit_id, generated_by, notes)
			VALUES ($1,$2,$3,'draft',$4,$5,$6,NULLIF($7,''))
		`, id, year, month, locID, buID, p.MembershipID, notes); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An active payroll run already exists for this period")
			}
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Payroll run not found", runSQL+` WHERE r.id = $1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payroll_run.created", "payroll_run", id, nil, out)
	})
	return out, err
}

func (s *Service) ListRuns(ctx context.Context, p *auth.Principal, status, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "r.status = ?::app.payroll_status", status)
		q.If(from != "", "(make_date(r.period_year, r.period_month, 1) + interval '1 month' - interval '1 day')::date >= ?::date", from)
		q.If(to != "", "make_date(r.period_year, r.period_month, 1) <= ?::date", to)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.payroll_runs r`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, runSQL+q.Where()+` ORDER BY r.period_year DESC, r.period_month DESC, r.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) loadRun(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (map[string]any, error) {
	if lock {
		if _, err := tx.Exec(ctx, "SELECT 1 FROM app.payroll_runs WHERE id = $1 FOR UPDATE", id); err != nil {
			return nil, err
		}
	}
	return hrkit.One(ctx, tx, "Payroll run not found", runSQL+` WHERE r.id = $1`, id)
}

func (s *Service) GetRun(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, id, false)
		if err != nil {
			return err
		}
		slips, err := s.slipSummaries(ctx, tx, id)
		if err != nil {
			return err
		}
		run["payslips"] = slips
		out = run
		return nil
	})
	return out, err
}

func (s *Service) slipSummaries(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]map[string]any, error) {
	return hrkit.All(ctx, tx, `
		SELECT pl.id, pl.id AS payroll_line_id, ps.id AS payslip_id,
		       COALESCE(ps.payslip_no, '') AS number,
		       pl.employee_id, e.full_name::text AS name, e.employee_no,
		       pl.basic_salary, pl.basic_salary AS base_pay,
		       pl.allowances, pl.allowances AS allowance,
		       pl.commission, pl.overtime, pl.bonuses,
		       pl.deductions, pl.advance_repayment, pl.advance_repayment AS loan,
		       pl.statutory_deductions,
		       pl.gross_pay AS gross, pl.gross_pay,
		       (pl.deductions + pl.advance_repayment + pl.statutory_deductions) AS total_deductions,
		       pl.net_pay AS net, pl.net_pay,
		       CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END AS paid_total,
		       CASE WHEN pl.paid_at IS NOT NULL THEN 0 ELSE pl.net_pay END AS balance,
		       pl.pay_method::text AS pay_method, pl.payment_reference, pl.paid_at, pl.adjustment_note,
		       CASE WHEN pl.paid_at IS NOT NULL THEN 'paid' WHEN r.status = 'approved' THEN 'approved' ELSE r.status::text END AS status,
		       COALESCE((SELECT SUM(si.employer_amount) FROM app.payroll_statutory_items si WHERE si.payroll_line_id = pl.id), 0) AS employer
		FROM app.payroll_lines pl
		JOIN app.payroll_runs r ON r.id = pl.payroll_run_id
		JOIN app.employees e ON e.id = pl.employee_id
		LEFT JOIN app.payslips ps ON ps.payroll_line_id = pl.id
		WHERE pl.payroll_run_id = $1
		ORDER BY e.full_name
	`, runID)
}

// ---------- calculation ----------

type settings struct {
	workingDays     int
	standardHours   decimal.Decimal
	multiplier      decimal.Decimal
	latenessDeducts bool
}

func loadSettings(ctx context.Context, tx pgx.Tx) settings {
	return settings{
		workingDays:     setting.Int(ctx, tx, "payroll.working_days", 26),
		standardHours:   setting.Decimal(ctx, tx, "payroll.standard_hours", decimal.NewFromInt(8)),
		multiplier:      setting.Decimal(ctx, tx, "payroll.overtime_multiplier", decimal.NewFromFloat(1.5)),
		latenessDeducts: setting.Bool(ctx, tx, "payroll.lateness_deducts", false),
	}
}

type employee struct {
	id         uuid.UUID
	locationID *uuid.UUID
	name       string
	base       decimal.Decimal
	payMethod  string
	hire       string
	terminated *string
}

// Calculate computes or recomputes every payroll line of a draft run.
// Large runs are queued and the caller gets 202.
func (s *Service) Calculate(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, map[string]any, error) {
	var n int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, runID, false)
		if err != nil {
			return err
		}
		if run["status"] != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft payroll run can be calculated")
		}
		emps, err := s.employees(ctx, tx, run["period_start"].(string), run["period_end"].(string))
		n = len(emps)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	if n > largeRun && s.Jobs != nil {
		if err := s.Jobs.Enqueue(ctx, jobs.PayrollCalculate, map[string]any{
			"run_id":        runID,
			"user_id":       p.UserID,
			"membership_id": p.MembershipID,
		}); err != nil {
			return 0, nil, err
		}
		return 202, map[string]any{"id": runID, "status": "calculating", "employees": n}, nil
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.calculateTx(ctx, tx, p, runID)
		return err
	})
	return 200, out, err
}

// CalculateJob runs a queued calculation as the user who asked for it.
func (s *Service) CalculateJob(ctx context.Context, payload json.RawMessage) error {
	var in struct {
		RunID        uuid.UUID `json:"run_id"`
		UserID       uuid.UUID `json:"user_id"`
		MembershipID uuid.UUID `json:"membership_id"`
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return err
	}
	p := &auth.Principal{
		UserID:       in.UserID,
		MembershipID: in.MembershipID,
		IsOwner:      true,
		RequestID:    "job:" + jobs.PayrollCalculate,
	}
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.calculateTx(ctx, tx, p, in.RunID); err != nil {
			return err
		}
		return s.Notify.ToUser(ctx, tx, in.UserID, notify.Message{
			Type: "salary_due", Title: "Payroll calculated", Body: "The payroll run is ready to review",
			Link: "/payroll-runs/" + in.RunID.String(), EntityType: "payroll_run", EntityID: &in.RunID,
		})
	})
}

func (s *Service) employees(ctx context.Context, tx pgx.Tx, start, end string) ([]employee, error) {
	rows, err := tx.Query(ctx, "\n\t\tSELECT e.id, e.location_id, e.full_name::text,\n\t\t       COALESCE(ec.basic_salary, 0),\n\t\t       COALESCE(ec.pay_method::text, 'cash'),\n\t\t       e.start_date::text, e.end_date::text\n\t\tFROM app.employees e\n\t\tLEFT JOIN LATERAL (\n\t\t\tSELECT basic_salary, pay_method\n\t\t\tFROM app.employee_compensation c\n\t\t\tWHERE c.employee_id = e.id\n\t\t\t  AND c.effective_from <= $2::date\n\t\t\t  AND (c.effective_to IS NULL OR c.effective_to > $1::date)\n\t\t\tORDER BY c.effective_from DESC\n\t\t\tLIMIT 1\n\t\t) ec ON true\n\t\tWHERE e.start_date <= $2::date\n\t\t  AND (e.status = 'active' OR (e.status = 'terminated' AND e.end_date >= $1::date))\n\t\tORDER BY e.full_name\n\t", start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []employee
	for rows.Next() {
		var e employee
		if err := rows.Scan(&e.id, &e.locationID, &e.name, &e.base, &e.payMethod, &e.hire, &e.terminated); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) calculateTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, runID uuid.UUID) (map[string]any, error) {
	run, err := s.loadRun(ctx, tx, runID, true)
	if err != nil {
		return nil, err
	}
	if run["status"] != "draft" {
		return nil, httpx.Conflict("invalid_status", "Only a draft payroll run can be calculated")
	}
	year := int(run["period_year"].(int16))
	month := int(run["period_month"].(int16))
	start, end := run["period_start"].(string), run["period_end"].(string)

	// Release previously linked pay adjustments, commission entries, and advance repayments.
	if _, err := tx.Exec(ctx, "\n\t\tUPDATE app.employee_pay_adjustments SET payroll_line_id = NULL\n\t\tWHERE payroll_line_id IN (SELECT id FROM app.payroll_lines WHERE payroll_run_id = $1)\n\t", runID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "\n\t\tUPDATE app.commission_entries SET status = 'accrued', payroll_line_id = NULL\n\t\tWHERE payroll_line_id IN (SELECT id FROM app.payroll_lines WHERE payroll_run_id = $1)\n\t", runID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "\n\t\tDELETE FROM app.advance_repayments\n\t\tWHERE payroll_line_id IN (SELECT id FROM app.payroll_lines WHERE payroll_run_id = $1)\n\t", runID); err != nil {
		return nil, err
	}
	// Preserve any line that already has an issued payslip (e.g. a reopened run) by updating it in place;
	// delete lines that have no issued payslip.
	existing := map[uuid.UUID]uuid.UUID{}
	erows, err := tx.Query(ctx, "\n\t\tSELECT pl.employee_id, pl.id FROM app.payroll_lines pl\n\t\tWHERE pl.payroll_run_id = $1\n\t\t  AND EXISTS (SELECT 1 FROM app.payslips ps WHERE ps.payroll_line_id = pl.id)\n\t", runID)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var empID, lineID uuid.UUID
		if err := erows.Scan(&empID, &lineID); err != nil {
			erows.Close()
			return nil, err
		}
		existing[empID] = lineID
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "\n\t\tDELETE FROM app.payroll_lines pl\n\t\tWHERE pl.payroll_run_id = $1\n\t\t  AND NOT EXISTS (SELECT 1 FROM app.payslips ps WHERE ps.payroll_line_id = pl.id)\n\t", runID); err != nil {
		return nil, err
	}

	cfg := loadSettings(ctx, tx)
	emps, err := s.employees(ctx, tx, start, end)
	if err != nil {
		return nil, err
	}

	var gross, net decimal.Decimal
	seen := map[uuid.UUID]bool{}
	for _, e := range emps {
		seen[e.id] = true
		in, commIDs, err := s.inputFor(ctx, tx, cfg, e, year, month, start, end)
		if err != nil {
			return nil, err
		}
		res := Compute(in)

		var commission, bonuses, deductions, statutory decimal.Decimal
		for _, l := range res.Lines {
			switch l.Kind {
			case "commission":
				commission = commission.Add(l.Amount)
			case "bonus":
				bonuses = bonuses.Add(l.Amount)
			case "deduction":
				deductions = deductions.Add(l.Amount)
			case "statutory":
				statutory = statutory.Add(l.Amount)
			}
		}

		var lineID uuid.UUID
		if prevID, ok := existing[e.id]; ok {
			lineID = prevID
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.payroll_lines\n\t\t\t\tSET basic_salary = $2, allowances = $3, commission = $4, overtime = $5, bonuses = $6,\n\t\t\t\t    deductions = $7, advance_repayment = $8, statutory_deductions = $9, pay_method = $10::app.pay_method\n\t\t\t\tWHERE id = $1\n\t\t\t", lineID, res.ProratedBase, res.Allowances, commission, res.Overtime, bonuses, deductions, res.Loan, statutory, e.payMethod); err != nil {
				return nil, err
			}
		} else {
			lineID = ids.New()
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.payroll_lines (id, payroll_run_id, employee_id, basic_salary, allowances, commission, overtime, bonuses, deductions, advance_repayment, statutory_deductions, pay_method) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::app.pay_method)\n\t\t\t", lineID, runID, e.id, res.ProratedBase, res.Allowances, commission, res.Overtime, bonuses, deductions, res.Loan, statutory, e.payMethod); err != nil {
				return nil, err
			}
		}

		// Link adjustments and record scheduled advance repayments.
		for _, l := range res.Lines {
			if l.AdjustID != nil {
				if _, err := tx.Exec(ctx, "UPDATE app.employee_pay_adjustments SET payroll_line_id = $2 WHERE id = $1", *l.AdjustID, lineID); err != nil {
					return nil, err
				}
			}
			if l.Kind == "loan" && l.LoanID != nil && l.Amount.IsPositive() {
				if _, err := tx.Exec(ctx, "\n\t\t\t\t\tINSERT INTO app.advance_repayments (id, advance_id, payroll_line_id, amount)\n\t\t\t\t\tVALUES ($1, $2, $3, $4)\n\t\t\t\t", ids.New(), *l.LoanID, lineID, l.Amount); err != nil {
					return nil, err
				}
			}
		}
		if len(commIDs) > 0 {
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.commission_entries SET status = 'included', payroll_line_id = $2\n\t\t\t\tWHERE id = ANY($1)\n\t\t\t", commIDs, lineID); err != nil {
				return nil, err
			}
		}

		gross = gross.Add(res.Gross)
		net = net.Add(res.Net)
	}

	// Zero out any retained line with an existing payslip whose employee is no longer eligible.
	for empID, lineID := range existing {
		if !seen[empID] {
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.payroll_lines\n\t\t\t\tSET basic_salary = 0, allowances = 0, commission = 0, overtime = 0, bonuses = 0,\n\t\t\t\t    deductions = 0, advance_repayment = 0, statutory_deductions = 0\n\t\t\t\tWHERE id = $1\n\t\t\t", lineID); err != nil {
				return nil, err
			}
		}
	}

	if err := s.Audit.Record(ctx, tx, p, "payroll_run.calculated", "payroll_run", runID,
		map[string]any{"status": run["status"]},
		map[string]any{"status": "draft", "employees": len(emps), "gross_total": gross, "net_total": net}); err != nil {
		return nil, err
	}
	out, err := s.loadRun(ctx, tx, runID, false)
	if err != nil {
		return nil, err
	}
	slips, err := s.slipSummaries(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	out["payslips"] = slips
	return out, nil
}

// inputFor gathers attendance, allowances, accrued commissions, adjustments and active advances
// for one employee in the period.
func (s *Service) inputFor(ctx context.Context, tx pgx.Tx, cfg settings, e employee, year, month int, start, end string) (Input, []uuid.UUID, error) {
	in := Input{
		PayType: "monthly", Base: e.base, WorkingDays: cfg.workingDays, StandardHours: cfg.standardHours,
		OvertimeMultiplier: cfg.multiplier, LatenessDeducts: cfg.latenessDeducts,
	}

	var absent, halfDays, present, unpaidLeave decimal.Decimal
	var otMinutes, lateMinutes, workedMinutes int
	if err := tx.QueryRow(ctx, "\n\t\tSELECT count(*) FILTER (WHERE d.status = 'absent'),\n\t\t       count(*) FILTER (WHERE d.status = 'half_day'),\n\t\t       count(*) FILTER (WHERE d.status IN ('present','late')),\n\t\t       count(*) FILTER (WHERE d.status = 'on_leave' AND EXISTS (\n\t\t           SELECT 1 FROM app.leave_requests lr\n\t\t           JOIN app.leave_types lt ON lt.id = lr.leave_type_id\n\t\t           WHERE lr.employee_id = d.employee_id\n\t\t             AND lr.status = 'approved' AND NOT lt.is_paid\n\t\t             AND d.work_date BETWEEN lr.start_date AND lr.end_date)),\n\t\t       COALESCE(SUM(d.overtime_minutes), 0),\n\t\t       COALESCE(SUM(d.minutes_late), 0),\n\t\t       COALESCE(SUM(CASE WHEN d.clock_in_at IS NOT NULL AND d.clock_out_at IS NOT NULL\n\t\t                         THEN GREATEST(0, EXTRACT(EPOCH FROM (d.clock_out_at - d.clock_in_at))::int / 60)\n\t\t                         ELSE 0 END), 0)\n\t\tFROM app.attendance_records d\n\t\tWHERE d.employee_id = $1 AND d.work_date BETWEEN $2::date AND $3::date\n\t", e.id, start, end).Scan(&absent, &halfDays, &present, &unpaidLeave, &otMinutes, &lateMinutes, &workedMinutes); err != nil {
		return in, nil, err
	}
	half := halfDays.Mul(decimal.NewFromFloat(0.5))
	in.UnpaidDays = absent.Add(half).Add(unpaidLeave)
	in.DaysWorked = present.Add(half)
	in.HoursWorked = decimal.NewFromInt(int64(workedMinutes)).Div(decimal.NewFromInt(60)).Round(2)
	in.OvertimeHours = decimal.NewFromInt(int64(otMinutes)).Div(decimal.NewFromInt(60))
	in.LateMinutes = lateMinutes

	// Joiners and leavers: working days outside employment are unpaid.
	var locID uuid.UUID
	if e.locationID != nil {
		locID = *e.locationID
	}
	cal, err := hrkit.LoadCalendar(ctx, tx, &e.id, locID)
	if err != nil {
		return in, nil, err
	}
	ps, pe := hrkit.MustDate(start), hrkit.MustDate(end)
	if h := hrkit.MustDate(e.hire); h.After(ps) {
		in.UnpaidDays = in.UnpaidDays.Add(decimal.NewFromInt(int64(cal.WorkingDays(ps, h.AddDate(0, 0, -1)))))
	}
	if e.terminated != nil {
		if t := hrkit.MustDate(*e.terminated); t.Before(pe) {
			in.UnpaidDays = in.UnpaidDays.Add(decimal.NewFromInt(int64(cal.WorkingDays(t.AddDate(0, 0, 1), pe))))
		}
	}

	// Recurring allowances active during the period.
	alRows, err := tx.Query(ctx, "\n\t\tSELECT id, name::text, amount FROM app.employee_allowances\n\t\tWHERE employee_id = $1\n\t\t  AND effective_from <= $3::date\n\t\t  AND (effective_to IS NULL OR effective_to > $2::date)\n\t\tORDER BY name\n\t", e.id, start, end)
	if err != nil {
		return in, nil, err
	}
	for alRows.Next() {
		var id uuid.UUID
		var name string
		var amt decimal.Decimal
		if err := alRows.Scan(&id, &name, &amt); err != nil {
			alRows.Close()
			return in, nil, err
		}
		in.Components = append(in.Components, Component{ID: id, Name: name, Kind: "allowance", Calc: "fixed", Value: amt, Taxable: true})
	}
	alRows.Close()
	if err := alRows.Err(); err != nil {
		return in, nil, err
	}

	// Accrued commissions earned up to the end of the period.
	cRows, err := tx.Query(ctx, "\n\t\tSELECT id, amount FROM app.commission_entries\n\t\tWHERE employee_id = $1 AND status = 'accrued' AND earned_on <= $2::date\n\t\tORDER BY earned_on, created_at\n\t", e.id, end)
	if err != nil {
		return in, nil, err
	}
	var commIDs []uuid.UUID
	commTotal := decimal.Zero
	for cRows.Next() {
		var id uuid.UUID
		var amt decimal.Decimal
		if err := cRows.Scan(&id, &amt); err != nil {
			cRows.Close()
			return in, nil, err
		}
		commIDs = append(commIDs, id)
		commTotal = commTotal.Add(amt)
	}
	cRows.Close()
	if err := cRows.Err(); err != nil {
		return in, nil, err
	}
	if commTotal.IsPositive() {
		in.Commission = r2(commTotal)
	} else if commTotal.IsNegative() {
		in.Deductions = append(in.Deductions, Item{Label: "Commission reversal", Amount: r2(commTotal.Abs())})
	}

	// Unapplied pay adjustments for this year/month.
	aRows, err := tx.Query(ctx, "\n\t\tSELECT id, kind::text, amount, reason FROM app.employee_pay_adjustments\n\t\tWHERE employee_id = $1 AND period_year = $2 AND period_month = $3 AND payroll_line_id IS NULL\n\t\tORDER BY created_at\n\t", e.id, year, month)
	if err != nil {
		return in, nil, err
	}
	for aRows.Next() {
		var id uuid.UUID
		var kind, reason string
		var amt decimal.Decimal
		if err := aRows.Scan(&id, &kind, &amt, &reason); err != nil {
			aRows.Close()
			return in, nil, err
		}
		item := Item{ID: &id, Label: reason, Amount: amt}
		if kind == "bonus" {
			in.Bonuses = append(in.Bonuses, item)
		} else {
			in.Deductions = append(in.Deductions, item)
		}
	}
	aRows.Close()
	if err := aRows.Err(); err != nil {
		return in, nil, err
	}

	// Active employee advances/loans: monthly deduction capped by balance less repayments already scheduled
	// on approved (unpaid) runs. Paid runs have already updated employee_advances.amount_repaid via trigger.
	lRows, err := tx.Query(ctx, "\n\t\tSELECT a.id, a.kind::text, a.monthly_deduction,\n\t\t       a.balance - COALESCE((\n\t\t           SELECT SUM(ar.amount)\n\t\t           FROM app.advance_repayments ar\n\t\t           JOIN app.payroll_lines pl ON pl.id = ar.payroll_line_id\n\t\t           JOIN app.payroll_runs r ON r.id = pl.payroll_run_id\n\t\t           WHERE ar.advance_id = a.id AND r.status = 'approved'\n\t\t       ), 0)\n\t\tFROM app.employee_advances a\n\t\tWHERE a.employee_id = $1 AND a.status = 'active' AND a.balance > 0 AND a.issued_on <= $2::date\n\t\tORDER BY a.issued_on, a.created_at\n\t", e.id, end)
	if err != nil {
		return in, nil, err
	}
	for lRows.Next() {
		var id uuid.UUID
		var kind string
		var monthly, open decimal.Decimal
		if err := lRows.Scan(&id, &kind, &monthly, &open); err != nil {
			lRows.Close()
			return in, nil, err
		}
		due := decimal.Min(monthly, open)
		if due.IsPositive() {
			label := "Loan repayment"
			if kind == "advance" {
				label = "Salary advance"
			}
			in.Loans = append(in.Loans, LoanDue{ID: id, Label: label, Due: due})
		}
	}
	lRows.Close()
	return in, commIDs, lRows.Err()
}

// ---------- manual line adjustments ----------

type ManualLine struct {
	Kind   string          `json:"kind" validate:"required,oneof=allowance overtime commission bonus deduction statutory"`
	Label  string          `json:"label" validate:"required"`
	Amount decimal.Decimal `json:"amount" validate:"dpos"`
}

type StatutoryItemIn struct {
	Code           string          `json:"code" validate:"required"`
	EmployeeAmount decimal.Decimal `json:"employee_amount"`
	EmployerAmount decimal.Decimal `json:"employer_amount"`
}

type LinesIn struct {
	Lines               []ManualLine      `json:"lines" validate:"dive"`
	Reason              string            `json:"reason" validate:"required"`
	BasicSalary         *decimal.Decimal  `json:"basic_salary"`
	Allowances          *decimal.Decimal  `json:"allowances"`
	Commission          *decimal.Decimal  `json:"commission"`
	Overtime            *decimal.Decimal  `json:"overtime"`
	Bonuses             *decimal.Decimal  `json:"bonuses"`
	Deductions          *decimal.Decimal  `json:"deductions"`
	AdvanceRepayment    *decimal.Decimal  `json:"advance_repayment"`
	StatutoryDeductions *decimal.Decimal  `json:"statutory_deductions"`
	PayMethod           string            `json:"pay_method" validate:"omitempty,oneof=bank mobile_money cash"`
	PaymentReference    *string           `json:"payment_reference"`
	StatutoryItems      []StatutoryItemIn `json:"statutory_items" validate:"dive"`
}

// PutLines updates one payroll line of a draft run (either via structured column overrides,
// statutory item breakdown, or legacy manual lines) and records an adjustment note.
func (s *Service) PutLines(ctx context.Context, p *auth.Principal, runID, slipOrLineID uuid.UUID, in LinesIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "draft" {
			return httpx.Conflict("invalid_status", "Payroll lines can only be adjusted while the run is in draft")
		}
		var lineID uuid.UUID
		var basic, allow, comm, ot, bonus, ded, adv, stat decimal.Decimal
		var payMethod string
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT pl.id, pl.basic_salary, pl.allowances, pl.commission, pl.overtime, pl.bonuses,\n\t\t\t       pl.deductions, pl.advance_repayment, pl.statutory_deductions, pl.pay_method::text\n\t\t\tFROM app.payroll_lines pl\n\t\t\tLEFT JOIN app.payslips ps ON ps.payroll_line_id = pl.id\n\t\t\tWHERE pl.payroll_run_id = $1 AND (pl.id = $2 OR ps.id = $2)\n\t\t\tFOR UPDATE OF pl\n\t\t", runID, slipOrLineID).Scan(&lineID, &basic, &allow, &comm, &ot, &bonus, &ded, &adv, &stat, &payMethod); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.NotFound("not_found", "Payslip not found")
			}
			return err
		}
		before, err := s.slip(ctx, tx, lineID)
		if err != nil {
			return err
		}

		// Apply structured overrides when present.
		if in.BasicSalary != nil {
			basic = money.Money(*in.BasicSalary)
		}
		if in.Allowances != nil {
			allow = money.Money(*in.Allowances)
		}
		if in.Commission != nil {
			comm = money.Money(*in.Commission)
		}
		if in.Overtime != nil {
			ot = money.Money(*in.Overtime)
		}
		if in.Bonuses != nil {
			bonus = money.Money(*in.Bonuses)
		}
		if in.Deductions != nil {
			ded = money.Money(*in.Deductions)
		}
		if in.AdvanceRepayment != nil {
			adv = money.Money(*in.AdvanceRepayment)
		}
		if in.StatutoryDeductions != nil {
			stat = money.Money(*in.StatutoryDeductions)
		}
		if in.PayMethod != "" {
			payMethod = in.PayMethod
		}

		// Apply legacy manual line additions.
		for _, l := range in.Lines {
			amt := money.Money(l.Amount)
			switch l.Kind {
			case "allowance":
				allow = allow.Add(amt)
			case "overtime":
				ot = ot.Add(amt)
			case "commission":
				comm = comm.Add(amt)
			case "bonus":
				bonus = bonus.Add(amt)
			case "deduction":
				ded = ded.Add(amt)
			case "statutory":
				stat = stat.Add(amt)
			}
		}

		// Replace statutory breakdown items if provided and keep statutory_deductions aligned when not explicitly overridden.
		if in.StatutoryItems != nil {
			if _, err := tx.Exec(ctx, "DELETE FROM app.payroll_statutory_items WHERE payroll_line_id = $1", lineID); err != nil {
				return err
			}
			empStat := decimal.Zero
			for _, si := range in.StatutoryItems {
				code := strings.ToUpper(strings.TrimSpace(si.Code))
				if si.EmployeeAmount.IsNegative() || si.EmployerAmount.IsNegative() {
					return httpx.BadRequest("invalid_body", "Statutory amounts cannot be negative")
				}
				if _, err := tx.Exec(ctx, "\n\t\t\t\t\tINSERT INTO app.payroll_statutory_items (id, payroll_line_id, code, employee_amount, employer_amount)\n\t\t\t\t\tVALUES ($1, $2, $3, $4, $5)\n\t\t\t\t", ids.New(), lineID, code, money.Money(si.EmployeeAmount), money.Money(si.EmployerAmount)); err != nil {
					if db.IsUnique(err) {
						return httpx.Conflict("duplicate", "Duplicate statutory item code "+code)
					}
					return err
				}
				empStat = empStat.Add(money.Money(si.EmployeeAmount))
			}
			if in.StatutoryDeductions == nil {
				stat = empStat
			}
		}

		for _, v := range []decimal.Decimal{basic, allow, comm, ot, bonus, ded, adv, stat} {
			if v.IsNegative() {
				return httpx.BadRequest("invalid_body", "Payroll line amounts cannot be negative")
			}
		}
		gross := basic.Add(allow).Add(comm).Add(ot).Add(bonus)
		totalDed := ded.Add(adv).Add(stat)
		if totalDed.GreaterThan(gross) {
			return httpx.Conflict("negative_net", "Net pay cannot be negative")
		}

		reason := strings.TrimSpace(in.Reason)
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.payroll_lines\n\t\t\tSET basic_salary = $2, allowances = $3, commission = $4, overtime = $5, bonuses = $6,\n\t\t\t    deductions = $7, advance_repayment = $8, statutory_deductions = $9,\n\t\t\t    pay_method = $10::app.pay_method,\n\t\t\t    payment_reference = COALESCE($11, payment_reference),\n\t\t\t    adjustment_note = $12\n\t\t\tWHERE id = $1\n\t\t", lineID, basic, allow, comm, ot, bonus, ded, adv, stat, payMethod, in.PaymentReference, reason); err != nil {
			return err
		}

		if out, err = s.slip(ctx, tx, lineID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payslip.adjusted", "payroll_line", lineID, before,
			map[string]any{"reason": reason, "net": out["net"]})
	})
	return out, err
}

// ---------- submit, approve, pay, reverse ----------

// Submit notifies payroll approvers that a draft run is ready to review and approve.
func (s *Service) Submit(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft run can be submitted for approval")
		}
		if run["payslip_count"].(int64) == 0 {
			return httpx.Conflict("invalid_status", "Calculate the run before submitting it")
		}
		var loc *uuid.UUID
		if id, ok := run["location_id"].(uuid.UUID); ok {
			loc = &id
		}
		if err := s.Notify.ToPermission(ctx, tx, loc, "hr.payroll.approve", notify.Message{
			Type: "approval_waiting", Title: "Payroll awaits approval",
			Body: "Payroll " + asStr(run["number"]) + " is ready to approve",
			Link: "/payroll-runs/" + runID.String(), EntityType: "payroll_run", EntityID: &runID,
		}); err != nil {
			return err
		}
		out = map[string]any{"id": runID, "status": "draft", "submitted": true}
		return s.Audit.Record(ctx, tx, p, "payroll_run.submitted", "payroll_run", runID, map[string]any{"status": "draft"}, out)
	})
	return 200, out, err
}

// Approve transitions a draft run to approved (enforcing hr.payroll.approve and SoD via tg_payroll_guard)
// and issues payslip numbers for every payroll line.
func (s *Service) Approve(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		run, err := s.loadRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft run can be approved")
		}
		if run["payslip_count"].(int64) == 0 {
			return httpx.Conflict("invalid_status", "Calculate the run before approving it")
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.payroll_runs\n\t\t\tSET status = 'approved', approved_by = $2, approved_at = now()\n\t\t\tWHERE id = $1\n\t\t", runID, p.MembershipID); err != nil {
			if db.PgCode(err) == "BM007" {
				return httpx.Forbidden("cannot_approve_own", "Payroll must be approved by someone other than who generated it")
			}
			return err
		}

		// Issue payslips for every line that does not yet have one.
		lRows, err := tx.Query(ctx, "\n\t\t\tSELECT pl.id FROM app.payroll_lines pl\n\t\t\tWHERE pl.payroll_run_id = $1\n\t\t\t  AND NOT EXISTS (SELECT 1 FROM app.payslips ps WHERE ps.payroll_line_id = pl.id)\n\t\t\tORDER BY pl.created_at, pl.id\n\t\t", runID)
		if err != nil {
			return err
		}
		var lineIDs []uuid.UUID
		for lRows.Next() {
			var lid uuid.UUID
			if err := lRows.Scan(&lid); err != nil {
				lRows.Close()
				return err
			}
			lineIDs = append(lineIDs, lid)
		}
		lRows.Close()
		if err := lRows.Err(); err != nil {
			return err
		}
		for _, lid := range lineIDs {
			no, err := sequence.Next(ctx, tx, uuid.Nil, "payslip")
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.payslips (id, payslip_no, payroll_line_id)\n\t\t\t\tVALUES ($1, $2, $3)\n\t\t\t\tON CONFLICT (payroll_line_id) DO NOTHING\n\t\t\t", ids.New(), no, lid); err != nil {
				return err
			}
		}

		after, err := s.loadRun(ctx, tx, runID, false)
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "payroll_run.approved", "payroll_run", runID,
			map[string]any{"status": "draft"},
			map[string]any{"status": "approved", "gross_total": after["gross_total"], "net_total": after["net_total"], "employer_total": after["employer_total"]}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(after))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type PayIn struct {
	AccountID      *uuid.UUID      `json:"account_id"`
	MoneyAccountID *uuid.UUID      `json:"money_account_id"`
	PayslipIDs     json.RawMessage `json:"payslip_ids"`
	Reference      string          `json:"reference"`
	Date           string          `json:"date" validate:"omitempty,date"`
}

// Pay marks an approved payroll run as paid. The database trigger tg_payroll_post posts
// salary outflows to app.financial_transactions, stamps payroll_lines.paid_at, and applies
// advance repayments to app.employee_advances.
func (s *Service) Pay(ctx context.Context, p *auth.Principal, runID uuid.UUID, in PayIn) (int, []byte, error) {
	acctID := in.AccountID
	if acctID == nil {
		acctID = in.MoneyAccountID
	}
	if acctID == nil {
		return 0, nil, httpx.BadRequest("invalid_body", "account_id or money_account_id is required")
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		run, err := s.loadRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "approved" {
			return httpx.Conflict("invalid_status", "Approve the run before paying it")
		}
		acct, err := payments.ActiveFin(ctx, tx, *acctID)
		if err != nil {
			return err
		}
		netTotal := toDecimal(run["net_total"])
		if !acct.AllowNegative && acct.Balance.LessThan(netTotal) {
			return httpx.Conflict("insufficient_funds", "Not enough balance in "+acct.Name+" to pay this payroll run")
		}
		date := strings.TrimSpace(in.Date)
		if date == "" {
			date = hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.payroll_runs\n\t\t\tSET status = 'paid', account_id = $2, paid_by = $3, paid_at = now(), paid_business_date = $4::date\n\t\t\tWHERE id = $1\n\t\t", runID, *acctID, p.MembershipID, date); err != nil {
			switch db.PgCode(err) {
			case "BM002":
				return httpx.Conflict("day_closed", "Business day is closed for this location")
			case "BM007":
				return httpx.Forbidden("permission_denied", "You do not have permission to pay payroll")
			}
			return err
		}
		paidCount := run["payslip_count"].(int64)
		if err := s.Audit.Record(ctx, tx, p, "payroll_run.paid", "payroll_run", runID,
			map[string]any{"status": "approved"},
			map[string]any{"status": "paid", "amount": netTotal, "account_id": *acctID, "paid_business_date": date, "paid_payslips": paidCount}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(map[string]any{
			"id": runID, "status": "paid", "paid_payslips": paidCount, "amount": netTotal, "unpaid_payslips": 0,
		}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// Reverse reopens an approved run back to draft (approved -> draft) or cancels a draft run
// (draft -> cancelled). Paid runs cannot be reversed.
func (s *Service) Reverse(ctx context.Context, p *auth.Principal, runID uuid.UUID, reason string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		run, err := s.loadRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		reason = strings.TrimSpace(reason)
		switch run["status"] {
		case "approved":
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.payroll_runs\n\t\t\t\tSET status = 'draft', approved_by = NULL, approved_at = NULL\n\t\t\t\tWHERE id = $1\n\t\t\t", runID); err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "payroll_run.reopened", "payroll_run", runID,
				map[string]any{"status": "approved"}, map[string]any{"status": "draft", "reason": reason}); err != nil {
				return err
			}
		case "draft":
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.employee_pay_adjustments SET payroll_line_id = NULL\n\t\t\t\tWHERE payroll_line_id IN (SELECT id FROM app.payroll_lines WHERE payroll_run_id = $1)\n\t\t\t", runID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.commission_entries SET status = 'accrued', payroll_line_id = NULL\n\t\t\t\tWHERE payroll_line_id IN (SELECT id FROM app.payroll_lines WHERE payroll_run_id = $1)\n\t\t\t", runID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tDELETE FROM app.payroll_lines pl\n\t\t\t\tWHERE pl.payroll_run_id = $1\n\t\t\t\t  AND NOT EXISTS (SELECT 1 FROM app.payslips ps WHERE ps.payroll_line_id = pl.id)\n\t\t\t", runID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.payroll_runs SET status = 'cancelled'\n\t\t\t\tWHERE id = $1\n\t\t\t", runID); err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "payroll_run.cancelled", "payroll_run", runID,
				map[string]any{"status": "draft"}, map[string]any{"status": "cancelled", "reason": reason}); err != nil {
				return err
			}
		default:
			return httpx.Conflict("invalid_status", "Only a draft or approved unpaid run can be reversed")
		}
		out, err := s.loadRun(ctx, tx, runID, false)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// PaymentList returns the bank or mobile money disbursement rows for an approved or paid run.
func (s *Service) PaymentList(ctx context.Context, p *auth.Principal, runID uuid.UUID) (map[string]any, []map[string]any, error) {
	var run map[string]any
	var rows []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if run, err = s.loadRun(ctx, tx, runID, false); err != nil {
			return err
		}
		if st := run["status"]; st != "approved" && st != "paid" {
			return httpx.Conflict("invalid_status", "Approve the run before exporting its payment list")
		}
		rows, err = hrkit.All(ctx, tx, `
			SELECT e.employee_no, e.full_name::text AS name, pl.pay_method::text AS pay_method,
			       COALESCE(NULLIF(trim(COALESCE(ec.payout_provider, '') || CASE WHEN ec.payout_account_last4 IS NOT NULL THEN ' ****' || ec.payout_account_last4 ELSE '' END), ''), '') AS pay_account,
			       COALESCE(ps.payslip_no, '') AS payslip_number,
			       pl.net_pay AS net,
			       CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END AS paid_total,
			       CASE WHEN pl.paid_at IS NOT NULL THEN 0 ELSE pl.net_pay END AS amount_due
			FROM app.payroll_lines pl
			JOIN app.employees e ON e.id = pl.employee_id
			LEFT JOIN app.payslips ps ON ps.payroll_line_id = pl.id
			LEFT JOIN LATERAL (
				SELECT payout_provider, payout_account_last4
				FROM app.employee_compensation c
				WHERE c.employee_id = e.id
				ORDER BY c.effective_from DESC
				LIMIT 1
			) ec ON true
			WHERE pl.payroll_run_id = $1
			ORDER BY pl.pay_method, e.full_name
		`, runID)
		return err
	})
	return run, rows, err
}

// ---------- payslips and salary payments ----------

func (s *Service) slip(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	return hrkit.One(ctx, tx, "Payslip not found", `
		SELECT COALESCE(ps.id, pl.id) AS id, pl.id AS payroll_line_id, ps.id AS payslip_id,
		       COALESCE(ps.payslip_no, '') AS number,
		       pl.payroll_run_id AS run_id,
		       CASE WHEN r.id IS NOT NULL THEN format('PR-%s-%s', r.period_year, lpad(r.period_month::text, 2, '0')) ELSE '' END AS run_number,
		       COALESCE(r.status::text, CASE WHEN pl.paid_at IS NOT NULL THEN 'paid' ELSE 'approved' END) AS run_status,
		       COALESCE(r.period_year, EXTRACT(YEAR FROM COALESCE(ps.issued_at, pl.created_at))::smallint) AS period_year,
		       COALESCE(r.period_month, EXTRACT(MONTH FROM COALESCE(ps.issued_at, pl.created_at))::smallint) AS period_month,
		       CASE WHEN r.id IS NOT NULL THEN make_date(r.period_year, r.period_month, 1)::text ELSE '' END AS period_start,
		       CASE WHEN r.id IS NOT NULL THEN (make_date(r.period_year, r.period_month, 1) + interval '1 month' - interval '1 day')::date::text ELSE '' END AS period_end,
		       pl.employee_id, e.full_name::text AS employee_name, e.employee_no, e.location_id AS branch_id,
		       pl.basic_salary, pl.basic_salary AS base_pay,
		       pl.allowances, pl.commission, pl.overtime, pl.bonuses,
		       pl.deductions, pl.advance_repayment, pl.statutory_deductions,
		       pl.gross_pay AS gross,
		       (pl.deductions + pl.advance_repayment + pl.statutory_deductions) AS total_deductions,
		       pl.net_pay AS net,
		       CASE WHEN pl.paid_at IS NOT NULL THEN pl.net_pay ELSE 0 END AS paid_total,
		       pl.pay_method::text AS pay_method, pl.payment_reference, pl.paid_at, pl.adjustment_note,
		       CASE WHEN pl.paid_at IS NOT NULL THEN 'paid' WHEN r.status IS NOT NULL THEN r.status::text ELSE 'approved' END AS status
		FROM app.payroll_lines pl
		JOIN app.employees e ON e.id = pl.employee_id
		LEFT JOIN app.payslips ps ON ps.payroll_line_id = pl.id
		LEFT JOIN app.payroll_runs r ON r.id = pl.payroll_run_id
		WHERE (ps.id = $1 OR pl.id = $1)
	`, id)
}

// Payslip returns one payslip (by payslip id or payroll_line id) with a synthesized breakdown
// of earnings, statutory items, and advance repayments. Staff without hr.payroll.view can
// view only their own issued payslips (enforced both here and by PostgreSQL RLS).
func (s *Service) Payslip(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		sl, err := s.slip(ctx, tx, id)
		if err != nil {
			return err
		}
		hasView := p.IsOwner || p.Has("hr.payroll.view") || p.Has("hr.payroll.manage") || p.Has("hr.payroll.approve")
		empID, _ := sl["employee_id"].(uuid.UUID)
		mine := p.EmployeeID != nil && empID == *p.EmployeeID
		if !hasView {
			if !mine || sl["payslip_id"] == nil {
				return httpx.NotFound("not_found", "Payslip not found")
			}
		}

		lineID := sl["payroll_line_id"].(uuid.UUID)
		var lines []map[string]any
		addLine := func(kind, label string, amt decimal.Decimal) {
			if amt.IsPositive() {
				lines = append(lines, map[string]any{"kind": kind, "label": label, "amount": amt.StringFixed(2)})
			}
		}
		addLine("base", "Basic salary", toDecimal(sl["basic_salary"]))
		addLine("allowance", "Allowances", toDecimal(sl["allowances"]))
		addLine("overtime", "Overtime", toDecimal(sl["overtime"]))
		addLine("commission", "Commission", toDecimal(sl["commission"]))
		addLine("bonus", "Bonuses", toDecimal(sl["bonuses"]))

		// Statutory items breakdown if present, otherwise single summary line.
		statRows, err := hrkit.All(ctx, tx, `
			SELECT id, code, employee_amount, employer_amount
			FROM app.payroll_statutory_items
			WHERE payroll_line_id = $1
			ORDER BY code
		`, lineID)
		if err != nil {
			return err
		}
		if len(statRows) > 0 {
			for _, sr := range statRows {
				empAmt := toDecimal(sr["employee_amount"])
				if empAmt.IsPositive() {
					lines = append(lines, map[string]any{"kind": "statutory", "label": sr["code"], "amount": empAmt.StringFixed(2)})
				}
				erAmt := toDecimal(sr["employer_amount"])
				if erAmt.IsPositive() {
					lines = append(lines, map[string]any{"kind": "employer_contribution", "label": asStr(sr["code"]) + " (Employer)", "amount": erAmt.StringFixed(2)})
				}
			}
		} else {
			addLine("statutory", "Statutory deductions", toDecimal(sl["statutory_deductions"]))
		}

		addLine("deduction", "Deductions", toDecimal(sl["deductions"]))

		// Advance / loan repayments linked to this line.
		repRows, err := hrkit.All(ctx, tx, `
			SELECT ar.id, ar.advance_id, a.kind::text AS advance_kind, ar.amount, a.balance
			FROM app.advance_repayments ar
			JOIN app.employee_advances a ON a.id = ar.advance_id
			WHERE ar.payroll_line_id = $1
			ORDER BY ar.created_at
		`, lineID)
		if err != nil {
			return err
		}
		paid := sl["paid_at"] != nil
		if len(repRows) > 0 {
			for _, rr := range repRows {
				amt := toDecimal(rr["amount"])
				bal := toDecimal(rr["balance"])
				if !paid {
					bal = decimal.Max(bal.Sub(amt), decimal.Zero)
				}
				label := "Loan repayment"
				if rr["advance_kind"] == "advance" {
					label = "Salary advance"
				}
				lines = append(lines, map[string]any{
					"kind":               "loan",
					"label":              label,
					"amount":             amt.StringFixed(2),
					"loan_id":            rr["advance_id"],
					"loan_balance_after": bal.StringFixed(2),
				})
			}
		} else {
			addLine("loan", "Advance repayment", toDecimal(sl["advance_repayment"]))
		}

		if lines == nil {
			lines = []map[string]any{}
		}
		sl["lines"] = lines
		sl["statutory_items"] = statRows
		out = sl
		return nil
	})
	return out, err
}

func (s *Service) SalaryPayments(ctx context.Context, p *auth.Principal, employee, run *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("ft.txn_type = 'salary'")
		q.If(employee != nil, "pl.employee_id = ?", employee)
		q.If(run != nil, "pl.payroll_run_id = ?", run)
		q.If(from != "", "ft.business_date >= ?::date", from)
		q.If(to != "", "ft.business_date <= ?::date", to)
		const from_ = ` FROM app.financial_transactions ft
			JOIN app.payroll_lines pl ON pl.id = ft.payroll_line_id
			JOIN app.payroll_runs r ON r.id = pl.payroll_run_id
			JOIN app.employees e ON e.id = pl.employee_id
			LEFT JOIN app.payslips ps ON ps.payroll_line_id = pl.id
			LEFT JOIN app.financial_accounts fa ON fa.id = ft.account_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from_+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT ft.id, COALESCE(ps.payslip_no, '') AS number, ft.business_date::text AS payment_date,
			       ft.amount, pl.pay_method::text AS method, 'posted' AS status,
			       COALESCE(pl.payment_reference, '') AS reference,
			       pl.employee_id, e.full_name::text AS employee_name,
			       fa.name::text AS money_account,
			       ps.id AS payslip_id, COALESCE(ps.payslip_no, '') AS payslip_number,
			       r.id AS run_id, format('PR-%s-%s', r.period_year, lpad(r.period_month::text, 2, '0')) AS run_number`+
			from_+q.Where()+` ORDER BY ft.business_date DESC, ft.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// ---------- salary components (removed in approved schema) ----------

type ComponentIn struct {
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Calc         string           `json:"calc"`
	Value        *decimal.Decimal `json:"value"`
	Brackets     []Bracket        `json:"brackets"`
	IsTaxable    *bool            `json:"is_taxable"`
	IsStatutory  *bool            `json:"is_statutory"`
	AppliesToAll *bool            `json:"applies_to_all"`
	SortOrder    *int             `json:"sort_order"`
	IsActive     *bool            `json:"is_active"`
}

func (s *Service) ComponentsList(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	return []map[string]any{}, nil
}

func (s *Service) SaveComponent(ctx context.Context, p *auth.Principal, id *uuid.UUID, in ComponentIn) (map[string]any, error) {
	return nil, &httpx.Error{Status: 410, Code: "feature_removed", Message: "Salary components are replaced by employee allowances and payroll statutory items"}
}

// ---------- salary due job ----------

// SalaryDue notifies payroll managers before pay day and reminds approvers about runs
// awaiting approval or payment.
func (s *Service) SalaryDue(ctx context.Context, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InJobsTx(ctx, db.Actor{}, func(ctx context.Context, tx pgx.Tx) error {
		today := hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"))
		if in.Date != "" {
			if d, err := hrkit.Date(in.Date); err == nil {
				today = d
			}
		}
		payDay := setting.Int(ctx, tx, "payroll.pay_day", 25)
		before := s.Notify.DaysBefore(ctx, tx, "salary_due", setting.Int(ctx, tx, "notifications.salary_due_days", 3))
		last := time.Date(today.Year(), today.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		if payDay > last {
			payDay = last
		}
		due := time.Date(today.Year(), today.Month(), payDay, 0, 0, 0, 0, time.UTC)
		if today.Equal(due.AddDate(0, 0, -before)) {
			if err := s.Notify.ToPermission(ctx, tx, nil, "hr.payroll.manage", notify.Message{
				Type: "salary_due", Title: "Salary due soon",
				Body: fmt.Sprintf("Salaries are due on %s, in %d days", due.Format(hrkit.DateFmt), before),
				Link: "/payroll-runs",
			}); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT id, format('PR-%s-%s', period_year, lpad(period_month::text, 2, '0')), status::text, location_id\n\t\t\tFROM app.payroll_runs\n\t\t\tWHERE status IN ('draft','approved')\n\t\t")
		if err != nil {
			return err
		}
		type waiting struct {
			id             uuid.UUID
			number, status string
			loc            uuid.UUID
		}
		var list []waiting
		for rows.Next() {
			var w waiting
			if err := rows.Scan(&w.id, &w.number, &w.status, &w.loc); err != nil {
				rows.Close()
				return err
			}
			list = append(list, w)
		}
		rows.Close()
		for _, w := range list {
			title := "Payroll awaits approval"
			if w.status == "approved" {
				title = "Payroll awaits payment"
			}
			id := w.id
			loc := w.loc
			if err := s.Notify.ToPermission(ctx, tx, &loc, "hr.payroll.approve", notify.Message{
				Type: "salary_due", Title: title, Body: "Payroll " + w.number,
				Link: "/payroll-runs/" + id.String(), EntityType: "payroll_run", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func toDecimal(v any) decimal.Decimal {
	switch x := v.(type) {
	case decimal.Decimal:
		return x
	case *decimal.Decimal:
		if x != nil {
			return *x
		}
		return decimal.Zero
	case json.Number:
		d, _ := decimal.NewFromString(x.String())
		return d
	case string:
		d, _ := decimal.NewFromString(strings.TrimSpace(x))
		return d
	case int64:
		return decimal.NewFromInt(x)
	case int:
		return decimal.NewFromInt(int64(x))
	case float64:
		d, _ := decimal.NewFromString(strconv.FormatFloat(x, 'f', -1, 64))
		return d
	default:
		return decimal.Zero
	}
}
