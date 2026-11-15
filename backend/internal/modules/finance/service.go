package finance

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/chart"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
)

type Service struct {
	DB     *db.DB
	Ledger *ledger.Service
	Audit  *audit.Service
	Clock  clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Clock: clk}
}

type Account struct {
	ID             uuid.UUID        `json:"id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"`
	BranchID       *uuid.UUID       `json:"branch_id"`
	Provider       *string          `json:"provider"`
	AccountNumber  *string          `json:"account_number"`
	AllowOverdraft bool             `json:"allow_overdraft"`
	IsActive       bool             `json:"is_active"`
	Balance        *decimal.Decimal `json:"balance,omitempty"`
}

func (s *Service) List(ctx context.Context, p *auth.Principal, typ string, withBalance bool) ([]Account, error) {
	var out []Account
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ma.id, ma.name, ma.type, ma.branch_id, ma.provider, ma.account_number, ma.allow_overdraft, ma.is_active,
			       COALESCE((SELECT SUM(debit - credit) FROM journal_lines jl WHERE jl.money_account_id = ma.id), 0)
			FROM money_accounts ma WHERE ($1='' OR ma.type=$1) ORDER BY ma.name
		`, typ)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a Account
			var bal decimal.Decimal
			if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.BranchID, &a.Provider, &a.AccountNumber, &a.AllowOverdraft, &a.IsActive, &bal); err != nil {
				return err
			}
			if withBalance || a.Type == "cash" || a.Type == "petty_cash" {
				a.Balance = &bal
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if out == nil {
		out = []Account{}
	}
	return out, err
}

type AccountIn struct {
	Name           string          `json:"name"`
	Type           string          `json:"type"`
	BranchID       *uuid.UUID      `json:"branch_id"`
	Provider       string          `json:"provider"`
	AccountNumber  string          `json:"account_number"`
	Opening        decimal.Decimal `json:"opening_balance"`
	AllowOverdraft bool            `json:"allow_overdraft"`
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in AccountIn) (Account, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Account{}, httpx.BadRequest("invalid_body", "Account name is required")
	}
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	var id uuid.UUID
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = chart.CreateMoneyAccount(ctx, tx, s.Ledger, chart.MoneyInput{
			BusinessID: p.BusinessID, BranchID: branch, Name: strings.TrimSpace(in.Name), Type: in.Type,
			Provider: in.Provider, AccountNumber: in.AccountNumber, Opening: in.Opening, AllowOverdraft: in.AllowOverdraft,
			UserID: p.UserID, EntryDate: s.Clock.Now().Format("2006-01-02"),
		})
		if err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An account with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "money_account.created", "money_account", id, nil, in)
	})
	if err != nil {
		return Account{}, err
	}
	list, err := s.List(ctx, p, "", true)
	if err != nil {
		return Account{}, err
	}
	for _, a := range list {
		if a.ID == id {
			return a, nil
		}
	}
	return Account{ID: id, Name: in.Name, Type: in.Type}, nil
}

type TransferIn struct {
	FromAccountID uuid.UUID       `json:"from_account_id"`
	ToAccountID   uuid.UUID       `json:"to_account_id"`
	Amount        decimal.Decimal `json:"amount"`
	Fee           decimal.Decimal `json:"fee"`
	Date          string          `json:"date"`
	Reference     string          `json:"reference"`
	Note          string          `json:"note"`
}

func (s *Service) Transfer(ctx context.Context, p *auth.Principal, in TransferIn) (int, []byte, error) {
	if in.Amount.LessThanOrEqual(money.Zero) {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
	}
	if in.FromAccountID == in.ToAccountID {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose two different accounts")
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
		fee := money.Money(in.Fee)
		if err := s.Ledger.EnsureFunds(ctx, tx, in.FromAccountID, amount.Add(fee)); err != nil {
			return err
		}
		branch := p.BranchID
		if branch == nil {
			var b uuid.UUID
			_ = tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&b)
			branch = &b
		}
		number, err := sequence.Next(ctx, tx, *branch, "money_transfer")
		if err != nil {
			return err
		}
		id := ids.New()
		date := in.Date
		if date == "" {
			date = s.Clock.Now().Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO money_transfers (id, business_id, number, from_account_id, to_account_id, amount, fee, transfer_date, reference, note, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, id, p.BusinessID, number, in.FromAccountID, in.ToAccountID, amount, fee, date, in.Reference, in.Note, p.UserID); err != nil {
			return err
		}
		fromCode, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.FromAccountID)
		if err != nil {
			return err
		}
		toCode, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.ToAccountID)
		if err != nil {
			return err
		}
		user := p.UserID
		lines := []ledger.Line{
			{Code: toCode, BranchID: branch, MoneyAccountID: &in.ToAccountID, Debit: amount},
			{Code: fromCode, BranchID: branch, MoneyAccountID: &in.FromAccountID, Credit: amount.Add(fee)},
		}
		if fee.IsPositive() {
			lines = append(lines, ledger.Line{Code: "6400", BranchID: branch, Debit: fee})
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "money_transfer", id, "Transfer "+number, &user, lines); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "money_transfer.posted", "money_transfer", id, nil, map[string]any{"number": number, "amount": amount}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": id, "number": number, "amount": amount, "fee": fee}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) TrialBalance(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	var lines []map[string]any
	debit, credit := money.Zero, money.Zero
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT a.code, a.name, a.type, COALESCE(SUM(l.debit),0), COALESCE(SUM(l.credit),0)
			FROM ledger_accounts a
			LEFT JOIN journal_lines l ON l.ledger_account_id=a.id
			GROUP BY a.id ORDER BY a.code
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code, name, typ string
			var d, c decimal.Decimal
			if err := rows.Scan(&code, &name, &typ, &d, &c); err != nil {
				return err
			}
			if d.IsZero() && c.IsZero() {
				continue
			}
			debit = debit.Add(d)
			credit = credit.Add(c)
			lines = append(lines, map[string]any{"code": code, "name": name, "type": typ, "debit": d, "credit": c})
		}
		return rows.Err()
	})
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"lines": lines, "debit_total": debit, "credit_total": credit, "balanced": debit.Equal(credit)}, err
}

func (s *Service) Transactions(ctx context.Context, p *auth.Principal, account *uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT l.entry_date::text, a.name, l.debit, l.credit, e.description, e.source_type
			FROM journal_lines l
			JOIN journal_entries e ON e.id=l.entry_id
			JOIN ledger_accounts a ON a.id=l.ledger_account_id
			WHERE l.money_account_id IS NOT NULL AND ($1::uuid IS NULL OR l.money_account_id=$1)
			ORDER BY l.entry_date, e.created_at
		`, account)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var date, name, desc, source string
			var d, c decimal.Decimal
			if err := rows.Scan(&date, &name, &d, &c, &desc, &source); err != nil {
				return err
			}
			out = append(out, map[string]any{"date": date, "account": name, "in": d, "out": c, "description": desc, "type": source})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}
