package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/pdf"
	"bms/internal/platform/phone"
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
			if err := tx.QueryRow(ctx, "SELECT name::text, standard_price FROM app.services WHERE id = $1", *l.ServiceID).Scan(&name, &std); err != nil {
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

func (s *Service) saveQuotationLines(ctx context.Context, tx pgx.Tx, quotationID uuid.UUID, lines []line) error {
	if _, err := tx.Exec(ctx, "DELETE FROM app.quotation_lines WHERE quotation_id = $1", quotationID); err != nil {
		return err
	}
	var fallback *uuid.UUID
	for i, l := range lines {
		sid := l.service
		if sid == nil {
			if fallback == nil {
				f, err := s.defaultCustomService(ctx, tx)
				if err != nil {
					return err
				}
				fallback = &f
			}
			sid = fallback
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.quotation_lines (id, quotation_id, line_no, service_id, description, quantity, unit_price, line_discount, line_total)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8)",
			ids.New(), quotationID, i+1, *sid, l.desc, l.qty, l.price, l.total); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) saveInvoiceLines(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, lines []line) error {
	if _, err := tx.Exec(ctx, "DELETE FROM app.invoice_lines WHERE invoice_id = $1", invoiceID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.invoice_lines (id, invoice_id, line_no, service_id, description, quantity, unit_price, line_discount, line_total)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7, 0, $8)",
			ids.New(), invoiceID, i+1, uuidOrNil(l.service), l.desc, l.qty, l.price, l.total); err != nil {
			return err
		}
	}
	return nil
}

func totals(sub, discount decimal.Decimal) (decimal.Decimal, error) {
	discount = money.Money(discount)
	if discount.IsNegative() || discount.GreaterThan(sub) {
		return money.Zero, httpx.BadRequest("invalid_body", "Discount cannot be more than the subtotal")
	}
	return sub.Sub(discount), nil
}

// checkServiceDiscount enforces discount permissions and authority percentage limits
// on service quotations and invoices so staff cannot apply unauthorized discounts.
func checkServiceDiscount(p *auth.Principal, sub, discount decimal.Decimal) error {
	disc := money.Money(discount)
	if !disc.IsPositive() || p.IsOwner {
		return nil
	}
	if !p.Has("shop.discount.apply") && !p.Has("services.price.approve") {
		return httpx.Forbidden("permission_denied", "You do not have permission to apply discounts")
	}
	if sub.IsPositive() {
		pct := disc.Mul(decimal.NewFromInt(100)).Div(sub).Round(2)
		if p.Has("services.price.approve") {
			if lim, capped := p.Limits["price_change_approve_pct"]; capped && pct.GreaterThan(lim) {
				return httpx.Forbidden("discount_limit_exceeded", "Discount exceeds your approval limit of "+lim.StringFixed(2)+"%")
			}
			return nil
		}
		if lim, capped := p.Limits["sale_discount_pct"]; capped && pct.GreaterThan(lim) {
			return httpx.Forbidden("discount_limit_exceeded", "Discount exceeds your discount limit of "+lim.StringFixed(2)+"%")
		}
	}
	return nil
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

const quotationCols = `q.id, q.quotation_no AS number, q.status::text AS status, q.issue_date, q.valid_until,
	q.subtotal, q.discount_amount AS discount, q.total, q.notes, q.customer_id AS client_id, c.full_name::text AS client_name,
	(SELECT pr.id FROM app.projects pr WHERE pr.quotation_id = q.id LIMIT 1) AS project_id,
	(SELECT i.id FROM app.invoices i WHERE i.quotation_id = q.id ORDER BY i.created_at DESC LIMIT 1) AS invoice_id,
	q.created_at`

const quotationFrom = ` FROM app.quotations q JOIN app.customers c ON c.id = q.customer_id`

func ownQuotation(q *sqlq.Q, p *auth.Principal) {
	if !p.Has("services.quotation.manage") {
		q.Add("q.created_by = ?", p.MembershipID)
	}
}

func (s *Service) ListQuotations(ctx context.Context, p *auth.Principal, status string, clientID *uuid.UUID, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(status != "", "q.status = ?::app.quotation_status", status)
		q.If(clientID != nil, "q.customer_id = ?", clientID)
		if t := clean(q0); t != "" {
			q.Add("(q.quotation_no ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%')", t, t)
		}
		ownQuotation(q, p)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+quotationFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+quotationCols+quotationFrom+q.Where()+` ORDER BY q.created_at DESC, q.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.loadQuotation(ctx, tx, p, id)
		return err
	})
	return out, err
}

func (s *Service) loadQuotation(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	qb := sqlq.New(id)
	qb.Add("q.id = $1")
	ownQuotation(qb, p)
	q, err := one(ctx, tx, "Quotation", `SELECT `+quotationCols+quotationFrom+qb.Where(), qb.Args...)
	if err != nil {
		return nil, err
	}
	q["lines"], err = rows(ctx, tx, `
		SELECT line_no AS position, service_id, description::text AS description, quantity, unit_price, line_total AS total
		FROM app.quotation_lines WHERE quotation_id = $1 ORDER BY line_no`, id)
	return q, err
}

func defaultValidUntil(issueDate, given string) (string, error) {
	if clean(given) != "" {
		if given < issueDate {
			return "", httpx.BadRequest("invalid_body", "Valid-until date cannot be before issue date")
		}
		return given, nil
	}
	t, err := time.Parse("2006-01-02", issueDate)
	if err != nil {
		return issueDate, nil
	}
	return t.AddDate(0, 0, 30).Format("2006-01-02"), nil
}

func (s *Service) CreateQuotation(ctx context.Context, p *auth.Principal, in QuotationIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	id := ids.New()
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "app.customers", "Client", in.ClientID); err != nil {
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
		if err := checkServiceDiscount(p, sub, in.Discount); err != nil {
			return err
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		validUntil, err := defaultValidUntil(today, in.ValidUntil)
		if err != nil {
			return err
		}
		buID, err := s.servicesBU(ctx, tx)
		if err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "quotation")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.quotations (id, quotation_no, customer_id, business_unit_id, issue_date, valid_until, subtotal, discount_amount, tax_amount, total, notes)\n\t\t\tVALUES ($1, $2, $3, $4, $5::date, $6::date, $7, $8, 0, $9, $10)",
			id, number, in.ClientID, buID, today, validUntil, sub, money.Money(in.Discount), total, nz(in.Notes)); err != nil {
			return err
		}
		if err := s.saveQuotationLines(ctx, tx, id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.created", "quotation", id, nil, map[string]any{"number": number, "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetQuotation(ctx, p, id)
}

func (s *Service) UpdateQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in QuotationIn) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		qb := sqlq.New(id)
		qb.Add("q.id = $1")
		ownQuotation(qb, p)
		var status, issueDate string
		if err := tx.QueryRow(ctx, `SELECT q.status::text, q.issue_date::text FROM app.quotations q`+qb.Where()+` FOR UPDATE`, qb.Args...).Scan(&status, &issueDate); err != nil {
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
		if err := checkServiceDiscount(p, sub, in.Discount); err != nil {
			return err
		}
		var validUntil any
		if clean(in.ValidUntil) != "" {
			if in.ValidUntil < issueDate {
				return httpx.BadRequest("invalid_body", "Valid-until date cannot be before issue date")
			}
			validUntil = in.ValidUntil
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.quotations SET valid_until = COALESCE($2::date, valid_until), subtotal = $3, discount_amount = $4, total = $5, notes = $6\n\t\t\tWHERE id = $1", id, validUntil, sub, money.Money(in.Discount), total, nz(in.Notes)); err != nil {
			return err
		}
		if err := s.saveQuotationLines(ctx, tx, id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.updated", "quotation", id, nil, map[string]any{"total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetQuotation(ctx, p, id)
}

// MoveQuotation sends or rejects a quotation.
func (s *Service) MoveQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, to string) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, "SELECT status::text FROM app.quotations WHERE id = $1 FOR UPDATE", id).Scan(&status); err != nil {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		ok := (to == "sent" && status == "draft") || (to == "rejected" && (status == "draft" || status == "sent"))
		if !ok {
			return httpx.Conflict("invalid_status", "A "+status+" quotation cannot be marked "+to)
		}
		if to == "rejected" && status == "draft" {
			if _, err := tx.Exec(ctx, "UPDATE app.quotations SET status = 'sent', sent_at = COALESCE(sent_at, now()) WHERE id = $1", id); err != nil {
				return err
			}
		}
		if to == "sent" {
			if _, err := tx.Exec(ctx, "UPDATE app.quotations SET status = 'sent', sent_at = COALESCE(sent_at, now()) WHERE id = $1", id); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, "UPDATE app.quotations SET status = 'rejected', decided_at = now() WHERE id = $1", id); err != nil {
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation."+to, "quotation", id, map[string]any{"status": status}, map[string]any{"status": to})
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

// AcceptQuotation marks it accepted and creates the draft invoice (and project when asked).
func (s *Service) AcceptQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in AcceptIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	var invoiceID uuid.UUID
	var projectID *uuid.UUID
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var clientID, buID uuid.UUID
		var total, subtotal, discount decimal.Decimal
		var notes *string
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT status::text, customer_id, business_unit_id, total, subtotal, discount_amount, notes\n\t\t\tFROM app.quotations WHERE id = $1 FOR UPDATE", id).
			Scan(&status, &clientID, &buID, &total, &subtotal, &discount, &notes); err != nil {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		if status != "draft" && status != "sent" {
			return httpx.Conflict("invalid_status", "A "+status+" quotation cannot be accepted")
		}
		if status == "draft" {
			if _, err := tx.Exec(ctx, "UPDATE app.quotations SET status = 'sent', sent_at = COALESCE(sent_at, now()) WHERE id = $1", id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "UPDATE app.quotations SET status = 'accepted', decided_at = now() WHERE id = $1", id); err != nil {
			return err
		}
		if in.CreateProject {
			pid, err := s.createProjectTx(ctx, tx, p, ProjectIn{
				ClientID: clientID, ServiceID: in.ServiceID, Name: in.ProjectName, StartDate: in.StartDate, OwnerID: in.OwnerID, TotalPrice: &total,
			}, &id)
			if err != nil {
				return err
			}
			projectID = &pid
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		dueDate, _ := defaultValidUntil(today, "")
		number, err := sequence.Next(ctx, tx, uuid.Nil, "invoice")
		if err != nil {
			return err
		}
		invoiceID = ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.invoices (id, invoice_no, customer_id, business_unit_id, project_id, quotation_id, issue_date, due_date, subtotal, discount_amount, tax_amount, total, notes)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7::date, $8::date, $9, $10, 0, $11, $12)",
			invoiceID, number, clientID, buID, uuidOrNil(projectID), id, today, dueDate, subtotal, discount, total, notes); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.invoice_lines (id, invoice_id, line_no, service_id, description, quantity, unit_price, line_discount, line_total)\n\t\t\tSELECT gen_random_uuid(), $2, line_no, service_id, description, quantity, unit_price, line_discount, line_total\n\t\t\tFROM app.quotation_lines WHERE quotation_id = $1", id, invoiceID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_quotation.accepted", "quotation", id, map[string]any{"status": status}, map[string]any{"invoice_id": invoiceID, "project_id": projectID})
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

func (s *Service) QuotationPDF(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]byte, error) {
	var out []byte
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q, err := s.loadQuotation(ctx, tx, p, id)
		if err != nil {
			return err
		}
		out = quotationPDF(s.businessName(ctx, tx), q)
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

const invoiceCols = `i.id, i.invoice_no AS number,
	CASE WHEN i.status = 'partially_paid' THEN 'partial' ELSE i.status::text END AS status,
	i.issue_date, i.due_date, i.subtotal, i.discount_amount AS discount, i.total,
	i.amount_paid AS paid_total, i.balance_due AS balance, i.notes, i.customer_id AS client_id, c.full_name::text AS client_name,
	i.project_id, pr.project_no AS project_number, i.design_job_id, i.quotation_id, i.created_at, i.void_reason`

const invoiceFrom = ` FROM app.invoices i
	JOIN app.customers c ON c.id = i.customer_id
	LEFT JOIN app.projects pr ON pr.id = i.project_id`

func mapInvoiceStatusFilter(st string) string {
	if st == "partial" {
		return "partially_paid"
	}
	return st
}

func ownInvoice(q *sqlq.Q, p *auth.Principal) {
	if !p.Has("services.invoice.manage") {
		q.Add("i.created_by = ?", p.MembershipID)
	}
}

func (s *Service) ListInvoices(ctx context.Context, p *auth.Principal, status string, clientID, projectID *uuid.UUID, overdue bool, q0 string, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := sqlq.New()
		q.If(status != "", "i.status = ?::app.invoice_status", mapInvoiceStatusFilter(status))
		q.If(clientID != nil, "i.customer_id = ?", clientID)
		q.If(projectID != nil, "i.project_id = ?", projectID)
		q.If(overdue, "i.status IN ('issued','partially_paid') AND i.due_date < ?::date", today)
		if t := clean(q0); t != "" {
			q.Add("(i.invoice_no ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%')", t, t)
		}
		ownInvoice(q, p)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+invoiceFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		out, err = rows(ctx, tx, `SELECT `+invoiceCols+invoiceFrom+q.Where()+` ORDER BY i.created_at DESC, i.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) GetInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.loadInvoice(ctx, tx, p, id)
		return err
	})
	return out, err
}

func (s *Service) loadInvoice(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	qb := sqlq.New(id)
	qb.Add("i.id = $1")
	ownInvoice(qb, p)
	inv, err := one(ctx, tx, "Invoice", `SELECT `+invoiceCols+invoiceFrom+qb.Where(), qb.Args...)
	if err != nil {
		return nil, err
	}
	if inv["lines"], err = rows(ctx, tx, `
		SELECT line_no AS position, service_id, description::text AS description, quantity, unit_price, line_total AS total
		FROM app.invoice_lines WHERE invoice_id = $1 ORDER BY line_no`, id); err != nil {
		return nil, err
	}
	inst, err := rows(ctx, tx, `
		SELECT seq AS position, label, due_date, amount
		FROM app.invoice_installments WHERE invoice_id = $1 ORDER BY seq`, id)
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
		SELECT pr.id, pr.receipt_no AS number, pr.business_date AS date, a.amount,
		       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
		       pr.reference, pr.status::text AS status
		FROM app.payment_allocations a
		JOIN app.payments_received pr ON pr.id = a.payment_received_id
		WHERE a.invoice_id = $1
		ORDER BY pr.business_date, pr.created_at`, id)
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
		if err := tx.QueryRow(ctx, "SELECT project_no, title::text, total_price FROM app.projects WHERE id = $1 AND customer_id = $2", *in.ProjectID, in.ClientID).Scan(&number, &name, &price); err != nil {
			return nil, httpx.NotFound("not_found", "Project not found for this client")
		}
		var billed decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT COALESCE(SUM(total), 0) FROM app.invoices WHERE project_id = $1 AND status <> 'void'", *in.ProjectID).Scan(&billed); err != nil {
			return nil, err
		}
		desc, amount = number+" "+name, price.Sub(billed)
	case in.DesignJobID != nil:
		var number, title string
		var price decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT job_no, title::text, price FROM app.design_jobs WHERE id = $1 AND customer_id = $2", *in.DesignJobID, in.ClientID).Scan(&number, &title, &price); err != nil {
			return nil, httpx.NotFound("not_found", "Design job not found for this client")
		}
		var billed decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT COALESCE(SUM(total), 0) FROM app.invoices WHERE design_job_id = $1 AND status <> 'void'", *in.DesignJobID).Scan(&billed); err != nil {
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

func (s *Service) saveInstallments(ctx context.Context, tx pgx.Tx, id uuid.UUID, in []InstallmentIn) error {
	if _, err := tx.Exec(ctx, "DELETE FROM app.invoice_installments WHERE invoice_id = $1", id); err != nil {
		return err
	}
	for i, x := range in {
		lbl := "installment"
		switch {
		case len(in) == 1 || i == len(in)-1:
			lbl = "final"
		case i == 0:
			lbl = "deposit"
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.invoice_installments (id, invoice_id, seq, label, due_date, amount)\n\t\t\tVALUES ($1, $2, $3, $4, $5::date, $6)",
			ids.New(), id, i+1, lbl, x.DueDate, money.Money(x.Amount)); err != nil {
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if err := require404(ctx, tx, "app.customers", "Client", in.ClientID); err != nil {
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
		if err := checkServiceDiscount(p, sub, in.Discount); err != nil {
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
			if err := tx.QueryRow(ctx, "SELECT customer_id FROM app.projects WHERE id = $1", *in.ProjectID).Scan(&c); err != nil || c != in.ClientID {
				return httpx.NotFound("not_found", "Project not found for this client")
			}
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		dueOn := clean(in.DueDate)
		if dueOn == "" {
			dueOn = today
		} else if dueOn < today {
			return httpx.BadRequest("invalid_body", "Due date cannot be before issue date")
		}
		buID, err := s.servicesBU(ctx, tx)
		if err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, uuid.Nil, "invoice")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.invoices (id, invoice_no, customer_id, business_unit_id, project_id, design_job_id, issue_date, due_date, subtotal, discount_amount, tax_amount, total, notes)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7::date, $8::date, $9, $10, 0, $11, $12)",
			id, number, in.ClientID, buID, uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), today, dueOn, sub, money.Money(in.Discount), total, nz(in.Notes)); err != nil {
			return err
		}
		if err := s.saveInvoiceLines(ctx, tx, id, lines); err != nil {
			return err
		}
		if err := s.saveInstallments(ctx, tx, id, in.Installments); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.created", "invoice", id, nil, map[string]any{"number": number, "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) UpdateInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, in InvoiceIn) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		qb := sqlq.New(id)
		qb.Add("i.id = $1")
		ownInvoice(qb, p)
		var status, issueDate string
		var client uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT i.status::text, i.customer_id, i.issue_date::text FROM app.invoices i`+qb.Where()+` FOR UPDATE`, qb.Args...).Scan(&status, &client, &issueDate); err != nil {
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
		if err := checkServiceDiscount(p, sub, in.Discount); err != nil {
			return err
		}
		if !total.IsPositive() {
			return httpx.BadRequest("invalid_total", "An invoice total must be greater than zero")
		}
		if err := s.checkInstallments(in.Installments, total); err != nil {
			return err
		}
		var dueOn any
		if clean(in.DueDate) != "" {
			if in.DueDate < issueDate {
				return httpx.BadRequest("invalid_body", "Due date cannot be before issue date")
			}
			dueOn = in.DueDate
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.invoices SET due_date = COALESCE($2::date, due_date), subtotal = $3, discount_amount = $4, total = $5, notes = $6\n\t\t\tWHERE id = $1", id, dueOn, sub, money.Money(in.Discount), total, nz(in.Notes)); err != nil {
			return err
		}
		if err := s.saveInvoiceLines(ctx, tx, id, lines); err != nil {
			return err
		}
		if err := s.saveInstallments(ctx, tx, id, in.Installments); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.updated", "invoice", id, nil, map[string]any{"total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

// IssueInvoice marks a draft invoice issued and applies any unallocated deposits for the project or job.
func (s *Service) IssueInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, dueDate string) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var client uuid.UUID
		var project, job *uuid.UUID
		var total decimal.Decimal
		var due string
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT status::text, customer_id, project_id, design_job_id, total, due_date::text\n\t\t\tFROM app.invoices WHERE id = $1 FOR UPDATE", id).
			Scan(&status, &client, &project, &job, &total, &due); err != nil {
			return httpx.NotFound("not_found", "Invoice not found")
		}
		if status != "draft" {
			return httpx.Conflict("invalid_status", "Only a draft invoice can be issued")
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		dueOn := clean(dueDate)
		if dueOn == "" {
			dueOn = due
		}
		if dueOn == "" || dueOn < date {
			dueOn = date
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.invoices SET status = 'issued', issue_date = $2::date, due_date = $3::date, issued_at = now()\n\t\t\tWHERE id = $1", id, date, dueOn); err != nil {
			return err
		}
		if err := s.applyDeposits(ctx, tx, id, client, project, job); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.issued", "invoice", id, map[string]any{"status": "draft"}, map[string]any{"status": "issued", "total": total})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) applyDeposits(ctx context.Context, tx pgx.Tx, invoiceID, client uuid.UUID, project, job *uuid.UUID) error {
	if project == nil && job == nil {
		return nil
	}
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, "SELECT balance_due FROM app.invoices WHERE id = $1", invoiceID).Scan(&balance); err != nil {
		return err
	}
	var tag string
	if project != nil {
		tag = "%[project:" + project.String() + "]%"
	} else {
		tag = "%[design_job:" + job.String() + "]%"
	}
	type dep struct {
		id    uuid.UUID
		avail decimal.Decimal
	}
	var deps []dep
	r, err := tx.Query(ctx, "\n\t\tSELECT pr.id, pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS avail\n\t\tFROM app.payments_received pr\n\t\tWHERE pr.customer_id = $1 AND pr.status = 'posted' AND pr.notes LIKE $2\n\t\tORDER BY pr.created_at, pr.id FOR UPDATE", client, tag)
	if err != nil {
		return err
	}
	for r.Next() {
		var d dep
		if err := r.Scan(&d.id, &d.avail); err != nil {
			r.Close()
			return err
		}
		if d.avail.IsPositive() {
			deps = append(deps, d)
		}
	}
	r.Close()
	if err := r.Err(); err != nil {
		return err
	}
	for _, d := range deps {
		if !balance.IsPositive() {
			break
		}
		use := decimal.Min(d.avail, balance)
		balance = balance.Sub(use)
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.payment_allocations (payment_received_id, invoice_id, amount)\n\t\t\tVALUES ($1, $2, $3)", d.id, invoiceID, use); err != nil {
			return err
		}
	}
	return nil
}

// VoidInvoice cancels a draft or unpaid issued invoice.
func (s *Service) VoidInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var paid decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT status::text, amount_paid FROM app.invoices WHERE id = $1 FOR UPDATE", id).Scan(&status, &paid); err != nil {
			return httpx.NotFound("not_found", "Invoice not found")
		}
		if status == "void" {
			return httpx.Conflict("invalid_status", "This invoice is already void")
		}
		if paid.IsPositive() {
			return httpx.Conflict("has_payments", "Void the payments on this invoice first")
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.invoices SET status = 'void', void_reason = $2, voided_at = now(), issued_at = COALESCE(issued_at, now())\n\t\t\tWHERE id = $1", id, reason); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_invoice.voided", "invoice", id, map[string]any{"status": status}, map[string]any{"status": "void", "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.GetInvoice(ctx, p, id)
}

func (s *Service) InvoicePDF(ctx context.Context, p *auth.Principal, id uuid.UUID) ([]byte, error) {
	var out []byte
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		inv, err := s.loadInvoice(ctx, tx, p, id)
		if err != nil {
			return err
		}
		title := "Invoice"
		if st, _ := inv["status"].(string); st == "void" {
			title = "Invoice (VOID)"
		}
		out = docPDF(s.businessName(ctx, tx), title, inv, true)
		return nil
	})
	return out, err
}

// ---------- payments ----------

type PayIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank bank_transfer card cheque"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=60"`
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		st, v, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, st, httpx.Data(v))
		status, body = st, raw
		return err
	})
	return status, body, err
}

// receive books a client payment into app.payments_received and allocates it to open invoices.
// Any remainder stays unallocated as a deposit tagged with the project/design job so future invoices absorb it.
func (s *Service) receive(ctx context.Context, tx pgx.Tx, p *auth.Principal, sc payScope, in PayIn) (map[string]any, error) {
	if err := needBranch(p); err != nil {
		return nil, err
	}
	amount := money.Money(in.Amount)
	date := in.Date
	if date == "" {
		var err error
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
	q := sqlq.New(sc.client)
	q.Add("customer_id = $1 AND status IN ('issued','partially_paid')")
	if sc.invoice != nil {
		q.Add("id = ?", *sc.invoice)
	} else if sc.project != nil {
		q.Add("project_id = ?", *sc.project)
	} else if sc.job != nil {
		q.Add("design_job_id = ?", *sc.job)
	}
	r, err := tx.Query(ctx, `SELECT id, balance_due FROM app.invoices`+q.Where()+` ORDER BY issue_date, created_at FOR UPDATE`, q.Args...)
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
		if left.IsPositive() && bal.IsPositive() {
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
	applied := amount.Sub(left)
	purpose := "installment"
	if applied.IsZero() {
		purpose = "deposit"
	} else if left.IsZero() && sc.invoice != nil {
		var bal decimal.Decimal
		_ = tx.QueryRow(ctx, "SELECT balance_due FROM app.invoices WHERE id = $1", *sc.invoice).Scan(&bal)
		if bal.Equal(applied) {
			purpose = "final"
		}
	}
	var notes string
	if sc.project != nil {
		notes = "[project:" + sc.project.String() + "]"
	} else if sc.job != nil {
		notes = "[design_job:" + sc.job.String() + "]"
	}
	allocInputs := make([]payments.Alloc, 0, len(docs))
	allocsOut := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		invID := d.id
		allocInputs = append(allocInputs, payments.Alloc{InvoiceID: &invID, Amount: d.use})
		allocsOut = append(allocsOut, map[string]any{"invoice_id": d.id, "amount": d.use})
	}
	clientID := sc.client
	res, err := payments.Receive(ctx, tx, payments.ReceiveInput{
		LocationID: *p.BranchID,
		AccountID:  in.MoneyAccountID,
		CustomerID: &clientID, Purpose: purpose, Method: payments.Method(in.Method),
		Amount: amount, Reference: clean(in.Reference), BusinessDate: date, Notes: notes,
		Allocations: allocInputs,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "applied": applied, "deposit": left, "allocations": allocsOut, "date": date}
	return out, s.Audit.Record(ctx, tx, p, "service_payment.received", "payment_received", res.ID, nil, map[string]any{"amount": amount, "client_id": sc.client, "project_id": sc.project, "design_job_id": sc.job})
}

func (s *Service) PayInvoice(ctx context.Context, p *auth.Principal, id uuid.UUID, in PayIn) (int, []byte, error) {
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var client uuid.UUID
		var project, job *uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT customer_id, project_id, design_job_id FROM app.invoices WHERE id = $1 FOR UPDATE", id).Scan(&client, &project, &job); err != nil {
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
		if err := tx.QueryRow(ctx, "SELECT customer_id, status::text FROM app.projects WHERE id = $1 FOR UPDATE", id).Scan(&client, &status); err != nil {
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
		if err := tx.QueryRow(ctx, "SELECT customer_id, status::text FROM app.design_jobs WHERE id = $1 FOR UPDATE", id).Scan(&client, &status); err != nil {
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("c.client_status IS NOT NULL")
		q.If(clientID != nil, "pr.customer_id = ?", clientID)
		q.If(projectID != nil, `(pr.notes LIKE '%[project:' || ?::text || ']%' OR EXISTS (SELECT 1 FROM app.payment_allocations a JOIN app.invoices i ON i.id = a.invoice_id WHERE a.payment_received_id = pr.id AND i.project_id = ?))`, projectID, projectID)
		q.If(jobID != nil, `(pr.notes LIKE '%[design_job:' || ?::text || ']%' OR EXISTS (SELECT 1 FROM app.payment_allocations a JOIN app.invoices i ON i.id = a.invoice_id WHERE a.payment_received_id = pr.id AND i.design_job_id = ?))`, jobID, jobID)
		q.If(from != "", "pr.business_date >= ?::date", from)
		q.If(to != "", "pr.business_date <= ?::date", to)
		fromSQL := ` FROM app.payments_received pr JOIN app.customers c ON c.id = pr.customer_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+fromSQL+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `
			SELECT pr.id, pr.receipt_no AS number, pr.business_date AS date, pr.amount,
			       COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS applied,
			       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
			       pr.reference, pr.status::text AS status, pr.customer_id AS client_id, c.full_name::text AS client_name,
			       (SELECT i.project_id FROM app.payment_allocations a JOIN app.invoices i ON i.id = a.invoice_id WHERE a.payment_received_id = pr.id AND i.project_id IS NOT NULL LIMIT 1) AS project_id,
			       (SELECT i.design_job_id FROM app.payment_allocations a JOIN app.invoices i ON i.id = a.invoice_id WHERE a.payment_received_id = pr.id AND i.design_job_id IS NOT NULL LIMIT 1) AS design_job_id`+
			fromSQL+q.Where()+` ORDER BY pr.business_date DESC, pr.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

// PaymentReceipt returns a service payment receipt with business details, client, project/design job,
// purpose, and balance remaining. When recordPrint is true (PDF download), it records a
// service_receipt.printed audit row: the first PDF is original (copy=false) and later PDFs are copy=true.
func (s *Service) PaymentReceipt(ctx context.Context, p *auth.Principal, id uuid.UUID, recordPrint bool) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := one(ctx, tx, "Payment", `
			SELECT pr.id, pr.receipt_no AS number, pr.business_date AS date, pr.amount,
			       COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS applied,
			       pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS deposit,
			       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
			       COALESCE(pr.reference, '') AS reference, pr.status::text AS status, pr.notes,
			       pr.customer_id AS client_id, COALESCE(c.full_name::text, '') AS client_name, COALESCE(c.phone::text, '') AS client_phone,
			       COALESCE(u.full_name::text, '') AS received_by
			FROM app.payments_received pr
			LEFT JOIN app.customers c ON c.id = pr.customer_id
			LEFT JOIN app.memberships m ON m.id = pr.created_by
			LEFT JOIN iam.users u ON u.id = m.user_id
			WHERE pr.id = $1`, id)
		if err != nil {
			return err
		}
		var bName, bPhone, bAddr, bTin string
		_ = tx.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(s.business_name::text,''), b.name::text),
			       COALESCE(s.phone::text, ''), COALESCE(s.address, ''), COALESCE(s.tin, '')
			FROM app.business_profile b CROSS JOIN app.business_settings s`).Scan(&bName, &bPhone, &bAddr, &bTin)

		allocs, err := rows(ctx, tx, `
			SELECT a.invoice_id, i.invoice_no AS invoice_number, a.amount, i.project_id, i.design_job_id
			FROM app.payment_allocations a
			JOIN app.invoices i ON i.id = a.invoice_id
			WHERE a.payment_received_id = $1 ORDER BY i.invoice_no`, id)
		if err != nil {
			return err
		}
		var projID, jobID *uuid.UUID
		for _, a := range allocs {
			if pid, ok := a["project_id"].(uuid.UUID); ok && pid != uuid.Nil && projID == nil {
				projID = &pid
			}
			if jid, ok := a["design_job_id"].(uuid.UUID); ok && jid != uuid.Nil && jobID == nil {
				jobID = &jid
			}
		}
		if notes, _ := row["notes"].(string); notes != "" {
			if projID == nil && strings.Contains(notes, "[project:") {
				if parsed, err := extractTagUUID(notes, "[project:"); err == nil {
					projID = &parsed
				}
			}
			if jobID == nil && strings.Contains(notes, "[design_job:") {
				if parsed, err := extractTagUUID(notes, "[design_job:"); err == nil {
					jobID = &parsed
				}
			}
		}
		delete(row, "notes")
		row["project_id"] = projID
		row["project_number"] = ""
		row["project_name"] = ""
		row["design_job_id"] = jobID
		row["design_job_number"] = ""
		row["design_job_title"] = ""

		totalPaid := money.Zero
		totalPrice := money.Zero
		if projID != nil {
			var pno, pname string
			_ = tx.QueryRow(ctx, "SELECT project_no, title::text, total_price FROM app.projects WHERE id = $1", *projID).Scan(&pno, &pname, &totalPrice)
			row["project_number"], row["project_name"] = pno, pname
			_ = tx.QueryRow(ctx, "SELECT COALESCE(SUM(amount_paid), 0) FROM app.invoices WHERE project_id = $1 AND status <> 'void'", *projID).Scan(&totalPaid)
			var dep decimal.Decimal
			_ = tx.QueryRow(ctx, "\n\t\t\t\tSELECT COALESCE(SUM(pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0)), 0)\n\t\t\t\tFROM app.payments_received pr WHERE pr.status = 'posted' AND pr.notes LIKE $1", "%[project:"+projID.String()+"]%").Scan(&dep)
			totalPaid = totalPaid.Add(dep)
		} else if jobID != nil {
			var jno, jtitle string
			_ = tx.QueryRow(ctx, "SELECT job_no, title::text, price FROM app.design_jobs WHERE id = $1", *jobID).Scan(&jno, &jtitle, &totalPrice)
			row["design_job_number"], row["design_job_title"] = jno, jtitle
			_ = tx.QueryRow(ctx, "SELECT COALESCE(SUM(amount_paid), 0) FROM app.invoices WHERE design_job_id = $1 AND status <> 'void'", *jobID).Scan(&totalPaid)
			var dep decimal.Decimal
			_ = tx.QueryRow(ctx, "\n\t\t\t\tSELECT COALESCE(SUM(pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0)), 0)\n\t\t\t\tFROM app.payments_received pr WHERE pr.status = 'posted' AND pr.notes LIKE $1", "%[design_job:"+jobID.String()+"]%").Scan(&dep)
			totalPaid = totalPaid.Add(dep)
		} else if cid, ok := row["client_id"].(uuid.UUID); ok && cid != uuid.Nil {
			_ = tx.QueryRow(ctx, "SELECT COALESCE(SUM(total), 0) FROM app.invoices WHERE customer_id = $1 AND status IN ('issued','partially_paid','paid')", cid).Scan(&totalPrice)
			_ = tx.QueryRow(ctx, "SELECT COALESCE(SUM(amount), 0) FROM app.payments_received WHERE customer_id = $1 AND status = 'posted'", cid).Scan(&totalPaid)
		}
		balance := totalPrice.Sub(totalPaid)
		if balance.IsNegative() {
			balance = money.Zero
		}
		purpose := "Installment payment"
		if num(row, "deposit").IsPositive() && num(row, "applied").IsZero() {
			purpose = "Deposit"
		} else if balance.IsZero() && totalPrice.IsPositive() {
			purpose = "Final payment"
		}
		if recordPrint {
			if err := s.Audit.Record(ctx, tx, p, "service_receipt.printed", "payment_received", id, nil, nil); err != nil {
				return err
			}
		}
		var prints int
		_ = tx.QueryRow(ctx, "\n\t\t\tSELECT count(*) FROM audit.audit_log\n\t\t\tWHERE entity_table = 'payment_received' AND entity_id = $1 AND action = 'service_receipt.printed'", id).Scan(&prints)
		if recordPrint {
			if prints < 1 {
				prints = 1
			}
			row["receipt_prints"] = prints
			row["copy"] = prints > 1
		} else {
			row["receipt_prints"] = prints
			row["copy"] = prints >= 1
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

func extractTagUUID(s, prefix string) (uuid.UUID, error) {
	i := strings.Index(s, prefix)
	if i < 0 {
		return uuid.Nil, fmt.Errorf("missing tag")
	}
	rest := s[i+len(prefix):]
	j := strings.Index(rest, "]")
	if j < 0 {
		return uuid.Nil, fmt.Errorf("unclosed tag")
	}
	return uuid.Parse(rest[:j])
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
	if copyFlag, _ := m["copy"].(bool); copyFlag {
		d.Heading("*** COPY ***", 11)
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

// VoidPayment reverses a client payment; database triggers reverse financial_transactions and invoice allocations.
func (s *Service) VoidPayment(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var amount decimal.Decimal
		if err := tx.QueryRow(ctx, "SELECT status::text, amount FROM app.payments_received WHERE id = $1 FOR UPDATE", id).Scan(&status, &amount); err != nil {
			return httpx.NotFound("not_found", "Payment not found")
		}
		if status != "posted" {
			return httpx.Conflict("invalid_status", "This payment is already voided")
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.payments_received SET status = 'voided', voided_by = $2, voided_at = now(), void_reason = $3\n\t\t\tWHERE id = $1", id, p.MembershipID, reason); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_payment.voided", "payment_received", id, map[string]any{"status": "posted"}, map[string]any{"status": "voided", "reason": reason, "amount": amount})
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
	Method         string          `json:"method" validate:"omitempty,oneof=cash mobile_money bank bank_transfer"`
}

const costUnionSQL = `SELECT * FROM (
	SELECT b.id, b.bill_no AS number, 'contractor'::text AS kind, b.description::text AS description,
	       b.amount, b.bill_date AS date,
	       CASE WHEN b.status = 'open' OR b.status = 'partially_paid' THEN 'owed'
	            WHEN b.status = 'void' THEN 'voided' ELSE b.status::text END AS status,
	       b.amount_paid AS settled, b.project_id, b.design_job_id, b.contractor_id, ct.name::text AS contractor_name, b.created_at
	FROM app.contractor_bills b
	LEFT JOIN app.contractors ct ON ct.id = b.contractor_id
	WHERE true
	UNION ALL
	SELECT e.id, e.expense_no AS number, lower(replace(ec.name::text, ' ', '_')) AS kind, e.description::text AS description,
	       e.amount, e.expense_date AS date,
	       CASE WHEN e.status IN ('cancelled','rejected') THEN 'voided' ELSE e.status::text END AS status,
	       CASE WHEN e.status = 'paid' THEN e.amount ELSE 0::numeric END AS settled,
	       e.project_id, e.design_job_id, NULL::uuid AS contractor_id, NULL::text AS contractor_name, e.created_at
	FROM app.expenses e
	JOIN app.expense_categories ec ON ec.id = e.category_id
	WHERE (e.project_id IS NOT NULL OR e.design_job_id IS NOT NULL)
) k`

func costKindLabel(kind string) string {
	switch kind {
	case "government_fee":
		return "Government fee"
	case "materials":
		return "Materials"
	case "printing":
		return "Printing"
	case "stock_images":
		return "Stock images"
	case "fonts":
		return "Fonts"
	case "designer_time":
		return "Designer time"
	case "contractor":
		return "Contractor"
	default:
		return "Project cost"
	}
}

func upsertProjectCostCategory(ctx context.Context, tx pgx.Tx, kind string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tINSERT INTO app.expense_categories (name, is_project_cost) VALUES ($1, true)\n\t\tON CONFLICT (name) DO UPDATE SET is_project_cost = true\n\t\tRETURNING id", costKindLabel(kind)).Scan(&id)
	return id, err
}

// AddCost records a project or job cost. With a contractor it creates a contractor bill (and pays it immediately
// if money_account_id is given). Without a contractor it submits a project expense linked to the project/job.
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
		out, err := s.addCostTx(ctx, tx, p, in, nil)
		return 201, out, err
	})
}

func (s *Service) addCostTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, in CostIn, printOrderID *uuid.UUID) (map[string]any, error) {
	if in.ProjectID != nil {
		if err := require404(ctx, tx, "app.projects", "Project", *in.ProjectID); err != nil {
			return nil, err
		}
	}
	if in.DesignJobID != nil {
		if err := require404(ctx, tx, "app.design_jobs", "Design job", *in.DesignJobID); err != nil {
			return nil, err
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
	buID, err := s.servicesBU(ctx, tx)
	if err != nil {
		return nil, err
	}

	if in.ContractorID != nil {
		if err := require404(ctx, tx, "app.contractors", "Contractor", *in.ContractorID); err != nil {
			return nil, err
		}
		billNo, err := sequence.Next(ctx, tx, uuid.Nil, "contractor_bill")
		if err != nil {
			return nil, err
		}
		billID := ids.New()
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.contractor_bills (id, bill_no, contractor_id, business_unit_id, project_id, design_job_id, print_order_id, description, amount, bill_date)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date)",
			billID, billNo, *in.ContractorID, buID, uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), uuidOrNil(printOrderID), clean(in.Description), amount, date); err != nil {
			return nil, err
		}
		status := "owed"
		if in.MoneyAccountID != nil {
			status = "paid"
			if _, err := payments.Pay(ctx, tx, payments.PayInput{
				LocationID:   *p.BranchID,
				AccountID:    *in.MoneyAccountID,
				ContractorID: in.ContractorID, Method: payments.Method(in.Method), Amount: amount,
				BusinessDate: date, Notes: clean(in.Description),
				Allocations: []payments.Alloc{{BillID: &billID, Amount: amount}},
			}); err != nil {
				return nil, err
			}
		}
		if err := s.Audit.Record(ctx, tx, p, "service_cost.created", "contractor_bill", billID, nil, map[string]any{"number": billNo, "amount": amount, "status": status}); err != nil {
			return nil, err
		}
		return one(ctx, tx, "Cost", costUnionSQL+` WHERE k.id = $1`, billID)
	}

	// Non-contractor cost: validate account and submit a project expense in app.expenses.
	if _, err := payments.ActiveFin(ctx, tx, *in.MoneyAccountID); err != nil {
		return nil, err
	}
	catID, err := upsertProjectCostCategory(ctx, tx, in.Kind)
	if err != nil {
		return nil, err
	}
	expNo, err := sequence.Next(ctx, tx, uuid.Nil, "expense")
	if err != nil {
		return nil, err
	}
	expID := ids.New()
	if _, err := tx.Exec(ctx, "\n\t\tINSERT INTO app.expenses (id, expense_no, category_id, description, amount, expense_date, business_unit_id, location_id, project_id, design_job_id, account_id, status)\n\t\tVALUES ($1, $2, $3, $4, $5, $6::date, $7, $8, $9, $10, $11, 'submitted')",
		expID, expNo, catID, clean(in.Description), amount, date, buID, *p.BranchID, uuidOrNil(in.ProjectID), uuidOrNil(in.DesignJobID), *in.MoneyAccountID); err != nil {
		return nil, err
	}
	if err := s.Audit.Record(ctx, tx, p, "service_cost.created", "expense", expID, nil, map[string]any{"number": expNo, "amount": amount, "status": "submitted"}); err != nil {
		return nil, err
	}
	return one(ctx, tx, "Cost", costUnionSQL+` WHERE k.id = $1`, expID)
}

func (s *Service) ListCosts(ctx context.Context, p *auth.Principal, projectID, jobID, contractorID *uuid.UUID, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(projectID != nil, "k.project_id = ?", projectID)
		q.If(jobID != nil, "k.design_job_id = ?", jobID)
		q.If(contractorID != nil, "k.contractor_id = ?", contractorID)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (`+costUnionSQL+`) u`+strings.ReplaceAll(q.Where(), "k.", "u."), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, costUnionSQL+q.Where()+` ORDER BY k.date DESC, k.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) voidCostTx(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID, reason string) error {
	var status string
	var paid decimal.Decimal
	err := tx.QueryRow(ctx, "SELECT status::text, amount_paid FROM app.contractor_bills WHERE id = $1 FOR UPDATE", id).Scan(&status, &paid)
	if err == nil {
		if status == "void" {
			return httpx.Conflict("invalid_status", "This cost is already voided")
		}
		if paid.IsPositive() {
			return httpx.Conflict("has_payments", "This cost has been paid to the contractor. Void those payments first")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.contractor_bills SET status = 'void' WHERE id = $1", id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "service_cost.voided", "contractor_bill", id, map[string]any{"status": status}, map[string]any{"status": "void", "reason": reason})
	}
	if !isNoRows(err) {
		return err
	}
	err = tx.QueryRow(ctx, "SELECT status::text FROM app.expenses WHERE id = $1 FOR UPDATE", id).Scan(&status)
	if isNoRows(err) {
		return httpx.NotFound("not_found", "Cost not found")
	}
	if err != nil {
		return err
	}
	switch status {
	case "cancelled", "rejected":
		return httpx.Conflict("invalid_status", "This cost is already voided")
	case "paid":
		return httpx.Conflict("has_payments", "A paid expense cannot be voided directly; create a transaction correction")
	}
	if _, err := tx.Exec(ctx, "UPDATE app.expenses SET status = 'cancelled' WHERE id = $1", id); err != nil {
		return err
	}
	return s.Audit.Record(ctx, tx, p, "service_cost.voided", "expense", id, map[string]any{"status": status}, map[string]any{"status": "cancelled", "reason": reason})
}

func (s *Service) VoidCost(ctx context.Context, p *auth.Principal, id uuid.UUID, reason string) (map[string]any, error) {
	reason = clean(reason)
	if reason == "" {
		return nil, httpx.BadRequest("invalid_body", "A reason is required")
	}
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.voidCostTx(ctx, tx, p, id, reason); err != nil {
			return err
		}
		var err error
		out, err = one(ctx, tx, "Cost", costUnionSQL+` WHERE k.id = $1`, id)
		return err
	})
	return out, err
}

// ---------- contractors ----------

type ContractorIn struct {
	Name    string `json:"name" validate:"max=200"`
	Trade   string `json:"trade" validate:"max=100"`
	Phone   string `json:"phone" validate:"max=40"`
	Email   string `json:"email" validate:"omitempty,email"`
	Address string `json:"address" validate:"max=500"`
	Notes   string `json:"notes" validate:"max=2000"`
	TIN     string `json:"tin" validate:"max=20"`
}

const contractorCols = `c.id, c.name::text AS name, c.trade::text AS trade, c.phone::text AS phone, c.email::text AS email,
	c.tin, NULL::text AS address, NULL::text AS notes, (c.status = 'active') AS is_active, c.created_at,
	COALESCE((SELECT SUM(b.balance_due) FROM app.contractor_bills b WHERE b.contractor_id = c.id AND b.status IN ('open','partially_paid')), 0) AS owed`

func (s *Service) ListContractors(ctx context.Context, p *auth.Principal, q0 string, all bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if t := clean(q0); t != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.trade ILIKE '%' || ? || '%' OR c.phone::text ILIKE '%' || ? || '%')", t, t, t)
		}
		q.If(!all, "c.status = 'active'")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.contractors c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+contractorCols+` FROM app.contractors c`+q.Where()+` ORDER BY c.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateContractor(ctx context.Context, p *auth.Principal, in ContractorIn) (map[string]any, error) {
	if clean(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Contractor name is required")
	}
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return nil, err
	}
	trade := clean(in.Trade)
	if trade == "" {
		trade = "General"
	}
	id := ids.New()
	err = s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.contractors (id, name, trade, phone, email, tin)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6)",
			id, clean(in.Name), trade, nz(ph), nz(in.Email), nz(in.TIN)); err != nil {
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
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = one(ctx, tx, "Contractor", `SELECT `+contractorCols+` FROM app.contractors c WHERE c.id = $1`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateContractor(ctx context.Context, p *auth.Principal, id uuid.UUID, in ContractorIn, active *bool) (map[string]any, error) {
	var ph string
	if clean(in.Phone) != "" {
		var err error
		if ph, err = phone.Optional(in.Phone); err != nil {
			return nil, err
		}
	}
	var st any
	if active != nil {
		if *active {
			st = "active"
		} else {
			st = "inactive"
		}
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.contractors SET\n\t\t\t  name = COALESCE(NULLIF($2,''), name),\n\t\t\t  trade = COALESCE(NULLIF($3,''), trade),\n\t\t\t  phone = COALESCE($4, phone),\n\t\t\t  email = COALESCE(NULLIF($5,'')::citext, email),\n\t\t\t  tin = COALESCE(NULLIF($6,''), tin),\n\t\t\t  status = COALESCE($7::app.record_status, status)\n\t\t\tWHERE id = $1", id, clean(in.Name), clean(in.Trade), nz(ph), clean(in.Email), clean(in.TIN), st)
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

// ContractorStatement lists bills (work done) and payments made.
func (s *Service) ContractorStatement(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Contractor", `SELECT `+contractorCols+` FROM app.contractors c WHERE c.id = $1`, id)
		if err != nil {
			return err
		}
		lines, err := rows(ctx, tx, `
			SELECT * FROM (
			  SELECT b.bill_date AS date, b.bill_no AS number, b.description::text AS description, b.amount AS owed_amount, 0::numeric AS paid_amount, b.created_at
			  FROM app.contractor_bills b WHERE b.contractor_id = $1 AND b.status <> 'void'
			  UNION ALL
			  SELECT pm.business_date AS date, pm.payment_no AS number, 'Payment to contractor' AS description, 0::numeric AS owed_amount, pm.amount AS paid_amount, pm.created_at
			  FROM app.payments_made pm WHERE pm.contractor_id = $1 AND pm.status = 'posted'
			) x ORDER BY date, created_at`, id)
		if err != nil {
			return err
		}
		bal := money.Zero
		for _, ln := range lines {
			bal = bal.Add(num(ln, "owed_amount")).Sub(num(ln, "paid_amount"))
			ln["balance"] = bal
			delete(ln, "created_at")
		}
		out = map[string]any{"contractor": c, "lines": lines, "owed": c["owed"], "business_name": s.businessName(ctx, tx)}
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
	Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank bank_transfer card cheque"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=60"`
	Date           string          `json:"date" validate:"omitempty,date"`
}

// PayContractor settles open contractor bills, oldest first, from a money account.
func (s *Service) PayContractor(ctx context.Context, p *auth.Principal, id uuid.UUID, in ContractorPayIn) (int, []byte, error) {
	if err := needBranch(p); err != nil {
		return 0, nil, err
	}
	return s.idemTx(ctx, p, func(ctx context.Context, tx pgx.Tx) (int, any, error) {
		var contractorID uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM app.contractors WHERE id = $1 FOR UPDATE", id).Scan(&contractorID); err != nil {
			return 0, nil, httpx.NotFound("not_found", "Contractor not found")
		}
		amount := money.Money(in.Amount)
		r, err := tx.Query(ctx, "\n\t\t\tSELECT id, balance_due FROM app.contractor_bills\n\t\t\tWHERE contractor_id = $1 AND status IN ('open','partially_paid') AND balance_due > 0\n\t\t\tORDER BY bill_date, created_at FOR UPDATE", id)
		if err != nil {
			return 0, nil, err
		}
		var allocs []payments.Alloc
		left := amount
		for r.Next() {
			var bid uuid.UUID
			var bal decimal.Decimal
			if err := r.Scan(&bid, &bal); err != nil {
				r.Close()
				return 0, nil, err
			}
			if left.IsPositive() {
				use := decimal.Min(left, bal)
				left = left.Sub(use)
				idCopy := bid
				allocs = append(allocs, payments.Alloc{BillID: &idCopy, Amount: use})
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
		res, err := payments.Pay(ctx, tx, payments.PayInput{
			LocationID:   *p.BranchID,
			AccountID:    in.MoneyAccountID,
			ContractorID: &id, Method: payments.Method(in.Method), Amount: amount,
			Reference: clean(in.Reference), BusinessDate: date, Allocations: allocs,
		})
		if err != nil {
			return 0, nil, err
		}
		if err := s.Audit.Record(ctx, tx, p, "contractor.paid", "payment_made", res.ID, nil, map[string]any{"contractor_id": id, "amount": amount}); err != nil {
			return 0, nil, err
		}
		return 201, map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "date": date}, nil
	})
}
