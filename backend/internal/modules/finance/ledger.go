package finance

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/sqlq"
)

type TxFilter struct {
	AccountID *uuid.UUID
	Type      string
	From, To  string
	Q         string
}

// txnTypes maps the movement names clients used before the schema replacement to app.txn_type.
var txnTypes = map[string]string{
	"payment_in": "customer_receipt", "payment_out": "supplier_payment", "customer_receipt": "customer_receipt",
	"supplier_payment": "supplier_payment", "contractor_payment": "contractor_payment", "refund": "refund",
	"other_payment": "other_payment", "expense": "expense", "salary": "salary", "payroll": "salary", "transfer": "transfer",
	"money_transfer": "transfer", "opening_balance": "opening_balance", "correction": "correction", "reversal": "reversal",
}

// Transactions is the unified money movement history. The running balance is per account over all its
// rows, then the filters apply.
func (s *Service) Transactions(ctx context.Context, p *auth.Principal, f TxFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	typ := txnTypes[f.Type]
	if f.Type != "" && typ == "" {
		return out, 0, nil
	}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(f.AccountID != nil, "t.account_id = ?", f.AccountID)
		q.If(typ != "", "t.txn_type = ?::app.txn_type", typ)
		q.If(f.From != "", "t.business_date >= ?::date", f.From)
		q.If(f.To != "", "t.business_date <= ?::date", f.To)
		q.If(f.Q != "", "t.description ILIKE ?", "%"+f.Q+"%")
		base := ` FROM (
			SELECT t.*, sum(CASE t.direction WHEN 'in' THEN t.amount ELSE -t.amount END)
			         OVER (PARTITION BY t.account_id ORDER BY t.business_date, t.occurred_at, t.id) AS running
			FROM app.financial_transactions t WHERE true) t
			JOIN app.financial_accounts fa ON fa.id = t.account_id
			LEFT JOIN app.memberships m ON m.id = t.created_by LEFT JOIN iam.users u ON u.id = m.user_id
			LEFT JOIN app.payments_received pr ON pr.id = t.payment_received_id
			LEFT JOIN app.payments_made pm ON pm.id = t.payment_made_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+base+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.business_date::text, t.location_id, t.account_id, fa.name::text, fa.kind::text,
			       CASE t.direction WHEN 'in' THEN t.amount ELSE 0 END, CASE t.direction WHEN 'out' THEN t.amount ELSE 0 END, t.running,
			       t.description, t.txn_type::text, coalesce(t.payment_received_id, t.payment_made_id, t.expense_id, t.account_transfer_id, t.payroll_line_id, t.id),
			       coalesce(pr.receipt_no, pm.payment_no, ''), coalesce(pr.reference, pm.reference, ''), coalesce(u.full_name::text, ''), t.created_at`+base+q.Where()+
			` ORDER BY t.business_date DESC, t.occurred_at DESC, t.id DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		showBalance := canSeeBalance(p)
		for rows.Next() {
			var id, branch, account, source uuid.UUID
			var date, name, kind, desc, typ, number, ref, user string
			var d, c, run decimal.Decimal
			var created time.Time
			if err := rows.Scan(&id, &date, &branch, &account, &name, &kind, &d, &c, &run, &desc, &typ, &source, &number, &ref, &user, &created); err != nil {
				return err
			}
			m := map[string]any{"id": id, "entry_id": id, "date": date, "branch_id": branch, "account_id": account, "account": name,
				"account_type": kind, "in": d, "out": c, "description": desc, "type": typ, "source_id": source,
				"number": number, "reference": ref, "user": user, "created_at": created, "running_balance": nil}
			if showBalance {
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

// The approved schema keeps a money ledger per account (Transactions, Statement) and does not keep a
// double-entry general ledger, so these answer 410.

func (s *Service) Entries(ctx context.Context, p *auth.Principal, f EntryFilter, page httpx.Page) ([]map[string]any, int, error) {
	return nil, 0, removed("The journal")
}

func (s *Service) TrialBalance(ctx context.Context, p *auth.Principal, to string, all bool) (map[string]any, error) {
	return nil, removed("The trial balance")
}

func (s *Service) PeriodLock(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	return nil, removed("The period lock (days are closed one at a time with the day-end closing)")
}

func (s *Service) SetPeriodLock(ctx context.Context, p *auth.Principal, until *string) (map[string]any, error) {
	return nil, removed("The period lock (days are closed one at a time with the day-end closing)")
}
