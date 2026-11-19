package finance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type TxFilter struct {
	AccountID *uuid.UUID
	Type      string
	From, To  string
	Q         string
}

// Transactions is the unified money movement history (FR-FIN-14). The running
// balance is per money account over all its lines, then the filters apply.
func (s *Service) Transactions(ctx context.Context, p *auth.Principal, f TxFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		inner := sqlq.New()
		inner.Add("l.money_account_id IS NOT NULL")
		inner.If(f.AccountID != nil, "l.money_account_id = ?", f.AccountID)
		outer := sqlq.New()
		outer.Args = inner.Args // placeholders continue from the inner query
		outer.If(f.Type != "", "t.source_type = ?", f.Type)
		outer.If(f.From != "", "t.entry_date >= ?::date", f.From)
		outer.If(f.To != "", "t.entry_date <= ?::date", f.To)
		outer.If(f.Q != "", "t.description ILIKE ?", "%"+f.Q+"%")
		if b := p.ScopeBranches("transactions.view"); b != nil {
			outer.Add("t.branch_id = ANY(?)", b)
		}
		base := `FROM (
			SELECT l.id, l.entry_id, l.entry_date, l.branch_id, l.money_account_id, l.debit, l.credit, e.description, e.source_type, e.source_id,
			       e.created_by, e.created_at,
			       SUM(l.debit - l.credit) OVER (PARTITION BY l.money_account_id ORDER BY l.entry_date, e.created_at, l.id) AS running
			FROM journal_lines l JOIN journal_entries e ON e.id = l.entry_id` + inner.Where() + `
		) t JOIN money_accounts ma ON ma.id = t.money_account_id LEFT JOIN users u ON u.id = t.created_by
		LEFT JOIN payments pay ON pay.id = t.source_id`
		if err := tx.QueryRow(ctx, `SELECT count(*) `+base+outer.Where(), outer.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.entry_id, t.entry_date::text, t.branch_id, t.money_account_id, ma.name, ma.type, t.debit, t.credit, t.running,
			       t.description, t.source_type, t.source_id, COALESCE(pay.number, ''), COALESCE(pay.reference, ''), COALESCE(u.name, ''), t.created_at `+
			base+outer.Where()+` ORDER BY t.entry_date DESC, t.created_at DESC, t.id DESC`+outer.Page(page.PerPage, page.Offset), outer.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, entry, account, source uuid.UUID
			var branch *uuid.UUID
			var date, name, typ, desc, sourceType, number, ref, user string
			var d, c, run decimal.Decimal
			var created time.Time
			if err := rows.Scan(&id, &entry, &date, &branch, &account, &name, &typ, &d, &c, &run, &desc, &sourceType, &source, &number, &ref, &user, &created); err != nil {
				return err
			}
			m := map[string]any{"id": id, "entry_id": entry, "date": date, "branch_id": branch, "account_id": account, "account": name,
				"account_type": typ, "in": d, "out": c, "description": desc, "type": sourceType, "source_id": source,
				"number": number, "reference": ref, "user": user, "created_at": created, "running_balance": nil}
			if canSeeBalance(p, typ) {
				m["running_balance"] = run
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, total, err
}

type EntryFilter struct {
	From, To, SourceType, AccountCode string
	SourceID                          *uuid.UUID
}

// Entries lists journal entries with their lines.
func (s *Service) Entries(ctx context.Context, p *auth.Principal, f EntryFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(f.From != "", "e.entry_date >= ?::date", f.From)
		q.If(f.To != "", "e.entry_date <= ?::date", f.To)
		q.If(f.SourceType != "", "e.source_type = ?", f.SourceType)
		q.If(f.SourceID != nil, "e.source_id = ?", f.SourceID)
		q.If(f.AccountCode != "", "EXISTS (SELECT 1 FROM journal_lines jl JOIN ledger_accounts la ON la.id=jl.ledger_account_id WHERE jl.entry_id=e.id AND la.code = ?)", f.AccountCode)
		if b := p.ScopeBranches("reports.financial"); b != nil {
			q.Add("EXISTS (SELECT 1 FROM journal_lines jl WHERE jl.entry_id=e.id AND jl.branch_id = ANY(?))", b)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries e`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT e.id, e.entry_date::text, e.source_type, e.source_id, e.description, e.reverses_id, COALESCE(u.name,''), e.created_at
			FROM journal_entries e LEFT JOIN users u ON u.id=e.created_by`+q.Where()+
			` ORDER BY e.entry_date DESC, e.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		idsList := []uuid.UUID{}
		byID := map[uuid.UUID]map[string]any{}
		for rows.Next() {
			var id, source uuid.UUID
			var reverses *uuid.UUID
			var date, typ, desc, user string
			var created time.Time
			if err := rows.Scan(&id, &date, &typ, &source, &desc, &reverses, &user, &created); err != nil {
				rows.Close()
				return err
			}
			m := map[string]any{"id": id, "entry_date": date, "source_type": typ, "source_id": source, "description": desc,
				"reverses_id": reverses, "created_by_name": user, "created_at": created, "lines": []map[string]any{}}
			idsList = append(idsList, id)
			byID[id] = m
			out = append(out, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(idsList) == 0 {
			return nil
		}
		lrows, err := tx.Query(ctx, `
			SELECT l.entry_id, a.code, a.name, l.branch_id, l.money_account_id, l.party_type, l.party_id, l.debit, l.credit
			FROM journal_lines l JOIN ledger_accounts a ON a.id=l.ledger_account_id
			WHERE l.entry_id = ANY($1) ORDER BY l.entry_id, l.debit DESC, a.code`, idsList)
		if err != nil {
			return err
		}
		defer lrows.Close()
		for lrows.Next() {
			var entry uuid.UUID
			var code, name string
			var branch, account, party *uuid.UUID
			var partyType *string
			var d, c decimal.Decimal
			if err := lrows.Scan(&entry, &code, &name, &branch, &account, &partyType, &party, &d, &c); err != nil {
				return err
			}
			m := byID[entry]
			m["lines"] = append(m["lines"].([]map[string]any), map[string]any{"account_code": code, "account_name": name, "branch_id": branch,
				"money_account_id": account, "party_type": partyType, "party_id": party, "debit": d, "credit": c})
		}
		return lrows.Err()
	})
	return out, total, err
}

// TrialBalance sums every ledger account up to an optional date.
// all=true includes accounts with no movement (the chart of accounts view).
func (s *Service) TrialBalance(ctx context.Context, p *auth.Principal, to string, all bool) (map[string]any, error) {
	lines := []map[string]any{}
	debit, credit := money.Zero, money.Zero
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.code, a.name, a.type, a.is_system, a.is_active, COALESCE(a.parent_code,''), COALESCE(SUM(l.debit),0), COALESCE(SUM(l.credit),0)
			FROM ledger_accounts a
			LEFT JOIN journal_lines l ON l.ledger_account_id=a.id AND ($1='' OR l.entry_date <= $1::date)
			GROUP BY a.id ORDER BY a.code
		`, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var code, name, typ, parent string
			var system, active bool
			var d, c decimal.Decimal
			if err := rows.Scan(&id, &code, &name, &typ, &system, &active, &parent, &d, &c); err != nil {
				return err
			}
			if d.IsZero() && c.IsZero() && !all {
				continue
			}
			debit, credit = debit.Add(d), credit.Add(c)
			bal := d.Sub(c)
			if typ == "liability" || typ == "equity" || typ == "income" {
				bal = c.Sub(d)
			}
			lines = append(lines, map[string]any{"id": id, "code": code, "name": name, "type": typ, "is_system": system, "is_active": active,
				"parent_code": nullable(parent), "debit": d, "credit": c, "balance": bal})
		}
		return rows.Err()
	})
	return map[string]any{"as_of": nullable(to), "lines": lines, "debit_total": debit, "credit_total": credit, "balanced": debit.Equal(credit)}, err
}

func (s *Service) PeriodLock(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	out := map[string]any{"locked_until": nil, "locked_by": nil, "locked_by_name": nil, "locked_at": nil}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var until, name string
		var by uuid.UUID
		var at time.Time
		err := tx.QueryRow(ctx, `SELECT pl.locked_until::text, pl.locked_by, COALESCE(u.name,''), pl.locked_at FROM period_locks pl LEFT JOIN users u ON u.id=pl.locked_by`).Scan(&until, &by, &name, &at)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		out = map[string]any{"locked_until": until, "locked_by": by, "locked_by_name": name, "locked_at": at}
		return nil
	})
	return out, err
}

// SetPeriodLock sets the locked-until date (FR-FIN-19). A null date reopens all periods.
func (s *Service) SetPeriodLock(ctx context.Context, p *auth.Principal, until *string) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.Ledger.LockedUntil(ctx, tx)
		if err != nil {
			return err
		}
		if until == nil || *until == "" {
			if _, err := tx.Exec(ctx, `DELETE FROM period_locks`); err != nil {
				return err
			}
			return s.Audit.Record(ctx, tx, p, "period.unlocked", "period_lock", p.BusinessID, map[string]any{"locked_until": before}, map[string]any{"locked_until": nil})
		}
		if _, err := time.Parse("2006-01-02", *until); err != nil {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "locked_until must be a date", Fields: map[string]string{"locked_until": "date"}}
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if *until >= today {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "You can only lock dates before today", Fields: map[string]string{"locked_until": "before_today"}}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO period_locks (business_id, locked_until, locked_by, locked_at) VALUES ($1,$2,$3,now())
			ON CONFLICT (business_id) DO UPDATE SET locked_until=EXCLUDED.locked_until, locked_by=EXCLUDED.locked_by, locked_at=now()
		`, p.BusinessID, *until, p.UserID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "period.locked", "period_lock", p.BusinessID, map[string]any{"locked_until": nullable(before)}, map[string]any{"locked_until": *until})
	})
	if err != nil {
		return nil, err
	}
	return s.PeriodLock(ctx, p)
}
