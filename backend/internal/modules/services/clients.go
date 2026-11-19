package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/export"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/pdf"
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
	Status   string `json:"status" validate:"omitempty,oneof=lead active completed"`
}

const clientCols = `c.id, c.name, c.phone, c.email::text AS email, c.address, c.id_number, c.source, c.needs, c.notes, c.status, c.is_active, c.created_at,
	(SELECT count(*) FROM svc_projects pr WHERE pr.client_id = c.id) AS projects,
	COALESCE((SELECT SUM(i.balance) FROM svc_invoices i WHERE i.client_id = c.id AND i.status IN ('issued','partial')), 0) AS balance,
	COALESCE((SELECT SUM(sp.amount - sp.applied) FROM svc_payments sp WHERE sp.client_id = c.id AND sp.status = 'posted'), 0) AS deposit_held`

// ownClient limits a list to clients the user created or runs a project for.
func ownClient(q *sqlq.Q, own *uuid.UUID) {
	if own != nil {
		q.Add(`(c.created_by = ? OR EXISTS (SELECT 1 FROM svc_projects pr WHERE pr.client_id = c.id AND (pr.owner_id = ? OR EXISTS (SELECT 1 FROM svc_project_stages st WHERE st.project_id = pr.id AND st.owner_id = ?))))`, *own, *own, *own)
	}
}

func (s *Service) ListClients(ctx context.Context, p *auth.Principal, q0, status string, active *bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	total := 0
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		if t := clean(q0); t != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%' OR c.email::text ILIKE '%' || ? || '%')", t, t, t)
		}
		q.If(status != "", "c.status = ?", status)
		q.If(active != nil, "c.is_active = ?", active)
		ownClient(q, scopeOwn(p, "clients.view"))
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM svc_clients c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		var err error
		out, err = rows(ctx, tx, `SELECT `+clientCols+` FROM svc_clients c`+q.Where()+` ORDER BY c.name, c.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		return err
	})
	return out, total, err
}

func (s *Service) CreateClient(ctx context.Context, p *auth.Principal, in ClientIn) (map[string]any, error) {
	if clean(in.Name) == "" {
		return nil, httpx.BadRequest("invalid_body", "Client name is required")
	}
	status := in.Status
	if status == "" {
		status = "lead"
	}
	id := ids.New()
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO svc_clients (id, business_id, name, phone, email, address, id_number, source, needs, notes, status, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		`, id, p.BusinessID, clean(in.Name), nz(in.Phone), nz(in.Email), nz(in.Address), nz(in.IDNumber), nz(in.Source), nz(in.Needs), nz(in.Notes), status, p.UserID); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.created", "svc_client", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

func (s *Service) GetClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(id)
		q.Add("c.id = $1")
		ownClient(q, scopeOwn(p, "clients.view"))
		var err error
		out, err = one(ctx, tx, "Client", `SELECT `+clientCols+` FROM svc_clients c`+q.Where(), q.Args...)
		if err != nil {
			return err
		}
		if out["projects_list"], err = rows(ctx, tx, `
			SELECT pr.id, pr.number, pr.name, pr.status, pr.total_price,
			       COALESCE((SELECT SUM(sp.amount) FROM svc_payments sp WHERE sp.project_id = pr.id AND sp.status='posted'),0) AS paid
			FROM svc_projects pr WHERE pr.client_id = $1 ORDER BY pr.created_at DESC`, id); err != nil {
			return err
		}
		if out["invoices"], err = rows(ctx, tx, `
			SELECT id, number, status, issue_date, due_date, total, paid_total, balance FROM svc_invoices WHERE client_id = $1 ORDER BY created_at DESC LIMIT 50`, id); err != nil {
			return err
		}
		if out["payments"], err = rows(ctx, tx, `
			SELECT sp.id, pa.number, pa.payment_date AS date, sp.amount, sp.applied, pa.method, sp.status
			FROM svc_payments sp JOIN payments pa ON pa.id = sp.id WHERE sp.client_id = $1 ORDER BY pa.payment_date DESC, pa.created_at DESC LIMIT 50`, id); err != nil {
			return err
		}
		if out["documents"], err = rows(ctx, tx, `
			SELECT id, name, doc_type, status, file_id, project_id, received_at FROM svc_documents WHERE client_id = $1 ORDER BY created_at DESC LIMIT 100`, id); err != nil {
			return err
		}
		out["communication"], err = rows(ctx, tx, `
			SELECT n.id, n.kind, n.body, n.noted_on, pr.number AS project_number, n.project_id
			FROM svc_project_notes n JOIN svc_projects pr ON pr.id = n.project_id
			WHERE pr.client_id = $1 ORDER BY n.noted_on DESC, n.created_at DESC LIMIT 50`, id)
		return err
	})
	return out, err
}

func (s *Service) UpdateClient(ctx context.Context, p *auth.Principal, id uuid.UUID, in ClientIn) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := one(ctx, tx, "Client", `SELECT `+clientCols+` FROM svc_clients c WHERE c.id=$1 FOR UPDATE OF c`, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE svc_clients SET name=COALESCE(NULLIF($2,''), name), phone=COALESCE(NULLIF($3,''), phone), email=COALESCE(NULLIF($4,'')::citext, email),
			  address=COALESCE(NULLIF($5,''), address), id_number=COALESCE(NULLIF($6,''), id_number), source=COALESCE(NULLIF($7,''), source),
			  needs=COALESCE(NULLIF($8,''), needs), notes=COALESCE(NULLIF($9,''), notes), status=COALESCE(NULLIF($10,''), status)
			WHERE id=$1`, id, clean(in.Name), clean(in.Phone), clean(in.Email), clean(in.Address), clean(in.IDNumber), clean(in.Source), clean(in.Needs), clean(in.Notes), in.Status); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.updated", "svc_client", id, before, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

// SetClientStatus converts a lead (active), completes a client, or toggles is_active.
func (s *Service) SetClientStatus(ctx context.Context, p *auth.Principal, id uuid.UUID, status string) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status FROM svc_clients WHERE id=$1 FOR UPDATE`, id).Scan(&cur); err != nil {
			return httpx.NotFound("not_found", "Client not found")
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_clients SET status=$2 WHERE id=$1`, id, status); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.status", "svc_client", id, map[string]any{"status": cur}, map[string]any{"status": status})
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

func (s *Service) DeactivateClient(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Client", `SELECT `+clientCols+` FROM svc_clients c WHERE c.id=$1 FOR UPDATE OF c`, id)
		if err != nil {
			return err
		}
		if num(c, "balance").IsPositive() {
			return httpx.Conflict("has_balance", "This client still owes "+num(c, "balance").StringFixed(2)+". Collect or void the invoices first")
		}
		if _, err := tx.Exec(ctx, `UPDATE svc_clients SET is_active=false WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "client.deactivated", "svc_client", id, nil, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetClient(ctx, p, id)
}

// ClientStatement lists issued invoices (debit) and payments (credit) with a running balance.
func (s *Service) ClientStatement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.tx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		c, err := one(ctx, tx, "Client", `SELECT id, name, phone FROM svc_clients WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if from == "" {
			from = "1970-01-01"
		}
		if to == "" {
			to = "2999-12-31"
		}
		var opening = money.Zero
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT SUM(total) FROM svc_invoices WHERE client_id=$1 AND status IN ('issued','partial','paid') AND issue_date < $2::date),0)
			     - COALESCE((SELECT SUM(sp.amount) FROM svc_payments sp JOIN payments pa ON pa.id=sp.id WHERE sp.client_id=$1 AND sp.status='posted' AND pa.payment_date < $2::date),0)
		`, id, from).Scan(&opening); err != nil {
			return err
		}
		lines, err := rows(ctx, tx, `
			SELECT * FROM (
			  SELECT issue_date AS date, number, 'Invoice' AS description, total AS debit, 0::numeric AS credit, created_at
			  FROM svc_invoices WHERE client_id=$1 AND status IN ('issued','partial','paid') AND issue_date BETWEEN $2::date AND $3::date
			  UNION ALL
			  SELECT pa.payment_date, pa.number, 'Payment received', 0::numeric, sp.amount, sp.created_at
			  FROM svc_payments sp JOIN payments pa ON pa.id=sp.id WHERE sp.client_id=$1 AND sp.status='posted' AND pa.payment_date BETWEEN $2::date AND $3::date
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

func isDup(err error) bool { return db.IsUnique(err) }
