package sales

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/notify"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type ReturnItemIn struct {
	SaleItemID    uuid.UUID       `json:"sale_item_id"`
	Qty           decimal.Decimal `json:"qty"`
	Restock       *bool           `json:"restock"`
	Condition     string          `json:"condition"`
	ProductUnitID *uuid.UUID      `json:"product_unit_id,omitempty"`
}

type ReturnIn struct {
	Reason     string         `json:"reason"`
	Resolution string         `json:"resolution"` // refund (default) or exchange
	Items      []ReturnItemIn `json:"items"`
}

type CompleteReturnIn struct {
	AccountID      *uuid.UUID `json:"money_account_id"`
	Method         string     `json:"method"`
	Reference      string     `json:"reference"`
	ExchangeSaleID *uuid.UUID `json:"exchange_sale_id,omitempty"`
}

type retLine struct {
	saleLineID    uuid.UUID
	qty, amt      decimal.Decimal
	condition     string
	restock       bool
	productUnitID *uuid.UUID
}

// CreateReturn files a return request on a completed sale. Approval is a separate step on the
// return itself (app.sale_returns requires decided_by <> requested_by and checks shop.return.approve).
func (s *Service) CreateReturn(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in ReturnIn) (int, []byte, error) {
	reason := strings.TrimSpace(in.Reason)
	if len(reason) < 3 {
		return 0, nil, httpx.BadRequest("invalid_body", "Give a reason of at least 3 characters for the return")
	}
	if len(in.Items) == 0 {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose at least one item to return")
	}
	resolution := strings.ToLower(strings.TrimSpace(in.Resolution))
	if resolution == "" {
		resolution = "refund"
	}
	if resolution != "refund" && resolution != "exchange" {
		return 0, nil, httpx.BadRequest("invalid_body", "Resolution must be refund or exchange")
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
		var locID uuid.UUID
		var number string
		var subtotal, saleTotal, paid decimal.Decimal
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT location_id, COALESCE(receipt_no, ''), subtotal, total, amount_paid\n\t\t\tFROM app.sales WHERE id = $1 AND status = 'completed' FOR UPDATE", saleID).
			Scan(&locID, &number, &subtotal, &saleTotal, &paid); err != nil {
			return httpx.NotFound("not_found", "Completed sale not found")
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
			var qty, returned, pending, lineTotal decimal.Decimal
			if err := tx.QueryRow(ctx, "\n\t\t\t\tSELECT sl.quantity, sl.returned_quantity, sl.line_total,\n\t\t\t\t       COALESCE((SELECT SUM(rl.quantity) FROM app.sale_return_lines rl\n\t\t\t\t                 JOIN app.sale_returns r ON r.id = rl.sale_return_id\n\t\t\t\t                 WHERE rl.sale_line_id = sl.id AND r.status IN ('requested','approved')), 0)\n\t\t\t\tFROM app.sale_lines sl WHERE sl.id = $1 AND sl.sale_id = $2", it.SaleItemID, saleID).Scan(&qty, &returned, &lineTotal, &pending); err != nil {
				return httpx.NotFound("not_found", "Sale line not found")
			}
			if returned.Add(pending).Add(it.Qty).GreaterThan(qty) {
				return httpx.Conflict("return_exceeds_sold", "Returned quantity cannot exceed the quantity sold less earlier returns")
			}
			restock := true
			if it.Restock != nil {
				restock = *it.Restock
			}
			cond := strings.ToLower(strings.TrimSpace(it.Condition))
			if cond == "" {
				if restock {
					cond = "resellable"
				} else {
					cond = "damaged"
				}
			}
			if cond != "resellable" && cond != "damaged" {
				return httpx.BadRequest("invalid_body", "Item condition must be resellable or damaged")
			}
			if restock && cond != "resellable" {
				return httpx.BadRequest("invalid_body", "Damaged items cannot be restocked")
			}
			part := money.Zero
			if resolution == "refund" {
				netLine := lineTotal
				if subtotal.IsPositive() && saleTotal.LessThan(subtotal) {
					netLine = lineTotal.Mul(saleTotal).Div(subtotal)
				}
				part = money.Money(netLine.Mul(it.Qty).Div(qty))
			}
			rets = append(rets, retLine{
				saleLineID: it.SaleItemID, qty: it.Qty, amt: part, condition: cond,
				restock: restock, productUnitID: it.ProductUnitID,
			})
			amount = amount.Add(part)
		}
		if resolution == "refund" {
			if amount.LessThanOrEqual(money.Zero) {
				return httpx.BadRequest("invalid_body", "Refund amount must be greater than zero")
			}
			var existingRefunds decimal.Decimal
			if err := tx.QueryRow(ctx, "\n\t\t\t\tSELECT COALESCE(SUM(refund_amount), 0) FROM app.sale_returns\n\t\t\t\tWHERE sale_id = $1 AND status IN ('requested','approved','completed')", saleID).Scan(&existingRefunds); err != nil {
				return err
			}
			if existingRefunds.Add(amount).GreaterThan(paid) {
				return httpx.Conflict("refund_exceeds_paid", "Refund amount cannot exceed the amount paid on this sale")
			}
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		retNumber, err := sequence.Next(ctx, tx, uuid.Nil, "sale_return")
		if err != nil {
			return err
		}
		var rid uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.sale_returns (return_no, sale_id, location_id, status, resolution, reason, refund_amount, business_date)\n\t\t\tVALUES ($1, $2, $3, 'requested', $4::app.return_resolution, $5, $6, $7::date)\n\t\t\tRETURNING id", retNumber, saleID, locID, resolution, reason, amount, date).Scan(&rid); err != nil {
			return err
		}
		for _, r := range rets {
			if _, err := tx.Exec(ctx, "\n\t\t\t\tINSERT INTO app.sale_return_lines (sale_return_id, sale_line_id, quantity, condition, restock, refund_amount, product_unit_id)\n\t\t\t\tVALUES ($1, $2, $3, $4::app.item_condition, $5, $6, $7)", rid, r.saleLineID, r.qty, r.condition, r.restock, r.amt, r.productUnitID); err != nil {
				return err
			}
		}
		var amtPtr *decimal.Decimal
		if amount.IsPositive() {
			a := amount
			amtPtr = &a
		}
		_ = s.Notify.ToApprovers(ctx, tx, &locID, "shop.return.approve", amtPtr, p.UserID, notify.Message{
			Type: "approval_waiting", Title: "Return awaiting approval",
			Body: "Return " + retNumber + " on sale " + number + ": " + reason,
			Link: "/sale-returns/" + rid.String(), EntityType: "sale_return", EntityID: &rid,
		})
		if err := s.Audit.Record(ctx, tx, p, "sale_return.requested", "sale_return", rid, nil, map[string]any{
			"number": retNumber, "sale_id": saleID, "resolution": resolution, "refund_total": amount, "reason": reason,
		}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(map[string]any{
			"id": rid, "number": retNumber, "sale_id": saleID, "status": "requested", "resolution": resolution, "refund_total": amount,
		}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

// DecideReturn approves or rejects a requested return. The database enforces separation of duties
// (decided_by <> requested_by) and the approver's refund_approve_amount authority limit.
func (s *Service) DecideReturn(ctx context.Context, p *auth.Principal, id uuid.UUID, approve bool) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var st string
		var requestedBy uuid.UUID
		err := tx.QueryRow(ctx, "SELECT status::text, requested_by FROM app.sale_returns WHERE id = $1 FOR UPDATE", id).Scan(&st, &requestedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if err != nil {
			return err
		}
		if st != "requested" {
			return httpx.Conflict("invalid_status", "This return is no longer waiting for a decision")
		}
		if requestedBy == p.MembershipID {
			return httpx.Forbidden("cannot_approve_own", "You cannot decide your own return request")
		}
		next := "rejected"
		if approve {
			next = "approved"
		}
		if _, err := tx.Exec(ctx, "UPDATE app.sale_returns SET status = $2::app.return_status WHERE id = $1", id, next); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale_return."+next, "sale_return", id, nil, map[string]any{"status": next}); err != nil {
			return err
		}
		out = map[string]any{"id": id, "status": next}
		return nil
	})
	return out, err
}

// CancelReturn cancels a requested or approved return before completion.
func (s *Service) CancelReturn(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var st string
		var requestedBy uuid.UUID
		err := tx.QueryRow(ctx, "SELECT status::text, requested_by FROM app.sale_returns WHERE id = $1 FOR UPDATE", id).Scan(&st, &requestedBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if err != nil {
			return err
		}
		if !p.Has("shop.return.approve") {
			if requestedBy != p.MembershipID {
				return httpx.NotFound("not_found", "Return not found")
			}
			if st != "requested" {
				return httpx.Forbidden("permission_denied", "Only an approver can cancel an approved return")
			}
		}
		if st != "requested" && st != "approved" {
			return httpx.Conflict("invalid_status", "This return cannot be cancelled")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.sale_returns SET status = 'cancelled' WHERE id = $1", id); err != nil {
			return err
		}
		out = map[string]any{"id": id, "status": "cancelled"}
		return s.Audit.Record(ctx, tx, p, "sale_return.cancelled", "sale_return", id, nil, out)
	})
	return out, err
}

// CompleteReturn finalises an approved return: the database restocks eligible lines, updates serial
// units and reverses commissions; for a refund resolution, a payment out is posted from AccountID.
func (s *Service) CompleteReturn(ctx context.Context, p *auth.Principal, id uuid.UUID, in CompleteReturnIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var saleID, locID uuid.UUID
		var custID *uuid.UUID
		var st, resolution, retNo string
		var refundAmt decimal.Decimal
		err := tx.QueryRow(ctx, "\n\t\t\tSELECT r.sale_id, r.location_id, s.customer_id, r.status::text, r.resolution::text, r.return_no, r.refund_amount\n\t\t\tFROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id\n\t\t\tWHERE r.id = $1 FOR UPDATE OF r, s", id).
			Scan(&saleID, &locID, &custID, &st, &resolution, &retNo, &refundAmt)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if err != nil {
			return err
		}
		if st != "approved" {
			return httpx.Conflict("invalid_status", "Only an approved return can be completed")
		}
		if resolution == "refund" && refundAmt.IsPositive() && in.AccountID == nil {
			return httpx.BadRequest("invalid_body", "Choose the money account to refund from")
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.sale_returns SET status = 'completed', completed_at = now(), exchange_sale_id = COALESCE($2, exchange_sale_id)\n\t\t\tWHERE id = $1", id, in.ExchangeSaleID); err != nil {
			return err
		}
		out := map[string]any{"id": id, "number": retNo, "sale_id": saleID, "status": "completed", "resolution": resolution, "refund_total": refundAmt}
		if resolution == "refund" && refundAmt.IsPositive() && in.AccountID != nil {
			date, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			otherPayee := ""
			if custID == nil {
				otherPayee = "Walk-in customer"
			}
			method := ""
			if in.Method != "" {
				method = payments.Method(in.Method)
				if method == "" {
					return httpx.BadRequest("invalid_body", "Payment method must be cash, mobile_money, bank, card or cheque")
				}
			}
			pay, err := payments.Pay(ctx, tx, payments.PayInput{
				LocationID: locID,
				AccountID:  *in.AccountID,
				CustomerID: custID, OtherPayee: otherPayee, Method: method,
				Amount: refundAmt, Reference: strings.TrimSpace(in.Reference), BusinessDate: date,
				Notes: "Refund " + retNo, Allocations: []payments.Alloc{{SaleReturnID: &id, Amount: refundAmt}},
			})
			if err != nil {
				return err
			}
			out["payment_id"] = pay.ID
			out["payment_number"] = pay.Number
		}
		if err := s.Audit.Record(ctx, tx, p, "sale_return.completed", "sale_return", id, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type ReturnFilter struct {
	SaleID *uuid.UUID
	Status string
	From   string
	To     string
}

// Returns lists sale returns inside the caller's scope.
func (s *Service) Returns(ctx context.Context, p *auth.Principal, f ReturnFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(p.BranchID != nil, "r.location_id = ?", p.BranchID)
		q.If(!p.Has("shop.sale.view_all") && !p.Has("shop.return.approve"), "(r.requested_by = ? OR s.sold_by = ?)", p.MembershipID, p.MembershipID)
		q.If(f.SaleID != nil, "r.sale_id = ?", f.SaleID)
		q.If(f.Status != "", "r.status = ?::app.return_status", f.Status)
		q.If(f.From != "", "r.business_date >= ?::date", f.From)
		q.If(f.To != "", "r.business_date <= ?::date", f.To)
		from := ` FROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.return_no, r.sale_id, COALESCE(s.receipt_no, ''), r.status::text, r.resolution::text,
			       r.reason, r.refund_amount, r.requested_by, r.decided_by, r.created_at, r.completed_at, r.location_id`+
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
	var number, saleNo, status, resolution, reason string
	var amount decimal.Decimal
	var created, completed any
	if err := row.Scan(&id, &number, &sale, &saleNo, &status, &resolution, &reason, &amount, &by, &approved, &created, &completed, &branch); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "sale_id": sale, "sale_number": saleNo, "status": status, "resolution": resolution,
		"reason": reason, "refund_total": amount, "requested_by": by, "approved_by": approved, "created_at": created,
		"completed_at": completed, "branch_id": branch}, nil
}

// Return is one sale return with its lines.
func (s *Service) Return(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := scanReturn(tx.QueryRow(ctx, "\n\t\t\tSELECT r.id, r.return_no, r.sale_id, COALESCE(s.receipt_no, ''), r.status::text, r.resolution::text,\n\t\t\t       r.reason, r.refund_amount, r.requested_by, r.decided_by, r.created_at, r.completed_at, r.location_id\n\t\t\tFROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id\n\t\t\tWHERE r.id = $1", id))
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NotFound("not_found", "Return not found")
		}
		if err != nil {
			return err
		}
		if !p.Has("shop.sale.view_all") && !p.Has("shop.return.approve") {
			var soldBy uuid.UUID
			if err := tx.QueryRow(ctx, "SELECT sold_by FROM app.sales WHERE id = $1", m["sale_id"]).Scan(&soldBy); err != nil {
				return err
			}
			if m["requested_by"] != p.MembershipID && soldBy != p.MembershipID {
				return httpx.NotFound("not_found", "Return not found")
			}
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT rl.id, rl.sale_line_id, sl.product_id, sl.product_name::text, rl.quantity, rl.condition::text, rl.restock, rl.refund_amount, rl.product_unit_id\n\t\t\tFROM app.sale_return_lines rl\n\t\t\tJOIN app.sale_lines sl ON sl.id = rl.sale_line_id\n\t\t\tWHERE rl.sale_return_id = $1 ORDER BY sl.line_no", id)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var rid, item, product uuid.UUID
			var unitID *uuid.UUID
			var name, cond string
			var qty, amt decimal.Decimal
			var restock bool
			if err := rows.Scan(&rid, &item, &product, &name, &qty, &cond, &restock, &amt, &unitID); err != nil {
				return err
			}
			items = append(items, map[string]any{"id": rid, "sale_item_id": item, "product_id": product, "product_name": name,
				"qty": qty, "condition": cond, "restock": restock, "amount": amt, "product_unit_id": unitID})
		}
		m["items"] = items
		out = m
		return rows.Err()
	})
	return out, err
}
