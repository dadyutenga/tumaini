package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/outbound"
	"bms/internal/platform/auth"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/pdf"
	"bms/internal/platform/phone"
	"bms/internal/platform/sqlq"
)

type ClientIn struct {
	Name     string `json:"name" validate:"max=200"`
	Phone    string `json:"phone" validate:"max=40"`
	Email    string `json:"email" validate:"omitempty,email"`
	Address  string `json:"address" validate:"max=500"`
	IDNumber string `json:"id_number" validate:"max=60"`
	Source   string `json:"source" validate:"max=120"`
	Needs    string `json:"needs" validate:"max=2000"`
	Notes    string `json:"notes" validate:"max=2000"`
	Status   string `json:"status" validate:"omitempty,oneof=lead active completed inactive"`
}

const clientCols = `c.id, c.full_name::text AS name, c.phone::text AS phone, c.email::text AS email, c.address,
	c.national_id_last4 AS id_number, ls.name::text AS source, c.needs_summary AS needs, c.notes,
	c.client_status::text AS status, (c.archived_at IS NULL AND c.client_status <> 'inactive') AS is_active, c.created_at,
	(SELECT count(*) FROM app.projects pr WHERE pr.customer_id = c.id) AS projects,
	COALESCE((SELECT SUM(i.balance_due) FROM app.invoices i WHERE i.customer_id = c.id AND i.status IN ('issued','partially_paid')), 0) AS balance,
	COALESCE((SELECT SUM(pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0))
	          FROM app.payments_received pr WHERE pr.customer_id = c.id AND pr.status = 'posted'), 0) AS deposit_held`

const clientFrom = ` FROM app.customers c LEFT JOIN app.lead_sources ls ON ls.id = c.lead_source_id`

// ownClient limits a list to clients the member registered, is assigned to, or runs a project/stage for.
func ownClient(q *sqlq.Q, p *auth.Principal) {
	if !p.Has("services.client.view_all") {
		mid := p.MembershipID
		q.Add(`(c.registered_by = ? OR c.assigned_to = ? OR EXISTS (SELECT 1 FROM app.projects pr WHERE pr.customer_id = c.id AND (pr.owner_id = ? OR EXISTS (SELECT 1 FROM app.project_stages st WHERE st.project_id = pr.id AND st.owner_id = ?))))`, mid, mid, mid, mid)
	}
}

func upsertLeadSource(ctx context.Context, tx pgx.Tx, name string) (any, error) {
	name = clean(name)
	if name == "" {
		return nil, nil
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tINSERT INTO app.lead_sources (name) VALUES ($1)\n\t\tON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name\n\t\tRETURNING id", name).Scan(&id)
	return id, err
}

func (s *Service) ListClients(ctx context.Context, p *auth.Principal, q0, status string, active *bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("c.client_status IS NOT NULL")
		if t := clean(q0); t != "" {
			q.Add("(c.full_name ILIKE '%' || ? || '%' OR c.phone::text ILIKE '%' || ? || '%' OR c.email::text ILIKE '%' || ? || '%')", t, t, t)
		}
		q.If(status != "", "c.client_status = ?::app.client_status", status)
		q.If(active != nil, "(c.archived_at IS NULL AND c.client_status <> 'inactive') = ?", active)
		ownClient(q, p)
		if err := tx.QueryRow(ctx, `SELECT count(*)`+clientFrom+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+clientCols+clientFrom+q.Where()+` ORDER BY c.full_name, c.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateClient(ctx context.Context, p *auth.Principal, in ClientIn) (map[string]any, error) {
	if clean(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Client name is required")
	}
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return nil, err
	}
	status := in.Status
	if status == "" {
		status = "lead"
	}
	id := ids.New()
	err = s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		srcID, err := upsertLeadSource(ctx, tx, in.Source)
		if err != nil {
			return err
		}
		natID, err := s.IDs.protect(in.IDNumber)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tINSERT INTO app.customers (id, full_name, phone, email, address, national_id_ciphertext, national_id_key_id, national_id_hmac, national_id_last4, client_status, lead_source_id, needs_summary, notes, registered_by, assigned_to)\n\t\t\tVALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::app.client_status, $11, $12, $13, $14, $14)\n\t\t", id, clean(in.Name), nz(ph), nz(in.Email), nz(in.Address), natID.cipher, natID.keyID, natID.hmac, natID.last4, status, srcID, nz(in.Needs), nz(in.Notes), p.MembershipID); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A client with that phone or ID number already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.created", "customer", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

func (s *Service) GetClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		q.Add("c.id = $1 AND c.client_status IS NOT NULL")
		ownClient(q, p)
		var err error
		out, err = one(ctx, tx, "Client", `SELECT `+clientCols+clientFrom+q.Where(), q.Args...)
		if err != nil {
			return err
		}
		if out["projects_list"], err = rows(ctx, tx, `
			SELECT pr.id, pr.project_no AS number, pr.title::text AS name, pr.status::text AS status, pr.total_price,
			       COALESCE((SELECT SUM(i.amount_paid) FROM app.invoices i WHERE i.project_id = pr.id AND i.status <> 'void'), 0) AS paid
			FROM app.projects pr WHERE pr.customer_id = $1 ORDER BY pr.created_at DESC`, id); err != nil {
			return err
		}
		if out["invoices"], err = rows(ctx, tx, `
			SELECT id, invoice_no AS number,
			       CASE WHEN status = 'partially_paid' THEN 'partial' ELSE status::text END AS status,
			       issue_date, due_date, total, amount_paid AS paid_total, balance_due AS balance
			FROM app.invoices WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 50`, id); err != nil {
			return err
		}
		if out["payments"], err = rows(ctx, tx, `
			SELECT pr.id, pr.receipt_no AS number, pr.business_date AS date, pr.amount,
			       COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id = pr.id), 0) AS applied,
			       CASE WHEN pr.method = 'bank_transfer' THEN 'bank' ELSE pr.method::text END AS method,
			       pr.status::text AS status
			FROM app.payments_received pr
			WHERE pr.customer_id = $1
			ORDER BY pr.business_date DESC, pr.created_at DESC LIMIT 50`, id); err != nil {
			return err
		}
		if out["documents"], err = rows(ctx, tx, `
			SELECT d.id, d.title::text AS name, COALESCE(dt.name::text, CASE WHEN d.is_final_deliverable THEN 'deliverable' ELSE 'required' END) AS doc_type,
			       d.status::text AS status, d.file_id, d.project_id, d.received_at
			FROM app.client_documents d
			LEFT JOIN app.document_types dt ON dt.id = d.document_type_id
			WHERE d.customer_id = $1 ORDER BY d.created_at DESC LIMIT 100`, id); err != nil {
			return err
		}
		out["communication"], err = rows(ctx, tx, `
			SELECT n.id, n.kind::text AS kind, n.summary AS body, n.occurred_at::date AS noted_on, pr.project_no AS project_number, n.project_id
			FROM app.client_interactions n
			LEFT JOIN app.projects pr ON pr.id = n.project_id
			WHERE n.customer_id = $1
			ORDER BY n.occurred_at DESC, n.created_at DESC LIMIT 50`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateClient(ctx context.Context, p *auth.Principal, id uuid.UUID, in ClientIn) (map[string]any, error) {
	var ph string
	if clean(in.Phone) != "" {
		var err error
		if ph, err = phone.Optional(in.Phone); err != nil {
			return nil, err
		}
	}
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		q.Add("c.id = $1 AND c.client_status IS NOT NULL")
		ownClient(q, p)
		before, err := one(ctx, tx, "Client", `SELECT `+clientCols+clientFrom+q.Where()+` FOR UPDATE OF c`, q.Args...)
		if err != nil {
			return err
		}
		srcID, err := upsertLeadSource(ctx, tx, in.Source)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "\n\t\t\tUPDATE app.customers SET\n\t\t\t  full_name = COALESCE(NULLIF($2,''), full_name),\n\t\t\t  phone = COALESCE($3, phone),\n\t\t\t  email = COALESCE(NULLIF($4,'')::citext, email),\n\t\t\t  address = COALESCE(NULLIF($5,''), address),\n\t\t\t  lead_source_id = COALESCE($6, lead_source_id),\n\t\t\t  needs_summary = COALESCE(NULLIF($7,''), needs_summary),\n\t\t\t  notes = COALESCE(NULLIF($8,''), notes),\n\t\t\t  client_status = COALESCE(NULLIF($9,'')::app.client_status, client_status)\n\t\t\tWHERE id = $1", id, clean(in.Name), nz(ph), clean(in.Email), clean(in.Address), srcID, clean(in.Needs), clean(in.Notes), in.Status); err != nil {
			if isDup(err) {
				return httpx.Conflict("duplicate", "A client with that phone already exists")
			}
			return err
		}
		if clean(in.IDNumber) != "" {
			natID, err := s.IDs.protect(in.IDNumber)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "\n\t\t\t\tUPDATE app.customers SET national_id_ciphertext=$2, national_id_key_id=$3, national_id_hmac=$4, national_id_last4=$5\n\t\t\t\tWHERE id=$1", id, natID.cipher, natID.keyID, natID.hmac, natID.last4); err != nil {
				if isDup(err) {
					return httpx.Conflict("duplicate", "A client with that ID number already exists")
				}
				return err
			}
		}
		return s.Audit.Record(ctx, tx, p, "client.updated", "customer", id, before, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

// SetClientStatus converts a lead (active) or updates client_status.
func (s *Service) SetClientStatus(ctx context.Context, p *auth.Principal, id uuid.UUID, status string) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		q.Add("c.id = $1 AND c.client_status IS NOT NULL")
		ownClient(q, p)
		var cur string
		if err := tx.QueryRow(ctx, `SELECT c.client_status::text FROM app.customers c`+q.Where()+` FOR UPDATE`, q.Args...).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Client not found")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.customers SET client_status = $2::app.client_status, archived_at = CASE WHEN $2 = 'inactive' THEN COALESCE(archived_at, now()) ELSE NULL END WHERE id = $1", id, status); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.status", "customer", id, map[string]any{"status": cur}, map[string]any{"status": status})
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

func (s *Service) DeactivateClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		q.Add("c.id = $1 AND c.client_status IS NOT NULL")
		ownClient(q, p)
		c, err := one(ctx, tx, "Client", `SELECT `+clientCols+clientFrom+q.Where()+` FOR UPDATE OF c`, q.Args...)
		if err != nil {
			return err
		}
		if num(c, "balance").IsPositive() {
			return httpx.Conflict("has_balance", "This client still owes "+num(c, "balance").StringFixed(2)+". Collect or void the invoices first")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.customers SET client_status = 'inactive', archived_at = now() WHERE id = $1", id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.deactivated", "customer", id, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

// ClientStatement lists issued invoices (debit) and payments (credit) with a running balance.
func (s *Service) ClientStatement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Client", `SELECT id, full_name::text AS name, phone::text AS phone, email::text AS email FROM app.customers WHERE id = $1 AND client_status IS NOT NULL`, id)
		if err != nil {
			return err
		}
		if from == "" {
			from = "1970-01-01"
		}
		if to == "" {
			to = "2999-12-31"
		}
		opening := money.Zero
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT COALESCE((SELECT SUM(total) FROM app.invoices WHERE customer_id = $1 AND status IN ('issued','partially_paid','paid') AND issue_date < $2::date), 0)\n\t\t\t     - COALESCE((SELECT SUM(amount) FROM app.payments_received WHERE customer_id = $1 AND status = 'posted' AND business_date < $2::date), 0)\n\t\t", id, from).Scan(&opening); err != nil {
			return err
		}
		lines, err := rows(ctx, tx, `
			SELECT * FROM (
			  SELECT issue_date AS date, invoice_no AS number, 'Invoice' AS description, total AS debit, 0::numeric AS credit, created_at
			  FROM app.invoices WHERE customer_id = $1 AND status IN ('issued','partially_paid','paid') AND issue_date BETWEEN $2::date AND $3::date
			  UNION ALL
			  SELECT business_date AS date, receipt_no AS number, 'Payment received' AS description, 0::numeric AS debit, amount AS credit, created_at
			  FROM app.payments_received WHERE customer_id = $1 AND status = 'posted' AND business_date BETWEEN $2::date AND $3::date
			) x ORDER BY date, created_at`, id, from, to)
		if err != nil {
			return err
		}
		bal := opening
		for _, ln := range lines {
			bal = bal.Add(num(ln, "debit")).Sub(num(ln, "credit"))
			ln["balance"] = bal
			delete(ln, "created_at")
		}
		out = map[string]any{"client": c, "from": from, "to": to, "opening_balance": opening, "lines": lines, "closing_balance": bal}
		return nil
	})
	return out, err
}

func statementPDF(m map[string]any) []byte {
	c := m["client"].(map[string]any)
	d := pdf.New()
	d.Heading("Client statement", 15)
	d.KV("Client", export.Cell(c["name"]))
	d.KV("Period", export.Cell(m["from"])+" to "+export.Cell(m["to"]))
	d.KV("Opening balance", export.Cell(m["opening_balance"]))
	d.Space(6)
	var body [][]string
	for _, ln := range m["lines"].([]map[string]any) {
		body = append(body, []string{export.Cell(ln["date"]), export.Cell(ln["number"]), export.Cell(ln["description"]), export.Cell(ln["debit"]), export.Cell(ln["credit"]), export.Cell(ln["balance"])})
	}
	d.Table([]string{"Date", "Number", "Description", "Debit", "Credit", "Balance"}, []float64{1.3, 2, 3, 1.4, 1.4, 1.4}, body)
	d.Rule()
	d.KV("Closing balance", export.Cell(m["closing_balance"]))
	return d.Bytes()
}

// SendClientStatement queues the client's statement summary into app.outbound_messages.
func (s *Service) SendClientStatement(ctx context.Context, p *auth.Principal, id uuid.UUID, channel, from, to, idemKey string) (map[string]any, error) {
	if clean(channel) == "" {
		channel = "sms"
	}
	ch, err := s.Outbound.CheckAvailable(channel)
	if err != nil {
		return nil, err
	}
	if from != "" {
		if _, err := time.Parse("2006-01-02", from); err != nil {
			return nil, httpx.BadRequest("invalid_query", "from must be YYYY-MM-DD")
		}
	}
	if to != "" {
		if _, err := time.Parse("2006-01-02", to); err != nil {
			return nil, httpx.BadRequest("invalid_query", "to must be YYYY-MM-DD")
		}
	}
	if from != "" && to != "" && to < from {
		return nil, httpx.BadRequest("invalid_query", "to must not be before from")
	}
	stmt, err := s.ClientStatement(ctx, p, id, from, to)
	if err != nil {
		return nil, err
	}
	c := stmt["client"].(map[string]any)
	recipient := ""
	if ch == "email" {
		recipient, _ = c["email"].(string)
	} else {
		recipient, _ = c["phone"].(string)
	}
	if len(strings.TrimSpace(recipient)) < 5 {
		return nil, httpx.BadRequest("no_recipient", "Client has no phone number or email for this channel")
	}
	closing := stmt["closing_balance"].(decimal.Decimal)
	period := "all dates"
	if from != "" && to != "" {
		period = from + " to " + to
	} else if from != "" {
		period = "from " + from
	} else if to != "" {
		period = "up to " + to
	}
	name, _ := c["name"].(string)
	body := fmt.Sprintf("Statement for %s (%s): closing balance %s.", name, period, closing.StringFixed(2))
	suffix := clean(idemKey)
	if suffix == "" {
		suffix = ids.New().String()
	}
	key := fmt.Sprintf("client-stmt:%s:%s:%s:%s:%s", id.String(), ch, from, to, suffix)
	var out map[string]any
	err = s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.Outbound.Enqueue(ctx, tx, outbound.Message{
			Channel:        ch,
			Recipient:      recipient,
			Subject:        "Client statement",
			Body:           body,
			IdempotencyKey: key,
			CustomerID:     &id,
		})
		if err != nil {
			return err
		}
		out = map[string]any{"id": row.ID, "status": row.Status}
		return nil
	})
	return out, err
}

// SendClientDebtReminder queues a debt reminder for a service client with an unpaid invoice balance.
func (s *Service) SendClientDebtReminder(ctx context.Context, p *auth.Principal, id uuid.UUID, channel string) (map[string]any, error) {
	if clean(channel) == "" {
		channel = "sms"
	}
	ch, err := s.Outbound.CheckAvailable(channel)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.tx(ctx, p, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Client", `SELECT `+clientCols+clientFrom+` WHERE c.id = $1 AND c.client_status IS NOT NULL`, id)
		if err != nil {
			return err
		}
		bal := num(c, "balance")
		if !bal.IsPositive() {
			return httpx.Conflict("nothing_due", "This client has no outstanding balance")
		}
		recipient := ""
		if ch == "email" {
			recipient, _ = c["email"].(string)
		} else {
			recipient, _ = c["phone"].(string)
		}
		if len(strings.TrimSpace(recipient)) < 5 {
			return httpx.BadRequest("no_recipient", "Client has no phone number or email for this channel")
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		name, _ := c["name"].(string)
		body := outbound.RenderTemplate(ctx, tx, "notifications.sms_template_debt",
			"Reminder: {{name}}, your outstanding balance is {{balance}}.",
			map[string]string{
				"name":    name,
				"balance": bal.StringFixed(2),
			},
		)
		key := fmt.Sprintf("debt-reminder:client:%s:%s:%s", id.String(), ch, today)
		row, err := s.Outbound.Enqueue(ctx, tx, outbound.Message{
			Channel:        ch,
			Recipient:      recipient,
			Subject:        "Payment reminder",
			Body:           body,
			IdempotencyKey: key,
			CustomerID:     &id,
		})
		if err != nil {
			return err
		}
		out = map[string]any{"id": row.ID, "status": row.Status}
		return nil
	})
	return out, err
}
