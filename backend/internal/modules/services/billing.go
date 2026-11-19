package services

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/pdf"
	"bms/internal/platform/sqlq"
)

// ---------- lines ----------

type LineIn struct {
	ServiceID   *uuid.UUID       `json:"service_id"`
	Description string           `json:"description" validate:"max=300"`
	Quantity    decimal.Decimal  `json:"quantity"`
	UnitPrice   *decimal.Decimal `json:"unit_price" validate:"omitempty,dgte0"`
}

type line struct {
	service *uuid.UUID
	desc    string
	qty     decimal.Decimal
	price   decimal.Decimal
	total   decimal.Decimal
}

func (s *Service) buildLines(ctx context.Context, tx pgx.Tx, in []LineIn) ([]line, decimal.Decimal, error) {
	if len(in) == 0 {
		return nil, money.Zero, httpx.BadRequest("invalid_body", "Add at least one line")
	}
	out := make([]line, 0, len(in))
	sub := money.Zero
	for _, l := range in {
		ln := line{service: l.ServiceID, desc: clean(l.Description), qty: l.Quantity}
		if ln.qty.IsZero() {
			ln.qty = decimal.NewFromInt(1)
		}
		if !ln.qty.IsPositive() {
			return nil, sub, httpx.BadRequest("invalid_body", "Quantity must be greater than zero")
		}
		var price decimal.Decimal
		priced := l.UnitPrice != nil
		if priced {
			price = *l.UnitPrice
		}
		if l.ServiceID != nil {
			var name string
			var std decimal.Decimal
			if err := tx.QueryRow(ctx, `SELECT name, standard_price FROM svc_catalog WHERE id=$1`, *l.ServiceID).Scan(&name, &std); err != nil {
				return nil, sub, httpx.NotFound("not_found", "A service on the document was not found")
			}
			if ln.desc == "" {
				ln.desc = name
			}
			if !priced {
				price = std
			}
		}
		if ln.desc == "" {
			return nil, sub, httpx.BadRequest("invalid_body", "Each line needs a description or a service")
		}
		ln.price = money.Money(price)
		ln.total = money.Money(ln.qty.Mul(ln.price))
		sub = sub.Add(ln.total)
		out = append(out, ln)
	}
	return out, sub, nil
}

func (s *Service) saveLines(ctx context.Context, tx pgx.Tx, p *auth.Principal, docType string, docID uuid.UUID, lines []line) error {
	if _, err := tx.Exec(ctx, `DELETE FROM svc_lines WHERE doc_type=$1 AND doc_id=$2`, docType, docID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO svc_lines (id, business_id, doc_type, doc_id, position, service_id, description, quantity, unit_price, total) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			ids.New(), p.BusinessID, docType, docID, i+1, uuidOrNil(l.service), l.desc, l.qty, l.price, l.total); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) docLines(ctx context.Context, tx pgx.Tx, docType string, id uuid.UUID) ([]map[string]any, error) {
	return rows(ctx, tx, `SELECT position, service_id, description, quantity, unit_price, total FROM svc_lines WHERE doc_type=$1 AND doc_id=$2 ORDER BY position`, docType, id)
}

func totals(sub, discount decimal.Decimal) (decimal.Decimal, error) {
	discount = money.Money(discount)
	if discount.IsNegative() || discount.GreaterThan(sub) {
		return money.Zero, httpx.BadRequest("invalid_body", "Discount cannot be more than the subtotal")
	}
	return sub.Sub(discount), nil
}

// ---------- quotations ----------

type QuotationIn struct {
	ClientID   uuid.UUID       `json:"client_id" validate:"required"`
	ProjectID  *uuid.UUID      `json:"project_id"`
	ValidUntil string          `json:"valid_until" validate:"omitempty,date"`
	Discount   decimal.Decimal `json:"discount" validate:"dgte0"`
	Notes      string          `json:"notes" validate:"max=2000"`
	Lines      []LineIn        `json:"lines" validate:"dive"`
}

const quotationCols = `q.id, q.number, q.status, q.valid_until, q.subtotal, q.discount, q.total, q.notes, q.client_id, c.name AS client_name, q.project_id, q.invoice_id, q.created_at`

func (s *Service) ListQuotations(ctx context.Context, p *auth.Principal, status string, clientID *uuid.UUID, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "q.status = ?", status)
		q.If(clientID != nil, "q.client_id = ?", clientID)
		if t := clean(q0); t != "" {
			q.Add("(q.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", t, t)
		}
		from := ` FROM svc_quotations q JOIN svc_clients c ON c.id = q.client_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+quotationCols+from+q.Where()+` ORDER BY q.created_at DESC, q.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.loadQuotation(ctx, tx, id)
		return err
	})
	return out, err
}

func (s *Service) loadQuotation(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	q, err := one(ctx, tx, "Quotation", `SELECT `+quotationCols+` FROM svc_quotations q JOIN svc_clients c ON c.id = q.client_id WHERE q.id=$1`, id)
	if err != nil {
		return nil, err
	}
	q["lines"], err = s.docLines(ctx, tx, "quotation", id)
	return q, err
}

func (s *Service) CreateQuotation(ctx context.Context, p *auth.Principal, in QuotationIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	id := ids.New()
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "svc_clients", "Client", in.ClientID); err != nil {
			return err
		}
		lines, sub, err := s.buildLines(ctx, tx, in.Lines)
		if err != nil {
			return err
		}
		total, err := totals(sub, in.Discount)
		if err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "svc_quotation")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_quotations (id, business_id, branch_id, number, client_id, project_id, valid_until, subtotal, discount, total, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			id, p.BusinessID, *p.BranchID, number, in.ClientID, uuidOrNil(in.ProjectID), dateOrNil(in.ValidUntil), sub, money.Money(in.Discount), total, nz(in.Notes), p.UserID); err != nil {
			return err
		}
		if err := s.saveLines(ctx, tx, p, "quotation", id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.created", "svc_quotation", id, nil, map[string]any{"number": number, "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetQuotation(ctx, p, id)
}

func (s *Service) UpdateQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in QuotationIn) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_quotations WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		if status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft quotation can be edited")
		}
		lines, sub, err := s.buildLines(ctx, tx, in.Lines)
		if err != nil {
			return err
		}
		total, err := totals(sub, in.Discount)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_quotations SET valid_until=$2, subtotal=$3, discount=$4, total=$5, notes=$6, project_id=$7 WHERE id=$1`,
			id, dateOrNil(in.ValidUntil), sub, money.Money(in.Discount), total, nz(in.Notes), uuidOrNil(in.ProjectID)); err != nil {
			return err
		}
		if err := s.saveLines(ctx, tx, p, "quotation", id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.updated", "svc_quotation", id, nil, map[string]any{"total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetQuotation(ctx, p, id)
}

// MoveQuotation sends or rejects a quotation.
func (s *Service) MoveQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, to string) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_quotations WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		ok := (to == "sent" && status == "draft") || (to == "rejected" && (status == "draft" || status == "sent"))
		if !ok {
			return httpx.Conflict("invalid_status", "A "+status+" quotation cannot be marked "+to)
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_quotations SET status=$2 WHERE id=$1`, id, to); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation."+to, "svc_quotation", id, map[string]any{"status": status}, map[string]any{"status": to})
	})
	if err != nil {
		return nil, err
	}
	return s.GetQuotation(ctx, p, id)
}

type AcceptIn struct {
	CreateProject bool       `json:"create_project"`
	ServiceID     *uuid.UUID `json:"service_id"`
	ProjectName   string     `json:"project_name" validate:"max=200"`
	OwnerID       *uuid.UUID `json:"owner_id"`
	StartDate     string     `json:"start_date" validate:"omitempty,date"`
}

// AcceptQuotation marks it accepted and creates the draft invoice, and the project when asked (FR: convert to invoice and project).
func (s *Service) AcceptQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in AcceptIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	var invoiceID uuid.UUID
	var projectID *uuid.UUID
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var clientID uuid.UUID
		var total, subtotal, discount decimal.Decimal
		var notes *string
		var existing *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, client_id, total, subtotal, discount, notes, project_id FROM svc_quotations WHERE id=$1 FOR UPDATE`, id).
			Scan(&status, &clientID, &total, &subtotal, &discount, &notes, &existing); err != nil {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		if status != "draft" && status != "sent" {
			return httpx.Conflict("invalid_status", "A "+status+" quotation cannot be accepted")
		}
		projectID = existing
		if in.CreateProject && projectID == nil {
			pid, err := s.createProjectTx(ctx, tx, p, ProjectIn{ClientID: clientID, ServiceID: in.ServiceID, Name: in.ProjectName, StartDate: in.StartDate, OwnerID: in.OwnerID, TotalPrice: &total})
			if err != nil {
				return err
			}
			projectID = &pid
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "svc_invoice")
		if err != nil {
			return err
		}
		invoiceID = ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_invoices (id, business_id, branch_id, number, client_id, project_id, quotation_id, subtotal, discount, total, balance, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$11,$12)`,
			invoiceID, p.BusinessID, *p.BranchID, number, clientID, uuidOrNil(projectID), id, subtotal, discount, total, notes, p.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_lines (id, business_id, doc_type, doc_id, position, service_id, description, quantity, unit_price, total)
			SELECT gen_random_uuid(), business_id, 'invoice', $2, position, service_id, description, quantity, unit_price, total FROM svc_lines WHERE doc_type='quotation' AND doc_id=$1`, id, invoiceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_quotations SET status='accepted', invoice_id=$2, project_id=$3 WHERE id=$1`, id, invoiceID, uuidOrNil(projectID)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.accepted", "svc_quotation", id, map[string]any{"status": status}, map[string]any{"invoice_id": invoiceID, "project_id": projectID})
	})
	if err != nil {
		return nil, err
	}
	q, err := s.GetQuotation(ctx, p, id)
	if err != nil {
		return nil, err
	}
	q["invoice_id"] = invoiceID
	q["project_id"] = projectID
	return q, nil
}

func quotationPDF(biz string, q map[string]any) []byte { return docPDF(biz, "Quotation", q, false) }

func docPDF(biz, title string, m map[string]any, withPaid bool) []byte {
	d := pdf.New()
	d.Heading(biz, 15)
	d.Heading(title+" "+export.Cell(m["number"]), 12)
	d.KV("Client", export.Cell(m["client_name"]))
	if v := export.Cell(m["issue_date"]); v != "" {
		d.KV("Date", v)
	}
	if v := export.Cell(m["due_date"]); v != "" {
		d.KV("Due", v)
	}
	if v := export.Cell(m["valid_until"]); v != "" {
		d.KV("Valid until", v)
	}
	d.Space(6)
	var body [][]string
	for _, ln := range m["lines"].([]map[string]any) {
		body = append(body, []string{export.Cell(ln["description"]), export.Cell(ln["quantity"]), export.Cell(ln["unit_price"]), export.Cell(ln["total"])})
	}
	d.Table([]string{"Description", "Qty", "Price", "Total"}, []float64{5, 1.2, 1.8, 1.8}, body)
	d.Rule()
	d.KV("Subtotal", export.Cell(m["subtotal"]))
	if !num(m, "discount").IsZero() {
		d.KV("Discount", export.Cell(m["discount"]))
	}
	d.KV("Total", export.Cell(m["total"]))
	if withPaid {
		d.KV("Paid", export.Cell(m["paid_total"]))
		d.KV("Balance due", export.Cell(m["balance"]))
	}
	if v := export.Cell(m["notes"]); v != "" {
		d.Space(6)
		d.Text(v)
	}
	return d.Bytes()
}

func (s *Service) businessName(ctx context.Context, tx pgx.Tx, p *auth.Principal) string {
	var name string
	_ = tx.QueryRow(ctx, `SELECT name FROM businesses WHERE id=$1`, p.BusinessID).Scan(&name)
	return name
}

func (s *Service) QuotationPDF(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]byte, error) {
	var out []byte
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q, err := s.loadQuotation(ctx, tx, id)
		if err != nil {
			return err
		}
		out = quotationPDF(s.businessName(ctx, tx, p), q)
		return nil
	})
	return out, err
}

// ---------- invoices ----------

type InstallmentIn struct {
	DueDate string          `json:"due_date" validate:"required,date"`
	Amount  decimal.Decimal `json:"amount" validate:"dpos"`
}

type InvoiceIn struct {
	ClientID     uuid.UUID       `json:"client_id" validate:"required"`
	ProjectID    *uuid.UUID      `json:"project_id"`
	DesignJobID  *uuid.UUID      `json:"design_job_id"`
	DueDate      string          `json:"due_date" validate:"omitempty,date"`
	Discount     decimal.Decimal `json:"discount" validate:"dgte0"`
	Notes        string          `json:"notes" validate:"max=2000"`
	Lines        []LineIn        `json:"lines" validate:"dive"`
	Installments []InstallmentIn `json:"installments" validate:"dive"`
}

const invoiceCols = `i.id, i.number, i.status, i.issue_date, i.due_date, i.subtotal, i.discount, i.total, i.paid_total, i.balance, i.notes, i.client_id, c.name AS client_name,
	i.project_id, pr.number AS project_number, i.design_job_id, i.quotation_id, i.created_at, i.void_reason`
const invoiceFrom = ` FROM svc_invoices i JOIN svc_clients c ON c.id = i.client_id LEFT JOIN svc_projects pr ON pr.id = i.project_id`

func (s *Service) ListInvoices(ctx context.Context, p *auth.Principal, status string, clientID, projectID *uuid.UUID, overdue bool, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "i.status = ?", status)
		q.If(clientID != nil, "i.client_id = ?", clientID)
		q.If(projectID != nil, "i.project_id = ?", projectID)
		q.If(overdue, "i.status IN ('issued','partial') AND i.due_date < svc_today()")
		if t := clean(q0); t != "" {
			q.Add("(i.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", t, t)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*)`+invoiceFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+invoiceCols+invoiceFrom+q.Where()+` ORDER BY i.created_at DESC, i.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.loadInvoice(ctx, tx, id)
		return err
	})
	return out, err
}

func (s *Service) loadInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	inv, err := one(ctx, tx, "Invoice", `SELECT `+invoiceCols+invoiceFrom+` WHERE i.id=$1`, id)
	if err != nil {
		return nil, err
	}
	if inv["lines"], err = s.docLines(ctx, tx, "invoice", id); err != nil {
		return nil, err
	}
	inst, err := rows(ctx, tx, `SELECT position, due_date, amount FROM svc_invoice_installments WHERE invoice_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	left := num(inv, "paid_total")
	for _, in := range inst {
		paid := decimal.Min(left, num(in, "amount"))
		in["paid"] = paid
		in["balance"] = num(in, "amount").Sub(paid)
		left = left.Sub(paid)
	}
	inv["installments"] = inst
	inv["payments"], err = rows(ctx, tx, `
		SELECT sp.id, pa.number, pa.payment_date AS date, a.amount, pa.method, pa.reference, sp.status
		FROM svc_payment_allocations a JOIN svc_payments sp ON sp.id = a.payment_id JOIN payments pa ON pa.id = sp.id
		WHERE a.invoice_id=$1 ORDER BY pa.payment_date, pa.created_at`, id)
	return inv, err
}

func (s *Service) invoiceFromScope(ctx context.Context, tx pgx.Tx, in *InvoiceIn) ([]line, error) {
	if len(in.Lines) > 0 {
		lines, _, err := s.buildLines(ctx, tx, in.Lines)
		return lines, err
	}
	var desc string
	var amount decimal.Decimal
	switch {
	case in.ProjectID != nil:
		var number, name string
		var price decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT number, name, total_price FROM svc_projects WHERE id=$1 AND client_id=$2`, *in.ProjectID, in.ClientID).Scan(&number, &name, &price); err != nil {
			return nil, httpx.NotFound("not_found", "Project not found for this client")
		}
		var billed decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(total),0) FROM svc_invoices WHERE project_id=$1 AND status <> 'void'`, *in.ProjectID).Scan(&billed); err != nil {
			return nil, err
		}
		desc, amount = number+" "+name, price.Sub(billed)
	case in.DesignJobID != nil:
		var number, title string
		var price decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT number, title, price FROM design_jobs WHERE id=$1 AND client_id=$2`, *in.DesignJobID, in.ClientID).Scan(&number, &title, &price); err != nil {
			return nil, httpx.NotFound("not_found", "Design job not found for this client")
		}
		var billed decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(total),0) FROM svc_invoices WHERE design_job_id=$1 AND status <> 'void'`, *in.DesignJobID).Scan(&billed); err != nil {
			return nil, err
		}
		desc, amount = number+" "+title, price.Sub(billed)
	default:
		return nil, httpx.BadRequest("invalid_body", "Add at least one line")
	}
	if !amount.IsPositive() {
		return nil, httpx.Conflict("nothing_to_invoice", "This work is already fully invoiced")
	}
	lines, _, err := s.buildLines(ctx, tx, []LineIn{{Description: desc, Quantity: decimal.NewFromInt(1), UnitPrice: &amount}})
	return lines, err
}

func (s *Service) checkInstallments(in []InstallmentIn, total decimal.Decimal) error {
	if len(in) == 0 {
		return nil
	}
	sum := money.Zero
	for _, i := range in {
		sum = sum.Add(money.Money(i.Amount))
	}
	if !sum.Equal(total) {
		return httpx.BadRequest("invalid_body", "Installments must add up to the invoice total "+total.StringFixed(2))
	}
	return nil
}

func (s *Service) saveInstallments(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, in []InstallmentIn) error {
	if _, err := tx.Exec(ctx, `DELETE FROM svc_invoice_installments WHERE invoice_id=$1`, id); err != nil {
		return err
	}
	for i, x := range in {
		if _, err := tx.Exec(ctx, `INSERT INTO svc_invoice_installments (id, business_id, invoice_id, position, due_date, amount) VALUES ($1,$2,$3,$4,$5,$6)`,
			ids.New(), p.BusinessID, id, i+1, x.DueDate, money.Money(x.Amount)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateInvoice(ctx context.Context, p *auth.Principal, in InvoiceIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	id := ids.New()
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "svc_clients", "Client", in.ClientID); err != nil {
			return err
		}
		if in.ProjectID != nil && in.DesignJobID != nil {
			return httpx.BadRequest("invalid_body", "An invoice belongs to a project or a design job, not both")
		}
		lines, err := s.invoiceFromScope(ctx, tx, &in)
		if err != nil {
			return err
		}
		sub := money.Zero
		for _, l := range lines {
			sub = sub.Add(l.total)
		}
		total, err := totals(sub, in.Discount)
		if err != nil {
			return err
		}
		if !total.IsPositive() {
			return httpx.BadRequest("invalid_total", "An invoice total must be greater than zero")
		}
		if err := s.checkInstallments(in.Installments, total); err != nil {
			return err
		}
		if in.ProjectID != nil {
			var c uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT client_id FROM svc_projects WHERE id=$1`, *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
				return httpx.NotFound("not_found", "Project not found for this client")
			}
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "svc_invoice")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_invoices (id, business_id, branch_id, number, client_id, project_id, design_job_id, due_date, subtotal, discount, total, balance, notes, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,$13)`,
			id, p.BusinessID, *p.BranchID, number, in.ClientID, uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), dateOrNil(in.DueDate), sub, money.Money(in.Discount), total, nz(in.Notes), p.UserID); err != nil {
			return err
		}
		if err := s.saveLines(ctx, tx, p, "invoice", id, lines); err != nil {
			return err
		}
		if err := s.saveInstallments(ctx, tx, p, id, in.Installments); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.created", "svc_invoice", id, nil, map[string]any{"number": number, "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) UpdateInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, in InvoiceIn) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var client uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status, client_id FROM svc_invoices WHERE id=$1 FOR UPDATE`, id).Scan(&status, &client); err != nil {
			return httpx.NotFound("not_found", "Invoice not found")
		}
		if status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft invoice can be edited. Void it and create a new one")
		}
		in.ClientID = client
		lines, _, err := s.buildLines(ctx, tx, in.Lines)
		if err != nil {
			return err
		}
		sub := money.Zero
		for _, l := range lines {
			sub = sub.Add(l.total)
		}
		total, err := totals(sub, in.Discount)
		if err != nil {
			return err
		}
		if !total.IsPositive() {
			return httpx.BadRequest("invalid_total", "An invoice total must be greater than zero")
		}
		if err := s.checkInstallments(in.Installments, total); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_invoices SET due_date=$2, subtotal=$3, discount=$4, total=$5, balance=$5, notes=$6 WHERE id=$1`,
			id, dateOrNil(in.DueDate), sub, money.Money(in.Discount), total, nz(in.Notes)); err != nil {
			return err
		}
		if err := s.saveLines(ctx, tx, p, "invoice", id, lines); err != nil {
			return err
		}
		if err := s.saveInstallments(ctx, tx, p, id, in.Installments); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.updated", "svc_invoice", id, nil, map[string]any{"total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

// recalc recomputes paid, balance and status from the posted allocations.
func recalcInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		WITH paid AS (
		  SELECT COALESCE(SUM(a.amount),0) AS amt FROM svc_payment_allocations a JOIN svc_payments sp ON sp.id = a.payment_id
		  WHERE a.invoice_id = $1 AND sp.status = 'posted')
		UPDATE svc_invoices i SET paid_total = paid.amt, balance = i.total - paid.amt,
		  status = CASE WHEN i.total - paid.amt <= 0 THEN 'paid' WHEN paid.amt > 0 THEN 'partial' ELSE 'issued' END
		FROM paid WHERE i.id = $1 AND i.status IN ('issued','partial','paid')`, id)
	return err
}

// IssueInvoice posts the receivable and applies deposits already received for the project or job.
func (s *Service) IssueInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, dueDate string) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status, number string
		var client uuid.UUID
		var project, job *uuid.UUID
		var total decimal.Decimal
		var due *string
		if err := tx.QueryRow(ctx, `SELECT status, number, client_id, project_id, design_job_id, total, due_date::text FROM svc_invoices WHERE id=$1 FOR UPDATE`, id).
			Scan(&status, &number, &client, &project, &job, &total, &due); err != nil {
			return httpx.NotFound("not_found", "Invoice not found")
		}
		if status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft invoice can be issued")
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		dueOn := dueDate
		if dueOn == "" && due != nil {
			dueOn = *due
		}
		if dueOn == "" {
			dueOn = date
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "service_invoice", id, "Invoice "+number, &user, []ledger.Line{
			{Code: "1100", BranchID: p.BranchID, PartyType: "client", PartyID: &client, Debit: total},
			{Code: "4300", BranchID: p.BranchID, Credit: total},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_invoices SET status='issued', issue_date=$2, due_date=$3 WHERE id=$1`, id, date, dueOn); err != nil {
			return err
		}
		if err := s.applyDeposits(ctx, tx, p, id, client, project, job, date); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.issued", "svc_invoice", id, map[string]any{"status": "draft"}, map[string]any{"status": "issued", "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) applyDeposits(ctx context.Context, tx pgx.Tx, p *auth.Principal, invoiceID, client uuid.UUID, project, job *uuid.UUID, date string) error {
	if project == nil && job == nil {
		return nil
	}
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `SELECT total - paid_total FROM svc_invoices WHERE id=$1`, invoiceID).Scan(&balance); err != nil {
		return err
	}
	type dep struct {
		id    uuid.UUID
		avail decimal.Decimal
	}
	var deps []dep
	r, err := tx.Query(ctx, `
		SELECT id, amount - applied FROM svc_payments
		WHERE status = 'posted' AND amount > applied AND (($1::uuid IS NOT NULL AND project_id = $1) OR ($2::uuid IS NOT NULL AND design_job_id = $2))
		ORDER BY created_at, id FOR UPDATE`, project, job)
	if err != nil {
		return err
	}
	for r.Next() {
		var d dep
		if err := r.Scan(&d.id, &d.avail); err != nil {
			r.Close()
			return err
		}
		deps = append(deps, d)
	}
	r.Close()
	if err := r.Err(); err != nil {
		return err
	}
	user := p.UserID
	for _, d := range deps {
		if !balance.IsPositive() {
			break
		}
		use := decimal.Min(d.avail, balance)
		balance = balance.Sub(use)
		if _, err := tx.Exec(ctx, `UPDATE svc_payments SET applied = applied + $2 WHERE id=$1`, d.id, use); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO svc_payment_allocations (id, business_id, payment_id, invoice_id, amount) VALUES ($1,$2,$3,$4,$5)`, ids.New(), p.BusinessID, d.id, invoiceID, use); err != nil {
			return err
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "service_deposit_apply", d.id, "Deposit applied to invoice", &user, []ledger.Line{
			{Code: "2400", BranchID: p.BranchID, PartyType: "client", PartyID: &client, Debit: use},
			{Code: "1100", BranchID: p.BranchID, PartyType: "client", PartyID: &client, Credit: use},
		}); err != nil {
			return err
		}
	}
	return recalcInvoice(ctx, tx, invoiceID)
}

// VoidInvoice cancels a draft, or reverses an issued invoice that has no payments.
func (s *Service) VoidInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status, number string
		var issued *string
		if err := tx.QueryRow(ctx, `SELECT status, number, issue_date::text FROM svc_invoices WHERE id=$1 FOR UPDATE`, id).Scan(&status, &number, &issued); err != nil {
			return httpx.NotFound("not_found", "Invoice not found")
		}
		switch status {
		case "void":
			return httpx.Conflict("invalid_status", "This invoice is already void")
		case "draft":
		default:
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_payment_allocations a JOIN svc_payments sp ON sp.id=a.payment_id WHERE a.invoice_id=$1 AND sp.status='posted'`, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return httpx.Conflict("has_payments", "Void the payments on this invoice first")
			}
			date, err := s.bizDate(ctx, tx)
			if err != nil {
				return err
			}
			user := p.UserID
			if _, err := s.Ledger.ReverseSource(ctx, tx, p.BusinessID, id, date, "Void of invoice "+number+": "+reason, &user); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_invoices SET status='void', balance=0, void_reason=$2, voided_at=now() WHERE id=$1`, id, reason); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.voided", "svc_invoice", id, map[string]any{"status": status}, map[string]any{"status": "void", "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) InvoicePDF(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]byte, error) {
	var out []byte
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		inv, err := s.loadInvoice(ctx, tx, id)
		if err != nil {
			return err
		}
		out = docPDF(s.businessName(ctx, tx, p), "Invoice", inv, true)
		return nil
	})
	return out, err
}

// ---------- payments ----------

type PayIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=120"`
	Date           string          `json:"date" validate:"omitempty,date"`
}

type payScope struct {
	client  uuid.UUID
	project *uuid.UUID
	job     *uuid.UUID
	invoice *uuid.UUID
}

// idemTx runs fn once per idempotency key and stores the response.
func (s *Service) idemTx(ctx context.Context, p *auth.Principal, fn func(ctx context.Context, tx pgx.Tx) (int, any, error)) (int, []byte, error) {
	var status int
	var body []byte
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		st, v, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, st, httpx.Data(v))
		status, body = st, raw
		return err
	})
	return status, body, err
}

// receive books a client payment. Money first settles the scope's issued invoices, oldest first;
// what is left stays as a deposit that the next invoice for the same project or job absorbs.
func (s *Service) receive(ctx context.Context, tx pgx.Tx, p *auth.Principal, sc payScope, in PayIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	acc, err := payments.ActiveAccount(ctx, tx, in.MoneyAccountID)
	if err != nil {
		return nil, err
	}
	amount := money.Money(in.Amount)
	date := in.Date
	if date == "" {
		if date, err = s.bizDate(ctx, tx); err != nil {
			return nil, err
		}
	}
	type doc struct {
		id  uuid.UUID
		use decimal.Decimal
	}
	var docs []doc
	left := amount
	q := sqlq.New()
	q.Add("client_id = ?", sc.client)
	q.Add("status IN ('issued','partial')")
	if sc.invoice != nil {
		q.Add("id = ?", *sc.invoice)
	} else if sc.project != nil {
		q.Add("project_id = ?", *sc.project)
	} else if sc.job != nil {
		q.Add("design_job_id = ?", *sc.job)
	}
	r, err := tx.Query(ctx, `SELECT id, balance FROM svc_invoices`+q.Where()+` ORDER BY issue_date, created_at FOR UPDATE`, q.Args...)
	if err != nil {
		return nil, err
	}
	for r.Next() {
		var id uuid.UUID
		var bal decimal.Decimal
		if err := r.Scan(&id, &bal); err != nil {
			r.Close()
			return nil, err
		}
		if left.IsPositive() {
			use := decimal.Min(left, bal)
			left = left.Sub(use)
			docs = append(docs, doc{id, use})
		}
	}
	r.Close()
	if err := r.Err(); err != nil {
		return nil, err
	}
	if sc.invoice != nil {
		if len(docs) == 0 {
			return nil, httpx.Conflict("invalid_status", "Only an issued invoice with a balance can take a payment")
		}
		if left.IsPositive() {
			return nil, httpx.Conflict("overpayment", "The payment is more than the invoice balance")
		}
	}
	res, err := payments.Insert(ctx, tx, payments.Input{
		BusinessID: p.BusinessID, BranchID: *p.BranchID, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
		Direction: "in", Kind: "service_payment", Method: in.Method, PartyType: "client", PartyID: &sc.client,
		Amount: amount, Reference: in.Reference, Date: date,
	})
	if err != nil {
		return nil, err
	}
	applied := amount.Sub(left)
	if _, err := tx.Exec(ctx, `INSERT INTO svc_payments (id, business_id, branch_id, client_id, project_id, design_job_id, amount, applied, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		res.ID, p.BusinessID, *p.BranchID, sc.client, uuidOrNil(sc.project), uuidOrNil(sc.job), amount, applied, p.UserID); err != nil {
		return nil, err
	}
	allocs := []map[string]any{}
	for _, d := range docs {
		if _, err := tx.Exec(ctx, `INSERT INTO svc_payment_allocations (id, business_id, payment_id, invoice_id, amount) VALUES ($1,$2,$3,$4,$5)`, ids.New(), p.BusinessID, res.ID, d.id, d.use); err != nil {
			return nil, err
		}
		if err := recalcInvoice(ctx, tx, d.id); err != nil {
			return nil, err
		}
		allocs = append(allocs, map[string]any{"invoice_id": d.id, "amount": d.use})
	}
	user := p.UserID
	lines := []ledger.Line{{Code: acc.Code, BranchID: p.BranchID, MoneyAccountID: &in.MoneyAccountID, PartyType: "client", PartyID: &sc.client, Debit: amount}}
	if applied.IsPositive() {
		lines = append(lines, ledger.Line{Code: "1100", BranchID: p.BranchID, PartyType: "client", PartyID: &sc.client, Credit: applied})
	}
	if left.IsPositive() {
		lines = append(lines, ledger.Line{Code: "2400", BranchID: p.BranchID, PartyType: "client", PartyID: &sc.client, Credit: left})
	}
	if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "service_payment", res.ID, "Client payment "+res.Number, &user, lines); err != nil {
		return nil, err
	}
	out := map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "applied": applied, "deposit": left, "allocations": allocs, "date": date}
	return out, s.Audit.Record(ctx, tx, p, "service_payment.received", "payment", res.ID, nil, map[string]any{"amount": amount, "client_id": sc.client, "project_id": sc.project, "design_job_id": sc.job})
}

func (s *Service) PayInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, in PayIn) (int, []byte, error) {
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var client uuid.UUID
		var project, job *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT client_id, project_id, design_job_id FROM svc_invoices WHERE id=$1 FOR UPDATE`, id).Scan(&client, &project, &job); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Invoice not found")
		}
		out, err := s.receive(ctx, tx, p, payScope{client: client, project: project, job: job, invoice: &id}, in)
		return 201, out, err
	})
}

func (s *Service) PayProject(ctx context.Context, p *auth.Principal, id uuid.UUID, in PayIn) (int, []byte, error) {
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var client uuid.UUID
		var status string
		if err := tx.QueryRow(ctx, `SELECT client_id, status FROM svc_projects WHERE id=$1 FOR UPDATE`, id).Scan(&client, &status); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Project not found")
		}
		if status == "cancelled" {
			return 0, nil, httpx.Conflict("invalid_status", "A cancelled project cannot take payments")
		}
		out, err := s.receive(ctx, tx, p, payScope{client: client, project: &id}, in)
		return 201, out, err
	})
}

func (s *Service) PayDesignJob(ctx context.Context, p *auth.Principal, id uuid.UUID, in PayIn) (int, []byte, error) {
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var client uuid.UUID
		var status string
		if err := tx.QueryRow(ctx, `SELECT client_id, status FROM design_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&client, &status); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Design job not found")
		}
		if status == "cancelled" {
			return 0, nil, httpx.Conflict("invalid_status", "A cancelled job cannot take payments")
		}
		out, err := s.receive(ctx, tx, p, payScope{client: client, job: &id}, in)
		return 201, out, err
	})
}

func (s *Service) ListPayments(ctx context.Context, p *auth.Principal, clientID, projectID, jobID *uuid.UUID, from, to string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(clientID != nil, "sp.client_id = ?", clientID)
		q.If(projectID != nil, "sp.project_id = ?", projectID)
		q.If(jobID != nil, "sp.design_job_id = ?", jobID)
		q.If(from != "", "pa.payment_date >= ?::date", from)
		q.If(to != "", "pa.payment_date <= ?::date", to)
		from := ` FROM svc_payments sp JOIN payments pa ON pa.id = sp.id JOIN svc_clients c ON c.id = sp.client_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT sp.id, pa.number, pa.payment_date AS date, sp.amount, sp.applied, pa.method, pa.reference, sp.status, sp.client_id, c.name AS client_name, sp.project_id, sp.design_job_id`+
			from+q.Where()+` ORDER BY pa.payment_date DESC, pa.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// PaymentReceipt returns a service payment receipt (SRS Section 11: Service receipt)
// with business details, client, project/design job, purpose, total paid so far and balance remaining.
func (s *Service) PaymentReceipt(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := one(ctx, tx, "Payment", `
			SELECT sp.id, pa.number, pa.payment_date AS date, sp.amount, sp.applied, (sp.amount - sp.applied) AS deposit,
			       pa.method, COALESCE(pa.reference,'') AS reference, sp.status,
			       sp.client_id, c.name AS client_name, COALESCE(c.phone,'') AS client_phone,
			       sp.project_id, COALESCE(pr.number,'') AS project_number, COALESCE(pr.name,'') AS project_name, COALESCE(pr.total_price, 0) AS project_price,
			       sp.design_job_id, COALESCE(dj.number,'') AS design_job_number, COALESCE(dj.title,'') AS design_job_title, COALESCE(dj.price, 0) AS design_job_price,
			       COALESCE(u.name,'') AS received_by
			FROM svc_payments sp
			JOIN payments pa ON pa.id = sp.id
			JOIN svc_clients c ON c.id = sp.client_id
			LEFT JOIN svc_projects pr ON pr.id = sp.project_id
			LEFT JOIN design_jobs dj ON dj.id = sp.design_job_id
			LEFT JOIN users u ON u.id = sp.created_by
			WHERE sp.id=$1`, id)
		if err != nil {
			return err
		}
		var bName, bPhone, bAddr, bTin string
		_ = tx.QueryRow(ctx, `SELECT name, COALESCE(phone,''), COALESCE(address,''), COALESCE(tax_number,'') FROM businesses WHERE id=$1`, p.BusinessID).Scan(&bName, &bPhone, &bAddr, &bTin)

		totalPaid := money.Zero
		totalPrice := money.Zero
		purpose := "Payment"
		if pid, ok := row["project_id"].(uuid.UUID); ok && pid != uuid.Nil {
			totalPrice = num(row, "project_price")
			_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM svc_payments WHERE project_id=$1 AND status='posted'`, pid).Scan(&totalPaid)
		} else if jid, ok := row["design_job_id"].(uuid.UUID); ok && jid != uuid.Nil {
			totalPrice = num(row, "design_job_price")
			_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM svc_payments WHERE design_job_id=$1 AND status='posted'`, jid).Scan(&totalPaid)
		} else {
			cid := row["client_id"].(uuid.UUID)
			_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(total),0) FROM svc_invoices WHERE client_id=$1 AND status IN ('issued','partial','paid')`, cid).Scan(&totalPrice)
			_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM svc_payments WHERE client_id=$1 AND status='posted'`, cid).Scan(&totalPaid)
		}
		balance := totalPrice.Sub(totalPaid)
		if balance.IsNegative() {
			balance = money.Zero
		}
		if num(row, "deposit").IsPositive() && num(row, "applied").IsZero() {
			purpose = "Deposit"
		} else if balance.IsZero() && totalPrice.IsPositive() {
			purpose = "Final payment"
		} else {
			purpose = "Installment payment"
		}
		allocs, err := rows(ctx, tx, `
			SELECT a.invoice_id, i.number AS invoice_number, a.amount
			FROM svc_payment_allocations a JOIN svc_invoices i ON i.id = a.invoice_id
			WHERE a.payment_id=$1 ORDER BY i.number`, id)
		if err != nil {
			return err
		}
		row["business"] = map[string]any{"name": bName, "phone": bPhone, "address": bAddr, "tax_number": bTin}
		row["purpose"] = purpose
		row["total_paid_so_far"] = totalPaid
		row["balance_remaining"] = balance
		row["allocations"] = allocs
		out = row
		return nil
	})
	return out, err
}

func serviceReceiptPDF(m map[string]any) []byte {
	biz := m["business"].(map[string]any)
	d := pdf.New()
	d.Heading(export.Cell(biz["name"]), 15)
	var sub []string
	if v := export.Cell(biz["address"]); v != "" {
		sub = append(sub, v)
	}
	if v := export.Cell(biz["phone"]); v != "" {
		sub = append(sub, v)
	}
	if v := export.Cell(biz["tax_number"]); v != "" {
		sub = append(sub, "TIN: "+v)
	}
	if len(sub) > 0 {
		d.Text(strings.Join(sub, " | "))
	}
	d.Rule()
	d.Heading("Service Payment Receipt "+export.Cell(m["number"]), 12)
	d.KV("Receipt No", export.Cell(m["number"]))
	d.KV("Date", export.Cell(m["date"]))
	d.KV("Client", export.Cell(m["client_name"]))
	if v := export.Cell(m["project_name"]); v != "" {
		d.KV("Project", export.Cell(m["project_number"])+" - "+v)
	}
	if v := export.Cell(m["design_job_title"]); v != "" {
		d.KV("Design job", export.Cell(m["design_job_number"])+" - "+v)
	}
	d.KV("Payment for", export.Cell(m["purpose"]))
	d.KV("Method", export.Cell(m["method"]))
	if v := export.Cell(m["reference"]); v != "" {
		d.KV("Reference", v)
	}
	d.Space(6)
	d.Rule()
	d.KV("Amount received", export.Cell(m["amount"]))
	d.KV("Total paid so far", export.Cell(m["total_paid_so_far"]))
	d.KV("Balance remaining", export.Cell(m["balance_remaining"]))
	d.Rule()
	if allocs, ok := m["allocations"].([]map[string]any); ok && len(allocs) > 0 {
		d.Space(4)
		var body [][]string
		for _, a := range allocs {
			body = append(body, []string{export.Cell(a["invoice_number"]), export.Cell(a["amount"])})
		}
		d.Table([]string{"Applied to invoice", "Amount"}, []float64{3, 1.5}, body)
	}
	d.Space(6)
	d.KV("Received by", export.Cell(m["received_by"]))
	if export.Cell(m["status"]) == "voided" {
		d.Space(4)
		d.Heading("VOIDED", 12)
	}
	return d.Bytes()
}

// VoidPayment reverses a client payment, its deposit applications and its invoice allocations.
func (s *Service) VoidPayment(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var status, number string
		var account uuid.UUID
		var amount decimal.Decimal
		var date string
		if err := tx.QueryRow(ctx, `
			SELECT sp.status, pa.number, pa.money_account_id, pa.amount, pa.payment_date::text FROM svc_payments sp JOIN payments pa ON pa.id = sp.id WHERE sp.id=$1 FOR UPDATE OF sp`, id).
			Scan(&status, &number, &account, &amount, &date); err != nil {
			return httpx.NotFound("not_found", "Payment not found")
		}
		if status != "posted" {
			return httpx.Conflict("invalid_status", "This payment is already voided")
		}
		if err := s.Ledger.EnsureOpen(ctx, tx, date); err != nil {
			return err
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, account, amount); err != nil {
			return err
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		user := p.UserID
		if _, err := s.Ledger.ReverseSource(ctx, tx, p.BusinessID, id, today, "Void of payment "+number+": "+reason, &user); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_payments SET status='voided' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3 WHERE id=$1`, id, p.UserID, reason); err != nil {
			return err
		}
		r, err := tx.Query(ctx, `SELECT DISTINCT invoice_id FROM svc_payment_allocations WHERE payment_id=$1`, id)
		if err != nil {
			return err
		}
		var inv []uuid.UUID
		for r.Next() {
			var i uuid.UUID
			if err := r.Scan(&i); err != nil {
				r.Close()
				return err
			}
			inv = append(inv, i)
		}
		r.Close()
		for _, i := range inv {
			if err := recalcInvoice(ctx, tx, i); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "service_payment.voided", "payment", id, map[string]any{"status": "posted"}, map[string]any{"status": "voided", "reason": reason, "amount": amount})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": "voided"}, nil
}

// ---------- costs and contractors ----------

type CostIn struct {
	ProjectID      *uuid.UUID      `json:"project_id"`
	DesignJobID    *uuid.UUID      `json:"design_job_id"`
	ContractorID   *uuid.UUID      `json:"contractor_id"`
	Kind           string          `json:"kind" validate:"required,oneof=government_fee contractor materials printing stock_images fonts designer_time other"`
	Description    string          `json:"description" validate:"required,max=300"`
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Date           string          `json:"date" validate:"omitempty,date"`
	MoneyAccountID *uuid.UUID      `json:"money_account_id"`
	Method         string          `json:"method" validate:"omitempty,oneof=cash mobile_money bank"`
}

// AddCost records a project or job cost. With a money account it is paid now; without one it is owed to the contractor.
func (s *Service) AddCost(ctx context.Context, p *auth.Principal, in CostIn) (int, []byte, error) {
	if err := needBranch(p); err != nil {
		return 0, nil, err
	}
	if in.ProjectID == nil && in.DesignJobID == nil && in.ContractorID == nil {
		return 0, nil, httpx.BadRequest("invalid_body", "A cost belongs to a project, a design job or a contractor")
	}
	if in.MoneyAccountID == nil && in.ContractorID == nil {
		return 0, nil, httpx.BadRequest("invalid_body", "Choose the money account that paid, or a contractor who is owed")
	}
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		out, err := s.addCostTx(ctx, tx, p, in)
		return 201, out, err
	})
}

func (s *Service) addCostTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CostIn) (map[string]any, error) {
	if in.ProjectID != nil {
		if err := require404(ctx, tx, "svc_projects", "Project", *in.ProjectID); err != nil {
			return nil, err
		}
	}
	if in.DesignJobID != nil {
		if err := require404(ctx, tx, "design_jobs", "Design job", *in.DesignJobID); err != nil {
			return nil, err
		}
	}
	if in.ContractorID != nil {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM contractors WHERE id=$1`, *in.ContractorID).Scan(&active); err != nil {
			return nil, httpx.NotFound("not_found", "Contractor not found")
		}
	}
	date := in.Date
	if date == "" {
		var err error
		if date, err = s.bizDate(ctx, tx); err != nil {
			return nil, err
		}
	}
	amount := money.Money(in.Amount)
	number, err := sequence.Next(ctx, tx, *p.BranchID, "svc_cost")
	if err != nil {
		return nil, err
	}
	id := ids.New()
	user := p.UserID
	var paymentID *uuid.UUID
	status := "owed"
	if in.MoneyAccountID != nil {
		status = "paid"
		acc, err := payments.ActiveAccount(ctx, tx, *in.MoneyAccountID)
		if err != nil {
			return nil, err
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, *in.MoneyAccountID, amount); err != nil {
			return nil, err
		}
		method := in.Method
		if method == "" {
			method = payments.MethodFor(acc.Type)
		}
		pin := payments.Input{BusinessID: p.BusinessID, BranchID: *p.BranchID, MoneyAccountID: *in.MoneyAccountID, UserID: p.UserID,
			Direction: "out", Kind: "expense", Method: method, Amount: amount, Date: date, Note: in.Description}
		if in.ContractorID != nil {
			pin.PartyType, pin.PartyID = "contractor", in.ContractorID
		}
		res, err := payments.Insert(ctx, tx, pin)
		if err != nil {
			return nil, err
		}
		paymentID = &res.ID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "service_cost", res.ID, "Service cost "+number, &user, []ledger.Line{
			{Code: "5200", BranchID: p.BranchID, Debit: amount},
			{Code: acc.Code, BranchID: p.BranchID, MoneyAccountID: in.MoneyAccountID, Credit: amount}}); err != nil {
			return nil, err
		}
	} else if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "service_cost", id, "Service cost "+number, &user, []ledger.Line{
		{Code: "5200", BranchID: p.BranchID, Debit: amount},
		{Code: "2000", BranchID: p.BranchID, PartyType: "contractor", PartyID: in.ContractorID, Credit: amount}}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO svc_costs (id, business_id, branch_id, number, project_id, design_job_id, contractor_id, kind, description, amount, cost_date, money_account_id, status, payment_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		id, p.BusinessID, *p.BranchID, number, uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), uuidOrNil(in.ContractorID), in.Kind, clean(in.Description), amount, date, uuidOrNil(in.MoneyAccountID), status, uuidOrNil(paymentID), p.UserID); err != nil {
		return nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "service_cost.created", "svc_cost", id, nil, map[string]any{"number": number, "amount": amount, "status": status}); err != nil {
		return nil, err
	}
	return one(ctx, tx, "Cost", costSQL+` WHERE k.id=$1`, id)
}

const costSQL = `SELECT k.id, k.number, k.kind, k.description, k.amount, k.cost_date AS date, k.status, k.settled, k.project_id, k.design_job_id, k.contractor_id, ct.name AS contractor_name
	FROM svc_costs k LEFT JOIN contractors ct ON ct.id = k.contractor_id`

func (s *Service) ListCosts(ctx context.Context, p *auth.Principal, projectID, jobID, contractorID *uuid.UUID, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(projectID != nil, "k.project_id = ?", projectID)
		q.If(jobID != nil, "k.design_job_id = ?", jobID)
		q.If(contractorID != nil, "k.contractor_id = ?", contractorID)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_costs k`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, costSQL+q.Where()+` ORDER BY k.cost_date DESC, k.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) voidCostTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, reason string) error {
	var status, number, date string
	var settled decimal.Decimal
	var payment *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT status, number, settled, payment_id, cost_date::text FROM svc_costs WHERE id=$1 FOR UPDATE`, id).Scan(&status, &number, &settled, &payment, &date); err != nil {
		return httpx.NotFound("not_found", "Cost not found")
	}
	if status == "voided" {
		return httpx.Conflict("invalid_status", "This cost is already voided")
	}
	if settled.IsPositive() {
		return httpx.Conflict("has_payments", "This cost has been paid to the contractor. Void those payments first")
	}
	if err := s.Ledger.EnsureOpen(ctx, tx, date); err != nil {
		return err
	}
	today, err := s.bizDate(ctx, tx)
	if err != nil {
		return err
	}
	src := id
	if payment != nil {
		src = *payment
	}
	user := p.UserID
	if _, err := s.Ledger.ReverseSource(ctx, tx, p.BusinessID, src, today, "Void of cost "+number+": "+reason, &user); err != nil {
		return err
	}
	if payment != nil {
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='voided', voided_by=$2, voided_at=now(), void_reason=$3 WHERE id=$1`, *payment, p.UserID, reason); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE svc_costs SET status='voided' WHERE id=$1`, id); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "service_cost.voided", "svc_cost", id, map[string]any{"status": status}, map[string]any{"status": "voided", "reason": reason})
}

func (s *Service) VoidCost(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.voidCostTx(ctx, tx, p, id, reason); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Cost", costSQL+` WHERE k.id=$1`, id)
		return err
	})
	return out, err
}

type ContractorIn struct {
	Name    string `json:"name" validate:"max=200"`
	Trade   string `json:"trade" validate:"max=100"`
	Phone   string `json:"phone" validate:"max=40"`
	Email   string `json:"email" validate:"omitempty,email"`
	Address string `json:"address" validate:"max=500"`
	Notes   string `json:"notes" validate:"max=2000"`
}

const contractorCols = `c.id, c.name, c.trade, c.phone, c.email::text AS email, c.address, c.notes, c.is_active, c.created_at,
	COALESCE((SELECT SUM(k.amount - k.settled) FROM svc_costs k WHERE k.contractor_id = c.id AND k.status = 'owed'), 0) AS owed`

func (s *Service) ListContractors(ctx context.Context, p *auth.Principal, q0 string, all bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if t := clean(q0); t != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.trade ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%')", t, t, t)
		}
		q.If(!all, "c.is_active")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contractors c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+contractorCols+` FROM contractors c`+q.Where()+` ORDER BY c.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateContractor(ctx context.Context, p *auth.Principal, in ContractorIn) (map[string]any, error) {
	if clean(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Contractor name is required")
	}
	id := ids.New()
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO contractors (id, business_id, name, trade, phone, email, address, notes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, p.BusinessID, clean(in.Name), nz(in.Trade), nz(in.Phone), nz(in.Email), nz(in.Address), nz(in.Notes)); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A contractor with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "contractor.created", "contractor", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetContractor(ctx, p, id)
}

func (s *Service) GetContractor(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = one(ctx, tx, "Contractor", `SELECT `+contractorCols+` FROM contractors c WHERE c.id=$1`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateContractor(ctx context.Context, p *auth.Principal, id uuid.UUID, in ContractorIn, active *bool) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE contractors SET name=COALESCE(NULLIF($2,''), name), trade=COALESCE(NULLIF($3,''), trade), phone=COALESCE(NULLIF($4,''), phone),
			  email=COALESCE(NULLIF($5,'')::citext, email), address=COALESCE(NULLIF($6,''), address), notes=COALESCE(NULLIF($7,''), notes), is_active=COALESCE($8, is_active)
			WHERE id=$1`, id, clean(in.Name), clean(in.Trade), clean(in.Phone), clean(in.Email), clean(in.Address), clean(in.Notes), active)
		if isDup(err) {
			return httpx.Conflict("duplicate", "A contractor with that name already exists")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Contractor not found")
		}
		return s.Audit.Record(ctx, tx, p, "contractor.updated", "contractor", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetContractor(ctx, p, id)
}

// ContractorStatement lists work done (we owe) and payments made.
func (s *Service) ContractorStatement(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Contractor", `SELECT `+contractorCols+` FROM contractors c WHERE c.id=$1`, id)
		if err != nil {
			return err
		}
		lines, err := rows(ctx, tx, `
			SELECT * FROM (
			  SELECT k.cost_date AS date, k.number, k.description, k.amount AS owed_amount, CASE WHEN k.status = 'paid' THEN k.amount ELSE 0::numeric END AS paid_amount, k.created_at
			  FROM svc_costs k WHERE k.contractor_id=$1 AND k.status IN ('owed','paid')
			  UNION ALL
			  SELECT pa.payment_date, pa.number, 'Payment to contractor', 0::numeric, cp.amount, cp.created_at
			  FROM contractor_payments cp JOIN payments pa ON pa.id = cp.id WHERE cp.contractor_id=$1 AND cp.status='posted'
			) x ORDER BY date, created_at`, id)
		if err != nil {
			return err
		}
		bal := money.Zero
		for _, ln := range lines {
			// A cost paid on the spot has equal owed and paid amounts, so it never leaves a balance.
			bal = bal.Add(num(ln, "owed_amount")).Sub(num(ln, "paid_amount"))
			ln["balance"] = bal
			delete(ln, "created_at")
		}
		out = map[string]any{"contractor": c, "lines": lines, "owed": c["owed"], "business_name": s.businessName(ctx, tx, p)}
		return nil
	})
	return out, err
}

func contractorStatementPDF(m map[string]any) []byte {
	c := m["contractor"].(map[string]any)
	d := pdf.New()
	if biz := export.Cell(m["business_name"]); biz != "" {
		d.Heading(biz, 15)
	}
	d.Heading("Contractor statement", 13)
	d.KV("Contractor", export.Cell(c["name"]))
	if v := export.Cell(c["trade"]); v != "" {
		d.KV("Trade", v)
	}
	if v := export.Cell(c["phone"]); v != "" {
		d.KV("Phone", v)
	}
	d.Space(6)
	var body [][]string
	for _, ln := range m["lines"].([]map[string]any) {
		body = append(body, []string{
			export.Cell(ln["date"]),
			export.Cell(ln["number"]),
			export.Cell(ln["description"]),
			export.Cell(ln["owed_amount"]),
			export.Cell(ln["paid_amount"]),
			export.Cell(ln["balance"]),
		})
	}
	d.Table([]string{"Date", "Reference", "Description", "Work", "Paid", "Balance"}, []float64{1.3, 1.8, 3, 1.3, 1.3, 1.3}, body)
	d.Rule()
	d.KV("Amount we owe", export.Cell(m["owed"]))
	return d.Bytes()
}

type ContractorPayIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=120"`
	Date           string          `json:"date" validate:"omitempty,date"`
}

// PayContractor settles owed costs, oldest first, from a money account.
func (s *Service) PayContractor(ctx context.Context, p *auth.Principal, id uuid.UUID, in ContractorPayIn) (int, []byte, error) {
	if err := needBranch(p); err != nil {
		return 0, nil, err
	}
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		if err := require404(ctx, tx, "contractors", "Contractor", id); err != nil {
			return 0, nil, err
		}
		acc, err := payments.ActiveAccount(ctx, tx, in.MoneyAccountID)
		if err != nil {
			return 0, nil, err
		}
		amount := money.Money(in.Amount)
		r, err := tx.Query(ctx, `SELECT id, amount - settled FROM svc_costs WHERE contractor_id=$1 AND status='owed' AND amount > settled ORDER BY cost_date, created_at FOR UPDATE`, id)
		if err != nil {
			return 0, nil, err
		}
		type owed struct {
			id  uuid.UUID
			use decimal.Decimal
		}
		var list []owed
		left := amount
		for r.Next() {
			var o owed
			var out decimal.Decimal
			if err := r.Scan(&o.id, &out); err != nil {
				r.Close()
				return 0, nil, err
			}
			if left.IsPositive() {
				o.use = decimal.Min(left, out)
				left = left.Sub(o.use)
				list = append(list, o)
			}
		}
		r.Close()
		if err := r.Err(); err != nil {
			return 0, nil, err
		}
		if left.IsPositive() {
			return 0, nil, httpx.Conflict("overpayment", "The payment is more than the amount owed to this contractor")
		}
		date := in.Date
		if date == "" {
			if date, err = s.bizDate(ctx, tx); err != nil {
				return 0, nil, err
			}
		}
		if err := s.Ledger.EnsureFunds(ctx, tx, in.MoneyAccountID, amount); err != nil {
			return 0, nil, err
		}
		res, err := payments.Insert(ctx, tx, payments.Input{BusinessID: p.BusinessID, BranchID: *p.BranchID, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
			Direction: "out", Kind: "contractor_payment", Method: in.Method, PartyType: "contractor", PartyID: &id, Amount: amount, Reference: in.Reference, Date: date})
		if err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO contractor_payments (id, business_id, contractor_id, amount) VALUES ($1,$2,$3,$4)`, res.ID, p.BusinessID, id, amount); err != nil {
			return 0, nil, err
		}
		for _, o := range list {
			if _, err := tx.Exec(ctx, `UPDATE svc_costs SET settled = settled + $2 WHERE id=$1`, o.id, o.use); err != nil {
				return 0, nil, err
			}
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "contractor_payment", res.ID, "Contractor payment "+res.Number, &user, []ledger.Line{
			{Code: "2000", BranchID: p.BranchID, PartyType: "contractor", PartyID: &id, Debit: amount},
			{Code: acc.Code, BranchID: p.BranchID, MoneyAccountID: &in.MoneyAccountID, Credit: amount}}); err != nil {
			return 0, nil, err
		}
		if err := s.Audit.Record(ctx, tx, p, "contractor.paid", "payment", res.ID, nil, map[string]any{"amount": amount, "contractor_id": id}); err != nil {
			return 0, nil, err
		}
		return 201, map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "date": date}, nil
	})
}
