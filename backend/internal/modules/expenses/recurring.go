package expenses

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type RecurringIn struct {
	BranchID    *uuid.UUID      `json:"branch_id"`
	CategoryID  uuid.UUID       `json:"category_id" validate:"required"`
	Amount      decimal.Decimal `json:"amount" validate:"dpos"`
	Description string          `json:"description" validate:"required,max=500"`
	Payee       string          `json:"payee" validate:"max=200"`
	Frequency   string          `json:"frequency" validate:"required,oneof=weekly monthly yearly"`
	StartDate   string          `json:"start_date" validate:"required,date"`
	EndDate     string          `json:"end_date" validate:"date"`
}

// advance returns the next due date after d for a frequency. Monthly and
// yearly keep the day of the start date where the month allows it.
func advance(d time.Time, freq string, anchorDay int) time.Time {
	switch freq {
	case "weekly":
		return d.AddDate(0, 0, 7)
	case "yearly":
		return clampDay(d.Year()+1, d.Month(), anchorDay)
	default:
		y, m := d.Year(), d.Month()+1
		if m > 12 {
			y, m = y+1, 1
		}
		return clampDay(y, m, anchorDay)
	}
}

func clampDay(y int, m time.Month, day int) time.Time {
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

const recurringCols = `r.id, r.branch_id, r.category_id, ec.name, r.amount, r.description, COALESCE(r.payee,''), r.frequency,
	r.start_date::text, COALESCE(r.end_date::text,''), r.next_run::text, r.is_active, r.created_by, r.created_at`

func scanRecurring(row pgx.Row) (map[string]any, error) {
	var id, branch, category, by uuid.UUID
	var catName, desc, payee, freq, start, end, next string
	var amount decimal.Decimal
	var active bool
	var created time.Time
	if err := row.Scan(&id, &branch, &category, &catName, &amount, &desc, &payee, &freq, &start, &end, &next, &active, &by, &created); err != nil {
		return nil, err
	}
	m := map[string]any{"id": id, "branch_id": branch, "category_id": category, "category_name": catName, "amount": amount,
		"description": desc, "payee": payee, "frequency": freq, "start_date": start, "end_date": nil, "next_run": next,
		"is_active": active, "created_by": by, "created_at": created}
	if end != "" {
		m["end_date"] = end
	}
	return m, nil
}

func (s *Service) Recurring(ctx context.Context, p *auth.Principal, page httpx.Page, active *bool) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("recurring_expenses.manage"); b != nil {
			q.Add("r.branch_id = ANY(?)", b)
		}
		q.If(active != nil, "r.is_active = ?", active)
		from := ` FROM recurring_expenses r JOIN expense_categories ec ON ec.id=r.category_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+recurringCols+from+q.Where()+` ORDER BY r.next_run, r.created_at`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanRecurring(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) getRecurring(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, lock bool) (map[string]any, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF r"
	}
	m, err := scanRecurring(tx.QueryRow(ctx, `SELECT `+recurringCols+` FROM recurring_expenses r JOIN expense_categories ec ON ec.id=r.category_id WHERE r.id=$1`+suffix, id))
	if err != nil || !p.SeesBranch("recurring_expenses.manage", m["branch_id"].(uuid.UUID)) {
		return nil, httpx.NotFound("not_found", "Recurring expense not found")
	}
	return m, nil
}

func (s *Service) checkRecurring(ctx context.Context, tx pgx.Tx, p *auth.Principal, in *RecurringIn) (uuid.UUID, error) {
	in.Description = strings.TrimSpace(in.Description)
	if err := httpx.Check(in); err != nil {
		return uuid.Nil, err
	}
	if in.EndDate != "" && in.EndDate < in.StartDate {
		return uuid.Nil, &httpx.Error{Status: 422, Code: "validation_failed", Message: "End date must not be before start date", Fields: map[string]string{"end_date": "after_start"}}
	}
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	if branch == nil {
		return uuid.Nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if !p.SeesBranch("recurring_expenses.manage", *branch) {
		return uuid.Nil, httpx.Forbidden("branch_forbidden", "You are not assigned to that branch")
	}
	if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
		return uuid.Nil, err
	}
	return *branch, nil
}

func (s *Service) CreateRecurring(ctx context.Context, p *auth.Principal, in RecurringIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		branch, err := s.checkRecurring(ctx, tx, p, &in)
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO recurring_expenses (id, business_id, branch_id, category_id, amount, description, payee, frequency, start_date, end_date, next_run, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,'')::date,$9,$11)
		`, id, p.BusinessID, branch, in.CategoryID, money.Money(in.Amount), in.Description, in.Payee, in.Frequency, in.StartDate, in.EndDate, p.UserID); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "recurring_expense.created", "recurring_expense", id, nil, out)
	})
	return out, err
}

func (s *Service) UpdateRecurring(ctx context.Context, p *auth.Principal, id uuid.UUID, in RecurringIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.getRecurring(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		if in.BranchID == nil {
			b := before["branch_id"].(uuid.UUID)
			in.BranchID = &b
		}
		branch, err := s.checkRecurring(ctx, tx, p, &in)
		if err != nil {
			return err
		}
		// Changing the start date moves the next run only when nothing was generated yet.
		next := before["next_run"].(string)
		var generated int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM expenses WHERE recurring_id=$1`, id).Scan(&generated); err != nil {
			return err
		}
		if generated == 0 {
			next = in.StartDate
		}
		if _, err := tx.Exec(ctx, `
			UPDATE recurring_expenses SET branch_id=$2, category_id=$3, amount=$4, description=$5, payee=NULLIF($6,''), frequency=$7,
				start_date=$8, end_date=NULLIF($9,'')::date, next_run=$10
			WHERE id=$1
		`, id, branch, in.CategoryID, money.Money(in.Amount), in.Description, in.Payee, in.Frequency, in.StartDate, in.EndDate, next); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "recurring_expense.updated", "recurring_expense", id, before, out)
	})
	return out, err
}

// SetRecurringActive pauses or resumes a template. Resuming skips due dates
// that passed while it was paused, so no backlog of expenses appears.
func (s *Service) SetRecurringActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.getRecurring(ctx, tx, p, id, true)
		if err != nil {
			return err
		}
		if before["is_active"] == active {
			return httpx.Conflict("invalid_status", map[bool]string{true: "This template is already active", false: "This template is already paused"}[active])
		}
		next := before["next_run"].(string)
		if active {
			today, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			start, _ := time.Parse("2006-01-02", before["start_date"].(string))
			d, _ := time.Parse("2006-01-02", next)
			for d.Format("2006-01-02") < today {
				d = advance(d, before["frequency"].(string), start.Day())
			}
			next = d.Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, `UPDATE recurring_expenses SET is_active=$2, next_run=$3 WHERE id=$1`, id, active, next); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		action := "recurring_expense.paused"
		if active {
			action = "recurring_expense.resumed"
		}
		return s.Audit.Record(ctx, tx, p, action, "recurring_expense", id, before, out)
	})
	return out, err
}

// RunRecurring is the RecurringExpenses job (FR-EXP-09). For each active
// template due on or before the run date it creates a submitted expense per
// due date, raises an approval request, and advances next_run. The unique
// (recurring_id, expense_date) index makes a second run a no-op.
func (s *Service) RunRecurring(ctx context.Context, businessID uuid.UUID, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		date := in.Date
		if _, err := time.Parse("2006-01-02", date); err != nil {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		rows, err := tx.Query(ctx, `
			SELECT id, branch_id, category_id, amount, description, COALESCE(payee,''), frequency, start_date::text, COALESCE(end_date::text,''), next_run::text, created_by
			FROM recurring_expenses WHERE is_active AND next_run <= $1::date
			ORDER BY next_run FOR UPDATE SKIP LOCKED
		`, date)
		if err != nil {
			return err
		}
		type tpl struct {
			id, branch, category, by            uuid.UUID
			amount                              decimal.Decimal
			desc, payee, freq, start, end, next string
		}
		var list []tpl
		for rows.Next() {
			var t tpl
			if err := rows.Scan(&t.id, &t.branch, &t.category, &t.amount, &t.desc, &t.payee, &t.freq, &t.start, &t.end, &t.next, &t.by); err != nil {
				rows.Close()
				return err
			}
			list = append(list, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, t := range list {
			branch := t.branch
			p := &auth.Principal{BusinessID: businessID, UserID: t.by, BranchID: &branch, RequestID: "job:recurring_expenses"}
			start, _ := time.Parse("2006-01-02", t.start)
			due, _ := time.Parse("2006-01-02", t.next)
			active := true
			for due.Format("2006-01-02") <= date {
				day := due.Format("2006-01-02")
				if t.end != "" && day > t.end {
					active = false
					break
				}
				if err := s.generate(ctx, tx, p, t.id, t.category, t.amount, t.desc, t.payee, day); err != nil {
					return err
				}
				due = advance(due, t.freq, start.Day())
			}
			if t.end != "" && due.Format("2006-01-02") > t.end {
				active = false
			}
			if _, err := tx.Exec(ctx, `UPDATE recurring_expenses SET next_run=$2, is_active=$3 WHERE id=$1`, t.id, due.Format("2006-01-02"), active); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) generate(ctx context.Context, tx pgx.Tx, p *auth.Principal, tplID, category uuid.UUID, amount decimal.Decimal, desc, payee, day string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM expenses WHERE recurring_id=$1 AND expense_date=$2)`, tplID, day).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	number, err := sequence.Next(ctx, tx, *p.BranchID, "expense")
	if err != nil {
		return err
	}
	id := ids.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO expenses (id, business_id, branch_id, number, category_id, expense_date, amount, description, payee, status, recurring_id, submitted_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),'submitted',$10,$11)
	`, id, p.BusinessID, *p.BranchID, number, category, day, amount, desc, payee, tplID, p.UserID); err != nil {
		return err
	}
	if _, err := s.Approval.Request(ctx, tx, p, approval.Input{
		BranchID: p.BranchID, Kind: "expense", SubjectType: "expense", SubjectID: id,
		Amount: decimal.NullDecimal{Decimal: amount, Valid: true}, Summary: "Recurring expense " + number + " " + desc,
	}); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "expense.created", "expense", id, nil, map[string]any{"number": number, "amount": amount, "recurring_id": tplID, "status": "submitted"})
}
