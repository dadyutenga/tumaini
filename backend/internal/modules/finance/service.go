package finance

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/chart"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
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
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Approval: ap, Clock: clk}
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// postingBranch is the branch a business-wide document is booked to: the
// account's branch, else the active branch, else the main branch.
func postingBranch(ctx context.Context, tx pgx.Tx, p *auth.Principal, acc *payments.Account) (uuid.UUID, error) {
	if acc != nil && acc.BranchID != nil {
		return *acc.BranchID, nil
	}
	if p.BranchID != nil {
		return *p.BranchID, nil
	}
	var main uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM branches WHERE is_main LIMIT 1`).Scan(&main)
	return main, err
}

// accountInScope reports whether a money account is visible for perm. Accounts
// without a branch belong to the whole business and are visible to all.
func accountInScope(p *auth.Principal, perm string, branch *uuid.UUID) bool {
	if branch == nil {
		_, ok := p.Can(perm)
		return ok || p.IsOwner
	}
	return p.SeesBranch(perm, *branch)
}

// canSeeBalance applies FR-FIN-18: cash balances are visible to anyone who can
// see the account; bank and mobile money balances need accounts.view_balance.
func canSeeBalance(p *auth.Principal, typ string) bool {
	if typ == "cash" || typ == "petty_cash" || p.IsOwner {
		return true
	}
	_, ok := p.Can("accounts.view_balance")
	return ok
}

type Account struct {
	ID             uuid.UUID        `json:"id"`
	Name           string           `json:"name"`
	Type           string           `json:"type"`
	BranchID       *uuid.UUID       `json:"branch_id"`
	Provider       *string          `json:"provider"`
	AccountNumber  *string          `json:"account_number"`
	CustodianID    *uuid.UUID       `json:"custodian_id"`
	LedgerCode     string           `json:"ledger_code"`
	AllowOverdraft bool             `json:"allow_overdraft"`
	IsActive       bool             `json:"is_active"`
	Balance        *decimal.Decimal `json:"balance,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

const accountCols = `ma.id, ma.name, ma.type, ma.branch_id, ma.provider, ma.account_number, ma.custodian_id, la.code, ma.allow_overdraft, ma.is_active, ma.created_at,
	COALESCE((SELECT SUM(debit - credit) FROM journal_lines jl WHERE jl.money_account_id = ma.id), 0)`

const accountFrom = ` FROM money_accounts ma JOIN ledger_accounts la ON la.id = ma.ledger_account_id`

func scanAccount(row pgx.Row, p *auth.Principal) (Account, error) {
	var a Account
	var bal decimal.Decimal
	if err := row.Scan(&a.ID, &a.Name, &a.Type, &a.BranchID, &a.Provider, &a.AccountNumber, &a.CustodianID, &a.LedgerCode, &a.AllowOverdraft, &a.IsActive, &a.CreatedAt, &bal); err != nil {
		return a, err
	}
	if canSeeBalance(p, a.Type) {
		a.Balance = &bal
	}
	return a, nil
}

func (s *Service) List(ctx context.Context, p *auth.Principal, typ string, active *bool) ([]Account, error) {
	out := []Account{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(typ != "", "ma.type = ?", typ)
		q.If(active != nil, "ma.is_active = ?", active)
		if b := p.ScopeBranches("accounts.view"); b != nil {
			q.Add("(ma.branch_id IS NULL OR ma.branch_id = ANY(?))", b)
		}
		rows, err := tx.Query(ctx, `SELECT `+accountCols+accountFrom+q.Where()+` ORDER BY ma.name`, q.Args...)
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

func (s *Service) account(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, perm string) (Account, error) {
	a, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountCols+accountFrom+` WHERE ma.id=$1`, id), p)
	if err != nil || !accountInScope(p, perm, a.BranchID) {
		return Account{}, httpx.NotFound("not_found", "Money account not found")
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.account(ctx, tx, p, id, "accounts.view")
		if err != nil {
			return err
		}
		var custodian, branch *string
		_ = tx.QueryRow(ctx, `SELECT (SELECT name FROM users WHERE id=$1), (SELECT name FROM branches WHERE id=$2)`, a.CustodianID, a.BranchID).Scan(&custodian, &branch)
		var last *string
		_ = tx.QueryRow(ctx, `SELECT max(as_of_date)::text FROM account_reconciliations WHERE money_account_id=$1`, id).Scan(&last)
		out = map[string]any{"id": a.ID, "name": a.Name, "type": a.Type, "branch_id": a.BranchID, "branch_name": branch,
			"provider": a.Provider, "account_number": a.AccountNumber, "custodian_id": a.CustodianID, "custodian_name": custodian,
			"ledger_code": a.LedgerCode, "allow_overdraft": a.AllowOverdraft, "is_active": a.IsActive, "created_at": a.CreatedAt,
			"last_reconciled": last}
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
	Provider       string          `json:"provider" validate:"max=100"`
	AccountNumber  string          `json:"account_number" validate:"max=100"`
	CustodianID    *uuid.UUID      `json:"custodian_id"`
	Opening        decimal.Decimal `json:"opening_balance" validate:"dgte0"`
	AllowOverdraft bool            `json:"allow_overdraft"`
}

func (s *Service) checkCustodian(ctx context.Context, tx pgx.Tx, id *uuid.UUID) error {
	if id == nil {
		return nil
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM users WHERE id=$1`, *id).Scan(&active); err != nil || !active {
		return &httpx.Error{Status: 422, Code: "validation_failed", Message: "Custodian must be an active user", Fields: map[string]string{"custodian_id": "active_user"}}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in AccountIn) (Account, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(&in); err != nil {
		return Account{}, err
	}
	if in.Type == "petty_cash" {
		in.AllowOverdraft = false // FR-EXP-07
	}
	branch := in.BranchID
	if branch == nil {
		branch = p.BranchID
	}
	if branch != nil && !p.SeesBranch("accounts.manage", *branch) {
		return Account{}, httpx.Forbidden("branch_forbidden", "You are not assigned to that branch")
	}
	var out Account
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.checkCustodian(ctx, tx, in.CustodianID); err != nil {
			return err
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		id, err := chart.CreateMoneyAccount(ctx, tx, s.Ledger, chart.MoneyInput{
			BusinessID: p.BusinessID, BranchID: branch, Name: in.Name, Type: in.Type, CustodianID: in.CustodianID,
			Provider: in.Provider, AccountNumber: in.AccountNumber, Opening: in.Opening, AllowOverdraft: in.AllowOverdraft,
			UserID: p.UserID, EntryDate: date,
		})
		if err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An account with that name already exists")
			}
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "money_account.created", "money_account", id, nil, in); err != nil {
			return err
		}
		out, err = s.account(ctx, tx, p, id, "accounts.manage")
		return err
	})
	return out, err
}

type AccountUpdate struct {
	Name           *string    `json:"name" validate:"omitempty,min=1,max=100"`
	Provider       *string    `json:"provider" validate:"omitempty,max=100"`
	AccountNumber  *string    `json:"account_number" validate:"omitempty,max=100"`
	CustodianID    *uuid.UUID `json:"custodian_id"`
	AllowOverdraft *bool      `json:"allow_overdraft"`
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in AccountUpdate) (Account, error) {
	if err := httpx.Check(&in); err != nil {
		return Account{}, err
	}
	var out Account
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.account(ctx, tx, p, id, "accounts.manage")
		if err != nil {
			return err
		}
		if err := s.checkCustodian(ctx, tx, in.CustodianID); err != nil {
			return err
		}
		if in.AllowOverdraft != nil && *in.AllowOverdraft && before.Type == "petty_cash" {
			return httpx.Conflict("invalid_status", "Petty cash cannot be allowed to go below zero")
		}
		name := before.Name
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE money_accounts SET name=$2,
				provider=CASE WHEN $3::boolean THEN NULLIF($4,'') ELSE provider END,
				account_number=CASE WHEN $5::boolean THEN NULLIF($6,'') ELSE account_number END,
				custodian_id=COALESCE($7, custodian_id),
				allow_overdraft=COALESCE($8, allow_overdraft)
			WHERE id=$1
		`, id, name, in.Provider != nil, deref(in.Provider), in.AccountNumber != nil, deref(in.AccountNumber), in.CustodianID, in.AllowOverdraft); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "An account with that name already exists")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger_accounts la SET name=$2 FROM money_accounts ma WHERE ma.id=$1 AND la.id=ma.ledger_account_id`, id, name); err != nil {
			return err
		}
		out, err = s.account(ctx, tx, p, id, "accounts.manage")
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "money_account.updated", "money_account", id, before, out)
	})
	return out, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// Deactivate turns off an account with a zero balance and no open till.
func (s *Service) Deactivate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Account, error) {
	var out Account
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.account(ctx, tx, p, id, "accounts.manage")
		if err != nil {
			return err
		}
		if !before.IsActive {
			return httpx.Conflict("invalid_status", "This account is already inactive")
		}
		bal, err := s.Ledger.MoneyBalance(ctx, tx, id)
		if err != nil {
			return err
		}
		if !bal.IsZero() {
			return httpx.Conflict("balance_not_zero", "Move the balance out of this account before deactivating it")
		}
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM till_sessions WHERE money_account_id=$1 AND status='open')`, id).Scan(&open); err != nil {
			return err
		}
		if open {
			return httpx.Conflict("till_open", "Close the open till on this account first")
		}
		if _, err := tx.Exec(ctx, `UPDATE money_accounts SET is_active=false WHERE id=$1`, id); err != nil {
			return err
		}
		out, err = s.account(ctx, tx, p, id, "accounts.manage")
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "money_account.deactivated", "money_account", id, map[string]any{"is_active": true}, map[string]any{"is_active": false})
	})
	return out, err
}

// Statement lists movements of one account with opening, running and closing balance (FR-REP-15).
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.account(ctx, tx, p, id, "accounts.view_balance")
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
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(debit-credit),0) FROM journal_lines WHERE money_account_id=$1 AND entry_date < $2::date`, id, from).Scan(&opening); err != nil {
			return err
		}
		q := sqlq.New(id, from)
		q.If(to != "", "l.entry_date <= ?::date", to)
		rows, err := tx.Query(ctx, `
			SELECT e.id, l.entry_date::text, e.description, e.source_type, e.source_id, l.debit, l.credit, e.created_at, COALESCE(u.name,'')
			FROM journal_lines l JOIN journal_entries e ON e.id=l.entry_id LEFT JOIN users u ON u.id=e.created_by
			WHERE l.money_account_id=$1 AND l.entry_date >= $2::date`+q.And()+`
			ORDER BY l.entry_date, e.created_at, l.id LIMIT 10000`, q.Args...)
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
		out = map[string]any{"account": a, "from": from, "to": to, "opening_balance": opening, "total_in": in, "total_out": outSum, "closing_balance": run, "lines": lines}
		return nil
	})
	return out, err
}

type ReconcileIn struct {
	ActualBalance decimal.Decimal `json:"actual_balance"`
	Date          string          `json:"date" validate:"date"`
	Note          string          `json:"note" validate:"max=500"`
	// DryRun shows the difference without posting anything.
	DryRun bool `json:"dry_run"`
}

func (s *Service) Reconcile(ctx context.Context, p *auth.Principal, id uuid.UUID, in ReconcileIn) (int, []byte, error) {
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
		if _, err := s.account(ctx, tx, p, id, "reconcile.manage"); err != nil {
			return err
		}
		acc, err := payments.LoadAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		// The difference gives away the balance, so for bank and mobile money it is only for people
		// who may see those balances (FR-FIN-18).
		if acc.Type != "cash" && acc.Type != "petty_cash" {
			if _, ok := p.Can("accounts.view_balance"); !ok && !p.IsOwner {
				return httpx.Forbidden("permission_denied", "You need permission to view bank and mobile money balances to reconcile this account")
			}
		}
		branch, err := postingBranch(ctx, tx, p, &acc)
		if err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		res, err := payments.Reconcile(ctx, tx, s.Ledger, payments.ReconcileInput{
			BusinessID: p.BusinessID, BranchID: branch, UserID: p.UserID, AccountID: id, Actual: in.ActualBalance, Date: date, Note: in.Note, DryRun: in.DryRun,
		})
		if err != nil {
			return err
		}
		if in.DryRun {
			raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(res))
			status, body = 200, raw
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "money_account.reconciled", "money_account", id, nil, res); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(res))
		status, body = 201, raw
		return err
	})
	return status, body, err
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

func (s *Service) Transfer(ctx context.Context, p *auth.Principal, in TransferIn) (int, []byte, error) {
	if err := httpx.Check(&in); err != nil {
		return 0, nil, err
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
		from, err := payments.LoadAccount(ctx, tx, in.FromAccountID)
		if err != nil {
			return err
		}
		to, err := payments.LoadAccount(ctx, tx, in.ToAccountID)
		if err != nil {
			return err
		}
		if !accountInScope(p, "transfers_money.create", from.BranchID) || !accountInScope(p, "transfers_money.create", to.BranchID) {
			return httpx.NotFound("not_found", "Money account not found")
		}
		branch, err := postingBranch(ctx, tx, p, &from)
		if err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		res, err := payments.Transfer(ctx, tx, s.Ledger, payments.TransferInput{
			BusinessID: p.BusinessID, BranchID: branch, UserID: p.UserID, From: in.FromAccountID, To: in.ToAccountID,
			Amount: in.Amount, Fee: in.Fee, Date: date, Reference: in.Reference, Note: in.Note,
		})
		if err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "money_transfer.posted", "money_transfer", res.ID, nil, map[string]any{"number": res.Number, "amount": res.Amount, "fee": res.Fee, "from_account_id": in.FromAccountID, "to_account_id": in.ToAccountID}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": res.ID, "number": res.Number, "amount": res.Amount, "fee": res.Fee, "date": date}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) Transfers(ctx context.Context, p *auth.Principal, account *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(account != nil, "(t.from_account_id = ? OR t.to_account_id = ?)", account, account)
		q.If(from != "", "t.transfer_date >= ?::date", from)
		q.If(to != "", "t.transfer_date <= ?::date", to)
		if b := p.ScopeBranches("accounts.view"); b != nil {
			q.Add("((fa.branch_id IS NULL OR fa.branch_id = ANY(?)) OR (ta.branch_id IS NULL OR ta.branch_id = ANY(?)))", b, b)
		}
		base := ` FROM money_transfers t JOIN money_accounts fa ON fa.id=t.from_account_id JOIN money_accounts ta ON ta.id=t.to_account_id LEFT JOIN users u ON u.id=t.created_by`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+base+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.number, t.from_account_id, fa.name, t.to_account_id, ta.name, t.amount, t.fee, t.transfer_date::text,
			       COALESCE(t.reference,''), COALESCE(t.note,''), t.created_by, COALESCE(u.name,''), t.created_at`+base+q.Where()+
			` ORDER BY t.transfer_date DESC, t.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, fromID, toID, by uuid.UUID
			var number, fromName, toName, date, ref, note, byName string
			var amount, fee decimal.Decimal
			var created time.Time
			if err := rows.Scan(&id, &number, &fromID, &fromName, &toID, &toName, &amount, &fee, &date, &ref, &note, &by, &byName, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "from_account_id": fromID, "from_account_name": fromName,
				"to_account_id": toID, "to_account_name": toName, "amount": amount, "fee": fee, "date": date, "reference": ref,
				"note": note, "created_by": by, "created_by_name": byName, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}
