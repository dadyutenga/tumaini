package sales

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
)

type Service struct {
	DB       *db.DB
	Stock    *stock.Service
	Ledger   *ledger.Service
	Audit    *audit.Service
	Approval *approval.Service
	Clock    clock.Clock
}

func New(database *db.DB, st *stock.Service, led *ledger.Service, aud *audit.Service, ap *approval.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Stock: st, Ledger: led, Audit: aud, Approval: ap, Clock: clk}
}

type Discount struct {
	Type  string          `json:"type"`
	Value decimal.Decimal `json:"value"`
}

type ItemIn struct {
	ProductID uuid.UUID       `json:"product_id"`
	Qty       decimal.Decimal `json:"qty"`
	Discount  *Discount       `json:"discount"`
}

type PayIn struct {
	Method         string          `json:"method"`
	MoneyAccountID uuid.UUID       `json:"money_account_id"`
	Amount         decimal.Decimal `json:"amount"`
	Reference      string          `json:"reference"`
}

type CreateIn struct {
	CustomerID *uuid.UUID `json:"customer_id"`
	Items      []ItemIn   `json:"items"`
	Discount   *Discount  `json:"discount"`
	Payments   []PayIn    `json:"payments"`
	Note       string     `json:"note"`
}

type line struct {
	ProductID uuid.UUID
	Name      string
	Kind      string
	Qty       decimal.Decimal
	UnitPrice decimal.Decimal
	Discount  decimal.Decimal
	TaxRate   decimal.Decimal
	Tax       decimal.Decimal
	LineTotal decimal.Decimal
	UnitCost  decimal.Decimal
}

type totals struct {
	Subtotal decimal.Decimal
	Discount decimal.Decimal
	Tax      decimal.Decimal
	Total    decimal.Decimal
}

func (s *Service) Preview(ctx context.Context, p *auth.Principal, in CreateIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		lines, tot, _, err := s.price(ctx, tx, p, in)
		if err != nil {
			return err
		}
		out = view(uuid.Nil, "", "preview", lines, tot, money.Zero, tot.Total, money.Zero)
		return nil
	})
	return out, err
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in CreateIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Add at least one product")
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
		lines, tot, includeTax, err := s.price(ctx, tx, p, in)
		if err != nil {
			return err
		}
		pct := money.Zero
		if tot.Subtotal.IsPositive() {
			pct = tot.Discount.Div(tot.Subtotal).Mul(decimal.NewFromInt(100))
		}
		applied, change, balance, err := splitPayments(in.Payments, tot.Total)
		if err != nil {
			return err
		}
		if hasCash(applied) {
			if _, _, err := openTill(ctx, tx, p.UserID); err != nil {
				return httpx.Conflict("till_not_open", "Open a till before taking cash")
			}
		}
		dg, dok := p.Can("sales.discount")
		if tot.Discount.IsPositive() && !dok {
			return httpx.Forbidden("permission_denied", "You cannot give a discount")
		}
		needsDiscount := tot.Discount.IsPositive() && !auth.WithinLimit(dg, dok, pct)
		cust, err := s.resolveCustomer(ctx, tx, in.CustomerID, balance)
		if err != nil {
			return err
		}
		needsCredit := false
		if balance.IsPositive() {
			var owed decimal.Decimal
			var limit decimal.Decimal
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(balance),0), credit_limit FROM customers c
				LEFT JOIN sales s ON s.customer_id=c.id AND s.status IN ('completed','partially_refunded')
				WHERE c.id=$1 GROUP BY c.credit_limit
			`, cust).Scan(&owed, &limit); err != nil {
				return err
			}
			if owed.Add(balance).GreaterThan(limit) {
				needsCredit = true
			}
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		footer := setting.String(ctx, tx, "receipt.footer", "")
		saleID := ids.New()
		number := ""
		state := "completed"
		if needsDiscount || needsCredit {
			state = "pending_approval"
			number, err = sequence.Next(ctx, tx, *p.BranchID, "sale")
			if err != nil {
				return err
			}
		} else {
			number, err = sequence.Next(ctx, tx, *p.BranchID, "sale")
			if err != nil {
				return err
			}
		}
		paid := tot.Total.Sub(balance)
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales (id, business_id, branch_id, number, kind, status, customer_id, cashier_id, business_date,
				subtotal, discount_total, tax_total, total, paid_total, balance, change_due, note, receipt_footer, prices_include_tax, completed_at)
			VALUES ($1,$2,$3,$4,'sale',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18, CASE WHEN $5='completed' THEN now() ELSE NULL END)
		`, saleID, p.BusinessID, *p.BranchID, number, state, cust, p.UserID, date, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, paid, balance, change, in.Note, footer, includeTax); err != nil {
			return err
		}
		for _, ln := range lines {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sale_items (id, business_id, sale_id, product_id, product_name, qty, unit_price, discount, tax_rate, tax_amount, line_total)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			`, ids.New(), p.BusinessID, saleID, ln.ProductID, ln.Name, ln.Qty, ln.UnitPrice, ln.Discount, ln.TaxRate, ln.Tax, ln.LineTotal); err != nil {
				return err
			}
		}
		if state == "pending_approval" {
			kind := "discount"
			if needsCredit {
				kind = "credit_override"
			}
			payload := map[string]any{"payments": applied, "change_due": change}
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: p.BranchID, Kind: kind, SubjectType: "sale", SubjectID: saleID,
				Amount: decimal.NullDecimal{Decimal: tot.Total, Valid: true}, Summary: "Sale " + number + " needs approval", Payload: payload,
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": saleID}))
			status, body = 202, raw
			return err
		}
		view, err := s.finish(ctx, tx, p, saleID, *p.BranchID, number, date, lines, applied, tot, paid, balance, change, cust)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(view))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) finish(ctx context.Context, tx pgx.Tx, p *auth.Principal, saleID, branch uuid.UUID, number, date string, lines []line, applied []PayIn, tot totals, paid, balance, change decimal.Decimal, cust uuid.UUID) (map[string]any, error) {
	user := p.UserID
	var stockLines []stock.Line
	for _, ln := range lines {
		if ln.Kind == "stock" {
			stockLines = append(stockLines, stock.Line{ProductID: ln.ProductID, Qty: ln.Qty})
		}
	}
	costs := map[uuid.UUID]decimal.Decimal{}
	var err error
	if len(stockLines) > 0 {
		costs, err = s.Stock.Deduct(ctx, tx, p.BusinessID, branch, &user, "sale", "sale", saleID, stockLines)
		if err != nil {
			return nil, err
		}
	}
	costTotal := money.Zero
	for i := range lines {
		if lines[i].Kind != "stock" {
			continue
		}
		lines[i].UnitCost = costs[lines[i].ProductID]
		costTotal = costTotal.Add(money.Money(lines[i].Qty.Mul(lines[i].UnitCost)))
		_, _ = tx.Exec(ctx, `UPDATE sale_items SET unit_cost=$3 WHERE sale_id=$1 AND product_id=$2`, saleID, lines[i].ProductID, lines[i].UnitCost)
	}
	_, _ = tx.Exec(ctx, `UPDATE sales SET cost_total=$2, status='completed', completed_at=now() WHERE id=$1`, saleID, costTotal)
	var tillID *uuid.UUID
	if hasCash(applied) {
		id, account, err := openTill(ctx, tx, p.UserID)
		if err != nil {
			return nil, httpx.Conflict("till_not_open", "Open a till before taking cash")
		}
		tillID = &id
		_ = account
		_, _ = tx.Exec(ctx, `UPDATE sales SET till_session_id=$2 WHERE id=$1`, saleID, id)
	}
	var jlines []ledger.Line
	for _, pay := range applied {
		if !pay.Amount.IsPositive() {
			continue
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, pay.MoneyAccountID)
		if err != nil {
			return nil, err
		}
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: pay.MoneyAccountID, UserID: p.UserID,
			TillSessionID: tillID, Direction: "in", Kind: "sale", Method: pay.Method, PartyType: "customer", PartyID: &cust,
			Amount: pay.Amount, Reference: pay.Reference, Date: date, AllocType: "sale", AllocID: saleID, AllocAmount: pay.Amount,
		})
		if err != nil {
			return nil, err
		}
		_ = res
		jlines = append(jlines, ledger.Line{Code: code, BranchID: &branch, MoneyAccountID: &pay.MoneyAccountID, PartyType: "customer", PartyID: &cust, Debit: pay.Amount})
	}
	if balance.IsPositive() {
		jlines = append(jlines, ledger.Line{Code: "1100", BranchID: &branch, PartyType: "customer", PartyID: &cust, Debit: balance})
	}
	net := tot.Total.Sub(tot.Tax)
	if net.IsPositive() {
		jlines = append(jlines, ledger.Line{Code: "4000", BranchID: &branch, Credit: net})
	}
	if tot.Tax.IsPositive() {
		jlines = append(jlines, ledger.Line{Code: "2100", BranchID: &branch, Credit: tot.Tax})
	}
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "sale", saleID, "Sale "+number, &user, jlines); err != nil {
		return nil, err
	}
	if costTotal.IsPositive() {
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "sale_cost", saleID, "Cost of sale "+number, &user, []ledger.Line{
			{Code: "5000", BranchID: &branch, Debit: costTotal},
			{Code: "1200", BranchID: &branch, Credit: costTotal},
		}); err != nil {
			return nil, err
		}
	}
	pids := make([]uuid.UUID, len(lines))
	for i, ln := range lines {
		pids[i] = ln.ProductID
	}
	_ = jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids})
	if err := s.Audit.Record(ctx, tx, p, "sale.completed", "sale", saleID, nil, map[string]any{"number": number, "total": tot.Total}); err != nil {
		return nil, err
	}
	return view(saleID, number, "completed", lines, tot, paid, balance, change), nil
}

func (s *Service) OnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var payload struct {
		Payments []PayIn         `json:"payments"`
		Change   decimal.Decimal `json:"change_due"`
	}
	_ = json.Unmarshal(req.Payload, &payload)
	var branch uuid.UUID
	var number, date string
	var sub, disc, tax, total, paid, balance decimal.Decimal
	var cust uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT branch_id, number, business_date::text, subtotal, discount_total, tax_total, total, paid_total, balance, customer_id
		FROM sales WHERE id=$1
	`, req.SubjectID).Scan(&branch, &number, &date, &sub, &disc, &tax, &total, &paid, &balance, &cust); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT product_id, product_name, qty, unit_price, discount, tax_rate, tax_amount, line_total FROM sale_items WHERE sale_id=$1`, req.SubjectID)
	if err != nil {
		return err
	}
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.ProductID, &ln.Name, &ln.Qty, &ln.UnitPrice, &ln.Discount, &ln.TaxRate, &ln.Tax, &ln.LineTotal); err != nil {
			rows.Close()
			return err
		}
		ln.Kind = "stock"
		var kind string
		_ = tx.QueryRow(ctx, `SELECT kind FROM products WHERE id=$1`, ln.ProductID).Scan(&kind)
		if kind != "" {
			ln.Kind = kind
		}
		lines = append(lines, ln)
	}
	rows.Close()
	_, err = s.finish(ctx, tx, p, req.SubjectID, branch, number, date, lines, payload.Payments, totals{sub, disc, tax, total}, paid, balance, payload.Change, cust)
	return err
}

func (s *Service) OnRejected(ctx context.Context, tx pgx.Tx, _ *auth.Principal, req approval.Request) error {
	_, err := tx.Exec(ctx, `UPDATE sales SET status='cancelled' WHERE id=$1 AND status='pending_approval'`, req.SubjectID)
	return err
}

func (s *Service) price(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CreateIn) ([]line, totals, bool, error) {
	include := setting.Bool(ctx, tx, "tax.prices_include_tax", false)
	var lines []line
	var tot totals
	for _, it := range in.Items {
		if it.Qty.LessThanOrEqual(money.Zero) {
			return nil, totals{}, false, httpx.BadRequest("invalid_body", "Quantity must be greater than zero")
		}
		var name, kind string
		var price, rate decimal.Decimal
		var active bool
		err := tx.QueryRow(ctx, `
			SELECT p.name, p.kind, COALESCE(bp.sell_price, p.sell_price), COALESCE(tr.rate, def.rate, 0), p.is_active
			FROM products p
			LEFT JOIN product_branch_prices bp ON bp.product_id=p.id AND bp.branch_id=$2
			LEFT JOIN tax_rates tr ON tr.id=p.tax_rate_id
			LEFT JOIN tax_rates def ON def.is_default
			WHERE p.id=$1
		`, it.ProductID, *p.BranchID).Scan(&name, &kind, &price, &rate, &active)
		if err != nil || !active {
			return nil, totals{}, false, httpx.NotFound("not_found", "Product not found or inactive")
		}
		gross := money.Money(it.Qty.Mul(price))
		disc := money.Zero
		if it.Discount != nil {
			disc = money.DiscountAmount(gross, it.Discount.Type, it.Discount.Value)
		}
		pr := money.PriceLine(money.LineInput{Qty: it.Qty, UnitPrice: price, Discount: disc, TaxRate: rate}, include)
		lines = append(lines, line{ProductID: it.ProductID, Name: name, Kind: kind, Qty: it.Qty, UnitPrice: price, Discount: pr.Discount, TaxRate: rate, Tax: pr.Tax, LineTotal: pr.Total})
		tot.Subtotal = tot.Subtotal.Add(pr.Gross)
		tot.Discount = tot.Discount.Add(pr.Discount)
		tot.Tax = tot.Tax.Add(pr.Tax)
		tot.Total = tot.Total.Add(pr.Total)
	}
	if in.Discount != nil && in.Discount.Value.IsPositive() && tot.Total.IsPositive() {
		extra := money.DiscountAmount(tot.Total, in.Discount.Type, in.Discount.Value)
		if extra.GreaterThan(tot.Total) {
			extra = tot.Total
		}
		share := extra.Div(tot.Total)
		tot = totals{}
		for i := range lines {
			cut := money.Money(lines[i].LineTotal.Mul(share))
			base := lines[i].LineTotal.Sub(cut)
			repriced := money.PriceLine(money.LineInput{Qty: decimal.NewFromInt(1), UnitPrice: base, TaxRate: lines[i].TaxRate}, include)
			lines[i].Discount = lines[i].Discount.Add(cut)
			lines[i].Tax = repriced.Tax
			lines[i].LineTotal = repriced.Total
			tot.Subtotal = tot.Subtotal.Add(money.Money(lines[i].Qty.Mul(lines[i].UnitPrice)))
			tot.Discount = tot.Discount.Add(lines[i].Discount)
			tot.Tax = tot.Tax.Add(lines[i].Tax)
			tot.Total = tot.Total.Add(lines[i].LineTotal)
		}
	}
	return lines, tot, include, nil
}

func splitPayments(pays []PayIn, total decimal.Decimal) ([]PayIn, decimal.Decimal, decimal.Decimal, error) {
	remaining := total
	change := money.Zero
	var applied []PayIn
	for _, pay := range pays {
		amt := money.Money(pay.Amount)
		if amt.LessThanOrEqual(money.Zero) {
			continue
		}
		if amt.GreaterThan(remaining) {
			if pay.Method != "cash" {
				return nil, money.Zero, money.Zero, httpx.BadRequest("invalid_body", "Only cash can be more than the amount due")
			}
			change = change.Add(amt.Sub(remaining))
			amt = remaining
		}
		remaining = remaining.Sub(amt)
		pay.Amount = amt
		if amt.IsPositive() {
			applied = append(applied, pay)
		}
	}
	return applied, change, remaining, nil
}

func hasCash(pays []PayIn) bool {
	for _, p := range pays {
		if p.Method == "cash" && p.Amount.IsPositive() {
			return true
		}
	}
	return false
}

func openTill(ctx context.Context, tx pgx.Tx, user uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var id, account uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id, money_account_id FROM till_sessions WHERE cashier_id=$1 AND status='open'`, user).Scan(&id, &account)
	return id, account, err
}

func (s *Service) resolveCustomer(ctx context.Context, tx pgx.Tx, id *uuid.UUID, balance decimal.Decimal) (uuid.UUID, error) {
	if id == nil || *id == uuid.Nil {
		var walk uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE is_walk_in LIMIT 1`).Scan(&walk); err != nil {
			return uuid.Nil, err
		}
		if balance.IsPositive() {
			return uuid.Nil, httpx.Conflict("customer_required", "A named customer is required for a credit sale")
		}
		return walk, nil
	}
	var walk bool
	if err := tx.QueryRow(ctx, `SELECT is_walk_in FROM customers WHERE id=$1 AND is_active`, *id).Scan(&walk); err != nil {
		return uuid.Nil, httpx.NotFound("not_found", "Customer not found")
	}
	if walk && balance.IsPositive() {
		return uuid.Nil, httpx.Conflict("customer_required", "The walk-in customer cannot take credit")
	}
	return *id, nil
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func view(id uuid.UUID, number, status string, lines []line, tot totals, paid, balance, change decimal.Decimal) map[string]any {
	items := make([]map[string]any, len(lines))
	for i, ln := range lines {
		items[i] = map[string]any{"product_id": ln.ProductID, "product_name": ln.Name, "qty": ln.Qty, "unit_price": ln.UnitPrice, "discount": ln.Discount, "tax_amount": ln.Tax, "line_total": ln.LineTotal, "unit_cost": ln.UnitCost}
	}
	out := map[string]any{
		"status": status, "subtotal": tot.Subtotal, "discount_total": tot.Discount, "tax_total": tot.Tax,
		"total": tot.Total, "paid_total": paid, "balance": balance, "change_due": change, "items": items,
	}
	if id != uuid.Nil {
		out["id"] = id
		out["number"] = number
	}
	return out
}

func (s *Service) OpenTill(ctx context.Context, p *auth.Principal, account uuid.UUID, float decimal.Decimal) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO till_sessions (id, business_id, branch_id, money_account_id, cashier_id, opening_float)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, id, p.BusinessID, *p.BranchID, account, p.UserID, money.Money(float))
		if db.IsUnique(err) {
			return httpx.Conflict("invalid_status", "You already have an open till")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "open", "opening_float": money.Money(float)}, nil
}

func (s *Service) CurrentTill(ctx context.Context, p *auth.Principal) (any, error) {
	var id, account uuid.UUID
	var float decimal.Decimal
	var opened any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, money_account_id, opening_float, opened_at FROM till_sessions WHERE cashier_id=$1 AND status='open'`, p.UserID).Scan(&id, &account, &float, &opened)
	})
	if err != nil {
		return nil, nil
	}
	return map[string]any{"id": id, "money_account_id": account, "opening_float": float, "opened_at": opened, "status": "open"}, nil
}

func (s *Service) CloseTill(ctx context.Context, p *auth.Principal, id uuid.UUID, counted decimal.Decimal, note string) (map[string]any, error) {
	var result map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var opening decimal.Decimal
		var account, branch uuid.UUID
		var cashier uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT opening_float, money_account_id, branch_id, cashier_id FROM till_sessions WHERE id=$1 AND status='open'`, id).Scan(&opening, &account, &branch, &cashier); err != nil {
			return httpx.NotFound("not_found", "Open till not found")
		}
		var movement decimal.Decimal
		_ = tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN direction='in' THEN amount ELSE -amount END),0)
			FROM payments WHERE till_session_id=$1 AND method='cash' AND status='posted'
		`, id).Scan(&movement)
		expected := opening.Add(movement)
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
		_, err := tx.Exec(ctx, `
			UPDATE till_sessions SET status='closed', expected_cash=$2, counted_cash=$3, difference=$4, closed_at=now(), closed_by=$5, note=$6 WHERE id=$1
		`, id, expected, money.Money(counted), diff, p.UserID, note)
		if err != nil {
			return err
		}
		result = map[string]any{"id": id, "expected_cash": expected, "counted_cash": money.Money(counted), "difference": diff}
		return s.Audit.Record(ctx, tx, p, "till.closed", "till_session", id, nil, result)
	})
	return result, err
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var number, status, footer string
		var branch, cashier uuid.UUID
		var customer *uuid.UUID
		var sub, disc, tax, total, cost, paid, bal decimal.Decimal
		var date string
		q := `SELECT number, status, branch_id, customer_id, cashier_id, business_date::text, subtotal, discount_total, tax_total, total, cost_total, paid_total, balance, receipt_footer FROM sales WHERE id=$1`
		args := []any{id}
		if !p.IsOwner {
			if g, ok := p.Can("sales.view"); ok && g.Scope == "own" {
				q += ` AND cashier_id=$2`
				args = append(args, p.UserID)
			} else if len(p.BranchIDs) > 0 {
				q += ` AND branch_id = ANY($2)`
				args = append(args, p.BranchIDs)
			}
		}
		if err := tx.QueryRow(ctx, q, args...).Scan(&number, &status, &branch, &customer, &cashier, &date, &sub, &disc, &tax, &total, &cost, &paid, &bal, &footer); err != nil {
			return httpx.NotFound("not_found", "Sale not found")
		}
		rows, err := tx.Query(ctx, `SELECT id, product_id, product_name, qty, unit_price, unit_cost, discount, tax_amount, line_total, qty_returned FROM sale_items WHERE sale_id=$1`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		var items []map[string]any
		for rows.Next() {
			var itemID, pid uuid.UUID
			var name string
			var qty, price, ucost, d, taxA, line, ret decimal.Decimal
			if err := rows.Scan(&itemID, &pid, &name, &qty, &price, &ucost, &d, &taxA, &line, &ret); err != nil {
				return err
			}
			item := map[string]any{"id": itemID, "product_id": pid, "product_name": name, "qty": qty, "unit_price": price, "discount": d, "tax_amount": taxA, "line_total": line, "qty_returned": ret}
			if _, ok := p.Can("products.view_cost"); ok {
				item["unit_cost"] = ucost
			}
			items = append(items, item)
		}
		out = map[string]any{"id": id, "number": number, "status": status, "branch_id": branch, "customer_id": customer, "cashier_id": cashier, "business_date": date, "subtotal": sub, "discount_total": disc, "tax_total": tax, "total": total, "paid_total": paid, "balance": bal, "receipt_footer": footer, "items": items}
		if _, ok := p.Can("products.view_cost"); ok {
			out["cost_total"] = cost
			out["profit"] = total.Sub(tax).Sub(cost)
		}
		return nil
	})
	return out, err
}

func (s *Service) List(ctx context.Context, p *auth.Principal, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		where := `WHERE kind='sale'`
		args := []any{}
		if !p.IsOwner {
			if g, ok := p.Can("sales.view"); ok && g.Scope == "own" {
				args = append(args, p.UserID)
				where += ` AND cashier_id=$1`
			} else if p.BranchID != nil {
				args = append(args, *p.BranchID)
				where += ` AND branch_id=$1`
			}
		} else if p.BranchID != nil {
			args = append(args, *p.BranchID)
			where += ` AND branch_id=$1`
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM sales `+where, args...).Scan(&total); err != nil {
			return err
		}
		args = append(args, page.PerPage, page.Offset)
		rows, err := tx.Query(ctx, `SELECT id, number, status, total, paid_total, balance, business_date::text, cashier_id FROM sales `+where+` ORDER BY created_at DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, cashier uuid.UUID
			var number, status, date string
			var totalD, paid, bal decimal.Decimal
			if err := rows.Scan(&id, &number, &status, &totalD, &paid, &bal, &date, &cashier); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "status": status, "total": totalD, "paid_total": paid, "balance": bal, "business_date": date, "cashier_id": cashier})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, total, err
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

type ReturnIn struct {
	Reason       string     `json:"reason"`
	RefundMethod string     `json:"refund_method"`
	AccountID    *uuid.UUID `json:"money_account_id"`
	Items        []struct {
		SaleItemID uuid.UUID       `json:"sale_item_id"`
		Qty        decimal.Decimal `json:"qty"`
		Restock    *bool           `json:"restock"`
	} `json:"items"`
}

func (s *Service) CreateReturn(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in ReturnIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var saleStatus string
		var branch uuid.UUID
		var cust *uuid.UUID
		var total, paid, bal decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT status, branch_id, customer_id, total, paid_total, balance FROM sales WHERE id=$1 AND status IN ('completed','partially_refunded')`, saleID).Scan(&saleStatus, &branch, &cust, &total, &paid, &bal); err != nil {
			return httpx.NotFound("not_found", "Sale not found")
		}
		_ = saleStatus
		amount := money.Zero
		var rets []retLine
		for _, it := range in.Items {
			var qty, returned, lineTotal, taxAmt, unitCost decimal.Decimal
			var product uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT qty, qty_returned, line_total, tax_amount, unit_cost, product_id FROM sale_items WHERE id=$1 AND sale_id=$2`, it.SaleItemID, saleID).Scan(&qty, &returned, &lineTotal, &taxAmt, &unitCost, &product); err != nil {
				return httpx.NotFound("not_found", "Sale line not found")
			}
			if returned.Add(it.Qty).GreaterThan(qty) {
				return httpx.Conflict("invalid_status", "Returned quantity cannot exceed the quantity sold")
			}
			part := money.Money(lineTotal.Mul(it.Qty).Div(qty))
			taxPart := money.Money(taxAmt.Mul(it.Qty).Div(qty))
			restock := true
			if it.Restock != nil {
				restock = *it.Restock
			}
			rets = append(rets, retLine{item: it.SaleItemID, product: product, qty: it.Qty, unitCost: unitCost, amt: part, tax: taxPart, restock: restock})
			amount = amount.Add(part)
		}
		if amount.LessThanOrEqual(money.Zero) {
			return httpx.BadRequest("invalid_body", "Choose at least one item to return")
		}
		g, ok := p.Can("sales.refund_approve")
		auto := ok && auth.WithinLimit(g, ok, amount)
		number, err := sequence.Next(ctx, tx, branch, "return")
		if err != nil {
			return err
		}
		rid := ids.New()
		st := "pending_approval"
		if auto {
			st = "completed"
		}
		method := in.RefundMethod
		if method == "" {
			if bal.IsPositive() {
				method = "reduce_debt"
			} else {
				method = "cash"
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sale_returns (id, business_id, branch_id, number, sale_id, status, reason, refund_method, money_account_id, refund_total, requested_by, completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, CASE WHEN $6='completed' THEN now() ELSE NULL END)
		`, rid, p.BusinessID, branch, number, saleID, st, in.Reason, method, in.AccountID, amount, p.UserID); err != nil {
			return err
		}
		for _, r := range rets {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sale_return_items (id, business_id, return_id, sale_item_id, qty, restock, amount, unit_cost)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			`, ids.New(), p.BusinessID, rid, r.item, r.qty, r.restock, r.amt, r.unitCost); err != nil {
				return err
			}
		}
		if !auto {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &branch, Kind: "refund", SubjectType: "sale_return", SubjectID: rid,
				Amount: decimal.NullDecimal{Decimal: amount, Valid: true}, Summary: "Refund " + number,
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": rid}))
			status, body = 202, raw
			return err
		}
		if err := s.applyReturn(ctx, tx, p, rid, saleID, branch, cust, method, in.AccountID, amount, rets); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": rid, "number": number, "status": "completed", "refund_total": amount}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

type retLine struct {
	item, product           uuid.UUID
	qty, unitCost, amt, tax decimal.Decimal
	restock                 bool
}

func (s *Service) applyReturn(ctx context.Context, tx pgx.Tx, p *auth.Principal, returnID, saleID, branch uuid.UUID, cust *uuid.UUID, method string, account *uuid.UUID, amount decimal.Decimal, lines []retLine) error {
	user := p.UserID
	taxSum := money.Zero
	costSum := money.Zero
	for _, ln := range lines {
		_, _ = tx.Exec(ctx, `UPDATE sale_items SET qty_returned = qty_returned + $2 WHERE id=$1`, ln.item, ln.qty)
		taxSum = taxSum.Add(ln.tax)
		if ln.restock {
			if err := s.Stock.ReturnAtCost(ctx, tx, p.BusinessID, branch, &user, "sale_return", "sale_return", returnID, []stock.Line{{ProductID: ln.product, Qty: ln.qty, UnitCost: ln.unitCost}}); err != nil {
				return err
			}
			costSum = costSum.Add(money.Money(ln.qty.Mul(ln.unitCost)))
		}
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return err
	}
	net := amount.Sub(taxSum)
	j := []ledger.Line{}
	if net.IsPositive() {
		j = append(j, ledger.Line{Code: "4010", BranchID: &branch, Debit: net})
	}
	if taxSum.IsPositive() {
		j = append(j, ledger.Line{Code: "2100", BranchID: &branch, Debit: taxSum})
	}
	if method == "reduce_debt" || method == "customer_credit" {
		code := "1100"
		if method == "customer_credit" {
			code = "2300"
		}
		j = append(j, ledger.Line{Code: code, BranchID: &branch, PartyType: "customer", PartyID: cust, Credit: amount})
		_, _ = tx.Exec(ctx, `UPDATE sales SET total=total-$2, balance=balance-$2, tax_total=tax_total-$3, cost_total=cost_total-$4 WHERE id=$1`, saleID, amount, taxSum, costSum)
	} else {
		if account == nil {
			return httpx.BadRequest("invalid_body", "Choose the account to refund from")
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, *account)
		if err != nil {
			return err
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, *account, amount); err != nil {
			return err
		}
		var till *uuid.UUID
		if method == "cash" {
			if id, _, err := openTill(ctx, tx, p.UserID); err == nil {
				till = &id
			}
		}
		if _, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: *account, UserID: p.UserID, TillSessionID: till,
			Direction: "out", Kind: "sale_refund", Method: method, PartyType: "customer", PartyID: cust,
			Amount: amount, Date: date, AllocType: "sale", AllocID: saleID, AllocAmount: amount,
		}); err != nil {
			return err
		}
		j = append(j, ledger.Line{Code: code, BranchID: &branch, MoneyAccountID: account, PartyType: "customer", PartyID: cust, Credit: amount})
		_, _ = tx.Exec(ctx, `UPDATE sales SET total=total-$2, paid_total=paid_total-$2, tax_total=tax_total-$3, cost_total=cost_total-$4 WHERE id=$1`, saleID, amount, taxSum, costSum)
	}
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "sale_return", returnID, "Sale return", &user, j); err != nil {
		return err
	}
	if costSum.IsPositive() {
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "sale_return_cost", returnID, "Stock returned", &user, []ledger.Line{
			{Code: "1200", BranchID: &branch, Debit: costSum},
			{Code: "5000", BranchID: &branch, Credit: costSum},
		}); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE sales SET status = CASE WHEN balance = 0 AND paid_total = 0 AND total = 0 THEN 'refunded'
			WHEN (SELECT COALESCE(SUM(qty-qty_returned),0) FROM sale_items WHERE sale_id=$1) = 0 THEN 'refunded'
			ELSE 'partially_refunded' END
		WHERE id=$1
	`, saleID)
	return err
}

func (s *Service) OnReturnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var saleID, branch uuid.UUID
	var method string
	var account *uuid.UUID
	var amount decimal.Decimal
	var cust *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT r.sale_id, r.branch_id, r.refund_method, r.money_account_id, r.refund_total, s.customer_id
		FROM sale_returns r JOIN sales s ON s.id=r.sale_id WHERE r.id=$1
	`, req.SubjectID).Scan(&saleID, &branch, &method, &account, &amount, &cust); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT ri.sale_item_id, si.product_id, ri.qty, ri.unit_cost, ri.amount, si.tax_amount * ri.qty / si.qty, ri.restock
		FROM sale_return_items ri JOIN sale_items si ON si.id=ri.sale_item_id WHERE ri.return_id=$1
	`, req.SubjectID)
	if err != nil {
		return err
	}
	var lines []retLine
	for rows.Next() {
		var ln retLine
		if err := rows.Scan(&ln.item, &ln.product, &ln.qty, &ln.unitCost, &ln.amt, &ln.tax, &ln.restock); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, ln)
	}
	rows.Close()
	if err := s.applyReturn(ctx, tx, p, req.SubjectID, saleID, branch, cust, method, account, amount, lines); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE sale_returns SET status='completed', approved_by=$2, completed_at=now() WHERE id=$1`, req.SubjectID, p.UserID)
	return err
}
