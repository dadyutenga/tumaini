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

// The API shows money in and money out as one list of payments. In the approved schema they are two
// tables, payments_received (customers) and payments_made (suppliers, contractors, staff, refunds,
// others), so the list is their union. An "in" payment's kind is its purpose; an "out" payment's kind
// is who it was paid to.
const paymentUnion = `(
	SELECT pr.id, pr.receipt_no AS number, pr.location_id, 'in' AS direction, pr.purpose::text AS kind,
	       CASE WHEN pr.customer_id IS NULL THEN '' ELSE 'customer' END AS party_type, pr.customer_id AS party_id,
	       coalesce(c.full_name::text, 'Walk-in') AS party_name, pr.account_id, pr.method::text AS method, pr.amount,
	       coalesce(pr.reference, '') AS reference, pr.business_date, pr.status::text AS status, coalesce(pr.notes, '') AS note,
	       pr.created_by, pr.created_at, pr.voided_at, coalesce(pr.void_reason, '') AS void_reason
	FROM app.payments_received pr LEFT JOIN app.customers c ON c.id = pr.customer_id
	UNION ALL
	SELECT pm.id, pm.payment_no, pm.location_id, 'out',
	       CASE WHEN pm.supplier_id IS NOT NULL THEN 'supplier' WHEN pm.contractor_id IS NOT NULL THEN 'contractor'
	            WHEN pm.employee_id IS NOT NULL THEN 'employee' WHEN pm.customer_id IS NOT NULL THEN 'refund' ELSE 'other' END,
	       CASE WHEN pm.supplier_id IS NOT NULL THEN 'supplier' WHEN pm.contractor_id IS NOT NULL THEN 'contractor'
	            WHEN pm.employee_id IS NOT NULL THEN 'employee' WHEN pm.customer_id IS NOT NULL THEN 'customer' ELSE '' END,
	       coalesce(pm.supplier_id, pm.contractor_id, pm.employee_id, pm.customer_id),
	       coalesce(s.name::text, ct.name::text, e.full_name::text, cu.full_name::text, pm.other_payee_name, ''),
	       pm.account_id, pm.method::text, pm.amount, coalesce(pm.reference, ''), pm.business_date, pm.status::text, coalesce(pm.notes, ''),
	       pm.created_by, pm.created_at, pm.voided_at, coalesce(pm.void_reason, '')
	FROM app.payments_made pm
	LEFT JOIN app.suppliers s ON s.id = pm.supplier_id
	LEFT JOIN app.contractors ct ON ct.id = pm.contractor_id
	LEFT JOIN app.employees e ON e.id = pm.employee_id
	LEFT JOIN app.customers cu ON cu.id = pm.customer_id
	WHERE true) p`

const paymentCols = `p.id, p.number, p.location_id, p.direction, p.kind, p.party_type, p.party_id, p.party_name,
	p.account_id, fa.name::text, fa.kind::text, p.method, p.amount, p.reference, p.business_date::text, p.status, p.note,
	m.user_id, coalesce(u.full_name::text, ''), p.created_at, p.voided_at, p.void_reason`

const paymentFrom = ` FROM ` + paymentUnion + `
	JOIN app.financial_accounts fa ON fa.id = p.account_id
	LEFT JOIN app.memberships m ON m.id = p.created_by LEFT JOIN iam.users u ON u.id = m.user_id`

func scanPayment(row pgx.Row) (map[string]any, error) {
	var id, branch, account uuid.UUID
	var party, by *uuid.UUID
	var number, direction, kind, partyType, partyName, accName, accType, method, ref, date, status, note, byName, voidReason string
	var amount decimal.Decimal
	var created time.Time
	var voided *time.Time
	if err := row.Scan(&id, &number, &branch, &direction, &kind, &partyType, &party, &partyName, &account, &accName, &accType, &method, &amount, &ref, &date, &status,
		&note, &by, &byName, &created, &voided, &voidReason); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "number": number, "branch_id": branch, "direction": direction, "kind": kind,
		"party_type": nullable(partyType), "party_id": party, "party_name": partyName, "money_account_id": account,
		"account_name": accName, "account_type": accType, "method": payments.MethodOut(method), "amount": amount, "reference": ref, "payment_date": date,
		"status": status, "note": note, "till_session_id": nil, "created_by": by, "created_by_name": byName, "created_at": created,
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(!p.Has("finance.payment.view_all"), "p.created_by = ?", p.MembershipID)
		q.If(p.BranchID != nil, "p.location_id = ?", p.BranchID)
		q.If(f.Direction != "", "p.direction = ?", f.Direction)
		if f.Kind != "" {
			q.Add("p.kind = ANY(?)", strings.Split(f.Kind, ","))
		}
		q.If(f.PartyType != "", "p.party_type = ?", f.PartyType)
		q.If(f.PartyID != nil, "p.party_id = ?", f.PartyID)
		q.If(f.AccountID != nil, "p.account_id = ?", f.AccountID)
		q.If(f.Status != "", "p.status = ?", f.Status)
		q.If(f.From != "", "p.business_date >= ?::date", f.From)
		q.If(f.To != "", "p.business_date <= ?::date", f.To)
		q.If(f.Q != "", "(p.number ILIKE ? OR p.reference ILIKE ?)", "%"+f.Q+"%", "%"+f.Q+"%")
		if err := tx.QueryRow(ctx, `SELECT count(*)`+paymentFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+paymentCols+paymentFrom+q.Where()+` ORDER BY p.business_date DESC, p.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
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

func (s *Service) payment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	m, err := scanPayment(tx.QueryRow(ctx, `SELECT `+paymentCols+paymentFrom+` WHERE p.id = $1`, id))
	if err != nil {
		return nil, httpx.NotFound("not_found", "Payment not found")
	}
	if !p.Has("finance.payment.view_all") {
		var mine bool
		_ = tx.QueryRow(ctx, "SELECT m.user_id = $1 FROM app.memberships m WHERE m.id = (\n\t\t\tSELECT created_by FROM app.payments_received WHERE id = $2\n\t\t\tUNION ALL SELECT created_by FROM app.payments_made WHERE id = $2 LIMIT 1)", p.UserID, id).Scan(&mine)
		if !mine {
			return nil, httpx.NotFound("not_found", "Payment not found")
		}
	}
	rows, err := tx.Query(ctx, "\n\t\tSELECT 'sale', a.sale_id, a.amount, coalesce(sa.receipt_no, '') FROM app.payment_allocations a\n\t\t  LEFT JOIN app.sales sa ON sa.id = a.sale_id WHERE a.payment_received_id = $1 AND a.sale_id IS NOT NULL\n\t\tUNION ALL\n\t\tSELECT 'invoice', a.invoice_id, a.amount, coalesce(i.invoice_no, '') FROM app.payment_allocations a\n\t\t  LEFT JOIN app.invoices i ON i.id = a.invoice_id WHERE a.payment_received_id = $1 AND a.invoice_id IS NOT NULL\n\t\tUNION ALL\n\t\tSELECT 'purchase', a.purchase_id, a.amount, coalesce(pu.purchase_no, '') FROM app.payment_made_allocations a\n\t\t  LEFT JOIN app.purchases pu ON pu.id = a.purchase_id WHERE a.payment_made_id = $1 AND a.purchase_id IS NOT NULL\n\t\tUNION ALL\n\t\tSELECT 'contractor_bill', a.contractor_bill_id, a.amount, coalesce(b.bill_no, '') FROM app.payment_made_allocations a\n\t\t  LEFT JOIN app.contractor_bills b ON b.id = a.contractor_bill_id WHERE a.payment_made_id = $1 AND a.contractor_bill_id IS NOT NULL\n\t\tUNION ALL\n\t\tSELECT 'sale_return', a.sale_return_id, a.amount, coalesce(r.return_no, '') FROM app.payment_made_allocations a\n\t\t  LEFT JOIN app.sale_returns r ON r.id = a.sale_return_id WHERE a.payment_made_id = $1 AND a.sale_return_id IS NOT NULL", id)
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
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.payment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		out["approval_request_id"], err = s.Approval.PendingFor(ctx, tx, "payment", id)
		return err
	})
	return out, err
}

// Voucher returns the payment with the business header for printing.
func (s *Service) Voucher(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.payment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		var name string
		var phone, address, tin *string
		if err := tx.QueryRow(ctx, "SELECT business_name::text, phone::text, address, tin FROM app.business_settings WHERE true").Scan(&name, &phone, &address, &tin); err != nil {
			return err
		}
		var branchName string
		_ = tx.QueryRow(ctx, "SELECT name::text FROM app.locations WHERE id = $1", out["branch_id"]).Scan(&branchName)
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

// Void voids a payment with a reason. The database allows it only for someone who holds
// finance.payment.void or while an approved payment_void request exists; anyone else files a request
// and gets 202.
func (s *Service) Void(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (int, []byte, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return 0, nil, httpx.BadRequest("reason_required", "Give a reason for voiding the payment")
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
		pay, err := s.payment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if pay["status"] != "posted" {
			return httpx.Conflict("invalid_status", "This payment is already voided")
		}
		if pending, err := s.Approval.PendingFor(ctx, tx, "payment", id); err != nil {
			return err
		} else if pending != nil {
			return httpx.Conflict("approval_pending", "A void request for this payment is already waiting for approval")
		}
		if !p.Has("finance.payment.void") {
			subject := "payment_received"
			if pay["direction"] == "out" {
				subject = "payment_made"
			}
			amount, _ := pay["amount"].(decimal.Decimal)
			reqID, err := s.Approval.Request(ctx, tx, p, approval.Input{
				Kind: "payment_void", SubjectType: subject, SubjectID: id,
				Amount: decimal.NullDecimal{Decimal: amount, Valid: true}, Summary: "Void payment " + pay["number"].(string) + ": " + reason,
			})
			if err != nil {
				return err
			}
			if err := s.Audit.Record(ctx, tx, p, "payment.void_requested", "payments", id, nil, map[string]any{"reason": reason, "approval_request_id": reqID}); err != nil {
				return err
			}
			raw, err := idem.Complete(ctx, tx, 202, httpx.Data(map[string]any{"id": id, "approval_request_id": reqID, "status": "pending_approval"}))
			status, body = 202, raw
			return err
		}
		if err := s.voidPayment(ctx, tx, p, id, pay["direction"].(string), reason); err != nil {
			return err
		}
		out, err := s.payment(ctx, tx, p, id)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 200, httpx.Data(out))
		status, body = 200, raw
		return err
	})
	return status, body, err
}

// voidPayment marks the payment voided; the database reverses the money and undoes the allocations.
func (s *Service) voidPayment(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, direction, reason string) error {
	table := "app.payments_received"
	if direction == "out" {
		table = "app.payments_made"
	}
	// table is one of two fixed names chosen above.
	if _, err := tx.Exec(ctx, `UPDATE `+table+` SET status = 'voided', voided_at = now(), voided_by = $1, void_reason = $2 WHERE id = $3`, p.MembershipID, reason, id); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "payment.voided", "payments", id, nil, map[string]any{"reason": reason})
}

// OnVoidApproved voids the payment in the approver's transaction; the approved request is what lets
// the database accept it.
func (s *Service) OnVoidApproved(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	direction := "in"
	if req.SubjectType == "payment_made" {
		direction = "out"
	}
	reason := strings.TrimPrefix(req.Summary, "Void payment ")
	if i := strings.Index(reason, ": "); i >= 0 {
		reason = reason[i+2:]
	}
	return s.voidPayment(ctx, tx, p, req.SubjectID, direction, reason)
}

func (s *Service) OnVoidRejected(ctx context.Context, tx pgx.Tx, p *auth.Principal, req approval.Request) error {
	return s.Audit.Record(ctx, tx, p, "payment.void_"+req.Status, "payments", req.SubjectID, nil, map[string]any{"note": req.Note})
}
