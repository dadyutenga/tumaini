package sales

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

func (s *Service) OpenTill(ctx context.Context, p *auth.Principal, account uuid.UUID, float decimal.Decimal) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if float.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Opening float cannot be negative")
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var typ string
		var active bool
		var branch *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT type, is_active, branch_id FROM money_accounts WHERE id=$1`, account).Scan(&typ, &active, &branch); err != nil || !active {
			return httpx.NotFound("not_found", "Money account not found or inactive")
		}
		if typ != "cash" {
			return httpx.Conflict("invalid_status", "A till must use a cash account")
		}
		if branch != nil && *branch != *p.BranchID {
			return httpx.Conflict("invalid_status", "That cash account belongs to another branch")
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO till_sessions (id, business_id, branch_id, money_account_id, cashier_id, opening_float)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, id, p.BusinessID, *p.BranchID, account, p.UserID, money.Money(float))
		if db.IsUnique(err) {
			return httpx.Conflict("invalid_status", "You already have an open till")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "till.opened", "till_session", id, nil, map[string]any{"money_account_id": account, "opening_float": money.Money(float)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "open", "money_account_id": account, "opening_float": money.Money(float)}, nil
}

func (s *Service) CurrentTill(ctx context.Context, p *auth.Principal) (any, error) {
	var out any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var id, account, branch uuid.UUID
		var float decimal.Decimal
		var opened any
		err := tx.QueryRow(ctx, `SELECT id, money_account_id, opening_float, opened_at, branch_id FROM till_sessions WHERE cashier_id=$1 AND status='open'`, p.UserID).Scan(&id, &account, &float, &opened, &branch)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		expected, err := expectedCash(ctx, tx, id, float)
		if err != nil {
			return err
		}
		out = map[string]any{"id": id, "money_account_id": account, "opening_float": float, "opened_at": opened, "status": "open", "branch_id": branch, "expected_cash": expected}
		return nil
	})
	return out, err
}

// expectedCash is the opening float plus cash taken, less cash paid out of the till.
func expectedCash(ctx context.Context, tx pgx.Tx, till uuid.UUID, opening decimal.Decimal) (decimal.Decimal, error) {
	var movement, payouts decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN direction='in' THEN amount ELSE -amount END),0)
		FROM payments WHERE till_session_id=$1 AND method='cash' AND status='posted'
	`, till).Scan(&movement); err != nil {
		return decimal.Zero, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount + fee),0) FROM money_transfers WHERE till_session_id=$1`, till).Scan(&payouts); err != nil {
		return decimal.Zero, err
	}
	return opening.Add(movement).Sub(payouts), nil
}

func (s *Service) CloseTill(ctx context.Context, p *auth.Principal, id uuid.UUID, counted decimal.Decimal, note string) (map[string]any, error) {
	if counted.IsNegative() {
		return nil, httpx.BadRequest("invalid_body", "Counted cash cannot be negative")
	}
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var opening decimal.Decimal
		var account, branch, cashier uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT opening_float, money_account_id, branch_id, cashier_id FROM till_sessions WHERE id=$1 AND status='open' FOR UPDATE`, id).Scan(&opening, &account, &branch, &cashier); err != nil {
			return httpx.NotFound("not_found", "Open till not found")
		}
		// The cashier closes their own till; a supervisor with tills.view for the branch may close it for them.
		if cashier != p.UserID && !p.SeesBranch("tills.view", branch) {
			return httpx.NotFound("not_found", "Open till not found")
		}
		expected, err := expectedCash(ctx, tx, id, opening)
		if err != nil {
			return err
		}
		diff := money.Money(counted).Sub(expected)
		if !diff.IsZero() {
			code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, account)
			if err != nil {
				return err
			}
			date, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			user := p.UserID
			lines := []ledger.Line{{Code: "6300", BranchID: &branch, Debit: diff.Abs()}, {Code: code, BranchID: &branch, MoneyAccountID: &account, Credit: diff.Abs()}}
			if diff.IsPositive() {
				lines = []ledger.Line{{Code: code, BranchID: &branch, MoneyAccountID: &account, Debit: diff}, {Code: "6300", BranchID: &branch, Credit: diff}}
			}
			if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "till_close", id, "Till difference", &user, lines); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE till_sessions SET status='closed', expected_cash=$2, counted_cash=$3, difference=$4, closed_at=now(), closed_by=$5, note=NULLIF($6,'') WHERE id=$1
		`, id, expected, money.Money(counted), diff, p.UserID, note); err != nil {
			return err
		}
		threshold := decimal.NewFromInt(1000)
		if raw, err := setting.Raw(ctx, tx, "system.till_difference_threshold"); err == nil {
			if d, err := decimal.NewFromString(strings.Trim(string(raw), `"`)); err == nil {
				threshold = d
			}
		}
		if diff.Abs().GreaterThan(threshold) {
			if err := s.Notify.ToPermission(ctx, tx, p.BusinessID, &branch, "tills.view", notify.Message{
				Type: "till_difference", Title: "Till difference", Body: "A till closed " + diff.StringFixed(2) + " against the expected cash",
				Link: "/tills/" + id.String(), EntityType: "till_session", EntityID: &id,
			}); err != nil {
				return err
			}
		}
		result = map[string]any{"id": id, "status": "closed", "expected_cash": expected, "counted_cash": money.Money(counted), "difference": diff}
		return s.Audit.Record(ctx, tx, p, "till.closed", "till_session", id, nil, result)
	})
	return result, err
}

type TillFilter struct {
	Status    string
	CashierID *uuid.UUID
	From, To  string
}

func (s *Service) Tills(ctx context.Context, p *auth.Principal, f TillFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("tills.view"); b != nil {
			q.Add("t.branch_id = ANY(?)", b)
		}
		if own := p.OwnOnly("tills.view"); own != nil {
			q.Add("t.cashier_id = ?", *own)
		}
		q.If(f.Status != "", "t.status = ?", f.Status)
		q.If(f.CashierID != nil, "t.cashier_id = ?", f.CashierID)
		q.If(f.From != "", "t.opened_at >= ?::date", f.From)
		q.If(f.To != "", "t.opened_at < ?::date + 1", f.To)
		from := ` FROM till_sessions t JOIN users u ON u.id=t.cashier_id JOIN money_accounts ma ON ma.id=t.money_account_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT t.id, t.branch_id, t.cashier_id, u.name, t.money_account_id, ma.name, t.status, t.opening_float, t.expected_cash, t.counted_cash, t.difference, t.opened_at, t.closed_at`+
			from+q.Where()+` ORDER BY t.opened_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch, cashier, account uuid.UUID
			var cname, aname, status string
			var opening decimal.Decimal
			var expected, counted, diff decimal.NullDecimal
			var opened, closed any
			if err := rows.Scan(&id, &branch, &cashier, &cname, &account, &aname, &status, &opening, &expected, &counted, &diff, &opened, &closed); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "branch_id": branch, "cashier_id": cashier, "cashier_name": cname, "money_account_id": account,
				"money_account_name": aname, "status": status, "opening_float": opening, "expected_cash": expected, "counted_cash": counted,
				"difference": diff, "opened_at": opened, "closed_at": closed})
		}
		return rows.Err()
	})
	return out, total, err
}

// Till is the session report: sales by method, refunds, customer payments and pay-outs (FR-REP-04).
func (s *Service) Till(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var branch, cashier, account uuid.UUID
		var cname, aname, status string
		var opening decimal.Decimal
		var expected, counted, diff decimal.NullDecimal
		var opened, closed any
		var note *string
		err := tx.QueryRow(ctx, `
			SELECT t.branch_id, t.cashier_id, u.name, t.money_account_id, ma.name, t.status, t.opening_float, t.expected_cash, t.counted_cash, t.difference, t.opened_at, t.closed_at, t.note
			FROM till_sessions t JOIN users u ON u.id=t.cashier_id JOIN money_accounts ma ON ma.id=t.money_account_id WHERE t.id=$1`, id).
			Scan(&branch, &cashier, &cname, &account, &aname, &status, &opening, &expected, &counted, &diff, &opened, &closed, &note)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Till session not found")
		}
		if err != nil {
			return err
		}
		if !p.SeesBranch("tills.view", branch) {
			return httpx.NotFound("not_found", "Till session not found")
		}
		if own := p.OwnOnly("tills.view"); own != nil && cashier != *own {
			return httpx.NotFound("not_found", "Till session not found")
		}
		byMethod := []map[string]any{}
		rows, err := tx.Query(ctx, `
			SELECT method, kind, direction, count(*), COALESCE(SUM(amount),0) FROM payments
			WHERE till_session_id=$1 AND status='posted' GROUP BY method, kind, direction ORDER BY method, kind`, id)
		if err != nil {
			return err
		}
		cashSales, refunds, custPay := decimal.Zero, decimal.Zero, decimal.Zero
		for rows.Next() {
			var method, kind, dir string
			var n int
			var amt decimal.Decimal
			if err := rows.Scan(&method, &kind, &dir, &n, &amt); err != nil {
				rows.Close()
				return err
			}
			byMethod = append(byMethod, map[string]any{"method": method, "kind": kind, "direction": dir, "count": n, "amount": amt})
			if method == "cash" {
				switch {
				case kind == "sale":
					cashSales = cashSales.Add(amt)
				case dir == "out":
					refunds = refunds.Add(amt)
				default:
					custPay = custPay.Add(amt)
				}
			}
		}
		rows.Close()
		var nSales int
		var salesTotal decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(total),0) FROM sales WHERE till_session_id=$1 AND status IN ('completed','partially_refunded','refunded')`, id).Scan(&nSales, &salesTotal); err != nil {
			return err
		}
		payouts := []map[string]any{}
		payoutTotal := decimal.Zero
		rows, err = tx.Query(ctx, `
			SELECT mt.id, mt.number, mt.to_account_id, ma.name, mt.amount, mt.fee, mt.reference, mt.note, mt.created_at
			FROM money_transfers mt JOIN money_accounts ma ON ma.id=mt.to_account_id WHERE mt.till_session_id=$1 ORDER BY mt.created_at`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pid, to uuid.UUID
			var number, toName string
			var amt, fee decimal.Decimal
			var ref, pnote *string
			var at any
			if err := rows.Scan(&pid, &number, &to, &toName, &amt, &fee, &ref, &pnote, &at); err != nil {
				return err
			}
			payoutTotal = payoutTotal.Add(amt).Add(fee)
			payouts = append(payouts, map[string]any{"id": pid, "number": number, "to_account_id": to, "to_account_name": toName, "amount": amt, "fee": fee, "reference": ref, "note": pnote, "created_at": at})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if status == "open" {
			e, err := expectedCash(ctx, tx, id, opening)
			if err != nil {
				return err
			}
			expected = decimal.NullDecimal{Decimal: e, Valid: true}
		}
		out = map[string]any{"id": id, "branch_id": branch, "cashier_id": cashier, "cashier_name": cname, "money_account_id": account,
			"money_account_name": aname, "status": status, "opening_float": opening, "opened_at": opened, "closed_at": closed, "note": note,
			"sales_count": nSales, "sales_total": salesTotal, "cash_sales": cashSales, "cash_refunds": refunds, "cash_customer_payments": custPay,
			"payouts": payouts, "payout_total": payoutTotal, "by_method": byMethod,
			"expected_cash": expected, "counted_cash": counted, "difference": diff}
		return nil
	})
	return out, err
}

type PayOutIn struct {
	ToAccountID uuid.UUID       `json:"to_account_id"`
	Amount      decimal.Decimal `json:"amount"`
	Reference   string          `json:"reference"`
	Note        string          `json:"note"`
}

// PayOut moves cash out of an open till to a safe or bank as a money transfer.
func (s *Service) PayOut(ctx context.Context, p *auth.Principal, tillID uuid.UUID, in PayOutIn) (int, []byte, error) {
	if !in.Amount.IsPositive() {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
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
		var from, branch, cashier uuid.UUID
		var opening decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT money_account_id, branch_id, cashier_id, opening_float FROM till_sessions WHERE id=$1 AND status='open' FOR UPDATE`, tillID).Scan(&from, &branch, &cashier, &opening); err != nil {
			return httpx.NotFound("not_found", "Open till not found")
		}
		if cashier != p.UserID && !p.SeesBranch("tills.view", branch) {
			return httpx.NotFound("not_found", "Open till not found")
		}
		if in.ToAccountID == from {
			return httpx.BadRequest("invalid_body", "Choose an account other than the till")
		}
		if err := checkAccount(ctx, tx, in.ToAccountID); err != nil {
			return err
		}
		amount := money.Money(in.Amount)
		expected, err := expectedCash(ctx, tx, tillID, opening)
		if err != nil {
			return err
		}
		if expected.LessThan(amount) {
			return httpx.Conflict("insufficient_funds", "The till does not hold that much cash")
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, from, amount); err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, branch, "money_transfer")
		if err != nil {
			return err
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO money_transfers (id, business_id, number, from_account_id, to_account_id, amount, fee, transfer_date, reference, note, created_by, till_session_id)
			VALUES ($1,$2,$3,$4,$5,$6,0,$7,NULLIF($8,''),NULLIF($9,''),$10,$11)
		`, id, p.BusinessID, number, from, in.ToAccountID, amount, date, in.Reference, in.Note, p.UserID, tillID); err != nil {
			return err
		}
		fromCode, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, from)
		if err != nil {
			return err
		}
		toCode, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.ToAccountID)
		if err != nil {
			return err
		}
		user := p.UserID
		to := in.ToAccountID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "money_transfer", id, "Till pay-out "+number, &user, []ledger.Line{
			{Code: toCode, BranchID: &branch, MoneyAccountID: &to, Debit: amount},
			{Code: fromCode, BranchID: &branch, MoneyAccountID: &from, Credit: amount},
		}); err != nil {
			return err
		}
		out := map[string]any{"id": id, "number": number, "till_session_id": tillID, "from_account_id": from, "to_account_id": to, "amount": amount, "date": date}
		if err := s.Audit.Record(ctx, tx, p, "till.pay_out", "money_transfer", id, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
