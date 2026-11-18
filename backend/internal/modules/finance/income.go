package finance

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

func (s *Service) IncomeCategories(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ic.id, ic.name, ic.is_active, la.code, la.name
			FROM income_categories ic JOIN ledger_accounts la ON la.id=ic.ledger_account_id ORDER BY ic.name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name, code, lname string
			var active bool
			if err := rows.Scan(&id, &name, &active, &code, &lname); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "is_active": active, "ledger_code": code, "ledger_name": lname})
		}
		return rows.Err()
	})
	return out, err
}

type IncomeCategoryIn struct {
	Name       string `json:"name" validate:"required,max=100"`
	LedgerCode string `json:"ledger_code" validate:"omitempty,max=10"`
}

func (s *Service) CreateIncomeCategory(ctx context.Context, p *auth.Principal, in IncomeCategoryIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return nil, err
	}
	if in.LedgerCode == "" {
		in.LedgerCode = "4100"
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var ledgerID uuid.UUID
		var typ string
		if err := tx.QueryRow(ctx, `SELECT id, type FROM ledger_accounts WHERE code=$1 AND is_active`, in.LedgerCode).Scan(&ledgerID, &typ); err != nil {
			return httpx.NotFound("not_found", "Ledger account not found")
		}
		// Sales revenue and returns are posted only by sales.
		if typ != "income" || in.LedgerCode == "4000" || in.LedgerCode == "4010" {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "Choose an other-income ledger account", Fields: map[string]string{"ledger_code": "income_account"}}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO income_categories (id, business_id, name, ledger_account_id) VALUES ($1,$2,$3,$4)`, id, p.BusinessID, in.Name, ledgerID); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A category with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "income_category.created", "income_category", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": in.Name, "is_active": true, "ledger_code": in.LedgerCode}, nil
}

type IncomeIn struct {
	Kind           string          `json:"kind" validate:"omitempty,oneof=income capital"`
	CategoryID     *uuid.UUID      `json:"category_id"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Date           string          `json:"date" validate:"date"`
	Description    string          `json:"description" validate:"required,max=500"`
	Reference      string          `json:"reference" validate:"max=100"`
}

// moneyIn prepares a posting into or out of an account the caller may use.
func (s *Service) moneyAccountFor(ctx context.Context, tx pgx.Tx, p *auth.Principal, perm string, id uuid.UUID) (payments.Account, uuid.UUID, error) {
	acc, err := payments.ActiveAccount(ctx, tx, id)
	if err != nil {
		return acc, uuid.Nil, err
	}
	if !accountInScope(p, perm, acc.BranchID) {
		return acc, uuid.Nil, httpx.NotFound("not_found", "Money account not found")
	}
	branch, err := postingBranch(ctx, tx, p, &acc)
	return acc, branch, err
}

// RecordIncome records other income or owner capital (FR-FIN-11).
func (s *Service) RecordIncome(ctx context.Context, p *auth.Principal, in IncomeIn) (int, []byte, error) {
	in.Description = strings.TrimSpace(in.Description)
	if in.Kind == "" {
		in.Kind = "income"
	}
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	if in.Kind == "income" && in.CategoryID == nil {
		return 0, nil, &httpx.Error{Status: 422, Code: "validation_failed", Message: "Choose an income category", Fields: map[string]string{"category_id": "required"}}
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
		acc, branch, err := s.moneyAccountFor(ctx, tx, p, "income.create", in.MoneyAccountID)
		if err != nil {
			return err
		}
		code := "3000"
		if in.Kind == "income" {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT la.code, ic.is_active FROM income_categories ic JOIN ledger_accounts la ON la.id=ic.ledger_account_id WHERE ic.id=$1`, *in.CategoryID).Scan(&code, &active); err != nil || !active {
				return httpx.NotFound("not_found", "Income category not found")
			}
		} else {
			in.CategoryID = nil
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		amount := money.Money(in.Amount)
		number, err := sequence.Next(ctx, tx, branch, "income")
		if err != nil {
			return err
		}
		pay, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: acc.ID, UserID: p.UserID, Direction: "in", Kind: in.Kind,
			Method: payments.MethodFor(acc.Type), Amount: amount, Reference: in.Reference, Date: date, Note: in.Description,
		})
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO other_income (id, business_id, branch_id, number, category_id, kind, amount, income_date, money_account_id, description, payment_id, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		`, id, p.BusinessID, branch, number, in.CategoryID, in.Kind, amount, date, acc.ID, in.Description, pay.ID, p.UserID); err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, in.Kind, pay.ID, map[string]string{"income": "Income", "capital": "Capital"}[in.Kind]+" "+number+" "+in.Description, &user, []ledger.Line{
			{Code: acc.Code, BranchID: &branch, MoneyAccountID: &acc.ID, Debit: amount},
			{Code: code, BranchID: &branch, Credit: amount},
		}); err != nil {
			return err
		}
		out := map[string]any{"id": id, "number": number, "kind": in.Kind, "amount": amount, "date": date, "payment_id": pay.ID, "payment_number": pay.Number}
		if err := s.Audit.Record(ctx, tx, p, "income.recorded", "other_income", id, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) Income(ctx context.Context, p *auth.Principal, kind string, category *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("payments.view"); b != nil {
			q.Add("i.branch_id = ANY(?)", b)
		}
		q.If(kind != "", "i.kind = ?", kind)
		q.If(category != nil, "i.category_id = ?", category)
		q.If(from != "", "i.income_date >= ?::date", from)
		q.If(to != "", "i.income_date <= ?::date", to)
		base := ` FROM other_income i JOIN money_accounts ma ON ma.id=i.money_account_id LEFT JOIN income_categories ic ON ic.id=i.category_id
			LEFT JOIN payments p ON p.id=i.payment_id LEFT JOIN users u ON u.id=i.created_by`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+base+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT i.id, i.number, i.branch_id, i.kind, i.category_id, COALESCE(ic.name,''), i.amount, i.income_date::text, i.money_account_id, ma.name,
			       i.description, i.payment_id, COALESCE(p.number,''), COALESCE(p.status,''), COALESCE(u.name,''), i.created_at`+base+q.Where()+
			` ORDER BY i.income_date DESC, i.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, account uuid.UUID
			var cat, payment *uuid.UUID
			var number, k, catName, date, accName, desc, payNumber, payStatus, by string
			var amount decimal.Decimal
			var created time.Time
			if err := rows.Scan(&id, &number, &branch, &k, &cat, &catName, &amount, &date, &account, &accName, &desc, &payment, &payNumber, &payStatus, &by, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "branch_id": branch, "kind": k, "category_id": cat, "category_name": catName,
				"amount": amount, "date": date, "money_account_id": account, "account_name": accName, "description": desc, "payment_id": payment,
				"payment_number": payNumber, "status": map[bool]string{true: "voided", false: "posted"}[payStatus == "voided"], "created_by_name": by, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}

type OutflowIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Date           string          `json:"date" validate:"date"`
	Reference      string          `json:"reference" validate:"max=100"`
	Note           string          `json:"note" validate:"max=500"`
}

// Drawing records money the owner takes out (FR-FIN-12): Dr 3200 Cr money.
func (s *Service) Drawing(ctx context.Context, p *auth.Principal, in OutflowIn) (int, []byte, error) {
	if !p.IsOwner {
		return 0, nil, httpx.Forbidden("permission_denied", "Only the Owner can record drawings")
	}
	return s.outflow(ctx, p, in, "drawings.create", "drawing", "3200", "Owner drawing")
}

// Statutory remits statutory deductions: Dr 2210 Cr money, never more than is owed.
func (s *Service) Statutory(ctx context.Context, p *auth.Principal, in OutflowIn) (int, []byte, error) {
	return s.outflow(ctx, p, in, "payments.pay", "statutory", "2210", "Statutory remittance")
}

func (s *Service) outflow(ctx context.Context, p *auth.Principal, in OutflowIn, perm, kind, code, label string) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
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
		amount := money.Money(in.Amount)
		if kind == "statutory" {
			g, ok := p.Can("payments.pay")
			if !p.IsOwner && !auth.WithinLimit(g, ok, amount) {
				return httpx.Forbidden("over_limit", "This amount is above your payment limit")
			}
			var owed decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(l.credit-l.debit),0) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.ledger_account_id WHERE a.code='2210'`).Scan(&owed); err != nil {
				return err
			}
			if amount.GreaterThan(owed) {
				return httpx.Conflict("exceeds_balance", "This is more than the statutory deductions owed ("+owed.StringFixed(2)+")")
			}
		}
		acc, branch, err := s.moneyAccountFor(ctx, tx, p, perm, in.MoneyAccountID)
		if err != nil {
			return err
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, acc.ID, amount); err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		pay, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: acc.ID, UserID: p.UserID, Direction: "out", Kind: kind,
			Method: payments.MethodFor(acc.Type), Amount: amount, Reference: in.Reference, Date: date, Note: in.Note,
		})
		if err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, kind, pay.ID, label+" "+pay.Number, &user, []ledger.Line{
			{Code: code, BranchID: &branch, Debit: amount},
			{Code: acc.Code, BranchID: &branch, MoneyAccountID: &acc.ID, Credit: amount},
		}); err != nil {
			return err
		}
		out := map[string]any{"id": pay.ID, "number": pay.Number, "kind": kind, "amount": amount, "date": date, "money_account_id": acc.ID}
		if err := s.Audit.Record(ctx, tx, p, kind+".recorded", "payment", pay.ID, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
