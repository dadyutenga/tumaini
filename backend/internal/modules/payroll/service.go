package payroll

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
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
	DB       *db.DB
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Jobs     *jobs.Client
	Clock    clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, j *jobs.Client, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Notify: n, Jobs: j, Clock: clk}
}

const runSQL = `SELECT r.id, r.number, r.branch_id, b.name AS branch_name, r.period_start, r.period_end, r.status,
	r.gross_total, r.deduction_total, r.net_total, r.employer_total, r.created_by, r.approved_by, r.approved_at, r.created_at, r.updated_at,
	(SELECT count(*) FROM payslips ps WHERE ps.run_id = r.id) AS payslip_count,
	(SELECT COALESCE(SUM(ps.paid_total),0) FROM payslips ps WHERE ps.run_id = r.id) AS paid_total
	FROM payroll_runs r LEFT JOIN branches b ON b.id = r.branch_id`

// ---------- runs ----------

type RunIn struct {
	Start    string     `json:"period_start" validate:"required,date"`
	End      string     `json:"period_end" validate:"required,date"`
	BranchID *uuid.UUID `json:"branch_id"`
	Scope    string     `json:"scope" validate:"omitempty,oneof=branch business"`
}

func (s *Service) CreateRun(ctx context.Context, p *auth.Principal, in RunIn) (map[string]any, error) {
	if in.End < in.Start {
		return nil, httpx.BadRequest("invalid_body", "The period end cannot be before its start")
	}
	if a, b := hrkit.MustDate(in.Start), hrkit.MustDate(in.End); b.Sub(a) > 31*24*time.Hour {
		return nil, httpx.BadRequest("invalid_body", "A payroll period cannot be longer than one month")
	}
	branch := in.BranchID
	if in.Scope == "business" {
		branch = nil
		if g, _ := p.Can("payroll.prepare"); g.Scope != "all" && !p.IsOwner {
			return nil, httpx.Forbidden("permission_denied", "Only a user with all-branch payroll access can run payroll for the whole business")
		}
	} else if branch == nil {
		branch = p.BranchID
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if branch != nil && !p.SeesBranch("payroll.prepare", *branch) {
			return httpx.NotFound("not_found", "Branch not found")
		}
		var clash int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM payroll_runs
			WHERE status <> 'reversed' AND period_start <= $2 AND period_end >= $1
			  AND ($3::uuid IS NULL OR branch_id IS NULL OR branch_id = $3)
		`, in.Start, in.End, branch).Scan(&clash); err != nil {
			return err
		}
		if clash > 0 {
			return httpx.Conflict("duplicate", "A payroll run already covers this period for this scope")
		}
		seqBranch := branch
		if seqBranch == nil {
			var main uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&main); err != nil {
				return err
			}
			seqBranch = &main
		}
		number, err := sequence.Next(ctx, tx, *seqBranch, "payroll")
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO payroll_runs (id, business_id, branch_id, number, period_start, period_end, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, p.BusinessID, branch, number, in.Start, in.End, p.UserID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A payroll run already covers this period for this scope")
			}
			return err
		}
		if out, err = hrkit.One(ctx, tx, "Payroll run not found", runSQL+` WHERE r.id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payroll_run.created", "payroll_run", id, nil, out)
	})
	return out, err
}

func (s *Service) ListRuns(ctx context.Context, p *auth.Principal, status, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if br := p.ScopeBranches("payroll.view"); br != nil {
			q.Add("r.branch_id = ANY(?)", br)
		}
		q.If(status != "", "r.status = ?", status)
		q.If(from != "", "r.period_end >= ?", from)
		q.If(to != "", "r.period_start <= ?", to)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM payroll_runs r`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, runSQL+q.Where()+` ORDER BY r.period_start DESC, r.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// loadRun reads a run inside the caller's scope for key, optionally locking it.
func (s *Service) loadRun(ctx context.Context, tx pgx.Tx, p *auth.Principal, key string, id uuid.UUID, lock bool) (map[string]any, error) {
	if lock {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM payroll_runs WHERE id=$1 FOR UPDATE`, id); err != nil {
			return nil, err
		}
	}
	run, err := hrkit.One(ctx, tx, "Payroll run not found", runSQL+` WHERE r.id=$1`, id)
	if err != nil {
		return nil, err
	}
	if key == "" || p.IsOwner {
		return run, nil
	}
	g, ok := p.Can(key)
	if !ok {
		return nil, httpx.NotFound("not_found", "Payroll run not found")
	}
	if b, ok2 := run["branch_id"].(string); ok2 {
		bid, _ := uuid.Parse(b)
		if !p.SeesBranch(key, bid) {
			return nil, httpx.NotFound("not_found", "Payroll run not found")
		}
	} else if g.Scope != "all" {
		return nil, httpx.NotFound("not_found", "Payroll run not found")
	}
	return run, nil
}

func (s *Service) GetRun(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, p, "payroll.view", id, false)
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
		SELECT ps.id, ps.number, ps.employee_id, e.name, e.employee_no, ps.days_worked, ps.days_absent, ps.unpaid_leave_days, ps.overtime_hours,
		       ps.base_pay, ps.gross, ps.total_deductions, ps.net, ps.paid_total, ps.net - ps.paid_total AS balance, ps.status,
		       COALESCE((SELECT SUM(amount) FROM payslip_lines pl WHERE pl.payslip_id = ps.id AND pl.kind = 'absence'), 0) AS absence,
		       COALESCE((SELECT SUM(amount) FROM payslip_lines pl WHERE pl.payslip_id = ps.id AND pl.kind = 'allowance'), 0) AS allowance,
		       COALESCE((SELECT SUM(amount) FROM payslip_lines pl WHERE pl.payslip_id = ps.id AND pl.kind = 'loan'), 0) AS loan,
		       COALESCE((SELECT SUM(amount) FROM payslip_lines pl WHERE pl.payslip_id = ps.id AND pl.kind = 'employer_contribution'), 0) AS employer
		FROM payslips ps JOIN employees e ON e.id = ps.employee_id
		WHERE ps.run_id = $1 ORDER BY e.name
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
		standardHours:   decimal.NewFromFloat(setting.Float(ctx, tx, "payroll.standard_hours", 8)),
		multiplier:      decimal.NewFromFloat(setting.Float(ctx, tx, "payroll.overtime_multiplier", 1.5)),
		latenessDeducts: setting.Bool(ctx, tx, "payroll.lateness_deducts", false),
	}
}

type employee struct {
	id, branch, position uuid.UUID
	hasPosition          bool
	name, payType        string
	base                 decimal.Decimal
	hire                 string
	terminated           *string
}

// Calculate computes or recomputes every payslip of a draft or calculated run.
// Large runs are queued and the caller gets 202.
func (s *Service) Calculate(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, map[string]any, error) {
	var n int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, p, "payroll.prepare", runID, false)
		if err != nil {
			return err
		}
		if st := run["status"]; st != "draft" && st != "calculated" {
			return httpx.Conflict("invalid_status", "This run can no longer be calculated")
		}
		emps, err := s.employees(ctx, tx, run)
		n = len(emps)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	if n > largeRun && s.Jobs != nil {
		if err := s.Jobs.Enqueue(ctx, p.BusinessID, jobs.PayrollCalculate, map[string]any{"run_id": runID, "user_id": p.UserID}); err != nil {
			return 0, nil, err
		}
		return 202, map[string]any{"id": runID, "status": "calculating", "employees": n}, nil
	}
	var out map[string]any
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.calculateTx(ctx, tx, p, runID)
		return err
	})
	return 200, out, err
}

// CalculateJob runs a queued calculation as the user who asked for it.
func (s *Service) CalculateJob(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error {
	var in struct {
		RunID  uuid.UUID `json:"run_id"`
		UserID uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &in); err != nil {
		return err
	}
	p := &auth.Principal{BusinessID: businessID, UserID: in.UserID, IsOwner: true, RequestID: "job:" + jobs.PayrollCalculate}
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.calculateTx(ctx, tx, p, in.RunID); err != nil {
			return err
		}
		return s.Notify.ToUser(ctx, tx, businessID, in.UserID, notify.Message{Type: "payroll_calculated", Title: "Payroll calculated", Body: "The payroll run is ready to review", Link: "/payroll-runs/" + in.RunID.String(), EntityType: "payroll_run", EntityID: &in.RunID})
	})
}

func (s *Service) employees(ctx context.Context, tx pgx.Tx, run map[string]any) ([]employee, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, branch_id, position_id, name, pay_type, base_pay, hire_date::text, terminated_on::text
		FROM employees
		WHERE ($1::uuid IS NULL OR branch_id = $1) AND hire_date <= $3
		  AND (status = 'active' OR (status = 'terminated' AND terminated_on >= $2))
		ORDER BY name
	`, run["branch_id"], run["period_start"], run["period_end"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []employee
	for rows.Next() {
		var e employee
		var pos *uuid.UUID
		if err := rows.Scan(&e.id, &e.branch, &pos, &e.name, &e.payType, &e.base, &e.hire, &e.terminated); err != nil {
			return nil, err
		}
		if pos != nil {
			e.position, e.hasPosition = *pos, true
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) calculateTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, runID uuid.UUID) (map[string]any, error) {
	run, err := s.loadRun(ctx, tx, p, "", runID, true)
	if err != nil {
		return nil, err
	}
	if st := run["status"]; st != "draft" && st != "calculated" {
		return nil, httpx.Conflict("invalid_status", "This run can no longer be calculated")
	}
	start, end := run["period_start"].(string), run["period_end"].(string)
	if _, err := tx.Exec(ctx, `UPDATE pay_adjustments SET payslip_id = NULL WHERE payslip_id IN (SELECT id FROM payslips WHERE run_id=$1)`, runID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM payslips WHERE run_id=$1`, runID); err != nil {
		return nil, err
	}
	cfg := loadSettings(ctx, tx)
	comps, err := loadComponents(ctx, tx)
	if err != nil {
		return nil, err
	}
	emps, err := s.employees(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	seqBranch, err := runBranch(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	var gross, ded, net, employer decimal.Decimal
	for _, e := range emps {
		in, meta, err := s.inputFor(ctx, tx, cfg, comps, e, start, end)
		if err != nil {
			return nil, err
		}
		res := Compute(in)
		pid := ids.New()
		number, err := sequence.Next(ctx, tx, seqBranch, "payslip")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO payslips (id, business_id, run_id, employee_id, number, days_worked, days_absent, unpaid_leave_days, overtime_hours, base_pay, gross, total_deductions, net)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`, pid, p.BusinessID, runID, e.id, number, meta.daysWorked, meta.absent, meta.unpaidLeave, in.OvertimeHours.Round(2), e.base, res.Gross, res.TotalDeductions, res.Net); err != nil {
			return nil, err
		}
		for _, l := range res.Lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payslip_lines (id, business_id, payslip_id, kind, component_id, loan_id, label, amount)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			`, ids.New(), p.BusinessID, pid, l.Kind, l.ComponentID, l.LoanID, l.Label, l.Amount); err != nil {
				return nil, err
			}
			if l.AdjustID != nil {
				if _, err := tx.Exec(ctx, `UPDATE pay_adjustments SET payslip_id=$2 WHERE id=$1`, *l.AdjustID, pid); err != nil {
					return nil, err
				}
			}
		}
		gross, ded, net, employer = gross.Add(res.Gross), ded.Add(res.TotalDeductions), net.Add(res.Net), employer.Add(res.Employer)
	}
	if _, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='calculated', gross_total=$2, deduction_total=$3, net_total=$4, employer_total=$5 WHERE id=$1`,
		runID, gross, ded, net, employer); err != nil {
		return nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "payroll_run.calculated", "payroll_run", runID, map[string]any{"status": run["status"]},
		map[string]any{"status": "calculated", "employees": len(emps), "gross_total": gross, "net_total": net}); err != nil {
		return nil, err
	}
	out, err := s.loadRun(ctx, tx, p, "", runID, false)
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

func runBranch(ctx context.Context, tx pgx.Tx, run map[string]any) (uuid.UUID, error) {
	if b, ok := run["branch_id"].(string); ok {
		return uuid.Parse(b)
	}
	var main uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&main)
	return main, err
}

type compRow struct {
	Component
	appliesToAll bool
}

func loadComponents(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]compRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, name, kind, calc, value, brackets, is_taxable, is_statutory, applies_to_all, sort_order
		FROM salary_components WHERE is_active
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]compRow{}
	for rows.Next() {
		var c compRow
		var raw []byte
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Calc, &c.Value, &raw, &c.Taxable, &c.Statutory, &c.appliesToAll, &c.Sort); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Brackets); err != nil {
				return nil, fmt.Errorf("component %s brackets: %w", c.Name, err)
			}
		}
		out[c.ID] = c
	}
	return out, rows.Err()
}

type slipMeta struct {
	daysWorked, absent, unpaidLeave decimal.Decimal
}

// inputFor gathers attendance, components, commission, adjustments and loans
// for one employee in the period.
func (s *Service) inputFor(ctx context.Context, tx pgx.Tx, cfg settings, comps map[uuid.UUID]compRow, e employee, start, end string) (Input, slipMeta, error) {
	var meta slipMeta
	in := Input{
		PayType: e.payType, Base: e.base, WorkingDays: cfg.workingDays, StandardHours: cfg.standardHours,
		OvertimeMultiplier: cfg.multiplier, LatenessDeducts: cfg.latenessDeducts,
	}
	var absent, halfDays, present, unpaidLeave decimal.Decimal
	var otMinutes, lateMinutes, workedMinutes int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE d.status = 'absent'),
		       count(*) FILTER (WHERE d.status = 'half_day'),
		       count(*) FILTER (WHERE d.status IN ('present','late')),
		       count(*) FILTER (WHERE d.status = 'leave' AND EXISTS (
		           SELECT 1 FROM leave_requests lr JOIN leave_types lt ON lt.id = lr.leave_type_id
		           WHERE lr.employee_id = d.employee_id AND lr.status = 'approved' AND NOT lt.is_paid
		             AND d.day BETWEEN lr.start_date AND lr.end_date)),
		       COALESCE(SUM(d.overtime_minutes) FILTER (WHERE d.overtime_status = 'approved'), 0),
		       COALESCE(SUM(d.late_minutes), 0),
		       COALESCE(SUM(d.worked_minutes), 0)
		FROM attendance_days d WHERE d.employee_id = $1 AND d.day BETWEEN $2 AND $3
	`, e.id, start, end).Scan(&absent, &halfDays, &present, &unpaidLeave, &otMinutes, &lateMinutes, &workedMinutes); err != nil {
		return in, meta, err
	}
	half := halfDays.Mul(decimal.NewFromFloat(0.5))
	meta.absent = absent.Add(half)
	meta.unpaidLeave = unpaidLeave
	meta.daysWorked = present.Add(half)
	in.UnpaidDays = absent.Add(half).Add(unpaidLeave)
	in.DaysWorked = meta.daysWorked
	in.HoursWorked = decimal.NewFromInt(int64(workedMinutes)).Div(decimal.NewFromInt(60)).Round(2)
	in.OvertimeHours = decimal.NewFromInt(int64(otMinutes)).Div(decimal.NewFromInt(60))
	in.LateMinutes = lateMinutes

	// Joiners and leavers: working days outside employment are unpaid.
	if e.payType == "monthly" || e.payType == "" {
		cal, err := hrkit.LoadCalendar(ctx, tx, &e.id, e.branch)
		if err != nil {
			return in, meta, err
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
	}

	// Components: business-wide ones plus the employee's own, whose value overrides.
	assigned := map[uuid.UUID]*decimal.Decimal{}
	rows, err := tx.Query(ctx, `SELECT component_id, value FROM employee_components WHERE employee_id=$1`, e.id)
	if err != nil {
		return in, meta, err
	}
	for rows.Next() {
		var id uuid.UUID
		var v *decimal.Decimal
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return in, meta, err
		}
		assigned[id] = v
	}
	rows.Close()
	for id, c := range comps {
		v, mine := assigned[id]
		if !mine && !c.appliesToAll {
			continue
		}
		comp := c.Component
		if mine && v != nil {
			comp.Value = *v
		}
		in.Components = append(in.Components, comp)
	}

	commission, err := s.commission(ctx, tx, e, start, end)
	if err != nil {
		return in, meta, err
	}
	in.Commission = commission

	arows, err := tx.Query(ctx, `
		SELECT id, kind, amount, reason FROM pay_adjustments
		WHERE employee_id=$1 AND period BETWEEN $2 AND $3 AND payslip_id IS NULL ORDER BY created_at
	`, e.id, start, end)
	if err != nil {
		return in, meta, err
	}
	for arows.Next() {
		var id uuid.UUID
		var kind, reason string
		var amt decimal.Decimal
		if err := arows.Scan(&id, &kind, &amt, &reason); err != nil {
			arows.Close()
			return in, meta, err
		}
		item := Item{ID: &id, Label: reason, Amount: amt}
		if kind == "bonus" {
			in.Bonuses = append(in.Bonuses, item)
		} else {
			in.Deductions = append(in.Deductions, item)
		}
	}
	arows.Close()

	// Loans: the scheduled amount, capped by what is left after deductions
	// already counted on other approved, unpaid runs.
	lrows, err := tx.Query(ctx, `
		SELECT l.id, l.number, l.kind, l.monthly_deduction,
		       l.balance - COALESCE((SELECT SUM(pl.amount) FROM payslip_lines pl JOIN payslips ps ON ps.id = pl.payslip_id
		                             JOIN payroll_runs r ON r.id = ps.run_id
		                             WHERE pl.loan_id = l.id AND pl.kind = 'loan' AND r.status IN ('approved','paid')), 0)
		FROM employee_loans l
		WHERE l.employee_id = $1 AND l.status = 'disbursed' AND l.balance > 0 AND l.start_period <= $2
		ORDER BY l.created_at
	`, e.id, end)
	if err != nil {
		return in, meta, err
	}
	for lrows.Next() {
		var id uuid.UUID
		var number, kind string
		var monthly, open decimal.Decimal
		if err := lrows.Scan(&id, &number, &kind, &monthly, &open); err != nil {
			lrows.Close()
			return in, meta, err
		}
		due := decimal.Min(monthly, open)
		if due.IsPositive() {
			label := "Loan repayment " + number
			if kind == "advance" {
				label = "Salary advance " + number
			}
			in.Loans = append(in.Loans, LoanDue{ID: id, Label: label, Due: due})
		}
	}
	lrows.Close()
	return in, meta, lrows.Err()
}

// commission applies the employee's rule, or else their position's rule, to
// their completed sales in the period, net of returns (FR-PAY-08).
func (s *Service) commission(ctx context.Context, tx pgx.Tx, e employee, start, end string) (decimal.Decimal, error) {
	var calc string
	var value, target decimal.Decimal
	err := tx.QueryRow(ctx, `
		SELECT calc, value, min_target FROM commission_rules
		WHERE is_active AND (employee_id = $1 OR (employee_id IS NULL AND position_id = $2))
		ORDER BY employee_id NULLS LAST LIMIT 1
	`, e.id, nullUUID(e.hasPosition, e.position)).Scan(&calc, &value, &target)
	if err == pgx.ErrNoRows {
		return zero, nil
	}
	if err != nil {
		return zero, err
	}
	if calc == "fixed_per_project" {
		var projTotal decimal.Decimal
		var projCount int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(p.total_price), 0), count(*)
			FROM svc_projects p
			WHERE p.status = 'completed'
			  AND COALESCE(p.completed_at::date, p.updated_at::date) BETWEEN $2::date AND $3::date
			  AND p.owner_id IN (SELECT u.id FROM users u WHERE u.employee_id = $1)
		`, e.id, start, end).Scan(&projTotal, &projCount); err != nil {
			return zero, err
		}
		if projCount == 0 || projTotal.LessThan(target) {
			return zero, nil
		}
		return r2(value.Mul(decimal.NewFromInt(int64(projCount)))), nil
	}
	var netSales decimal.Decimal
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(s.total - s.tax_total), 0), count(*) FILTER (WHERE s.total > 0)
		FROM sales s
		WHERE s.kind = 'sale' AND s.status IN ('completed','partially_refunded','refunded')
		  AND s.business_date BETWEEN $2 AND $3
		  AND (s.employee_id = $1 OR (s.employee_id IS NULL AND s.cashier_id IN (SELECT u.id FROM users u WHERE u.employee_id = $1)))
	`, e.id, start, end).Scan(&netSales, &count); err != nil {
		return zero, err
	}
	if netSales.LessThan(target) || !netSales.IsPositive() {
		return zero, nil
	}
	if calc == "fixed_per_sale" {
		return r2(value.Mul(decimal.NewFromInt(int64(count)))), nil
	}
	return r2(netSales.Mul(value).Div(hundred)), nil
}

func nullUUID(ok bool, id uuid.UUID) any {
	if !ok {
		return nil
	}
	return id
}

// ---------- manual lines ----------

type ManualLine struct {
	Kind   string          `json:"kind" validate:"required,oneof=allowance overtime commission bonus deduction"`
	Label  string          `json:"label" validate:"required"`
	Amount decimal.Decimal `json:"amount" validate:"dpos"`
}

type LinesIn struct {
	Lines  []ManualLine `json:"lines" validate:"dive"`
	Reason string       `json:"reason" validate:"required"`
}

var earningKinds = map[string]bool{"base": true, "allowance": true, "overtime": true, "commission": true, "bonus": true}
var deductionKinds = map[string]bool{"deduction": true, "statutory": true, "loan": true}

// PutLines replaces the manual lines of one payslip before approval and
// recomputes its totals. A recalculation of the run removes manual lines.
func (s *Service) PutLines(ctx context.Context, p *auth.Principal, runID, slipID uuid.UUID, in LinesIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, p, "payroll.prepare", runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "calculated" && run["status"] != "draft" {
			return httpx.Conflict("invalid_status", "Payslips can only be adjusted before the run is submitted")
		}
		before, err := s.slip(ctx, tx, slipID)
		if err != nil || before["run_id"] != runID.String() {
			return httpx.NotFound("not_found", "Payslip not found")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM payslip_lines WHERE payslip_id=$1 AND is_manual`, slipID); err != nil {
			return err
		}
		reason := strings.TrimSpace(in.Reason)
		for _, l := range in.Lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO payslip_lines (id, business_id, payslip_id, kind, label, amount, is_manual, note)
				VALUES ($1,$2,$3,$4,$5,$6,true,$7)
			`, ids.New(), p.BusinessID, slipID, l.Kind, strings.TrimSpace(l.Label), money.Money(l.Amount), reason); err != nil {
				return err
			}
		}
		if err := retotal(ctx, tx, slipID); err != nil {
			return err
		}
		if err := retotalRun(ctx, tx, runID); err != nil {
			return err
		}
		if out, err = s.slip(ctx, tx, slipID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payslip.adjusted", "payslip", slipID, before, map[string]any{"lines": in.Lines, "reason": reason, "net": out["net"]})
	})
	return out, err
}

func retotal(ctx context.Context, tx pgx.Tx, slipID uuid.UUID) error {
	rows, err := tx.Query(ctx, `SELECT kind, amount FROM payslip_lines WHERE payslip_id=$1`, slipID)
	if err != nil {
		return err
	}
	var gross, ded decimal.Decimal
	for rows.Next() {
		var kind string
		var amt decimal.Decimal
		if err := rows.Scan(&kind, &amt); err != nil {
			rows.Close()
			return err
		}
		switch {
		case earningKinds[kind]:
			gross = gross.Add(amt)
		case kind == "absence":
			gross = gross.Sub(amt)
		case deductionKinds[kind]:
			ded = ded.Add(amt)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if gross.IsNegative() || ded.GreaterThan(gross) {
		return httpx.Conflict("negative_net", "Net pay cannot be negative")
	}
	_, err = tx.Exec(ctx, `UPDATE payslips SET gross=$2, total_deductions=$3, net=$4 WHERE id=$1`, slipID, gross, ded, gross.Sub(ded))
	return err
}

func retotalRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE payroll_runs r SET
		  gross_total = COALESCE((SELECT SUM(gross) FROM payslips WHERE run_id = r.id), 0),
		  deduction_total = COALESCE((SELECT SUM(total_deductions) FROM payslips WHERE run_id = r.id), 0),
		  net_total = COALESCE((SELECT SUM(net) FROM payslips WHERE run_id = r.id), 0),
		  employer_total = COALESCE((SELECT SUM(pl.amount) FROM payslip_lines pl JOIN payslips ps ON ps.id = pl.payslip_id
		                             WHERE ps.run_id = r.id AND pl.kind = 'employer_contribution'), 0)
		WHERE r.id = $1
	`, runID)
	return err
}

// ---------- submit, approve, pay, reverse ----------

func (s *Service) Submit(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		run, err := s.loadRun(ctx, tx, p, "payroll.prepare", runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "calculated" {
			return httpx.Conflict("invalid_status", "Calculate the run before submitting it")
		}
		net, _ := decimal.NewFromString(run["net_total"].(string))
		var branch *uuid.UUID
		if b, ok := run["branch_id"].(string); ok {
			id, _ := uuid.Parse(b)
			branch = &id
		}
		if _, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='pending_approval' WHERE id=$1`, runID); err != nil {
			return err
		}
		reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: branch, Kind: "payroll_run", SubjectType: "payroll_run", SubjectID: runID,
			Amount: decimal.NullDecimal{Decimal: net, Valid: true}, Summary: "Payroll " + run["number"].(string) + " for " + run["period_start"].(string) + " to " + run["period_end"].(string),
		})
		if err != nil {
			return err
		}
		if err := s.Notify.ToPermission(ctx, tx, p.BusinessID, branch, "payroll.approve", notify.Message{
			Type: "payroll_awaiting_approval", Title: "Payroll awaits approval", Body: "Payroll " + run["number"].(string) + " is ready to approve",
			Link: "/payroll-runs/" + runID.String(), EntityType: "payroll_run", EntityID: &runID,
		}); err != nil {
			return err
		}
		out = map[string]any{"id": runID, "status": "pending_approval", "approval_request_id": reqID}
		return s.Audit.Record(ctx, tx, p, "payroll_run.submitted", "payroll_run", runID, map[string]any{"status": "calculated"}, out)
	})
	return 202, out, err
}

func (s *Service) Approve(ctx context.Context, p *auth.Principal, runID uuid.UUID) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if _, err := s.loadRun(ctx, tx, p, "payroll.approve", runID, true); err != nil {
			return err
		}
		var requester uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT requested_by FROM approval_requests WHERE subject_type='payroll_run' AND subject_id=$1 AND status='pending'`, runID).Scan(&requester); err == nil {
			if requester == p.UserID && !p.IsOwner {
				return httpx.Forbidden("cannot_approve_own", "You cannot approve a payroll you submitted")
			}
		}
		if err := s.approveTx(ctx, tx, p, runID); err != nil {
			return err
		}
		if err := hrkit.ResolveApproval(ctx, tx, p, "payroll_run", runID, "approved"); err != nil {
			return err
		}
		run, err := s.loadRun(ctx, tx, p, "", runID, false)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(run))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) onApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return s.approveTx(ctx, tx, p, req.SubjectID)
}

func (s *Service) onRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	tag, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='calculated' WHERE id=$1 AND status='pending_approval'`, req.SubjectID)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "payroll_run."+req.Status, "payroll_run", req.SubjectID, map[string]any{"status": "pending_approval"}, map[string]any{"status": "calculated", "note": req.Note})
}

// approveTx freezes the payslips, posts the payroll journal and locks the
// attendance of the period for the employees in the run (FR-PAY-11, FR-ATT-11).
func (s *Service) approveTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, runID uuid.UUID) error {
	run, err := s.loadRun(ctx, tx, p, "", runID, true)
	if err != nil {
		return err
	}
	if run["status"] != "calculated" && run["status"] != "pending_approval" {
		return httpx.Conflict("invalid_status", "Calculate the run before approving it")
	}
	if run["payslip_count"].(int64) == 0 {
		return httpx.Conflict("invalid_status", "This run has no payslips")
	}
	var branch *uuid.UUID
	if b, ok := run["branch_id"].(string); ok {
		id, _ := uuid.Parse(b)
		branch = &id
	}
	var gross, net, employer, statutory, other decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(ps.gross),0), COALESCE(SUM(ps.net),0),
		       COALESCE((SELECT SUM(pl.amount) FROM payslip_lines pl JOIN payslips x ON x.id = pl.payslip_id WHERE x.run_id = $1 AND pl.kind = 'employer_contribution'),0),
		       COALESCE((SELECT SUM(pl.amount) FROM payslip_lines pl JOIN payslips x ON x.id = pl.payslip_id WHERE x.run_id = $1 AND (pl.kind = 'statutory' OR (pl.kind = 'deduction' AND pl.component_id IS NOT NULL))),0),
		       COALESCE((SELECT SUM(pl.amount) FROM payslip_lines pl JOIN payslips x ON x.id = pl.payslip_id WHERE x.run_id = $1 AND pl.kind = 'deduction' AND pl.component_id IS NULL),0)
		FROM payslips ps WHERE ps.run_id = $1
	`, runID).Scan(&gross, &net, &employer, &statutory, &other); err != nil {
		return err
	}
	lines := []ledger.Line{
		{Code: "6100", BranchID: branch, Debit: gross},
		{Code: "6110", BranchID: branch, Debit: employer},
		{Code: "2210", BranchID: branch, Credit: statutory.Add(employer)},
		// One-off deductions such as shortages and fines recover money for
		// the business and are recorded as other income.
		{Code: "4100", BranchID: branch, Credit: other},
	}
	rows, err := tx.Query(ctx, `
		SELECT ps.employee_id, ps.net, COALESCE((SELECT SUM(amount) FROM payslip_lines pl WHERE pl.payslip_id = ps.id AND pl.kind = 'loan'),0)
		FROM payslips ps WHERE ps.run_id = $1
	`, runID)
	if err != nil {
		return err
	}
	type slipNet struct {
		emp       uuid.UUID
		net, loan decimal.Decimal
	}
	var slips []slipNet
	for rows.Next() {
		var x slipNet
		if err := rows.Scan(&x.emp, &x.net, &x.loan); err != nil {
			rows.Close()
			return err
		}
		slips = append(slips, x)
	}
	rows.Close()
	for i := range slips {
		x := &slips[i]
		lines = append(lines,
			ledger.Line{Code: "2200", BranchID: branch, PartyType: "employee", PartyID: &x.emp, Credit: x.net},
			ledger.Line{Code: "1300", BranchID: branch, PartyType: "employee", PartyID: &x.emp, Credit: x.loan})
	}
	user := p.UserID
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, run["period_end"].(string), "payroll_run", runID, "Payroll "+run["number"].(string), &user, lines); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='approved', approved_by=$2, approved_at=now() WHERE id=$1`, runID, p.UserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payslips SET status='approved' WHERE run_id=$1`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE attendance_days d SET is_locked = true
		FROM payslips ps WHERE ps.run_id = $1 AND d.employee_id = ps.employee_id AND d.day BETWEEN $2 AND $3
	`, runID, run["period_start"], run["period_end"]); err != nil {
		return err
	}
	_ = net
	return s.Audit.Record(ctx, tx, p, "payroll_run.approved", "payroll_run", runID, map[string]any{"status": run["status"]},
		map[string]any{"status": "approved", "gross_total": gross, "net_total": net, "employer_total": employer})
}

type PayIn struct {
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	PayslipIDs     json.RawMessage `json:"payslip_ids"`
	Reference      string          `json:"reference"`
	Date           string          `json:"date" validate:"date"`
}

// Pay records salary payments for the chosen payslips (or all unpaid ones).
// When every payslip is paid the run locks and loan balances go down.
func (s *Service) Pay(ctx context.Context, p *auth.Principal, runID uuid.UUID, in PayIn) (int, []byte, error) {
	var selected []uuid.UUID
	all := len(in.PayslipIDs) == 0 || string(in.PayslipIDs) == "null" || string(in.PayslipIDs) == `"all"`
	if !all {
		if err := json.Unmarshal(in.PayslipIDs, &selected); err != nil || len(selected) == 0 {
			return 0, nil, httpx.BadRequest("invalid_body", `payslip_ids must be "all" or a list of payslip ids`)
		}
	}
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		run, err := s.loadRun(ctx, tx, p, "payroll.pay", runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "approved" {
			return httpx.Conflict("invalid_status", "Approve the run before paying it")
		}
		method, err := hrkit.MethodFor(ctx, tx, p, "payroll.pay", in.MoneyAccountID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, employee_id, net - paid_total FROM payslips
			WHERE run_id=$1 AND net > paid_total AND ($2 OR id = ANY($3))
			ORDER BY number FOR UPDATE
		`, runID, all, selected)
		if err != nil {
			return err
		}
		type due struct {
			id, emp uuid.UUID
			amt     decimal.Decimal
		}
		var dues []due
		total := decimal.Zero
		for rows.Next() {
			var x due
			if err := rows.Scan(&x.id, &x.emp, &x.amt); err != nil {
				rows.Close()
				return err
			}
			dues = append(dues, x)
			total = total.Add(x.amt)
		}
		rows.Close()
		if !all && len(dues) != len(selected) {
			return httpx.Conflict("invalid_status", "Some payslips are not in this run or are already paid")
		}
		if len(dues) == 0 {
			return httpx.Conflict("invalid_status", "There is nothing left to pay on this run")
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, in.MoneyAccountID, total); err != nil {
			return err
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.MoneyAccountID)
		if err != nil {
			return err
		}
		branch, err := runBranch(ctx, tx, run)
		if err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			date = hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
		}
		var runBranchID *uuid.UUID
		if b, ok := run["branch_id"].(string); ok {
			id, _ := uuid.Parse(b)
			runBranchID = &id
		}
		jl := []ledger.Line{{Code: code, BranchID: runBranchID, MoneyAccountID: &in.MoneyAccountID, Credit: total}}
		paidIDs := make([]uuid.UUID, 0, len(dues))
		for i := range dues {
			x := dues[i]
			empID := x.emp
			if _, err := payments.Insert(ctx, tx, payments.Input{
				BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
				Direction: "out", Kind: "salary", Method: method, PartyType: "employee", PartyID: &empID,
				Amount: x.amt, Reference: in.Reference, Date: date, AllocType: "payslip", AllocID: x.id, AllocAmount: x.amt,
			}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payslips SET paid_total = net, status='paid' WHERE id=$1`, x.id); err != nil {
				return err
			}
			jl = append(jl, ledger.Line{Code: "2200", BranchID: runBranchID, PartyType: "employee", PartyID: &empID, Debit: x.amt})
			paidIDs = append(paidIDs, x.id)
		}
		user := p.UserID
		payRef := ids.New()
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "salary_payment", payRef, "Salary payment "+run["number"].(string), &user, jl); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM payslips WHERE run_id=$1 AND net > paid_total`, runID).Scan(&open); err != nil {
			return err
		}
		runStatus := "approved"
		if open == 0 {
			runStatus = "locked"
			if err := settleLoans(ctx, tx, p, runID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='locked' WHERE id=$1`, runID); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "payroll_run.paid", "payroll_run", runID, nil,
			map[string]any{"payslip_ids": paidIDs, "amount": total, "money_account_id": in.MoneyAccountID, "status": runStatus}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{
			"id": runID, "status": runStatus, "paid_payslips": len(paidIDs), "amount": total, "unpaid_payslips": open,
		}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// settleLoans reduces loan balances by the payroll deductions of a fully paid run.
func settleLoans(ctx context.Context, tx pgx.Tx, p *auth.Principal, runID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT pl.loan_id, pl.payslip_id, pl.amount FROM payslip_lines pl JOIN payslips ps ON ps.id = pl.payslip_id
		WHERE ps.run_id = $1 AND pl.kind = 'loan' AND pl.loan_id IS NOT NULL
	`, runID)
	if err != nil {
		return err
	}
	type rep struct {
		loan, slip uuid.UUID
		amt        decimal.Decimal
	}
	var reps []rep
	for rows.Next() {
		var x rep
		if err := rows.Scan(&x.loan, &x.slip, &x.amt); err != nil {
			rows.Close()
			return err
		}
		reps = append(reps, x)
	}
	rows.Close()
	for _, x := range reps {
		if _, err := tx.Exec(ctx, `INSERT INTO loan_repayments (id, business_id, loan_id, amount, source, payslip_id) VALUES ($1,$2,$3,$4,'payroll',$5)`,
			ids.New(), p.BusinessID, x.loan, x.amt, x.slip); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE employee_loans SET balance = GREATEST(balance - $2, 0),
			  status = CASE WHEN balance - $2 <= 0 THEN 'settled' ELSE status END
			WHERE id = $1
		`, x.loan, x.amt); err != nil {
			return err
		}
	}
	return nil
}

// Reverse undoes an approved run that has no payments by posting the mirror
// journal entry. The run stays in history as reversed (FR-PAY-17).
func (s *Service) Reverse(ctx context.Context, p *auth.Principal, runID uuid.UUID, reason string) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		run, err := s.loadRun(ctx, tx, p, "payroll.reverse", runID, true)
		if err != nil {
			return err
		}
		if run["status"] != "approved" {
			return httpx.Conflict("invalid_status", "Only an approved, unpaid run can be reversed")
		}
		if paid, _ := decimal.NewFromString(run["paid_total"].(string)); paid.IsPositive() {
			return httpx.Conflict("invalid_status", "This run has salary payments and cannot be reversed")
		}
		date := hrkit.LocalToday(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")).Format(hrkit.DateFmt)
		user := p.UserID
		if err := s.Ledger.Reverse(ctx, tx, p.BusinessID, "payroll_run", runID, date, "Reversal of payroll "+run["number"].(string), &user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='reversed' WHERE id=$1`, runID); err != nil {
			return err
		}
		// Unlock days not covered by another approved run.
		if _, err := tx.Exec(ctx, `
			UPDATE attendance_days d SET is_locked = false
			FROM payslips ps WHERE ps.run_id = $1 AND d.employee_id = ps.employee_id AND d.day BETWEEN $2 AND $3
			  AND NOT EXISTS (SELECT 1 FROM payslips o JOIN payroll_runs r ON r.id = o.run_id
			                  WHERE o.employee_id = d.employee_id AND r.id <> $1 AND r.status IN ('approved','paid','locked')
			                    AND d.day BETWEEN r.period_start AND r.period_end)
		`, runID, run["period_start"], run["period_end"]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE pay_adjustments SET payslip_id = NULL WHERE payslip_id IN (SELECT id FROM payslips WHERE run_id=$1)`, runID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "payroll_run.reversed", "payroll_run", runID, map[string]any{"status": "approved"}, map[string]any{"status": "reversed", "reason": reason}); err != nil {
			return err
		}
		out, err := s.loadRun(ctx, tx, p, "", runID, false)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// PaymentList returns the bank or mobile money file rows for a run.
func (s *Service) PaymentList(ctx context.Context, p *auth.Principal, runID uuid.UUID) (map[string]any, []map[string]any, error) {
	var run map[string]any
	var rows []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if run, err = s.loadRun(ctx, tx, p, "payroll.pay", runID, false); err != nil {
			return err
		}
		if st := run["status"]; st != "approved" && st != "locked" && st != "paid" {
			return httpx.Conflict("invalid_status", "Approve the run before exporting its payment list")
		}
		rows, err = hrkit.All(ctx, tx, `
			SELECT e.employee_no, e.name, COALESCE(e.pay_method, 'cash') AS pay_method, COALESCE(e.pay_account, '') AS pay_account,
			       ps.number AS payslip_number, ps.net, ps.paid_total, ps.net - ps.paid_total AS amount_due
			FROM payslips ps JOIN employees e ON e.id = ps.employee_id
			WHERE ps.run_id = $1 ORDER BY e.pay_method, e.name
		`, runID)
		return err
	})
	return run, rows, err
}

// ---------- payslips and salary payments ----------

func (s *Service) slip(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	return hrkit.One(ctx, tx, "Payslip not found", `
		SELECT ps.id, ps.number, ps.run_id, r.number AS run_number, r.status AS run_status, r.period_start, r.period_end,
		       ps.employee_id, e.name AS employee_name, e.employee_no, e.branch_id, ps.days_worked, ps.days_absent, ps.unpaid_leave_days,
		       ps.overtime_hours, ps.base_pay, ps.gross, ps.total_deductions, ps.net, ps.paid_total, ps.status
		FROM payslips ps JOIN payroll_runs r ON r.id = ps.run_id JOIN employees e ON e.id = ps.employee_id
		WHERE ps.id = $1
	`, id)
}

// Payslip returns one payslip with lines and loan balances. A caller with
// own scope sees only their own approved payslips (FR-PAY-14).
func (s *Service) Payslip(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		sl, err := s.slip(ctx, tx, id)
		if err != nil {
			return err
		}
		mine := p.EmployeeID != nil && sl["employee_id"] == p.EmployeeID.String()
		if !p.IsOwner {
			g, _ := p.Can("payslips.view")
			branch, _ := uuid.Parse(sl["branch_id"].(string))
			switch {
			case g.Scope == "own" && !mine:
				return httpx.NotFound("not_found", "Payslip not found")
			case g.Scope != "own" && !mine && !p.SeesBranch("payslips.view", branch):
				return httpx.NotFound("not_found", "Payslip not found")
			}
			if mine && g.Scope == "own" {
				if st := sl["run_status"]; st != "approved" && st != "paid" && st != "locked" {
					return httpx.NotFound("not_found", "Payslip not found")
				}
			}
		}
		lines, err := hrkit.All(ctx, tx, `
			SELECT pl.id, pl.kind, pl.label, pl.amount, pl.component_id, pl.loan_id, pl.is_manual, pl.note,
			       l.balance AS loan_balance
			FROM payslip_lines pl LEFT JOIN employee_loans l ON l.id = pl.loan_id
			WHERE pl.payslip_id = $1 ORDER BY CASE pl.kind WHEN 'base' THEN 0 WHEN 'absence' THEN 1 WHEN 'allowance' THEN 2 WHEN 'overtime' THEN 3
			  WHEN 'commission' THEN 4 WHEN 'bonus' THEN 5 WHEN 'statutory' THEN 6 WHEN 'deduction' THEN 7 WHEN 'loan' THEN 8 ELSE 9 END, pl.label
		`, id)
		if err != nil {
			return err
		}
		// Loan balance after this payslip: the current balance, less this
		// deduction while the run is not yet fully paid.
		locked := sl["run_status"] == "locked"
		for _, l := range lines {
			if l["kind"] == "loan" && l["loan_balance"] != nil {
				bal, _ := decimal.NewFromString(l["loan_balance"].(string))
				amt, _ := decimal.NewFromString(l["amount"].(string))
				if !locked {
					bal = decimal.Max(bal.Sub(amt), decimal.Zero)
				}
				l["loan_balance_after"] = bal.StringFixed(2)
			}
			delete(l, "loan_balance")
		}
		sl["lines"] = lines
		out = sl
		return nil
	})
	return out, err
}

func (s *Service) SalaryPayments(ctx context.Context, p *auth.Principal, employee, run *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("pm.kind = 'salary'")
		if br := p.ScopeBranches("payroll.view"); br != nil {
			q.Add("pm.branch_id = ANY(?)", br)
		}
		q.If(employee != nil, "pm.party_id = ?", employee)
		q.If(run != nil, "ps.run_id = ?", run)
		q.If(from != "", "pm.payment_date >= ?", from)
		q.If(to != "", "pm.payment_date <= ?", to)
		const from_ = ` FROM payments pm
			LEFT JOIN employees e ON e.id = pm.party_id
			LEFT JOIN payment_allocations pa ON pa.payment_id = pm.id AND pa.document_type = 'payslip'
			LEFT JOIN payslips ps ON ps.id = pa.document_id
			LEFT JOIN payroll_runs r ON r.id = ps.run_id
			LEFT JOIN money_accounts ma ON ma.id = pm.money_account_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from_+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = hrkit.All(ctx, tx, `
			SELECT pm.id, pm.number, pm.payment_date, pm.amount, pm.method, pm.status, pm.reference, pm.party_id AS employee_id, e.name AS employee_name,
			       ma.name AS money_account, ps.id AS payslip_id, ps.number AS payslip_number, r.id AS run_id, r.number AS run_number`+
			from_+q.Where()+` ORDER BY pm.payment_date DESC, pm.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// ---------- salary components ----------

type ComponentIn struct {
	Name         string           `json:"name"`
	Kind         string           `json:"kind" validate:"omitempty,oneof=allowance deduction employer_contribution"`
	Calc         string           `json:"calc" validate:"omitempty,oneof=fixed percent_of_base percent_of_gross bracket"`
	Value        *decimal.Decimal `json:"value"`
	Brackets     []Bracket        `json:"brackets"`
	IsTaxable    *bool            `json:"is_taxable"`
	IsStatutory  *bool            `json:"is_statutory"`
	AppliesToAll *bool            `json:"applies_to_all"`
	SortOrder    *int             `json:"sort_order"`
	IsActive     *bool            `json:"is_active"`
}

const compSQL = `SELECT id, name, kind, calc, value, brackets, is_taxable, is_statutory, applies_to_all, sort_order, is_active FROM salary_components`

func (s *Service) ComponentsList(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = hrkit.All(ctx, tx, compSQL+` ORDER BY kind, sort_order, name`)
		return err
	})
	return out, err
}

func validBrackets(bs []Bracket) error {
	for i, b := range bs {
		if b.From.IsNegative() || b.Rate.IsNegative() || b.Rate.GreaterThan(hundred) || b.Fixed.IsNegative() {
			return httpx.BadRequest("invalid_body", "Bracket values must be positive and rates at most 100")
		}
		if b.To != nil && b.To.LessThan(b.From) {
			return httpx.BadRequest("invalid_body", "A bracket cannot end before it starts")
		}
		if i > 0 && (bs[i-1].To == nil || b.From.LessThan(*bs[i-1].To)) {
			return httpx.BadRequest("invalid_body", "Brackets must be in order and not overlap")
		}
	}
	return nil
}

func (s *Service) SaveComponent(ctx context.Context, p *auth.Principal, id *uuid.UUID, in ComponentIn) (map[string]any, error) {
	name := strings.TrimSpace(in.Name)
	if in.Value != nil && in.Value.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Value cannot be negative")
	}
	if err := validBrackets(in.Brackets); err != nil {
		return nil, err
	}
	var brackets any
	if in.Brackets != nil {
		raw, _ := json.Marshal(in.Brackets)
		brackets = raw
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var before map[string]any
		rid := ids.New()
		if id == nil {
			if name == "" || in.Kind == "" {
				return httpx.BadRequest("invalid_body", "Name and kind are required")
			}
			calc := in.Calc
			if calc == "" {
				calc = "fixed"
			}
			if calc == "bracket" && len(in.Brackets) == 0 {
				return httpx.BadRequest("invalid_body", "A bracket component needs a bracket table")
			}
			if (calc == "percent_of_base" || calc == "percent_of_gross") && in.Value != nil && in.Value.GreaterThan(hundred) {
				return httpx.BadRequest("invalid_body", "A percentage cannot be above 100")
			}
			value := decimal.Zero
			if in.Value != nil {
				value = *in.Value
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO salary_components (id, business_id, name, kind, calc, value, brackets, is_taxable, is_statutory, applies_to_all, sort_order)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			`, rid, p.BusinessID, name, in.Kind, calc, value, brackets, boolOr(in.IsTaxable, true), boolOr(in.IsStatutory, false), boolOr(in.AppliesToAll, true), intOr(in.SortOrder, 0)); err != nil {
				if db.IsUnique(err) {
					return httpx.Conflict("duplicate", "A component with that name already exists")
				}
				return err
			}
		} else {
			rid = *id
			var err error
			if before, err = hrkit.One(ctx, tx, "Salary component not found", compSQL+` WHERE id=$1`, rid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE salary_components SET name=COALESCE(NULLIF($2,''),name), kind=COALESCE(NULLIF($3,''),kind), calc=COALESCE(NULLIF($4,''),calc),
				  value=COALESCE($5,value), brackets=COALESCE($6,brackets), is_taxable=COALESCE($7,is_taxable), is_statutory=COALESCE($8,is_statutory),
				  applies_to_all=COALESCE($9,applies_to_all), sort_order=COALESCE($10,sort_order), is_active=COALESCE($11,is_active)
				WHERE id=$1
			`, rid, name, in.Kind, in.Calc, in.Value, brackets, in.IsTaxable, in.IsStatutory, in.AppliesToAll, in.SortOrder, in.IsActive); err != nil {
				if db.IsUnique(err) {
					return httpx.Conflict("duplicate", "A component with that name already exists")
				}
				return err
			}
			var calc string
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT calc, brackets FROM salary_components WHERE id=$1`, rid).Scan(&calc, &raw); err != nil {
				return err
			}
			if calc == "bracket" && len(raw) == 0 {
				return httpx.BadRequest("invalid_body", "A bracket component needs a bracket table")
			}
		}
		var err error
		if out, err = hrkit.One(ctx, tx, "Salary component not found", compSQL+` WHERE id=$1`, rid); err != nil {
			return err
		}
		verb := "salary_component.created"
		if id != nil {
			verb = "salary_component.updated"
		}
		return s.Audit.Record(ctx, tx, p, verb, "salary_component", rid, before, out)
	})
	return out, err
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func intOr(n *int, def int) int {
	if n == nil {
		return def
	}
	return *n
}

// ---------- salary due job ----------

// SalaryDue notifies payroll preparers a set number of days before pay day,
// and approvers or payers of runs that are waiting on them (FR-NTF-04).
func (s *Service) SalaryDue(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
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
			if err := s.Notify.ToPermission(ctx, tx, businessID, nil, "payroll.prepare", notify.Message{
				Type: "salary_due", Title: "Salary due soon", Body: fmt.Sprintf("Salaries are due on %s, in %d days", due.Format(hrkit.DateFmt), before),
				Link: "/payroll-runs",
			}); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id, number, status, branch_id FROM payroll_runs WHERE status IN ('pending_approval','approved')`)
		if err != nil {
			return err
		}
		type waiting struct {
			id             uuid.UUID
			number, status string
			branch         *uuid.UUID
		}
		var list []waiting
		for rows.Next() {
			var w waiting
			if err := rows.Scan(&w.id, &w.number, &w.status, &w.branch); err != nil {
				rows.Close()
				return err
			}
			list = append(list, w)
		}
		rows.Close()
		for _, w := range list {
			perm, title := "payroll.approve", "Payroll awaits approval"
			if w.status == "approved" {
				perm, title = "payroll.pay", "Payroll awaits payment"
			}
			id := w.id
			if err := s.Notify.ToPermission(ctx, tx, businessID, w.branch, perm, notify.Message{
				Type: "salary_due", Title: title, Body: "Payroll " + w.number, Link: "/payroll-runs/" + id.String(), EntityType: "payroll_run", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
