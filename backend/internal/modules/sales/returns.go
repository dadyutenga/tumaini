package sales

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type ReturnItemIn struct {
	SaleItemID uuid.UUID       `json:"sale_item_id"`
	Qty        decimal.Decimal `json:"qty"`
	Restock    *bool           `json:"restock"`
}

type ReturnIn struct {
	Reason       string         `json:"reason"`
	RefundMethod string         `json:"refund_method"`
	AccountID    *uuid.UUID     `json:"money_account_id"`
	Items        []ReturnItemIn `json:"items"`
}

var refundMethods = map[string]bool{"cash": true, "mobile_money": true, "bank": true, "reduce_debt": true, "customer_credit": true}

func (s *Service) CreateReturn(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in ReturnIn) (int, []byte, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return 0, nil, httpx.BadRequest("invalid_body", "Give a reason for the return")
	}
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose at least one item to return")
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
		var branch uuid.UUID
		var cust *uuid.UUID
		var bal decimal.Decimal
		var number string
		if err := tx.QueryRow(ctx, `SELECT branch_id, customer_id, balance, number FROM sales WHERE id=$1 AND kind='sale' AND status IN ('completed','partially_refunded') FOR UPDATE`, saleID).Scan(&branch, &cust, &bal, &number); err != nil {
			return httpx.NotFound("not_found", "Sale not found")
		}
		if !p.SeesBranch("sales.refund_request", branch) {
			return httpx.NotFound("not_found", "Sale not found")
		}
		amount := money.Zero
		var rets []retLine
		seen := map[uuid.UUID]bool{}
		for _, it := range in.Items {
			if !it.Qty.IsPositive() {
				return httpx.BadRequest("invalid_body", "Return quantity must be greater than zero")
			}
			if seen[it.SaleItemID] {
				return httpx.BadRequest("invalid_body", "Each sale line can appear once")
			}
			seen[it.SaleItemID] = true
			var qty, returned, pending, lineTotal, taxAmt, unitCost decimal.Decimal
			var product uuid.UUID
			if err := tx.QueryRow(ctx, `
				SELECT si.qty, si.qty_returned, si.line_total, si.tax_amount, si.unit_cost, si.product_id,
				       COALESCE((SELECT SUM(ri.qty) FROM sale_return_items ri JOIN sale_returns r ON r.id=ri.return_id
				                 WHERE ri.sale_item_id=si.id AND r.status='pending_approval'),0)
				FROM sale_items si WHERE si.id=$1 AND si.sale_id=$2
			`, it.SaleItemID, saleID).Scan(&qty, &returned, &lineTotal, &taxAmt, &unitCost, &product, &pending); err != nil {
				return httpx.NotFound("not_found", "Sale line not found")
			}
			if returned.Add(pending).Add(it.Qty).GreaterThan(qty) {
				return httpx.Conflict("return_exceeds_sold", "Returned quantity cannot exceed the quantity sold less earlier returns")
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
		method := in.RefundMethod
		if method == "" {
			if bal.IsPositive() {
				method = "reduce_debt"
			} else {
				method = "cash"
			}
		}
		if !refundMethods[method] {
			return httpx.BadRequest("invalid_body", "Refund method must be cash, mobile_money, bank, reduce_debt or customer_credit")
		}
		if err := s.checkRefund(ctx, tx, method, in.AccountID, cust, bal, amount); err != nil {
			return err
		}
		g, ok := p.Can("sales.refund_approve")
		auto := ok && auth.WithinLimit(g, ok, amount)
		retNumber, err := sequence.Next(ctx, tx, branch, "return")
		if err != nil {
			return err
		}
		rid := ids.New()
		st := "pending_approval"
		if auto {
			st = "completed"
		}
		var approvedBy any
		if auto {
			approvedBy = p.UserID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sale_returns (id, business_id, branch_id, number, sale_id, status, reason, refund_method, money_account_id, refund_total, requested_by, approved_by, completed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, CASE WHEN $6='completed' THEN now() ELSE NULL END)
		`, rid, p.BusinessID, branch, retNumber, saleID, st, strings.TrimSpace(in.Reason), method, in.AccountID, amount, p.UserID, approvedBy); err != nil {
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
		if err := s.Audit.Record(ctx, tx, p, "sale_return.requested", "sale_return", rid, nil, map[string]any{"number": retNumber, "sale_id": saleID, "refund_total": amount, "method": method, "reason": in.Reason}); err != nil {
			return err
		}
		if !auto {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &branch, Kind: "refund", SubjectType: "sale_return", SubjectID: rid,
				Amount: decimal.NullDecimal{Decimal: amount, Valid: true}, Summary: "Refund " + retNumber + " on sale " + number + ": " + in.Reason,
			})
			if err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": rid, "number": retNumber}))
			status, body = 202, raw
			return err
		}
		if err := s.applyReturn(ctx, tx, p, rid, saleID, branch, cust, method, in.AccountID, amount, rets); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": rid, "number": retNumber, "status": "completed", "refund_total": amount, "refund_method": method}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) checkRefund(ctx context.Context, tx pgx.Tx, method string, account *uuid.UUID, cust *uuid.UUID, bal, amount decimal.Decimal) error {
	switch method {
	case "reduce_debt":
		if bal.LessThan(amount) {
			return httpx.Conflict("invalid_status", "The refund is larger than what the customer still owes on this sale. Refund in money or as customer credit")
		}
	case "customer_credit":
		var walk bool
		if cust == nil || tx.QueryRow(ctx, `SELECT is_walk_in FROM customers WHERE id=$1`, *cust).Scan(&walk) != nil || walk {
			return httpx.Conflict("customer_required", "Customer credit needs a named customer")
		}
	default:
		if account == nil {
			return httpx.BadRequest("invalid_body", "Choose the account to refund from")
		}
		return checkAccount(ctx, tx, *account)
	}
	return nil
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
		tag, err := tx.Exec(ctx, `UPDATE sale_items SET qty_returned = qty_returned + $2 WHERE id=$1 AND qty_returned + $2 <= qty`, ln.item, ln.qty)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.Conflict("return_exceeds_sold", "Returned quantity cannot exceed the quantity sold less earlier returns")
		}
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
		if method == "reduce_debt" {
			tag, err := tx.Exec(ctx, `UPDATE sales SET total=total-$2, balance=balance-$2, tax_total=tax_total-$3, cost_total=cost_total-$4 WHERE id=$1 AND balance >= $2`, saleID, amount, taxSum, costSum)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return httpx.Conflict("invalid_status", "The refund is larger than what the customer still owes on this sale")
			}
		} else if _, err := tx.Exec(ctx, `UPDATE sales SET total=total-$2, paid_total=paid_total-$2, tax_total=tax_total-$3, cost_total=cost_total-$4 WHERE id=$1`, saleID, amount, taxSum, costSum); err != nil {
			return err
		}
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
			Amount: amount, Date: date, AllocType: "sale", AllocID: saleID, AllocAmount: amount, Reference: "",
		}); err != nil {
			return err
		}
		j = append(j, ledger.Line{Code: code, BranchID: &branch, MoneyAccountID: account, PartyType: "customer", PartyID: cust, Credit: amount})
		if _, err := tx.Exec(ctx, `UPDATE sales SET total=total-$2, paid_total=paid_total-$2, tax_total=tax_total-$3, cost_total=cost_total-$4 WHERE id=$1`, saleID, amount, taxSum, costSum); err != nil {
			return err
		}
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
	if _, err := tx.Exec(ctx, `
		UPDATE sales SET status = CASE
			WHEN (SELECT COALESCE(SUM(qty-qty_returned),0) FROM sale_items WHERE sale_id=$1) = 0 THEN 'refunded'
			ELSE 'partially_refunded' END
		WHERE id=$1
	`, saleID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale_return.completed", "sale_return", returnID, nil, map[string]any{"sale_id": saleID, "refund_total": amount, "method": method})
}

func (s *Service) OnReturnApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var saleID, branch uuid.UUID
	var method, st string
	var account *uuid.UUID
	var amount decimal.Decimal
	var cust *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT r.sale_id, r.branch_id, r.refund_method, r.money_account_id, r.refund_total, s.customer_id, r.status
		FROM sale_returns r JOIN sales s ON s.id=r.sale_id WHERE r.id=$1 FOR UPDATE OF r
	`, req.SubjectID).Scan(&saleID, &branch, &method, &account, &amount, &cust, &st); err != nil {
		return err
	}
	if st != "pending_approval" {
		return httpx.Conflict("invalid_status", "This return is no longer waiting for approval")
	}
	rows, err := tx.Query(ctx, `
		SELECT ri.sale_item_id, si.product_id, ri.qty, ri.unit_cost, ri.amount, round(si.tax_amount * ri.qty / si.qty, 2), ri.restock
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
	if err := rows.Err(); err != nil {
		return err
	}
	if err := s.applyReturn(ctx, tx, p, req.SubjectID, saleID, branch, cust, method, account, amount, lines); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE sale_returns SET status='completed', approved_by=$2, completed_at=now() WHERE id=$1`, req.SubjectID, p.UserID)
	return err
}

// OnReturnRejected closes a refund request that was rejected or cancelled.
func (s *Service) OnReturnRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	if _, err := tx.Exec(ctx, `UPDATE sale_returns SET status='rejected' WHERE id=$1 AND status='pending_approval'`, req.SubjectID); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale_return."+req.Status, "sale_return", req.SubjectID, nil, map[string]any{"note": req.Note})
}

type ReturnFilter struct {
	SaleID *uuid.UUID
	Status string
	From   string
	To     string
}

// Returns lists sale returns inside the caller's sales.view scope.
func (s *Service) Returns(ctx context.Context, p *auth.Principal, f ReturnFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("sales.view"); b != nil {
			q.Add("r.branch_id = ANY(?)", b)
		}
		if own := p.OwnOnly("sales.view"); own != nil {
			q.Add("(r.requested_by = ? OR s.cashier_id = ?)", *own, *own)
		}
		q.If(f.SaleID != nil, "r.sale_id = ?", f.SaleID)
		q.If(f.Status != "", "r.status = ?", f.Status)
		q.If(f.From != "", "r.created_at >= ?::date", f.From)
		q.If(f.To != "", "r.created_at < ?::date + 1", f.To)
		from := ` FROM sale_returns r JOIN sales s ON s.id=r.sale_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.number, r.sale_id, s.number, r.status, r.reason, r.refund_method, r.refund_total, r.requested_by, r.approved_by, r.created_at, r.completed_at, r.branch_id`+
			from+q.Where()+` ORDER BY r.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanReturn(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, total, err
}

func scanReturn(row pgx.Row) (map[string]any, error) {
	var id, sale, by, branch uuid.UUID
	var approved *uuid.UUID
	var number, saleNo, status, reason, method string
	var amount decimal.Decimal
	var created, completed any
	if err := row.Scan(&id, &number, &sale, &saleNo, &status, &reason, &method, &amount, &by, &approved, &created, &completed, &branch); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "sale_id": sale, "sale_number": saleNo, "status": status, "reason": reason,
		"refund_method": method, "refund_total": amount, "requested_by": by, "approved_by": approved, "created_at": created,
		"completed_at": completed, "branch_id": branch}, nil
}

// Return is one sale return with its lines.
func (s *Service) Return(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var cashier uuid.UUID
		m, err := scanReturn(tx.QueryRow(ctx, `
			SELECT r.id, r.number, r.sale_id, s.number, r.status, r.reason, r.refund_method, r.refund_total, r.requested_by, r.approved_by, r.created_at, r.completed_at, r.branch_id
			FROM sale_returns r JOIN sales s ON s.id=r.sale_id WHERE r.id=$1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT cashier_id FROM sales WHERE id=$1`, m["sale_id"]).Scan(&cashier); err != nil {
			return err
		}
		if !p.SeesBranch("sales.view", m["branch_id"].(uuid.UUID)) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if own := p.OwnOnly("sales.view"); own != nil && m["requested_by"] != *own && cashier != *own {
			return httpx.NotFound("not_found", "Return not found")
		}
		rows, err := tx.Query(ctx, `
			SELECT ri.id, ri.sale_item_id, si.product_id, si.product_name, ri.qty, ri.restock, ri.amount
			FROM sale_return_items ri JOIN sale_items si ON si.id=ri.sale_item_id WHERE ri.return_id=$1 ORDER BY si.product_name`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var rid, item, product uuid.UUID
			var name string
			var qty, amt decimal.Decimal
			var restock bool
			if err := rows.Scan(&rid, &item, &product, &name, &qty, &restock, &amt); err != nil {
				return err
			}
			items = append(items, map[string]any{"id": rid, "sale_item_id": item, "product_id": product, "product_name": name, "qty": qty, "restock": restock, "amount": amt})
		}
		m["items"] = items
		out = m
		return rows.Err()
	})
	return out, err
}
