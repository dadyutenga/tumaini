package sales

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
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
	Notify   *notify.Service
	Clock    clock.Clock
}

func New(database *db.DB, st *stock.Service, led *ledger.Service, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Stock: st, Ledger: led, Audit: aud, Approval: ap, Notify: n, Clock: clk}
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
	// HeldID removes the held cart this sale was resumed from.
	HeldID *uuid.UUID `json:"held_id,omitempty"`

	// quoted fixes unit prices when a quotation is converted.
	quoted      map[uuid.UUID]decimal.Decimal
	quotationID *uuid.UUID
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
		out = view(uuid.Nil, "", "preview", lines, tot, money.Zero, tot.Total, money.Zero, canSeeCost(p))
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
		st, data, err := s.createTx(ctx, tx, p, in)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, st, httpx.Data(data))
		status, body = st, raw
		return err
	})
	return status, body, err
}

// createTx creates and, when no approval is needed, completes a sale inside tx.
// It returns 201 with the sale view, or 202 with the approval request.
func (s *Service) createTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CreateIn) (int, map[string]any, error) {
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	lines, tot, includeTax, err := s.price(ctx, tx, p, in)
	if err != nil {
		return 0, nil, err
	}
	if !tot.Total.IsPositive() {
		return 0, nil, httpx.BadRequest("invalid_total", "The sale total must be greater than zero. Check the product prices")
	}
	pct := money.Zero
	if tot.Subtotal.IsPositive() {
		pct = tot.Discount.Div(tot.Subtotal).Mul(decimal.NewFromInt(100))
	}
	if err := s.checkPayments(ctx, tx, in.Payments); err != nil {
		return 0, nil, err
	}
	applied, change, balance, err := splitPayments(in.Payments, tot.Total)
	if err != nil {
		return 0, nil, err
	}
	if hasCash(applied) {
		if _, _, err := openTill(ctx, tx, p.UserID); err != nil {
			return 0, nil, httpx.Conflict("till_not_open", "Open a till before taking cash")
		}
	}
	dg, dok := p.Can("sales.discount")
	if tot.Discount.IsPositive() && !dok {
		return 0, nil, httpx.Forbidden("permission_denied", "You cannot give a discount")
	}
	needsDiscount := tot.Discount.IsPositive() && !auth.WithinLimit(dg, dok, pct)
	cust, err := s.resolveCustomer(ctx, tx, in.CustomerID, balance)
	if err != nil {
		return 0, nil, err
	}
	needsCredit := false
	if balance.IsPositive() {
		var owed, limit decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(s.balance),0), c.credit_limit FROM customers c
			LEFT JOIN sales s ON s.customer_id=c.id AND s.status IN ('completed','partially_refunded')
			WHERE c.id=$1 GROUP BY c.credit_limit
		`, cust).Scan(&owed, &limit); err != nil {
			return 0, nil, err
		}
		if owed.Add(balance).GreaterThan(limit) {
			needsCredit = true
		}
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return 0, nil, err
	}
	footer := setting.String(ctx, tx, "receipt.footer", "")
	saleID := ids.New()
	state := "completed"
	if needsDiscount || needsCredit {
		state = "pending_approval"
	}
	number, err := sequence.Next(ctx, tx, *p.BranchID, "sale")
	if err != nil {
		return 0, nil, err
	}
	cart, err := json.Marshal(in)
	if err != nil {
		return 0, nil, err
	}
	var dueDate any
	if balance.IsPositive() {
		var days int
		if err := tx.QueryRow(ctx, `SELECT payment_terms_days FROM customers WHERE id=$1`, cust).Scan(&days); err != nil {
			return 0, nil, err
		}
		if days == 0 {
			days = setting.Int(ctx, tx, "invoice.due_days", 0)
		}
		dueDate = mustDate(date).AddDate(0, 0, days).Format("2006-01-02")
	}
	paid := tot.Total.Sub(balance)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sales (id, business_id, branch_id, number, kind, status, customer_id, cashier_id, employee_id, business_date,
			subtotal, discount_total, tax_total, total, paid_total, balance, change_due, note, receipt_footer, prices_include_tax, completed_at, cart, due_date, quotation_id)
		VALUES ($1,$2,$3,$4,'sale',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19, CASE WHEN $5='completed' THEN now() ELSE NULL END, $20, $21, $22)
	`, saleID, p.BusinessID, *p.BranchID, number, state, cust, p.UserID, p.EmployeeID, date, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, paid, balance, change, in.Note, footer, includeTax, cart, dueDate, in.quotationID); err != nil {
		return 0, nil, err
	}
	if err := insertItems(ctx, tx, p.BusinessID, saleID, lines); err != nil {
		return 0, nil, err
	}
	if in.HeldID != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM sales WHERE id=$1 AND status='held' AND cashier_id=$2`, *in.HeldID, p.UserID); err != nil {
			return 0, nil, err
		}
	}
	if state == "pending_approval" {
		kind := "discount"
		if needsCredit {
			kind = "credit_override"
		}
		payload := map[string]any{"payments": applied, "change_due": change}
		summary := "Sale " + number + " needs approval for a discount of " + pct.Round(2).String() + "%"
		if needsCredit {
			summary = "Sale " + number + " goes over the customer's credit limit"
		}
		reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: p.BranchID, Kind: kind, SubjectType: "sale", SubjectID: saleID,
			Amount: decimal.NullDecimal{Decimal: tot.Total, Valid: true}, Summary: summary, Payload: payload,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale.pending_approval", "sale", saleID, nil, map[string]any{"number": number, "total": tot.Total, "kind": kind}); err != nil {
			return 0, nil, err
		}
		return 202, map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": saleID, "number": number}, nil
	}
	v, err := s.finish(ctx, tx, p, saleID, *p.BranchID, number, date, lines, applied, tot, paid, balance, change, cust)
	if err != nil {
		return 0, nil, err
	}
	return 201, v, nil
}

func insertItems(ctx context.Context, tx pgx.Tx, businessID, saleID uuid.UUID, lines []line) error {
	for _, ln := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO sale_items (id, business_id, sale_id, product_id, product_name, qty, unit_price, discount, tax_rate, tax_amount, line_total)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, ids.New(), businessID, saleID, ln.ProductID, ln.Name, ln.Qty, ln.UnitPrice, ln.Discount, ln.TaxRate, ln.Tax, ln.LineTotal); err != nil {
			return err
		}
	}
	return nil
}

// checkPayments rejects unknown methods and inactive or missing money accounts.
func (s *Service) checkPayments(ctx context.Context, tx pgx.Tx, pays []PayIn) error {
	disabled := map[string]bool{}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key='payment.methods'`).Scan(&raw); err == nil {
		var cfg map[string]struct {
			Enabled *bool `json:"enabled"`
		}
		if json.Unmarshal(raw, &cfg) == nil {
			for m, c := range cfg {
				if c.Enabled != nil && !*c.Enabled {
					disabled[m] = true
				}
			}
		}
	}
	for _, pay := range pays {
		if !pay.Amount.IsPositive() {
			continue
		}
		switch pay.Method {
		case "cash", "mobile_money", "bank":
		default:
			return httpx.BadRequest("invalid_body", "Payment method must be cash, mobile_money or bank")
		}
		if disabled[pay.Method] {
			return httpx.Conflict("method_disabled", "This payment method is switched off in the settings")
		}
		if pay.Method != "cash" && strings.TrimSpace(pay.Reference) == "" {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "A reference is required for mobile money and bank payments", Fields: map[string]string{"reference": "required"}}
		}
		if err := checkAccount(ctx, tx, pay.MoneyAccountID); err != nil {
			return err
		}
	}
	return nil
}

// creditEnabled reports whether credit sales are switched on (FR-SET-04).
func creditEnabled(ctx context.Context, tx pgx.Tx) bool {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key='payment.methods'`).Scan(&raw); err != nil {
		return true
	}
	var cfg struct {
		Credit struct {
			Enabled *bool `json:"enabled"`
		} `json:"credit"`
	}
	return json.Unmarshal(raw, &cfg) != nil || cfg.Credit.Enabled == nil || *cfg.Credit.Enabled
}

func checkAccount(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT is_active FROM money_accounts WHERE id=$1`, id).Scan(&active); err != nil || !active {
		return httpx.NotFound("not_found", "Money account not found or inactive")
	}
	return nil
}

func mustDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
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
		if _, err := tx.Exec(ctx, `UPDATE sale_items SET unit_cost=$3 WHERE sale_id=$1 AND product_id=$2`, saleID, lines[i].ProductID, lines[i].UnitCost); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE sales SET cost_total=$2, status='completed', completed_at=now() WHERE id=$1`, saleID, costTotal); err != nil {
		return nil, err
	}
	var tillID *uuid.UUID
	if hasCash(applied) {
		// The cash sits in the cashier's till, also when an approver completes the sale.
		var cashier uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT cashier_id FROM sales WHERE id=$1`, saleID).Scan(&cashier); err != nil {
			return nil, err
		}
		id, account, err := openTill(ctx, tx, cashier)
		if err != nil {
			return nil, httpx.Conflict("till_not_open", "Open a till before taking cash")
		}
		tillID = &id
		_ = account
		if _, err := tx.Exec(ctx, `UPDATE sales SET till_session_id=$2 WHERE id=$1`, saleID, id); err != nil {
			return nil, err
		}
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
	if err := jobs.EnqueueTx(ctx, tx, p.BusinessID, jobs.CheckLowStock, map[string]any{"branch_id": branch, "product_ids": pids}); err != nil {
		return nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "sale.completed", "sale", saleID, nil, map[string]any{"number": number, "total": tot.Total}); err != nil {
		return nil, err
	}
	out := view(saleID, number, "completed", lines, tot, paid, balance, change, canSeeCost(p))
	out["receipt_url"] = "/api/v1/sales/" + saleID.String() + "/receipt"
	return out, nil
}

func (s *Service) OnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var payload struct {
		Payments []PayIn         `json:"payments"`
		Change   decimal.Decimal `json:"change_due"`
	}
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return err
		}
	}
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
	rows, err := tx.Query(ctx, `SELECT si.product_id, si.product_name, si.qty, si.unit_price, si.discount, si.tax_rate, si.tax_amount, si.line_total, p.kind FROM sale_items si JOIN products p ON p.id=si.product_id WHERE si.sale_id=$1`, req.SubjectID)
	if err != nil {
		return err
	}
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.ProductID, &ln.Name, &ln.Qty, &ln.UnitPrice, &ln.Discount, &ln.TaxRate, &ln.Tax, &ln.LineTotal, &ln.Kind); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, ln)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.finish(ctx, tx, p, req.SubjectID, branch, number, date, lines, payload.Payments, totals{sub, disc, tax, total}, paid, balance, payload.Change, cust)
	return err
}

func (s *Service) OnRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	if _, err := tx.Exec(ctx, `UPDATE sales SET status='cancelled' WHERE id=$1 AND status='pending_approval'`, req.SubjectID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale."+req.Status, "sale", req.SubjectID, nil, map[string]any{"approval_request_id": req.ID, "note": req.Note})
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
		if q, ok := in.quoted[it.ProductID]; ok {
			price = q
		}
		gross := money.Money(it.Qty.Mul(price))
		disc := money.Zero
		if it.Discount != nil {
			if err := checkDiscount(it.Discount); err != nil {
				return nil, totals{}, false, err
			}
			disc = money.DiscountAmount(gross, it.Discount.Type, it.Discount.Value)
		}
		pr := money.PriceLine(money.LineInput{Qty: it.Qty, UnitPrice: price, Discount: disc, TaxRate: rate}, include)
		lines = append(lines, line{ProductID: it.ProductID, Name: name, Kind: kind, Qty: it.Qty, UnitPrice: price, Discount: pr.Discount, TaxRate: rate, Tax: pr.Tax, LineTotal: pr.Total})
		tot.Subtotal = tot.Subtotal.Add(pr.Gross)
		tot.Discount = tot.Discount.Add(pr.Discount)
		tot.Tax = tot.Tax.Add(pr.Tax)
		tot.Total = tot.Total.Add(pr.Total)
	}
	if in.Discount != nil {
		if err := checkDiscount(in.Discount); err != nil {
			return nil, totals{}, false, err
		}
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

func checkDiscount(d *Discount) error {
	if d.Type != "percent" && d.Type != "amount" {
		return httpx.BadRequest("invalid_body", "Discount type must be percent or amount")
	}
	if d.Value.IsNegative() || (d.Type == "percent" && d.Value.GreaterThan(decimal.NewFromInt(100))) {
		return httpx.BadRequest("invalid_body", "Discount value is out of range")
	}
	return nil
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
	if balance.IsPositive() && !creditEnabled(ctx, tx) {
		return uuid.Nil, httpx.Conflict("method_disabled", "Credit sales are switched off in the settings")
	}
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

// canSeeCost reports whether the caller may see cost and margin (FR-INV-08).
func canSeeCost(p *auth.Principal) bool {
	_, ok := p.Can("products.view_cost")
	return ok || p.IsOwner
}

// view builds the sale response. Unit cost is included only when showCost is set, so cashiers
// do not learn what the shop paid for what they sell.
func view(id uuid.UUID, number, status string, lines []line, tot totals, paid, balance, change decimal.Decimal, showCost bool) map[string]any {
	items := make([]map[string]any, len(lines))
	for i, ln := range lines {
		items[i] = map[string]any{"product_id": ln.ProductID, "product_name": ln.Name, "qty": ln.Qty, "unit_price": ln.UnitPrice, "discount": ln.Discount, "tax_amount": ln.Tax, "line_total": ln.LineTotal}
		if showCost {
			items[i]["unit_cost"] = ln.UnitCost
		}
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
