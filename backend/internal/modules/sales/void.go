package sales

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/stock"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
)

// Void cancels a same-day sale: stock comes back, its payments are voided and
// its journal entries are reversed (FR-SAL-18). Above the caller's sales.void
// limit it waits for approval and returns 202.
func (s *Service) Void(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, nil, httpx.BadRequest("invalid_body", "Give a reason for the void")
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		v, err := s.lockForVoid(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if pending, err := s.Approval.PendingFor(ctx, tx, "sale", id); err != nil {
			return err
		} else if pending != nil {
			return httpx.Conflict("invalid_status", "This sale already has a request waiting for approval")
		}
		g, ok := p.Can("sales.void")
		if !p.IsOwner && !auth.WithinLimit(g, ok, v.total) {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &v.branch, Kind: "sale_void", SubjectType: "sale", SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: v.total, Valid: true}, Summary: "Void sale " + v.number + ": " + reason,
				Payload: map[string]any{"reason": reason},
			})
			if err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "sale.void_requested", "sale", id, nil, map[string]any{"reason": reason}); err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": id}))
			status, body = 202, raw
			return err
		}
		if err := s.applyVoid(ctx, tx, p, id, v, reason); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(map[string]any{"id": id, "number": v.number, "status": "voided", "void_reason": reason}))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

type voidable struct {
	branch uuid.UUID
	number string
	total  decimal.Decimal
}

func (s *Service) lockForVoid(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (voidable, error) {
	var v voidable
	var kind, st, date string
	if err := tx.QueryRow(ctx, `SELECT branch_id, number, total, kind, status, business_date::text FROM sales WHERE id=$1 FOR UPDATE`, id).Scan(&v.branch, &v.number, &v.total, &kind, &st, &date); err != nil {
		return v, httpx.NotFound("not_found", "Sale not found")
	}
	if kind != "sale" || !p.SeesBranch("sales.void", v.branch) {
		return v, httpx.NotFound("not_found", "Sale not found")
	}
	if st != "completed" {
		return v, httpx.Conflict("invalid_status", "Only a completed sale with no returns can be voided. Use a return instead")
	}
	today, err := s.bizDate(ctx, tx)
	if err != nil {
		return v, err
	}
	if date != today {
		return v, httpx.Conflict("void_not_same_day", "A sale can only be voided on the business day it was made. Use a return instead")
	}
	var later int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM payment_allocations pa JOIN payments py ON py.id=pa.payment_id
		WHERE pa.document_type='sale' AND pa.document_id=$1 AND py.kind <> 'sale' AND py.status='posted'
	`, id).Scan(&later); err != nil {
		return v, err
	}
	if later > 0 {
		return v, httpx.Conflict("invalid_status", "This sale has later payments. Void those payments first")
	}
	return v, nil
}

func (s *Service) applyVoid(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, v voidable, reason string) error {
	user := p.UserID
	rows, err := tx.Query(ctx, `
		SELECT si.product_id, si.qty - si.qty_returned, si.unit_cost FROM sale_items si JOIN products pr ON pr.id=si.product_id
		WHERE si.sale_id=$1 AND pr.kind='stock' AND si.qty > si.qty_returned`, id)
	if err != nil {
		return err
	}
	var lines []stock.Line
	for rows.Next() {
		var ln stock.Line
		if err := rows.Scan(&ln.ProductID, &ln.Qty, &ln.UnitCost); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, ln)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) > 0 {
		if err := s.Stock.ReturnAtCost(ctx, tx, p.BusinessID, v.branch, &user, "sale_return", "sale_void", id, lines); err != nil {
			return err
		}
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := s.Ledger.ReverseSource(ctx, tx, p.BusinessID, id, date, "Void sale "+v.number, &user); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payments SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3
		WHERE status='posted' AND kind='sale' AND id IN (SELECT payment_id FROM payment_allocations WHERE document_type='sale' AND document_id=$1)
	`, id, p.UserID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sales SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3 WHERE id=$1`, id, p.UserID, reason); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale.voided", "sale", id, map[string]any{"status": "completed"}, map[string]any{"status": "voided", "reason": reason})
}

func (s *Service) OnVoidApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var payload struct {
		Reason string `json:"reason"`
	}
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return err
		}
	}
	v, err := s.lockForVoid(ctx, tx, p, req.SubjectID)
	if err != nil {
		return err
	}
	return s.applyVoid(ctx, tx, p, req.SubjectID, v, payload.Reason)
}

func (s *Service) OnVoidRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return s.Audit.Record(ctx, tx, p, "sale.void_"+req.Status, "sale", req.SubjectID, nil, map[string]any{"note": req.Note})
}

type SalePayIn struct {
	Amount         decimal.Decimal `json:"amount"`
	Method         string          `json:"method"`
	MoneyAccountID uuid.UUID       `json:"money_account_id"`
	Reference      string          `json:"reference"`
}

// ReceivePayment records a customer payment against one sale's balance.
func (s *Service) ReceivePayment(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in SalePayIn) (int, []byte, error) {
	if !in.Amount.IsPositive() {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
	}
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var branch uuid.UUID
		var cust *uuid.UUID
		var bal decimal.Decimal
		var kind, st, number string
		if err := tx.QueryRow(ctx, `SELECT branch_id, customer_id, balance, kind, status, number FROM sales WHERE id=$1 FOR UPDATE`, saleID).Scan(&branch, &cust, &bal, &kind, &st, &number); err != nil {
			return httpx.NotFound("not_found", "Sale not found")
		}
		if kind != "sale" || !p.SeesBranch("payments.receive", branch) {
			return httpx.NotFound("not_found", "Sale not found")
		}
		if st != "completed" && st != "partially_refunded" {
			return httpx.Conflict("invalid_status", "This sale cannot take payments")
		}
		amount := money.Money(in.Amount)
		if amount.GreaterThan(bal) {
			return httpx.Conflict("overpayment", "The payment is more than the sale balance of "+bal.StringFixed(2)+". Record extra money as a customer payment")
		}
		if err := s.checkPayments(ctx, tx, []PayIn{{Method: in.Method, MoneyAccountID: in.MoneyAccountID, Amount: amount}}); err != nil {
			return err
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		var till *uuid.UUID
		if in.Method == "cash" {
			if id, _, err := openTill(ctx, tx, p.UserID); err == nil {
				till = &id
			}
		}
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: branch, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID, TillSessionID: till,
			Direction: "in", Kind: "customer_payment", Method: in.Method, PartyType: "customer", PartyID: cust,
			Amount: amount, Reference: in.Reference, Date: date, AllocType: "sale", AllocID: saleID, AllocAmount: amount,
		})
		if err != nil {
			return err
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.MoneyAccountID)
		if err != nil {
			return err
		}
		user := p.UserID
		acc := in.MoneyAccountID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "customer_payment", res.ID, "Payment "+res.Number+" for sale "+number, &user, []ledger.Line{
			{Code: code, BranchID: &branch, MoneyAccountID: &acc, PartyType: "customer", PartyID: cust, Debit: amount},
			{Code: "1100", BranchID: &branch, PartyType: "customer", PartyID: cust, Credit: amount},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sales SET paid_total=paid_total+$2, balance=balance-$2 WHERE id=$1`, saleID, amount); err != nil {
			return err
		}
		out := map[string]any{"id": res.ID, "number": res.Number, "sale_id": saleID, "amount": amount, "method": in.Method, "balance": bal.Sub(amount), "date": date}
		if err := s.Audit.Record(ctx, tx, p, "sale.payment", "sale", saleID, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
