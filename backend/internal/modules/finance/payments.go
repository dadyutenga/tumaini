package finance

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/approval"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/pdf"
	"bms/internal/platform/sqlq"
)

type PaymentFilter struct {
	Direction, Kind, PartyType, Status string
	PartyID, AccountID                 *uuid.UUID
	From, To, Q                        string
}

const paymentCols = `p.id, p.number, p.branch_id, p.direction, p.kind, COALESCE(p.party_type,''), p.party_id,
	COALESCE(CASE p.party_type
		WHEN 'customer' THEN (SELECT name FROM customers WHERE id=p.party_id)
		WHEN 'supplier' THEN (SELECT name FROM suppliers WHERE id=p.party_id)
		WHEN 'employee' THEN (SELECT name FROM employees WHERE id=p.party_id) END, ''),
	p.money_account_id, ma.name, ma.type, p.method, p.amount, COALESCE(p.reference,''), p.payment_date::text, p.status,
	COALESCE(p.note,''), p.till_session_id, p.created_by, COALESCE(u.name,''), p.created_at, p.voided_at, COALESCE(p.void_reason,'')`

const paymentFrom = ` FROM payments p JOIN money_accounts ma ON ma.id=p.money_account_id LEFT JOIN users u ON u.id=p.created_by`

func scanPayment(row pgx.Row) (map[string]any, error) {
	var id, branch, account, by uuid.UUID
	var party, till *uuid.UUID
	var number, direction, kind, partyType, partyName, accName, accType, method, ref, date, status, note, byName, voidReason string
	var amount decimal.Decimal
	var created time.Time
	var voided *time.Time
	if err := row.Scan(&id, &number, &branch, &direction, &kind, &partyType, &party, &partyName, &account, &accName, &accType, &method, &amount, &ref, &date, &status,
		&note, &till, &by, &byName, &created, &voided, &voidReason); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "branch_id": branch, "direction": direction, "kind": kind,
		"party_type": nullable(partyType), "party_id": party, "party_name": partyName, "money_account_id": account,
		"account_name": accName, "account_type": accType, "method": method, "amount": amount, "reference": ref, "payment_date": date,
		"status": status, "note": note, "till_session_id": till, "created_by": by, "created_by_name": byName, "created_at": created,
		"voided_at": voided, "void_reason": voidReason}, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) Payments(ctx context.Context, p *auth.Principal, f PaymentFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if b := p.ScopeBranches("payments.view"); b != nil {
			q.Add("p.branch_id = ANY(?)", b)
		}
		q.If(f.Direction != "", "p.direction = ?", f.Direction)
		if f.Kind != "" {
			q.Add("p.kind = ANY(?)", strings.Split(f.Kind, ","))
		}
		q.If(f.PartyType != "", "p.party_type = ?", f.PartyType)
		q.If(f.PartyID != nil, "p.party_id = ?", f.PartyID)
		q.If(f.AccountID != nil, "p.money_account_id = ?", f.AccountID)
		q.If(f.Status != "", "p.status = ?", f.Status)
		q.If(f.From != "", "p.payment_date >= ?::date", f.From)
		q.If(f.To != "", "p.payment_date <= ?::date", f.To)
		q.If(f.Q != "", "(p.number ILIKE ? OR p.reference ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%")
		if err := tx.QueryRow(ctx, `SELECT count(*)`+paymentFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+paymentCols+paymentFrom+q.Where()+` ORDER BY p.payment_date DESC, p.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanPayment(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) payment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, perm string) (map[string]any, error) {
	m, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+paymentFrom+` WHERE p.id=$1`, id))
	if err != nil || !p.SeesBranch(perm, m["branch_id"].(uuid.UUID)) {
		return nil, httpx.NotFound("not_found", "Payment not found")
	}
	rows, err := tx.Query(ctx, `
		SELECT a.document_type, a.document_id, a.amount,
		       COALESCE(CASE a.document_type
		         WHEN 'sale' THEN (SELECT number FROM sales WHERE id=a.document_id)
		         WHEN 'purchase_invoice' THEN (SELECT number FROM purchase_invoices WHERE id=a.document_id)
		         WHEN 'payslip' THEN (SELECT number FROM payslips WHERE id=a.document_id)
		         WHEN 'expense' THEN (SELECT number FROM expenses WHERE id=a.document_id)
		         WHEN 'loan' THEN (SELECT number FROM employee_loans WHERE id=a.document_id) END, '')
		FROM payment_allocations a WHERE a.payment_id=$1 ORDER BY a.created_at
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allocs := []map[string]any{}
	for rows.Next() {
		var typ, number string
		var doc uuid.UUID
		var amt decimal.Decimal
		if err := rows.Scan(&typ, &doc, &amt, &number); err != nil {
			return nil, err
		}
		allocs = append(allocs, map[string]any{"document_type": typ, "document_id": doc, "document_number": number, "amount": amt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	m["allocations"] = allocs
	return m, nil
}

func (s *Service) Payment(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.payment(ctx, tx, p, id, "payments.view")
		if err != nil {
			return err
		}
		out["approval_request_id"], err = s.Approval.PendingFor(ctx, tx, "payment", id)
		return err
	})
	return out, err
}

// Voucher returns the payment with the business header for printing (FR-FIN-20).
func (s *Service) Voucher(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.payment(ctx, tx, p, id, "payments.view")
		if err != nil {
			return err
		}
		var name string
		var phone, address, tin *string
		if err := tx.QueryRow(ctx, `SELECT name, phone, address, tax_number FROM businesses`).Scan(&name, &phone, &address, &tin); err != nil {
			return err
		}
		var branchName string
		_ = tx.QueryRow(ctx, `SELECT name FROM branches WHERE id=$1`, out["branch_id"]).Scan(&branchName)
		title := "Payment receipt"
		if out["direction"] == "out" {
			title = "Payment voucher"
		}
		out["title"] = title
		out["business"] = map[string]any{"name": name, "phone": phone, "address": address, "tax_number": tin, "branch_name": branchName}
		return nil
	})
	return out, err
}

// VoucherPDF renders a voucher as an A4 PDF.
func VoucherPDF(v map[string]any) []byte {
	d := pdf.New()
	biz, _ := v["business"].(map[string]any)
	d.Heading(str(biz["name"]), 16)
	if a := str(biz["address"]); a != "" {
		d.Text(a)
	}
	if ph := str(biz["phone"]); ph != "" {
		d.Text("Phone: " + ph)
	}
	d.Space(8)
	d.Heading(str(v["title"])+" "+str(v["number"]), 13)
	if v["status"] == "voided" {
		d.Heading("VOIDED: "+str(v["void_reason"]), 11)
	}
	d.KV("Date", str(v["payment_date"]))
	d.KV("Branch", str(biz["branch_name"]))
	if v["party_name"] != "" {
		d.KV(map[bool]string{true: "Paid to", false: "Received from"}[v["direction"] == "out"], str(v["party_name"]))
	}
	d.KV("Purpose", strings.ReplaceAll(str(v["kind"]), "_", " "))
	d.KV("Method", strings.ReplaceAll(str(v["method"]), "_", " "))
	d.KV("Account", str(v["account_name"]))
	if r := str(v["reference"]); r != "" {
		d.KV("Reference", r)
	}
	d.KV("Amount", str(v["amount"]))
	if n := str(v["note"]); n != "" {
		d.KV("Note", n)
	}
	if allocs, _ := v["allocations"].([]map[string]any); len(allocs) > 0 {
		d.Space(8)
		rows := make([][]string, len(allocs))
		for i, a := range allocs {
			rows[i] = []string{strings.ReplaceAll(str(a["document_type"]), "_", " "), str(a["document_number"]), str(a["amount"])}
		}
		d.Table([]string{"Document", "Number", "Amount"}, []float64{2, 2, 1}, rows)
	}
	d.Space(24)
	d.KV("Recorded by", str(v["created_by_name"]))
	d.Text("Signature: ______________________")
	return d.Bytes()
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	case decimal.Decimal:
		return t.StringFixed(2)
	case uuid.UUID:
		return t.String()
	default:
		b, _ := json.Marshal(v)
		return strings.Trim(string(b), `"`)
	}
}

// Void voids a payment with a reason (FR-FIN-15). Above the caller's
// payments.void limit it waits for approval and returns 202.
func (s *Service) Void(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	reason = strings.TrimSpace(reason)
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		pay, err := s.lockPayment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if pending, err := s.Approval.PendingFor(ctx, tx, "payment", id); err != nil {
			return err
		} else if pending != nil {
			return httpx.Conflict("approval_pending", "A void request for this payment is already waiting for approval")
		}
		g, ok := p.Can("payments.void")
		if !p.IsOwner && !auth.WithinLimit(g, ok, pay.amount) {
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				BranchID: &pay.branch, Kind: "payment_void", SubjectType: "payment", SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: pay.amount, Valid: true}, Summary: "Void payment " + pay.number + ": " + reason,
				Payload: map[string]any{"reason": reason},
			})
			if err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "payment.void_requested", "payment", id, nil, map[string]any{"reason": reason, "approval_request_id": reqID}); err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, p.UserID, 202, httpx.Data(map[string]any{"id": id, "approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		if err := s.voidPayment(ctx, tx, p, pay, reason); err != nil {
			return err
		}
		out, err := s.payment(ctx, tx, p, id, "payments.void")
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

func (s *Service) OnVoidApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	var payload struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(req.Payload, &payload)
	var pay paymentRow
	if err := tx.QueryRow(ctx, `SELECT id, number, branch_id, direction, kind, party_type, party_id, money_account_id, amount, payment_date::text, status FROM payments WHERE id=$1 FOR UPDATE`, req.SubjectID).
		Scan(&pay.id, &pay.number, &pay.branch, &pay.direction, &pay.kind, &pay.partyType, &pay.partyID, &pay.account, &pay.amount, &pay.date, &pay.status); err != nil {
		return httpx.NotFound("not_found", "Payment not found")
	}
	if pay.status != "posted" {
		return httpx.Conflict("invalid_status", "This payment is already voided")
	}
	return s.voidPayment(ctx, tx, p, pay, payload.Reason)
}

func (s *Service) OnVoidRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return s.Audit.Record(ctx, tx, p, "payment.void_"+req.Status, "payment", req.SubjectID, nil, map[string]any{"note": req.Note})
}

type paymentRow struct {
	id, branch, account uuid.UUID
	partyID             *uuid.UUID
	partyType           *string
	number, direction   string
	kind, date, status  string
	amount              decimal.Decimal
}

func (s *Service) lockPayment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (paymentRow, error) {
	var r paymentRow
	err := tx.QueryRow(ctx, `SELECT id, number, branch_id, direction, kind, party_type, party_id, money_account_id, amount, payment_date::text, status FROM payments WHERE id=$1 FOR UPDATE`, id).
		Scan(&r.id, &r.number, &r.branch, &r.direction, &r.kind, &r.partyType, &r.partyID, &r.account, &r.amount, &r.date, &r.status)
	if err != nil || !p.SeesBranch("payments.void", r.branch) {
		return r, httpx.NotFound("not_found", "Payment not found")
	}
	if r.status != "posted" {
		return r, httpx.Conflict("invalid_status", "This payment is already voided")
	}
	switch r.kind {
	case "sale_refund":
		return r, httpx.Conflict("invalid_status", "A refund is undone through its sale return, not by voiding the payment")
	}
	return r, nil
}

// counterCode is the account a payment's money line was matched against, for
// payments booked inside a larger document entry (a sale, a payroll run).
func counterCode(ctx context.Context, tx pgx.Tx, r paymentRow) (string, string, error) {
	switch r.kind {
	case "sale", "customer_payment":
		return "1100", "customer", nil
	case "supplier_payment", "supplier_refund":
		return "2000", "supplier", nil
	case "salary":
		return "2200", "", nil
	case "loan_disbursement", "loan_repayment":
		return "1300", "employee", nil
	case "statutory":
		return "2210", "", nil
	case "drawing":
		return "3200", "", nil
	case "capital":
		return "3000", "", nil
	case "income":
		return "4100", "", nil
	case "expense":
		var code string
		err := tx.QueryRow(ctx, `
			SELECT la.code FROM payment_allocations a JOIN expenses e ON e.id=a.document_id
			JOIN expense_categories ec ON ec.id=e.category_id JOIN ledger_accounts la ON la.id=ec.ledger_account_id
			WHERE a.payment_id=$1 AND a.document_type='expense' LIMIT 1`, r.id).Scan(&code)
		return code, "", err
	}
	return "", "", httpx.Conflict("invalid_status", "This kind of payment cannot be voided here")
}

// voidPayment posts the reversal, reopens what the payment paid and marks it voided.
func (s *Service) voidPayment(ctx context.Context, tx pgx.Tx, p *auth.Principal, r paymentRow, reason string) error {
	if reason == "" {
		reason = "Voided"
	}
	if err := s.Ledger.EnsureOpen(ctx, tx, r.date); err != nil {
		return err
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return err
	}
	user := p.UserID
	desc := "Void of payment " + r.number + ": " + reason
	n, err := s.Ledger.ReverseSource(ctx, tx, p.BusinessID, r.id, date, desc, &user)
	if err != nil {
		return err
	}
	if n == 0 {
		// The payment was booked inside its document's entry; post the mirror of its money line.
		acc, err := payments.LoadAccount(ctx, tx, r.account)
		if err != nil {
			return err
		}
		code, party, err := counterCode(ctx, tx, r)
		if err != nil {
			return err
		}
		counter := ledger.Line{Code: code, BranchID: &r.branch}
		if party != "" && r.partyID != nil {
			counter.PartyType, counter.PartyID = party, r.partyID
		}
		moneyLine := ledger.Line{Code: acc.Code, BranchID: &r.branch, MoneyAccountID: &r.account}
		if r.direction == "in" {
			if err := s.Ledger.EnsureFunds(ctx, tx, r.account, r.amount); err != nil {
				return err
			}
			counter.Debit, moneyLine.Credit = r.amount, r.amount
		} else {
			moneyLine.Debit, counter.Credit = r.amount, r.amount
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "payment_void", r.id, desc, &user, []ledger.Line{counter, moneyLine}); err != nil {
			return err
		}
	}
	if err := s.reopen(ctx, tx, r); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payments SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3 WHERE id=$1`, r.id, p.UserID, reason); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "payment.voided", "payment", r.id, map[string]any{"status": "posted"}, map[string]any{"status": "voided", "reason": reason, "amount": r.amount})
}

// reopen restores the balances of documents the payment was allocated to.
func (s *Service) reopen(ctx context.Context, tx pgx.Tx, r paymentRow) error {
	rows, err := tx.Query(ctx, `SELECT document_type, document_id, amount FROM payment_allocations WHERE payment_id=$1`, r.id)
	if err != nil {
		return err
	}
	type alloc struct {
		typ string
		doc uuid.UUID
		amt decimal.Decimal
	}
	var list []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.typ, &a.doc, &a.amt); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range list {
		var q string
		switch {
		case a.typ == "sale" && r.direction == "in":
			q = `UPDATE sales SET paid_total=paid_total-$2, balance=balance+$2 WHERE id=$1`
		case a.typ == "purchase_invoice" && r.direction == "out":
			q = `UPDATE purchase_invoices SET paid_total=paid_total-$2, balance=balance+$2 WHERE id=$1`
		case a.typ == "payslip":
			if _, err := tx.Exec(ctx, `
				UPDATE payroll_runs SET status='approved' WHERE id=(SELECT run_id FROM payslips WHERE id=$1) AND status IN ('paid','locked')
			`, a.doc); err != nil {
				return err
			}
			// Loan deductions on this payslip were applied when the run was fully paid; put them back.
			if _, err := tx.Exec(ctx, `
				UPDATE employee_loans l SET balance = l.balance + x.amt,
				  status = CASE WHEN l.status = 'settled' THEN 'disbursed' ELSE l.status END
				FROM (SELECT loan_id, SUM(amount) AS amt FROM loan_repayments
				      WHERE payslip_id = $1 AND voided_at IS NULL GROUP BY loan_id) x
				WHERE l.id = x.loan_id
			`, a.doc); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE loan_repayments SET voided_at = now() WHERE payslip_id = $1 AND voided_at IS NULL`, a.doc); err != nil {
				return err
			}
			q = `UPDATE payslips SET paid_total=GREATEST(paid_total-$2,0), status=CASE WHEN paid_total-$2 <= 0 THEN 'approved' ELSE 'partially_paid' END WHERE id=$1`
		case a.typ == "expense":
			q = `UPDATE expenses SET status='approved', payment_id=NULL, money_account_id=NULL, paid_at=NULL WHERE id=$1 AND status='paid' AND $2::numeric > 0`
		case a.typ == "loan" && r.kind == "loan_disbursement":
			var bal, principal decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT balance, principal FROM employee_loans WHERE id=$1 FOR UPDATE`, a.doc).Scan(&bal, &principal); err != nil {
				return err
			}
			if !bal.Equal(principal) {
				return httpx.Conflict("invalid_status", "This loan already has repayments, so its disbursement cannot be voided")
			}
			q = `UPDATE employee_loans SET status='approved', payment_id=NULL WHERE id=$1 AND $2::numeric > 0`
		case a.typ == "loan" && r.kind == "loan_repayment":
			if _, err := tx.Exec(ctx, `UPDATE loan_repayments SET voided_at = now() WHERE payment_id = $1 AND voided_at IS NULL`, r.id); err != nil {
				return err
			}
			q = `UPDATE employee_loans SET balance=balance+$2, status=CASE WHEN status='settled' THEN 'disbursed' ELSE status END WHERE id=$1`
		default:
			continue
		}
		if _, err := tx.Exec(ctx, q, a.doc, a.amt); err != nil {
			return err
		}
	}
	return nil
}
