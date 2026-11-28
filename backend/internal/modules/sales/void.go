package sales

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
)

// Void cancels a completed sale: its sale payments are voided (reversing the money ledger and
// reducing sales.amount_paid to zero), then status becomes 'voided' so tg_sales_post returns stock,
// restores serialised units and reverses accrued commissions. Callers without shop.sale.void file
// a sale_void approval request (202).
func (s *Service) Void(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return 0, nil, httpx.BadRequest("invalid_body", "Give a reason of at least 3 characters for the void")
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
		v, err := s.lockForVoid(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if pending, err := s.Approval.PendingFor(ctx, tx, "sale", id); err != nil {
			return err
		} else if pending != nil {
			return httpx.Conflict("invalid_status", "This sale already has a request waiting for approval")
		}
		if !p.Has("shop.sale.void") {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &v.branch, Kind: "sale_void", SubjectType: "sale", SubjectID: id,
				Amount:  decimal.NullDecimal{Decimal: v.total, Valid: true},
				Summary: reason,
			})
			if err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "sale.void_requested", "sale", id, nil, map[string]any{"reason": reason}); err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, 202, httpx.Data(map[string]any{"approval_request_id": reqID, "status": "pending_approval", "id": id}))
			status, body = 202, raw
			return err
		}
		if err := s.applyVoid(ctx, tx, p, id, v, reason); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(map[string]any{"id": id, "number": v.number, "status": "voided", "void_reason": reason}))
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
	var st string
	if err := tx.QueryRow(ctx, "\n\t\tSELECT location_id, COALESCE(receipt_no, ''), total, status::text\n\t\tFROM app.sales WHERE id = $1 FOR UPDATE", id).Scan(&v.branch, &v.number, &v.total, &st); err != nil {
		return v, httpx.NotFound("not_found", "Sale not found")
	}
	if st != "completed" {
		return v, httpx.Conflict("invalid_status", "Only a completed sale can be voided")
	}
	var returns int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM app.sale_returns WHERE sale_id = $1 AND status = 'completed'", id).Scan(&returns); err != nil {
		return v, err
	}
	if returns > 0 {
		return v, httpx.Conflict("invalid_status", "This sale has completed returns and cannot be voided")
	}
	var later int
	if err := tx.QueryRow(ctx, "\n\t\tSELECT count(*) FROM app.payment_allocations pa\n\t\tJOIN app.payments_received pr ON pr.id = pa.payment_received_id\n\t\tWHERE pa.sale_id = $1 AND pr.purpose <> 'sale' AND pr.status = 'posted'", id).Scan(&later); err != nil {
		return v, err
	}
	if later > 0 {
		return v, httpx.Conflict("invalid_status", "This sale has later debt payments. Void those payments first")
	}
	return v, nil
}

func (s *Service) applyVoid(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, v voidable, reason string) error {
	// Void the POS sale payments first so tg_payment_received_post reverses the money ledger and
	// brings sales.amount_paid to 0 (required by tg_sales_guard before voiding the sale).
	if _, err := tx.Exec(ctx, "\n\t\tUPDATE app.payments_received\n\t\tSET status = 'voided', voided_at = now(), voided_by = $2, void_reason = $3\n\t\tWHERE status = 'posted' AND purpose = 'sale'\n\t\t  AND id IN (SELECT payment_received_id FROM app.payment_allocations WHERE sale_id = $1)", id, p.MembershipID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "\n\t\tUPDATE app.sales SET status = 'voided', voided_at = now(), voided_by = $2, void_reason = $3\n\t\tWHERE id = $1", id, p.MembershipID, reason); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "sale.voided", "sale", id, map[string]any{"status": "completed"}, map[string]any{"status": "voided", "reason": reason})
}

func (s *Service) OnVoidApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	v, err := s.lockForVoid(ctx, tx, p, req.SubjectID)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(req.Summary)
	if reason == "" {
		reason = "Approved void"
	}
	return s.applyVoid(ctx, tx, p, req.SubjectID, v, reason)
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

// ReceivePayment records a customer payment against one completed sale's balance.
func (s *Service) ReceivePayment(ctx context.Context, p *auth.Principal, saleID uuid.UUID, in SalePayIn) (int, []byte, error) {
	if !in.Amount.IsPositive() {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
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
		var branch uuid.UUID
		var cust *uuid.UUID
		var bal decimal.Decimal
		var st, number string
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT location_id, customer_id, balance_due, status::text, COALESCE(receipt_no, '')\n\t\t\tFROM app.sales WHERE id = $1 FOR UPDATE", saleID).Scan(&branch, &cust, &bal, &st, &number); err != nil {
			return httpx.NotFound("not_found", "Sale not found")
		}
		if st != "completed" {
			return httpx.Conflict("invalid_status", "This sale cannot take payments")
		}
		amount := money.Money(in.Amount)
		if amount.GreaterThan(bal) {
			return httpx.Conflict("overpayment", "The payment is more than the sale balance of "+bal.StringFixed(2)+". Record extra money as a customer payment")
		}
		if err := s.checkPayments(ctx, tx, []PayIn{{Method: in.Method, MoneyAccountID: in.MoneyAccountID, Amount: amount, Reference: in.Reference}}); err != nil {
			return err
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		purpose := "debt_payment"
		if cust == nil {
			purpose = "sale"
		}
		res, err := payments.Receive(ctx, tx, payments.ReceiveInput{
			LocationID: branch,
			AccountID:  in.MoneyAccountID, CustomerID: cust,
			Purpose: purpose, Method: payments.Method(in.Method), Amount: amount, Reference: strings.TrimSpace(in.Reference),
			BusinessDate: date, Notes: "Payment for sale " + number,
			Allocations: []payments.Alloc{{SaleID: &saleID, Amount: amount}},
		})
		if err != nil {
			return err
		}
		out := map[string]any{"id": res.ID, "number": res.Number, "sale_id": saleID, "amount": amount,
			"method": payments.MethodOut(payments.Method(in.Method)), "balance": bal.Sub(amount), "date": date}
		if err := s.Audit.Record(ctx, tx, p, "sale.payment", "sale", saleID, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
