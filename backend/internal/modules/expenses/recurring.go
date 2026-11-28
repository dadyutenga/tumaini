package expenses

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type RecurringIn struct {
	BranchID       *uuid.UUID      `json:"branch_id"`
	CategoryID     uuid.UUID       `json:"category_id" validate:"required"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Description    string          `json:"description" validate:"required,max=200"`
	MoneyAccountID *uuid.UUID      `json:"money_account_id"`
	DayOfMonth     int             `json:"day_of_month" validate:"gte=0,lte=28"`
	NextRunOn      string          `json:"next_run_on" validate:"date"`
	StartDate      string          `json:"start_date" validate:"date"` // legacy alias for next_run_on
	BusinessUnit   string          `json:"business_unit" validate:"omitempty,oneof=A B"`
}

func nextMonth(d time.Time, dayOfMonth int) time.Time {
	y, m := d.Year(), d.Month()+1
	if m > 12 {
		y, m = y+1, 1
	}
	if dayOfMonth < 1 {
		dayOfMonth = 1
	}
	if dayOfMonth > 28 {
		dayOfMonth = 28
	}
	return time.Date(y, m, dayOfMonth, 0, 0, 0, 0, time.UTC)
}

const recurringCols = `r.id, r.location_id, r.category_id, ec.name::text, r.amount, r.description::text, r.account_id,
	r.day_of_month, r.next_run_on::text, r.active, r.created_by, r.created_at,
	(SELECT bu.code FROM app.business_units bu WHERE bu.id = r.business_unit_id)`

func scanRecurring(row pgx.Row) (map[string]any, error) {
	var id, branch, category uuid.UUID
	var account, by *uuid.UUID
	var catName, desc, next string
	var amount decimal.Decimal
	var dom int
	var active bool
	var created time.Time
	var unit string
	if err := row.Scan(&id, &branch, &category, &catName, &amount, &desc, &account, &dom, &next, &active, &by, &created, &unit); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "branch_id": branch, "category_id": category, "category_name": catName, "amount": amount,
		"description": desc, "money_account_id": account, "day_of_month": dom, "frequency": "monthly",
		"next_run": next, "next_run_on": next, "is_active": active, "created_by": by, "created_at": created, "business_unit": unit}, nil
}

func (s *Service) Recurring(ctx context.Context, p *auth.Principal, page httpx.Page, active *bool) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(p.BranchID != nil, "r.location_id = ?", p.BranchID)
		q.If(active != nil, "r.active = ?", active)
		from := ` FROM app.recurring_expenses r JOIN app.expense_categories ec ON ec.id = r.category_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+recurringCols+from+q.Where()+` ORDER BY r.next_run_on, r.created_at`+q.Page(page.PerPage, page.Offset), q.Args...)
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

func (s *Service) getRecurring(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock bool) (map[string]any, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF r"
	}
	m, err := scanRecurring(tx.QueryRow(ctx, `
		SELECT `+recurringCols+` FROM app.recurring_expenses r
		JOIN app.expense_categories ec ON ec.id = r.category_id
		WHERE r.id = $1`+suffix, id))
	if err != nil {
		return nil, httpx.NotFound("not_found", "Recurring expense not found")
	}
	return m, nil
}

func (s *Service) checkRecurring(ctx context.Context, tx pgx.Tx, p *auth.Principal, in *RecurringIn) (uuid.UUID, int, string, error) {
	in.Description = strings.TrimSpace(in.Description)
	if err := httpx.Check(in); err != nil {
		return uuid.Nil, 0, "", err
	}
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	if branch == nil {
		return uuid.Nil, 0, "", httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := s.categoryOK(ctx, tx, in.CategoryID); err != nil {
		return uuid.Nil, 0, "", err
	}
	next := strings.TrimSpace(in.NextRunOn)
	if next == "" {
		next = strings.TrimSpace(in.StartDate)
	}
	if next == "" {
		var err error
		if next, err = s.bizDate(ctx, tx); err != nil {
			return uuid.Nil, 0, "", err
		}
	}
	dom := in.DayOfMonth
	if dom == 0 {
		if d, err := time.Parse("2006-01-02", next); err == nil {
			dom = d.Day()
			if dom > 28 {
				dom = 28
			}
		} else {
			dom = 1
		}
	}
	return *branch, dom, next, nil
}

func (s *Service) CreateRecurring(ctx context.Context, p *auth.Principal, in RecurringIn) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		branch, dom, next, err := s.checkRecurring(ctx, tx, p, &in)
		if err != nil {
			return err
		}
		unit, err := businessFor(ctx, tx, p, in.BusinessUnit, branch)
		if err != nil {
			return err
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.recurring_expenses ( category_id, description, amount, business_unit_id, location_id, account_id, day_of_month, next_run_on)\n\t\t\tVALUES ($1,$2,$3,$8,$4,$5,$6,$7::date)\n\t\t\tRETURNING id", in.CategoryID, in.Description, money.Money(in.Amount), branch, in.MoneyAccountID, dom, next, unit).Scan(&id); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, id, false)
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
		before, err := s.getRecurring(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if in.BranchID == nil {
			b := before["branch_id"].(uuid.UUID)
			in.BranchID = &b
		}
		branch, dom, next, err := s.checkRecurring(ctx, tx, p, &in)
		if err != nil {
			return err
		}
		chosen, err := chosenBusiness(p, in.BusinessUnit)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.recurring_expenses\n\t\t\tSET business_unit_id = coalesce($9, business_unit_id),\n\t\t\t    location_id = $2, category_id = $3, amount = $4, description = $5, account_id = $6, day_of_month = $7, next_run_on = $8::date\n\t\t\tWHERE id = $1", id, branch, in.CategoryID, money.Money(in.Amount), in.Description, in.MoneyAccountID, dom, next, chosen); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, id, false)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "recurring_expense.updated", "recurring_expense", id, before, out)
	})
	return out, err
}

func (s *Service) SetRecurringActive(ctx context.Context, p *auth.Principal, id uuid.UUID, active bool) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.getRecurring(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if before["is_active"] == active {
			return httpx.Conflict("invalid_status", map[bool]string{true: "This template is already active", false: "This template is already paused"}[active])
		}
		next := before["next_run_on"].(string)
		if active {
			today, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			dom := before["day_of_month"].(int)
			d, _ := time.Parse("2006-01-02", next)
			for d.Format("2006-01-02") < today {
				d = nextMonth(d, dom)
			}
			next = d.Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.recurring_expenses SET active = $2, next_run_on = $3::date WHERE id = $1", id, active, next); err != nil {
			return err
		}
		out, err = s.getRecurring(ctx, tx, id, false)
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

// RunRecurring generates submitted expenses for every active template due on or before the run date
// and advances next_run_on by one month.
func (s *Service) RunRecurring(ctx context.Context, payload json.RawMessage) error {
	var in struct {
		Date string `json:"date"`
	}
	_ = json.Unmarshal(payload, &in)
	return s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		date := in.Date
		if _, err := time.Parse("2006-01-02", date); err != nil {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		var fallbackMember uuid.UUID
		_ = tx.QueryRow(ctx, "SELECT id FROM app.memberships WHERE status = 'active' ORDER BY created_at LIMIT 1").Scan(&fallbackMember)
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT id, business_unit_id, location_id, category_id, account_id, amount, description::text, day_of_month, next_run_on::text, created_by\n\t\t\tFROM app.recurring_expenses\n\t\t\tWHERE active AND next_run_on <= $1::date\n\t\t\tORDER BY next_run_on FOR UPDATE SKIP LOCKED", date)
		if err != nil {
			return err
		}
		type tpl struct {
			id, unit, loc, cat uuid.UUID
			account, by        *uuid.UUID
			amount             decimal.Decimal
			desc, next         string
			dom                int
		}
		var list []tpl
		for rows.Next() {
			var t tpl
			if err := rows.Scan(&t.id, &t.unit, &t.loc, &t.cat, &t.account, &t.amount, &t.desc, &t.dom, &t.next, &t.by); err != nil {
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
			reqBy := fallbackMember
			if t.by != nil {
				reqBy = *t.by
			}
			if reqBy == uuid.Nil {
				continue
			}
			due, _ := time.Parse("2006-01-02", t.next)
			for due.Format("2006-01-02") <= date {
				day := due.Format("2006-01-02")
				var exists bool
				if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app.expenses WHERE recurring_expense_id = $1 AND expense_date = $2::date)", t.id, day).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					number, err := sequence.Next(ctx, tx, uuid.Nil, "expense")
					if err != nil {
						return err
					}
					var expID uuid.UUID
					if err := tx.QueryRow(ctx, "\n\t\t\t\t\t\tINSERT INTO app.expenses (expense_no, category_id, description, amount, expense_date, business_unit_id, location_id, account_id, status, requested_by, created_by, recurring_expense_id)\n\t\t\t\t\t\tVALUES ($1, $2, $3, $4, $5::date, $6, $7, $8, 'submitted', $9, $9, $10)\n\t\t\t\t\t\tRETURNING id", number, t.cat, t.desc, t.amount, day, t.unit, t.loc, t.account, reqBy, t.id).Scan(&expID); err != nil {
						return err
					}
					amt := t.amount
					loc := t.loc
					_ = s.Notify.ToApprovers(ctx, tx, &loc, "finance.expense.approve", &amt, uuid.Nil, notify.Message{
						Type: "approval_waiting", Title: "Recurring expense awaiting approval",
						Body: "Expense " + number + ": " + t.desc,
						Link: "/expenses/" + expID.String(), EntityType: "expense", EntityID: &expID,
					})
				}
				due = nextMonth(due, t.dom)
			}
			if _, err := tx.Exec(ctx, "UPDATE app.recurring_expenses SET next_run_on = $2::date WHERE id = $1", t.id, due.Format("2006-01-02")); err != nil {
				return err
			}
		}
		return nil
	})
}
