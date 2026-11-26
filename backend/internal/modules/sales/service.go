package sales

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/audit"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/jobs"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Approval *approval.Service
	Notify   *notify.Service
	Outbound *outbound.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, ap *approval.Service, n *notify.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Approval: ap, Notify: n, Clock: clk}
}

type Discount struct {
	Type  string          `json:"type"`
	Value decimal.Decimal `json:"value"`
}

type ItemIn struct {
	ProductID     uuid.UUID       `json:"product_id"`
	Qty           decimal.Decimal `json:"qty"`
	Discount      *Discount       `json:"discount"`
	SerialNumber  string          `json:"serial_number"`
	SerialNumbers []string        `json:"serial_numbers,omitempty"`
	UnitIDs       []uuid.UUID     `json:"unit_ids,omitempty"`
}

type PayIn struct {
	Method         string          `json:"method"`
	MoneyAccountID uuid.UUID       `json:"money_account_id"`
	Amount         decimal.Decimal `json:"amount"`
	Reference      string          `json:"reference"`
}

type CreateIn struct {
	CustomerID *uuid.UUID `json:"customer_id"`
	DeviceID   *uuid.UUID `json:"device_id,omitempty"`
	ClientRef  *uuid.UUID `json:"client_ref,omitempty"`
	Items      []ItemIn   `json:"items"`
	Discount   *Discount  `json:"discount"`
	Payments   []PayIn    `json:"payments"`
	Note       string     `json:"note"`
	DueDate    string     `json:"due_date"`
	// HeldID removes the held cart this sale was resumed from.
	HeldID *uuid.UUID `json:"held_id,omitempty"`
}

type CompleteIn struct {
	Payments []PayIn `json:"payments"`
	DueDate  string  `json:"due_date"`
}

type line struct {
	ProductID      uuid.UUID
	Name           string
	Qty            decimal.Decimal
	UnitPrice      decimal.Decimal
	Discount       decimal.Decimal
	TaxRate        decimal.Decimal
	Tax            decimal.Decimal
	LineTotal      decimal.Decimal
	UnitCost       decimal.Decimal
	WarrantyMonths int
	TracksSerial   bool
	UnitIDs        []uuid.UUID
	Serials        []string
}

type totals struct {
	Subtotal      decimal.Decimal // sum of sale_lines.line_total (matches app.sales.subtotal)
	DiscountType  *string
	DiscountValue decimal.Decimal
	Discount      decimal.Decimal // sale-level discount_amount
	LineDiscount  decimal.Decimal // sum of line discounts
	Tax           decimal.Decimal
	Total         decimal.Decimal // subtotal - discount_amount
}

func (s *Service) Preview(ctx context.Context, p *auth.Principal, in CreateIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lines, tot, err := s.price(ctx, tx, p, in, false)
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
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

// createTx creates and, when no discount approval is needed, completes a sale inside tx.
// The database triggers (tg_sales_guard, tg_sales_post, tg_sales_check_totals) deduct stock,
// snapshot COGS, mark serialised units sold and accrue commissions when status becomes 'completed'.
func (s *Service) createTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CreateIn) (int, map[string]any, error) {
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	lines, tot, err := s.price(ctx, tx, p, in, true)
	if err != nil {
		return 0, nil, err
	}
	if !tot.Total.IsPositive() {
		return 0, nil, httpx.BadRequest("invalid_total", "The sale total must be greater than zero. Check the product prices")
	}
	totalDisc := tot.Discount.Add(tot.LineDiscount)
	if totalDisc.IsPositive() && !p.Has("shop.discount.apply") {
		return 0, nil, httpx.Forbidden("permission_denied", "You cannot give a discount")
	}
	grossTotal := tot.Subtotal.Add(tot.LineDiscount)
	pct := money.Zero
	if grossTotal.IsPositive() && totalDisc.IsPositive() {
		pct = totalDisc.Mul(decimal.NewFromInt(100)).Div(grossTotal).Round(2)
	}
	needsDiscount := false
	if totalDisc.IsPositive() {
		if lim, capped := p.Limits["sale_discount_pct"]; capped && pct.GreaterThan(lim) {
			needsDiscount = true
		}
	}
	if err := s.checkPayments(ctx, tx, in.Payments); err != nil {
		return 0, nil, err
	}
	applied, change, balance, err := splitPayments(in.Payments, tot.Total)
	if err != nil {
		return 0, nil, err
	}
	cust, err := s.resolveCustomer(ctx, tx, p.TenantID, in.CustomerID, balance)
	if err != nil {
		return 0, nil, err
	}
	if balance.IsPositive() && cust != nil {
		if err := checkCreditLimit(ctx, tx, p.TenantID, *cust, balance); err != nil {
			return 0, nil, err
		}
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return 0, nil, err
	}
	dueDate, err := resolveDueDate(ctx, tx, date, in.DueDate, balance.IsPositive())
	if err != nil {
		return 0, nil, err
	}
	// State machine requires new sales to start in 'draft'.
	var saleID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO app.sales (tenant_id, business_unit_id, location_id, status, customer_id, sold_by, device_id, client_ref,
		                       business_date, subtotal, discount_type, discount_value, discount_amount, tax_amount, total, due_date, notes)
		VALUES ($1, (SELECT business_unit_id FROM app.locations WHERE tenant_id = $1 AND id = $2), $2, 'draft', $3, $4, $5, $6,
		        $7::date, $8, $9::app.discount_type, $10, $11, $12, $13, $14::date, NULLIF($15,''))
		RETURNING id`,
		p.TenantID, *p.BranchID, cust, p.MembershipID, in.DeviceID, in.ClientRef,
		date, tot.Subtotal, tot.DiscountType, tot.DiscountValue, tot.Discount, tot.Tax, tot.Total, dueDate, strings.TrimSpace(in.Note)).Scan(&saleID); err != nil {
		return 0, nil, err
	}
	if err := insertLines(ctx, tx, p.TenantID, saleID, lines); err != nil {
		return 0, nil, err
	}
	if in.HeldID != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM app.sales WHERE tenant_id = $1 AND id = $2 AND status = 'held' AND sold_by = $3`, p.TenantID, *in.HeldID, p.MembershipID); err != nil {
			return 0, nil, err
		}
	}
	if needsDiscount {
		if _, err := tx.Exec(ctx, `UPDATE app.sales SET status = 'pending_approval' WHERE tenant_id = $1 AND id = $2`, p.TenantID, saleID); err != nil {
			return 0, nil, err
		}
		summary := "Sale needs approval for a discount of " + pct.StringFixed(2) + "%"
		reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
			BranchID: p.BranchID, Kind: "sale_discount", SubjectType: "sale", SubjectID: saleID,
			Amount:  decimal.NullDecimal{Decimal: totalDisc, Valid: true},
			Percent: decimal.NullDecimal{Decimal: pct, Valid: true},
			Summary: summary,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale.pending_approval", "sale", saleID, nil, map[string]any{"total": tot.Total, "discount_pct": pct}); err != nil {
			return 0, nil, err
		}
		return 202, map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": saleID}, nil
	}
	v, err := s.completeSale(ctx, tx, p, saleID, *p.BranchID, date, cust, lines, applied, tot, change)
	if err != nil {
		return 0, nil, err
	}
	return 201, v, nil
}

// Complete finishes a draft or approved sale by taking payments (if any) and marking it completed.
func (s *Service) Complete(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in CompleteIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var locID uuid.UUID
		var cust *uuid.UUID
		var st, date string
		var discType *string
		var sub, discVal, discAmt, taxAmt, total, paidSoFar decimal.Decimal
		var dueDate *string
		err := tx.QueryRow(ctx, `
			SELECT location_id, customer_id, status::text, business_date::text, subtotal, discount_type::text, discount_value,
			       discount_amount, tax_amount, total, amount_paid, due_date::text
			FROM app.sales WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, saleID).
			Scan(&locID, &cust, &st, &date, &sub, &discType, &discVal, &discAmt, &taxAmt, &total, &paidSoFar, &dueDate)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Sale not found")
		}
		if err != nil {
			return err
		}
		if st != "draft" && st != "held" && st != "pending_approval" {
			return httpx.Conflict("invalid_status", "This sale is already "+st)
		}
		if err := s.checkPayments(ctx, tx, in.Payments); err != nil {
			return err
		}
		remaining := total.Sub(paidSoFar)
		applied, change, balance, err := splitPayments(in.Payments, remaining)
		if err != nil {
			return err
		}
		if balance.IsPositive() {
			if !creditEnabled(ctx, tx) {
				return httpx.Conflict("method_disabled", "Credit sales are switched off in the settings")
			}
			if cust == nil {
				return httpx.Conflict("customer_required", "A named customer is required for a credit sale")
			}
			if err := checkCreditLimit(ctx, tx, p.TenantID, *cust, balance); err != nil {
				return err
			}
			if dueDate == nil || strings.TrimSpace(in.DueDate) != "" {
				d, err := resolveDueDate(ctx, tx, date, in.DueDate, true)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE app.sales SET due_date = $3::date WHERE tenant_id = $1 AND id = $2`, p.TenantID, saleID, d); err != nil {
					return err
				}
			}
		}
		lines, err := s.loadLinesStruct(ctx, tx, p.TenantID, saleID)
		if err != nil {
			return err
		}
		tot := totals{Subtotal: sub, DiscountType: discType, DiscountValue: discVal, Discount: discAmt, Tax: taxAmt, Total: total}
		v, err := s.completeSale(ctx, tx, p, saleID, locID, date, cust, lines, applied, tot, change)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(v))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) completeSale(ctx context.Context, tx pgx.Tx, p *auth.Principal, saleID, locID uuid.UUID, date string, cust *uuid.UUID, lines []line, applied []PayIn, tot totals, change decimal.Decimal) (map[string]any, error) {
	// Allocate payments first while sale is draft/held/pending_approval so sales.amount_paid is up to date
	// when status transitions to 'completed' (tg_sales_guard checks amount_paid vs total).
	for _, pay := range applied {
		if !pay.Amount.IsPositive() {
			continue
		}
		if _, err := payments.Receive(ctx, tx, payments.ReceiveInput{
			TenantID: p.TenantID, LocationID: locID, AccountID: pay.MoneyAccountID, CustomerID: cust,
			Purpose: "sale", Method: payments.Method(pay.Method), Amount: pay.Amount, Reference: strings.TrimSpace(pay.Reference),
			BusinessDate: date, Allocations: []payments.Alloc{{SaleID: &saleID, Amount: pay.Amount}},
		}); err != nil {
			return nil, err
		}
	}
	number, err := sequence.Next(ctx, tx, uuid.Nil, "sale_receipt")
	if err != nil {
		return nil, err
	}
	var paid, balance decimal.Decimal
	if err := tx.QueryRow(ctx, `
		UPDATE app.sales SET status = 'completed', receipt_no = $3, completed_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING amount_paid, balance_due`, p.TenantID, saleID, number).Scan(&paid, &balance); err != nil {
		return nil, err
	}
	// Reload unit_cost written by tg_sales_post for callers allowed to see cost.
	if canSeeCost(p) {
		rows, err := tx.Query(ctx, `SELECT product_id, COALESCE(unit_cost, 0) FROM app.sale_lines WHERE tenant_id = $1 AND sale_id = $2 ORDER BY line_no`, p.TenantID, saleID)
		if err == nil {
			costs := map[uuid.UUID]decimal.Decimal{}
			for rows.Next() {
				var pid uuid.UUID
				var c decimal.Decimal
				if rows.Scan(&pid, &c) == nil {
					costs[pid] = c
				}
			}
			rows.Close()
			for i := range lines {
				lines[i].UnitCost = costs[lines[i].ProductID]
			}
		}
	}
	pids := make([]uuid.UUID, len(lines))
	for i, ln := range lines {
		pids[i] = ln.ProductID
	}
	if err := jobs.EnqueueTx(ctx, tx, p.TenantID, jobs.CheckLowStock, map[string]any{"branch_id": locID, "product_ids": pids}); err != nil {
		return nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "sale.completed", "sale", saleID, nil, map[string]any{"number": number, "total": tot.Total}); err != nil {
		return nil, err
	}
	out := view(saleID, number, "completed", lines, tot, paid, balance, change, canSeeCost(p))
	out["receipt_url"] = "/api/v1/sales/" + saleID.String() + "/receipt"
	return out, nil
}

// OnApproved runs when a sale_discount approval request is approved. If the sale is a credit sale
// (customer and due date set) or already fully paid, it completes immediately; otherwise it returns
// to draft so the seller can take payment and complete it with the approved discount.
func (s *Service) OnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var locID uuid.UUID
	var cust *uuid.UUID
	var st, date string
	var discType *string
	var sub, discVal, discAmt, taxAmt, total, paid decimal.Decimal
	var dueDate *string
	err := tx.QueryRow(ctx, `
		SELECT location_id, customer_id, status::text, business_date::text, subtotal, discount_type::text, discount_value,
		       discount_amount, tax_amount, total, amount_paid, due_date::text
		FROM app.sales WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, p.TenantID, req.SubjectID).
		Scan(&locID, &cust, &st, &date, &sub, &discType, &discVal, &discAmt, &taxAmt, &total, &paid, &dueDate)
	if err != nil {
		return err
	}
	if st != "pending_approval" {
		return nil
	}
	if paid.Equal(total) || (cust != nil && dueDate != nil) {
		lines, err := s.loadLinesStruct(ctx, tx, p.TenantID, req.SubjectID)
		if err != nil {
			return err
		}
		tot := totals{Subtotal: sub, DiscountType: discType, DiscountValue: discVal, Discount: discAmt, Tax: taxAmt, Total: total}
		_, err = s.completeSale(ctx, tx, p, req.SubjectID, locID, date, cust, lines, nil, tot, money.Zero)
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE app.sales SET status = 'draft' WHERE tenant_id = $1 AND id = $2 AND status = 'pending_approval'`, p.TenantID, req.SubjectID)
	return err
}

func (s *Service) OnRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	// app.sale_status has no 'cancelled'; return to draft so the seller can adjust or discard it.
	if _, err := tx.Exec(ctx, `UPDATE app.sales SET status = 'draft' WHERE tenant_id = $1 AND id = $2 AND status = 'pending_approval'`, p.TenantID, req.SubjectID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale."+req.Status, "sale", req.SubjectID, nil, map[string]any{"approval_request_id": req.ID, "note": req.Note})
}

func insertLines(ctx context.Context, tx pgx.Tx, tenantID, saleID uuid.UUID, lines []line) error {
	for i, ln := range lines {
		var lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.sale_lines (tenant_id, sale_id, line_no, product_id, product_name, quantity, unit_price,
			                            line_discount, tax_rate_pct, tax_amount, line_total, warranty_months)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
			RETURNING id`,
			tenantID, saleID, i+1, ln.ProductID, ln.Name, ln.Qty, ln.UnitPrice,
			ln.Discount, ln.TaxRate, ln.Tax, ln.LineTotal, ln.WarrantyMonths).Scan(&lineID); err != nil {
			return err
		}
		for _, uid := range ln.UnitIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO app.sale_line_units (tenant_id, sale_line_id, product_unit_id) VALUES ($1,$2,$3)`, tenantID, lineID, uid); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) loadLinesStruct(ctx context.Context, tx pgx.Tx, tenantID, saleID uuid.UUID) ([]line, error) {
	rows, err := tx.Query(ctx, `
		SELECT product_id, product_name::text, quantity, unit_price, line_discount, tax_rate_pct, tax_amount, line_total, COALESCE(unit_cost,0), warranty_months
		FROM app.sale_lines WHERE tenant_id = $1 AND sale_id = $2 ORDER BY line_no`, tenantID, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lines []line
	for rows.Next() {
		var ln line
		if err := rows.Scan(&ln.ProductID, &ln.Name, &ln.Qty, &ln.UnitPrice, &ln.Discount, &ln.TaxRate, &ln.Tax, &ln.LineTotal, &ln.UnitCost, &ln.WarrantyMonths); err != nil {
			return nil, err
		}
		lines = append(lines, ln)
	}
	return lines, rows.Err()
}

// price resolves products, serial units (when resolveUnits is true) and computes totals matching the
// app.sales and app.sale_lines CHECK constraints:
//
//	sale_lines.line_total = round(quantity * unit_price, 2) - line_discount
//	sales.subtotal        = sum(sale_lines.line_total)
//	sales.total           = sales.subtotal - sales.discount_amount
func (s *Service) price(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CreateIn, resolveUnits bool) ([]line, totals, error) {
	var lines []line
	var tot totals
	for _, it := range in.Items {
		if it.Qty.LessThanOrEqual(money.Zero) {
			return nil, totals{}, httpx.BadRequest("invalid_body", "Quantity must be greater than zero")
		}
		var name string
		var sellPrice, avgCost, rate decimal.Decimal
		var warranty int
		var tracksSerial bool
		err := tx.QueryRow(ctx, `
			SELECT p.name::text, p.selling_price, p.average_cost, p.warranty_months, p.tracks_serial,
			       COALESCE(tr.rate_pct, def.rate_pct, 0)
			FROM app.products p
			LEFT JOIN app.tax_rates tr  ON tr.tenant_id = p.tenant_id AND tr.id = p.tax_rate_id AND tr.status = 'active'
			LEFT JOIN app.tax_rates def ON def.tenant_id = p.tenant_id AND def.is_default AND def.status = 'active'
			WHERE p.tenant_id = $1 AND p.id = $2 AND p.status = 'active'`,
			p.TenantID, it.ProductID).Scan(&name, &sellPrice, &avgCost, &warranty, &tracksSerial, &rate)
		if err != nil {
			return nil, totals{}, httpx.NotFound("not_found", "Product not found or inactive")
		}
		gross := money.Money(it.Qty.Mul(sellPrice))
		lineDisc := money.Zero
		if it.Discount != nil {
			if err := checkDiscount(it.Discount); err != nil {
				return nil, totals{}, err
			}
			lineDisc = money.DiscountAmount(gross, it.Discount.Type, it.Discount.Value)
			if lineDisc.GreaterThan(gross) {
				lineDisc = gross
			}
		}
		lineTotal := gross.Sub(lineDisc)
		// Prices include tax in the approved model; tax_amount is the informational VAT portion of line_total.
		taxAmt := money.PriceLine(money.LineInput{Qty: it.Qty, UnitPrice: sellPrice, Discount: lineDisc, TaxRate: rate}, true).Tax

		var unitIDs []uuid.UUID
		var serials []string
		if resolveUnits {
			unitIDs, serials, err = resolveSerialUnits(ctx, tx, p.TenantID, *p.BranchID, it.ProductID, tracksSerial, it)
			if err != nil {
				return nil, totals{}, err
			}
		}
		lines = append(lines, line{
			ProductID: it.ProductID, Name: name, Qty: it.Qty, UnitPrice: sellPrice, Discount: lineDisc,
			TaxRate: rate, Tax: taxAmt, LineTotal: lineTotal, UnitCost: avgCost,
			WarrantyMonths: warranty, TracksSerial: tracksSerial, UnitIDs: unitIDs, Serials: serials,
		})
		tot.Subtotal = tot.Subtotal.Add(lineTotal)
		tot.LineDiscount = tot.LineDiscount.Add(lineDisc)
		tot.Tax = tot.Tax.Add(taxAmt)
	}
	tot.Total = tot.Subtotal
	if in.Discount != nil && in.Discount.Value.IsPositive() {
		if err := checkDiscount(in.Discount); err != nil {
			return nil, totals{}, err
		}
		dt := in.Discount.Type
		tot.DiscountType = &dt
		tot.DiscountValue = money.Money(in.Discount.Value)
		tot.Discount = money.DiscountAmount(tot.Subtotal, dt, in.Discount.Value)
		if tot.Discount.GreaterThan(tot.Subtotal) {
			tot.Discount = tot.Subtotal
		}
		tot.Total = tot.Subtotal.Sub(tot.Discount)
		if tot.Subtotal.IsPositive() && tot.Tax.IsPositive() {
			tot.Tax = money.Money(tot.Tax.Mul(tot.Total).Div(tot.Subtotal))
		}
	}
	return lines, tot, nil
}

func resolveSerialUnits(ctx context.Context, tx pgx.Tx, tenantID, locID, productID uuid.UUID, required bool, it ItemIn) ([]uuid.UUID, []string, error) {
	var rawSerials []string
	if sn := strings.TrimSpace(it.SerialNumber); sn != "" {
		rawSerials = append(rawSerials, sn)
	}
	for _, sn := range it.SerialNumbers {
		if s := strings.TrimSpace(sn); s != "" {
			rawSerials = append(rawSerials, s)
		}
	}
	if !required && len(rawSerials) == 0 && len(it.UnitIDs) == 0 {
		return nil, nil, nil
	}
	seen := map[uuid.UUID]bool{}
	var unitIDs []uuid.UUID
	var serials []string
	for _, uid := range it.UnitIDs {
		var sn string
		err := tx.QueryRow(ctx, `
			SELECT serial_no FROM app.product_units
			WHERE tenant_id = $1 AND id = $2 AND product_id = $3 AND location_id = $4 AND status = 'in_stock'`,
			tenantID, uid, productID, locID).Scan(&sn)
		if err != nil {
			return nil, nil, httpx.Conflict("serial_unavailable", "Serial unit is not in stock at this location")
		}
		if !seen[uid] {
			seen[uid] = true
			unitIDs = append(unitIDs, uid)
			serials = append(serials, sn)
		}
	}
	for _, sn := range rawSerials {
		var uid uuid.UUID
		var foundSN string
		err := tx.QueryRow(ctx, `
			SELECT id, serial_no FROM app.product_units
			WHERE tenant_id = $1 AND product_id = $2 AND location_id = $3 AND status = 'in_stock' AND (serial_no = $4 OR imei = $4)`,
			tenantID, productID, locID, sn).Scan(&uid, &foundSN)
		if err != nil {
			return nil, nil, httpx.Conflict("serial_unavailable", "Serial number "+sn+" is not in stock at this location")
		}
		if !seen[uid] {
			seen[uid] = true
			unitIDs = append(unitIDs, uid)
			serials = append(serials, foundSN)
		}
	}
	if required && !decimal.NewFromInt(int64(len(unitIDs))).Equal(it.Qty) {
		return nil, nil, httpx.BadRequest("serial_required", "Provide one in-stock serial number or IMEI per unit of this product")
	}
	return unitIDs, serials, nil
}

func (s *Service) checkPayments(ctx context.Context, tx pgx.Tx, pays []PayIn) error {
	disabled := map[string]bool{}
	if raw, err := setting.Raw(ctx, tx, "payment.methods"); err == nil {
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
		m := payments.Method(pay.Method)
		if m == "" {
			return httpx.BadRequest("invalid_body", "Payment method must be cash, mobile_money, bank, card or cheque")
		}
		if disabled[pay.Method] || disabled[payments.MethodOut(m)] {
			return httpx.Conflict("method_disabled", "This payment method is switched off in the settings")
		}
		if m != "cash" && strings.TrimSpace(pay.Reference) == "" {
			return &httpx.Error{Status: 422, Code: "validation_failed", Message: "A reference is required for non-cash payments", Fields: map[string]string{"reference": "required"}}
		}
		if _, err := payments.ActiveFin(ctx, tx, pay.MoneyAccountID); err != nil {
			return err
		}
	}
	return nil
}

func creditEnabled(ctx context.Context, tx pgx.Tx) bool {
	raw, err := setting.Raw(ctx, tx, "payment.methods")
	if err != nil {
		return true
	}
	var cfg struct {
		Credit struct {
			Enabled *bool `json:"enabled"`
		} `json:"credit"`
	}
	return json.Unmarshal(raw, &cfg) != nil || cfg.Credit.Enabled == nil || *cfg.Credit.Enabled
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
			if payments.Method(pay.Method) != "cash" {
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

// resolveCustomer returns nil for walk-in sales (no customer row in the approved schema).
func (s *Service) resolveCustomer(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, id *uuid.UUID, balance decimal.Decimal) (*uuid.UUID, error) {
	if balance.IsPositive() && !creditEnabled(ctx, tx) {
		return nil, httpx.Conflict("method_disabled", "Credit sales are switched off in the settings")
	}
	if id == nil || *id == uuid.Nil {
		if balance.IsPositive() {
			return nil, httpx.Conflict("customer_required", "A named customer is required for a credit sale")
		}
		return nil, nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM app.customers WHERE tenant_id = $1 AND id = $2 AND archived_at IS NULL`, tenantID, *id).Scan(&exists); err != nil {
		return nil, httpx.NotFound("not_found", "Customer not found")
	}
	cid := *id
	return &cid, nil
}

func checkCreditLimit(ctx context.Context, tx pgx.Tx, tenantID, customerID uuid.UUID, added decimal.Decimal) error {
	var owed decimal.Decimal
	var limit *decimal.Decimal
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(s.balance_due) FROM app.sales s WHERE s.tenant_id = c.tenant_id AND s.customer_id = c.id AND s.status = 'completed'), 0),
		       c.credit_limit
		FROM app.customers c WHERE c.tenant_id = $1 AND c.id = $2`, tenantID, customerID).Scan(&owed, &limit); err != nil {
		return err
	}
	if limit != nil && owed.Add(added).GreaterThan(*limit) {
		return httpx.Conflict("credit_limit_exceeded", "This sale goes over the customer's credit limit")
	}
	return nil
}

func resolveDueDate(ctx context.Context, tx pgx.Tx, bizDate, raw string, required bool) (*string, error) {
	if s := strings.TrimSpace(raw); s != "" {
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return nil, httpx.BadRequest("invalid_body", "due_date must be YYYY-MM-DD")
		}
		return &s, nil
	}
	if !required {
		return nil, nil
	}
	days := setting.Int(ctx, tx, "invoice.due_days", 0)
	d := mustDate(bizDate).AddDate(0, 0, days).Format("2006-01-02")
	return &d, nil
}

func mustDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

func canSeeCost(p *auth.Principal) bool {
	return p.Has("shop.product.view_cost")
}

func view(id uuid.UUID, number, status string, lines []line, tot totals, paid, balance, change decimal.Decimal, showCost bool) map[string]any {
	items := make([]map[string]any, len(lines))
	for i, ln := range lines {
		items[i] = map[string]any{"product_id": ln.ProductID, "product_name": ln.Name, "qty": ln.Qty, "unit_price": ln.UnitPrice,
			"discount": ln.Discount, "tax_amount": ln.Tax, "line_total": ln.LineTotal, "warranty_months": ln.WarrantyMonths}
		if len(ln.Serials) == 1 {
			items[i]["serial_number"] = ln.Serials[0]
		} else if len(ln.Serials) > 1 {
			items[i]["serial_numbers"] = ln.Serials
		}
		if showCost {
			items[i]["unit_cost"] = ln.UnitCost
		}
	}
	out := map[string]any{
		"status": status, "subtotal": tot.Subtotal, "discount_total": tot.Discount.Add(tot.LineDiscount), "sale_discount": tot.Discount,
		"tax_total": tot.Tax, "total": tot.Total, "paid_total": paid, "balance": balance, "change_due": change, "items": items,
	}
	if id != uuid.Nil {
		out["id"] = id
		out["number"] = number
	}
	return out
}
