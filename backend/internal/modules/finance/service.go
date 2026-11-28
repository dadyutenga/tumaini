package finance

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Approval: ap, Clock: clk}
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// removed answers for features the approved schema does not have (a general ledger, trial balance,
// period lock, other income, owner drawings and statutory payments as separate postings).
func removed(what string) error {
	return httpx.New(410, "feature_removed", what+" is not part of the approved accounting model")
}

// canSeeBalance: balances are for people who may view accounts. Cashiers who only receive payments
// see account names and kinds, never balances.
func canSeeBalance(p *auth.Principal) bool { return p.Has("finance.account.view") }

// Account is a cash, bank, mobile-money or petty-cash account (app.financial_accounts).
type Account struct {
	ID             uuid.UUID        `json:"id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"`
	Currency       string           `json:"currency"`
	BranchID       *uuid.UUID       `json:"branch_id"`
	Provider       *string          `json:"provider"`
	AccountNumber  *string          `json:"account_number"`
	CustodianID    *uuid.UUID       `json:"custodian_id"`
	LedgerCode     string           `json:"ledger_code"`
	AllowOverdraft bool             `json:"allow_overdraft"`
	ShowOnInvoices bool             `json:"show_on_invoices"`
	IsActive       bool             `json:"is_active"`
	Balance        *decimal.Decimal `json:"balance,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

const accountCols = `fa.id, fa.name::text, fa.kind::text, fa.currency::text, fa.location_id, fa.provider, fa.account_number_masked, fa.allow_negative,
	fa.show_on_invoices, fa.status = 'active', fa.created_at, fa.current_balance`

func scanAccount(row pgx.Row, p *auth.Principal) (Account, error) {
	var a Account
	var bal decimal.Decimal
	if err := row.Scan(&a.ID, &a.Name, &a.Type, &a.Currency, &a.BranchID, &a.Provider, &a.AccountNumber, &a.AllowOverdraft, &a.ShowOnInvoices, &a.IsActive, &a.CreatedAt, &bal); err != nil {
		return a, err
	}
	if canSeeBalance(p) {
		a.Balance = &bal
	}
	return a, nil
}

func (s *Service) List(ctx context.Context, p *auth.Principal, typ string, active *bool) ([]Account, error) {
	out := []Account{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(typ != "", "fa.kind::text = ?", typ)
		q.If(active != nil, "(fa.status = 'active') = ?", active)
		rows, err := tx.Query(ctx, `SELECT `+accountCols+` FROM app.financial_accounts fa`+q.Where()+` ORDER BY fa.name`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAccount(rows, p)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) account(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (Account, error) {
	a, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountCols+` FROM app.financial_accounts fa WHERE fa.id = $1`, id), p)
	if err != nil {
		return Account{}, httpx.NotFound("not_found", "Money account not found")
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.account(ctx, tx, p, id)
		if err != nil {
			return err
		}
		var branch *string
		if a.BranchID != nil {
			_ = tx.QueryRow(ctx, "SELECT name::text FROM app.locations WHERE id = $1", *a.BranchID).Scan(&branch)
		}
		var last *string
		if err := tx.QueryRow(ctx, "SELECT max(period_end)::text FROM app.petty_cash_reconciliations WHERE account_id = $1", id).Scan(&last); err != nil {
			return err
		}
		out = map[string]any{"id": a.ID, "name": a.Name, "type": a.Type, "currency": a.Currency, "branch_id": a.BranchID, "branch_name": branch,
			"provider": a.Provider, "account_number": a.AccountNumber, "custodian_id": nil, "custodian_name": nil,
			"ledger_code": "", "allow_overdraft": a.AllowOverdraft, "show_on_invoices": a.ShowOnInvoices, "is_active": a.IsActive,
			"created_at": a.CreatedAt, "last_reconciled": last}
		if a.Balance != nil {
			out["balance"] = a.Balance
		}
		return nil
	})
	return out, err
}

type AccountIn struct {
	Name           string          `json:"name" validate:"required,max=100"`
	Type           string          `json:"type" validate:"required,oneof=cash bank mobile_money petty_cash"`
	BranchID       *uuid.UUID      `json:"branch_id"`
	Provider       string          `json:"provider" validate:"max=60"`
	AccountNumber  string          `json:"account_number" validate:"max=100"`
	CustodianID    *uuid.UUID      `json:"custodian_id"`
	Opening        decimal.Decimal `json:"opening_balance" validate:"dgte0"`
	AllowOverdraft bool            `json:"allow_overdraft"`
	ShowOnInvoices bool            `json:"show_on_invoices"`
}

var digits = regexp.MustCompile(`[^0-9]`)

// maskAccountNumber keeps the last four digits: the full number is never stored.
func maskAccountNumber(n string) string {
	d := digits.ReplaceAllString(n, "")
	if len(d) < 4 {
		return ""
	}
	return "**** " + d[len(d)-4:]
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in AccountIn) (Account, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return Account{}, err
	}
	if in.Type == "petty_cash" {
		in.AllowOverdraft = false // petty cash never goes below zero
	}
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	var out Account
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if branch == nil && (in.Type == "cash" || in.Type == "petty_cash") {
			if err := tx.QueryRow(ctx, "SELECT id FROM app.locations WHERE is_default").Scan(&branch); err != nil {
				return httpx.BadRequest("branch_required", "A cash account belongs to a branch")
			}
		}
		var unit *uuid.UUID
		if branch != nil {
			_ = tx.QueryRow(ctx, "SELECT business_unit_id FROM app.locations WHERE id = $1", *branch).Scan(&unit)
		}
		id := uuid.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.financial_accounts (id, name, kind, currency, provider, account_number_masked, location_id, business_unit_id, allow_negative, show_on_invoices)\n\t\t\tVALUES ($1,$2,$3::app.account_kind,COALESCE((SELECT currency FROM app.business_settings WHERE true), 'TZS'),NULLIF($4,''),NULLIF($5,''),$6,$7,$8,$9)",
			id, in.Name, in.Type, in.Provider, maskAccountNumber(in.AccountNumber), branch, unit, in.AllowOverdraft, in.ShowOnInvoices); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An account with that name already exists")
			}
			return err
		}
		if in.Opening.IsPositive() {
			date, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			if branch == nil || unit == nil {
				if err := tx.QueryRow(ctx, "SELECT id, business_unit_id FROM app.locations WHERE is_default").Scan(&branch, &unit); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id, location_id, business_date, description)\n\t\t\t\tVALUES ($1, 'in', $2, 'opening_balance', $3, $4, $5::date, 'Opening balance')", id, money.Money(in.Opening), unit, branch, date); err != nil {
				return err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "money_account.created", "financial_accounts", id, nil, in); err != nil {
			return err
		}
		var err error
		out, err = s.account(ctx, tx, p, id)
		return err
	})
	return out, err
}

type AccountUpdate struct {
	Name           *string    `json:"name" validate:"omitempty,min=1,max=100"`
	Provider       *string    `json:"provider" validate:"omitempty,max=60"`
	AccountNumber  *string    `json:"account_number" validate:"omitempty,max=100"`
	CustodianID    *uuid.UUID `json:"custodian_id"`
	AllowOverdraft *bool      `json:"allow_overdraft"`
	ShowOnInvoices *bool      `json:"show_on_invoices"`
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in AccountUpdate) (Account, error) {
	if err := httpx.Check(&in); err != nil {
		return Account{}, err
	}
	var out Account
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.account(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if in.AllowOverdraft != nil && *in.AllowOverdraft && before.Type == "petty_cash" {
			return httpx.Conflict("invalid_status", "Petty cash cannot be allowed to go below zero")
		}
		name := before.Name
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.financial_accounts SET name = $2,\n\t\t\t  provider = CASE WHEN $3::boolean THEN NULLIF($4,'') ELSE provider END,\n\t\t\t  account_number_masked = CASE WHEN $5::boolean THEN NULLIF($6,'') ELSE account_number_masked END,\n\t\t\t  allow_negative = coalesce($7, allow_negative), show_on_invoices = coalesce($8, show_on_invoices)\n\t\t\tWHERE id = $1", id, name, in.Provider != nil, deref(in.Provider), in.AccountNumber != nil, maskAccountNumber(deref(in.AccountNumber)),
			in.AllowOverdraft, in.ShowOnInvoices); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An account with that name already exists")
			}
			return err
		}
		out, err = s.account(ctx, tx, p, id)
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "money_account.updated", "financial_accounts", id, before, out)
	})
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// Deactivate turns off an account with a zero balance.
func (s *Service) Deactivate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Account, error) {
	var out Account
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var active bool
		var bal decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT status = 'active', current_balance FROM app.financial_accounts WHERE id = $1 FOR UPDATE", id).Scan(&active, &bal); err != nil {
			return httpx.NotFound("not_found", "Money account not found")
		}
		if !active {
			return httpx.Conflict("invalid_status", "This account is already inactive")
		}
		if !bal.IsZero() {
			return httpx.Conflict("balance_not_zero", "Move the balance out of this account before deactivating it")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.financial_accounts SET status = 'inactive' WHERE id = $1", id); err != nil {
			return err
		}
		var err error
		if out, err = s.account(ctx, tx, p, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "money_account.deactivated", "financial_accounts", id, map[string]any{"is_active": true}, map[string]any{"is_active": false})
	})
	return out, err
}

// Statement lists the movements of one account with opening, running and closing balance. The
// balance is the running sum of the append-only money ledger (app.financial_transactions) read
// under a single REPEATABLE READ snapshot so opening balance and lines always reconcile.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorReadTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.account(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if from == "" {
			today, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			from = today[:8] + "01"
		}
		var opening decimal.Decimal
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT coalesce(sum(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0) FROM app.financial_transactions\n\t\t\tWHERE account_id = $1 AND business_date < $2::date", id, from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT t.id, t.business_date::text, t.description, t.txn_type::text, coalesce(t.payment_received_id, t.payment_made_id, t.expense_id, t.account_transfer_id, t.payroll_line_id, t.id),\n\t\t\t       CASE t.direction WHEN 'in' THEN t.amount ELSE 0 END, CASE t.direction WHEN 'out' THEN t.amount ELSE 0 END, t.created_at, coalesce(u.full_name::text, '')\n\t\t\tFROM app.financial_transactions t\n\t\t\tLEFT JOIN app.memberships m ON m.id = t.created_by LEFT JOIN iam.users u ON u.id = m.user_id\n\t\t\tWHERE t.account_id = $1 AND t.business_date >= $2::date AND ($3 = '' OR t.business_date <= $3::date)\n\t\t\tORDER BY t.business_date, t.occurred_at, t.id LIMIT 10000", id, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		run := opening
		in, outSum := money.Zero, money.Zero
		lines := []map[string]any{}
		for rows.Next() {
			var entry, source uuid.UUID
			var date, desc, typ, user string
			var d, c decimal.Decimal
			var created time.Time
			if err := rows.Scan(&entry, &date, &desc, &typ, &source, &d, &c, &created, &user); err != nil {
				return err
			}
			run = run.Add(d).Sub(c)
			in, outSum = in.Add(d), outSum.Add(c)
			lines = append(lines, map[string]any{"entry_id": entry, "date": date, "description": desc, "type": typ, "source_id": source,
				"in": d, "out": c, "balance": run, "user": user, "created_at": created})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		cur := setting.String(ctx, tx, "currency.code", "TZS")
		out = map[string]any{"account": a, "currency": cur, "from": from, "to": to, "opening_balance": opening, "total_in": in, "total_out": outSum, "closing_balance": run, "lines": lines}
		return nil
	})
	return out, err
}

type ReconcileIn struct {
	ActualBalance decimal.Decimal `json:"actual_balance"`
	Date          string          `json:"date" validate:"date"`
	Note          string          `json:"note" validate:"max=500"`
	// DryRun shows the difference without recording anything.
	DryRun bool `json:"dry_run"`
}

// Reconcile compares counted money with the system balance. Only petty cash has a reconciliation
// record in the approved schema (app.petty_cash_reconciliations); the daily cash count of every
// account is part of closing the day.
func (s *Service) Reconcile(ctx context.Context, p *auth.Principal, id uuid.UUID, in ReconcileIn) (int, []byte, error) {
	return 0, nil, httpx.New(410, "feature_removed", "Count the cash when you close the day; petty cash is reconciled from the petty cash screen")
}

type TransferIn struct {
	FromAccountID uuid.UUID       `json:"from_account_id" validate:"required"`
	ToAccountID   uuid.UUID       `json:"to_account_id" validate:"required"`
	Amount        decimal.Decimal `json:"amount" validate:"dpos"`
	Fee           decimal.Decimal `json:"fee" validate:"dgte0"`
	Date          string          `json:"date" validate:"date"`
	Reference     string          `json:"reference" validate:"max=100"`
	Note          string          `json:"note" validate:"max=500"`
}

// Transfer moves money between two accounts; the database posts the out and in rows. Transfer fees are
// not part of the approved model: record the fee as an expense.
func (s *Service) Transfer(ctx context.Context, p *auth.Principal, in TransferIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
	}
	if in.FromAccountID == in.ToAccountID {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose two different accounts")
	}
	if in.Fee.IsPositive() {
		return 0, nil, httpx.BadRequest("fee_not_supported", "A transfer carries no fee. Record the fee as an expense")
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
		// Lock both accounts in deterministic UUID order to prevent deadlocks when A->B and B->A run concurrently.
		firstID, secondID := in.FromAccountID, in.ToAccountID
		if secondID.String() < firstID.String() {
			firstID, secondID = secondID, firstID
		}
		if _, err := tx.Exec(ctx, "SELECT id FROM app.financial_accounts WHERE id IN ($1, $2) ORDER BY id FOR UPDATE", firstID, secondID); err != nil {
			return err
		}
		var fromBal decimal.Decimal
		var allowNeg, fromActive bool
		var fromLoc *uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT current_balance, allow_negative, status = 'active', location_id\n\t\t\tFROM app.financial_accounts WHERE id = $1", in.FromAccountID).Scan(&fromBal, &allowNeg, &fromActive, &fromLoc); err != nil {
			return httpx.NotFound("not_found", "Money account not found")
		}
		if !fromActive {
			return httpx.Conflict("inactive_account", "The source account is inactive")
		}
		amt := money.Money(in.Amount)
		if !allowNeg && fromBal.LessThan(amt) {
			return httpx.Conflict("insufficient_funds", "Not enough balance in the source account")
		}
		var toActive bool
		var toLoc *uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT status = 'active', location_id\n\t\t\tFROM app.financial_accounts WHERE id = $1", in.ToAccountID).Scan(&toActive, &toLoc); err != nil {
			return httpx.NotFound("not_found", "Destination money account not found")
		}
		if !toActive {
			return httpx.Conflict("inactive_account", "The destination account is inactive")
		}
		var loc *uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT coalesce($1::uuid, $2::uuid, $3::uuid, (SELECT id FROM app.locations WHERE is_default))", fromLoc, toLoc, p.BranchID).Scan(&loc); err != nil || loc == nil {
			return httpx.NotFound("not_found", "Location not found")
		}
		date := in.Date
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "account_transfer")
		if err != nil {
			return err
		}
		note := strings.TrimSpace(in.Note)
		if in.Reference != "" {
			note = strings.TrimSpace(note + " " + in.Reference)
		}
		id := uuid.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO app.account_transfers (id, transfer_no, from_account_id, to_account_id, amount, location_id, business_date, note)
			VALUES ($1,$2,$3,$4,$5,$6,$7::date,NULLIF($8,''))`,
			id, number, in.FromAccountID, in.ToAccountID, money.Money(in.Amount), *loc, date, note); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "money_transfer.posted", "account_transfers", id, nil, map[string]any{"number": number, "amount": in.Amount, "from_account_id": in.FromAccountID, "to_account_id": in.ToAccountID}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(map[string]any{"id": id, "number": number, "amount": money.Money(in.Amount), "fee": money.Zero, "date": date}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) Transfers(ctx context.Context, p *auth.Principal, account *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(account != nil, "(t.from_account_id = ? OR t.to_account_id = ?)", account, account)
		q.If(from != "", "t.business_date >= ?::date", from)
		q.If(to != "", "t.business_date <= ?::date", to)
		base := ` FROM app.account_transfers t
			JOIN app.financial_accounts fa ON fa.id = t.from_account_id
			JOIN app.financial_accounts ta ON ta.id = t.to_account_id
			LEFT JOIN app.memberships m ON m.id = t.created_by LEFT JOIN iam.users u ON u.id = m.user_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+base+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.transfer_no, t.from_account_id, fa.name::text, t.to_account_id, ta.name::text, t.amount, t.business_date::text,
			       coalesce(t.note,''), m.user_id, coalesce(u.full_name::text,''), t.created_at`+base+q.Where()+
			` ORDER BY t.business_date DESC, t.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, fromID, toID uuid.UUID
			var by *uuid.UUID
			var number, fromName, toName, date, note, byName string
			var amount decimal.Decimal
			var created time.Time
			if err := rows.Scan(&id, &number, &fromID, &fromName, &toID, &toName, &amount, &date, &note, &by, &byName, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "from_account_id": fromID, "from_account_name": fromName,
				"to_account_id": toID, "to_account_name": toName, "amount": amount, "fee": money.Zero, "date": date, "reference": "",
				"note": note, "created_by": by, "created_by_name": byName, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}
