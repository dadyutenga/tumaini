package sales

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/sequence"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

// Hold saves a priced cart for later. Held carts reserve no stock and post nothing (FR-SAL-09).
func (s *Service) Hold(ctx context.Context, p *auth.Principal, in CreateIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if len(in.Items) == 0 {
		return nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lines, tot, includeTax, err := s.price(ctx, tx, p, in)
		if err != nil {
			return err
		}
		if in.CustomerID != nil {
			if _, err := s.resolveCustomer(ctx, tx, in.CustomerID, money.Zero); err != nil {
				return err
			}
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		in.Payments = nil
		in.HeldID = nil
		cart, err := json.Marshal(in)
		if err != nil {
			return err
		}
		id := ids.New()
		number := "HOLD-" + strings.ReplaceAll(id.String(), "-", "")
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales (id, business_id, branch_id, number, kind, status, customer_id, cashier_id, employee_id, business_date,
				subtotal, discount_total, tax_total, total, balance, note, prices_include_tax, cart)
			VALUES ($1,$2,$3,$4,'sale','held',$5,$6,$7,$8,$9,$10,$11,$12,$12,NULLIF($13,''),$14,$15)
		`, id, p.BusinessID, *p.BranchID, number, in.CustomerID, p.UserID, p.EmployeeID, date, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, in.Note, includeTax, cart); err != nil {
			return err
		}
		if err := insertItems(ctx, tx, p.BusinessID, id, lines); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale.held", "sale", id, nil, map[string]any{"total": tot.Total}); err != nil {
			return err
		}
		out = view(id, number, "held", lines, tot, money.Zero, tot.Total, money.Zero, canSeeCost(p))
		out["note"] = in.Note
		out["customer_id"] = in.CustomerID
		out["cart"] = json.RawMessage(cart)
		return nil
	})
	return out, err
}

// Held lists the caller's own held carts in the active branch.
func (s *Service) Held(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New().Add("s.status = 'held'").Add("s.cashier_id = ?", p.UserID)
		q.If(p.BranchID != nil, "s.branch_id = ?", p.BranchID)
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.number, s.note, s.customer_id, c.name, s.total, s.cart, s.created_at, s.branch_id,
			       (SELECT count(*) FROM sale_items si WHERE si.sale_id=s.id)
			FROM sales s LEFT JOIN customers c ON c.id=s.customer_id`+q.Where()+` ORDER BY s.created_at DESC LIMIT 100`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch uuid.UUID
			var number string
			var note, cname *string
			var cust *uuid.UUID
			var total decimal.Decimal
			var cart []byte
			var created time.Time
			var n int
			if err := rows.Scan(&id, &number, &note, &cust, &cname, &total, &cart, &created, &branch, &n); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "note": note, "customer_id": cust, "customer_name": cname,
				"total": total, "cart": json.RawMessage(cart), "created_at": created, "branch_id": branch, "item_count": n})
		}
		return rows.Err()
	})
	return out, err
}

func (s *Service) DiscardHeld(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM sales WHERE id=$1 AND status='held' AND cashier_id=$2`, id, p.UserID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Held cart not found")
		}
		return s.Audit.Record(ctx, tx, p, "sale.held_discarded", "sale", id, nil, nil)
	})
}

type QuoteIn struct {
	CustomerID *uuid.UUID `json:"customer_id"`
	Items      []ItemIn   `json:"items"`
	Discount   *Discount  `json:"discount"`
	Note       string     `json:"note"`
	ValidUntil string     `json:"valid_until" validate:"omitempty,date"`
}

func (q QuoteIn) cart() CreateIn {
	return CreateIn{CustomerID: q.CustomerID, Items: q.Items, Discount: q.Discount, Note: q.Note}
}

// quoteParts prices a quotation and resolves its customer and validity date.
func (s *Service) quoteParts(ctx context.Context, tx pgx.Tx, p *auth.Principal, in QuoteIn) ([]line, totals, bool, uuid.UUID, string, string, []byte, error) {
	if len(in.Items) == 0 {
		return nil, totals{}, false, uuid.Nil, "", "", nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	cin := in.cart()
	lines, tot, include, err := s.price(ctx, tx, p, cin)
	if err != nil {
		return nil, totals{}, false, uuid.Nil, "", "", nil, err
	}
	cust, err := s.resolveCustomer(ctx, tx, in.CustomerID, money.Zero)
	if err != nil {
		return nil, totals{}, false, uuid.Nil, "", "", nil, err
	}
	date, err := s.bizDate(ctx, tx)
	if err != nil {
		return nil, totals{}, false, uuid.Nil, "", "", nil, err
	}
	valid := in.ValidUntil
	if valid == "" {
		valid = mustDate(date).AddDate(0, 0, setting.Int(ctx, tx, "invoice.quotation_valid_days", 14)).Format("2006-01-02")
	}
	if valid < date {
		return nil, totals{}, false, uuid.Nil, "", "", nil, httpx.BadRequest("invalid_body", "valid_until cannot be in the past")
	}
	cart, err := json.Marshal(cin)
	if err != nil {
		return nil, totals{}, false, uuid.Nil, "", "", nil, err
	}
	return lines, tot, include, cust, date, valid, cart, nil
}

// CreateQuotation saves a priced cart with no stock or money effect (FR-SAL-13).
func (s *Service) CreateQuotation(ctx context.Context, p *auth.Principal, in QuoteIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	var id uuid.UUID
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lines, tot, include, cust, date, valid, cart, err := s.quoteParts(ctx, tx, p, in)
		if err != nil {
			return err
		}
		number, err := sequence.Next(ctx, tx, *p.BranchID, "quotation")
		if err != nil {
			return err
		}
		id = ids.New()
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales (id, business_id, branch_id, number, kind, status, customer_id, cashier_id, employee_id, business_date,
				subtotal, discount_total, tax_total, total, paid_total, balance, note, prices_include_tax, valid_until, cart)
			VALUES ($1,$2,$3,$4,'quotation','draft',$5,$6,$7,$8,$9,$10,$11,$12,0,$12,NULLIF($13,''),$14,$15,$16)
		`, id, p.BusinessID, *p.BranchID, number, cust, p.UserID, p.EmployeeID, date, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, in.Note, include, valid, cart); err != nil {
			return err
		}
		if err := insertItems(ctx, tx, p.BusinessID, id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "quotation.created", "quotation", id, nil, map[string]any{"number": number, "total": tot.Total, "valid_until": valid})
	})
	if err != nil {
		return nil, err
	}
	return s.Quotation(ctx, p, id)
}

func (s *Service) UpdateQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in QuoteIn) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var branch uuid.UUID
		var kind, st string
		var before decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT branch_id, kind, status, total FROM sales WHERE id=$1 FOR UPDATE`, id).Scan(&branch, &kind, &st, &before); err != nil || kind != "quotation" || !p.SeesBranch("quotations.manage", branch) {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "Only an open quotation can be edited")
		}
		pp := *p
		pp.BranchID = &branch
		lines, tot, include, cust, _, valid, cart, err := s.quoteParts(ctx, tx, &pp, in)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sale_items WHERE sale_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sales SET customer_id=$2, subtotal=$3, discount_total=$4, tax_total=$5, total=$6, balance=$6, note=NULLIF($7,''),
				prices_include_tax=$8, valid_until=$9, cart=$10 WHERE id=$1
		`, id, cust, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, in.Note, include, valid, cart); err != nil {
			return err
		}
		if err := insertItems(ctx, tx, p.BusinessID, id, lines); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "quotation.updated", "quotation", id, map[string]any{"total": before}, map[string]any{"total": tot.Total, "valid_until": valid})
	})
	if err != nil {
		return nil, err
	}
	return s.Quotation(ctx, p, id)
}

func (s *Service) Quotation(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, "quotations.manage", id, "quotation")
		out = m
		return err
	})
	return out, err
}

type QuoteFilter struct {
	Status     string
	CustomerID *uuid.UUID
	Q          string
	From, To   string
}

func (s *Service) Quotations(ctx context.Context, p *auth.Principal, f QuoteFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New().Add("s.kind = 'quotation'")
		if b := p.ScopeBranches("quotations.manage"); b != nil {
			q.Add("s.branch_id = ANY(?)", b)
		}
		if own := p.OwnOnly("quotations.manage"); own != nil {
			q.Add("s.cashier_id = ?", *own)
		}
		q.If(f.Status != "", "s.status = ?", f.Status)
		q.If(f.CustomerID != nil, "s.customer_id = ?", f.CustomerID)
		q.If(f.From != "", "s.business_date >= ?::date", f.From)
		q.If(f.To != "", "s.business_date <= ?::date", f.To)
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(s.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%')", qq, qq)
		}
		from := ` FROM sales s LEFT JOIN customers c ON c.id=s.customer_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.number, s.status, s.customer_id, c.name, s.total, s.business_date::text, s.valid_until::text, s.converted_sale_id, s.branch_id, s.created_at`+
			from+q.Where()+` ORDER BY s.created_at DESC`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, branch uuid.UUID
			var number, st, date string
			var cust, conv *uuid.UUID
			var cname, valid *string
			var tot decimal.Decimal
			var created any
			if err := rows.Scan(&id, &number, &st, &cust, &cname, &tot, &date, &valid, &conv, &branch, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "status": st, "customer_id": cust, "customer_name": cname, "total": tot,
				"business_date": date, "valid_until": valid, "converted_sale_id": conv, "branch_id": branch, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}

type ConvertIn struct {
	Payments []PayIn `json:"payments"`
	Note     string  `json:"note"`
}

// ConvertQuotation turns an open quotation into a sale at the quoted prices.
func (s *Service) ConvertQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in ConvertIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
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
		var kind, st, number string
		var valid *string
		var cart []byte
		if err := tx.QueryRow(ctx, `SELECT branch_id, kind, status, number, valid_until::text, cart FROM sales WHERE id=$1 FOR UPDATE`, id).Scan(&branch, &kind, &st, &number, &valid, &cart); err != nil || kind != "quotation" || !p.SeesBranch("sales.create", branch) {
			return httpx.NotFound("not_found", "Quotation not found")
		}
		if st != "draft" {
			return httpx.Conflict("invalid_status", "This quotation is no longer open")
		}
		if branch != *p.BranchID {
			return httpx.Conflict("invalid_status", "Switch to the quotation's branch to convert it")
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if valid != nil && *valid < today {
			return httpx.Conflict("quotation_expired", "This quotation has expired. Create a new one at current prices")
		}
		var cin CreateIn
		if err := json.Unmarshal(cart, &cin); err != nil {
			return err
		}
		cin.Payments = in.Payments
		if in.Note != "" {
			cin.Note = in.Note
		}
		cin.quoted = map[uuid.UUID]decimal.Decimal{}
		rows, err := tx.Query(ctx, `SELECT product_id, unit_price FROM sale_items WHERE sale_id=$1`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid uuid.UUID
			var price decimal.Decimal
			if err := rows.Scan(&pid, &price); err != nil {
				rows.Close()
				return err
			}
			cin.quoted[pid] = price
		}
		rows.Close()
		cin.quotationID = &id
		st2, data, err := s.createTx(ctx, tx, p, cin)
		if err != nil {
			return err
		}
		saleID := data["id"]
		if _, err := tx.Exec(ctx, `UPDATE sales SET status='converted', converted_sale_id=$2 WHERE id=$1`, id, saleID); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "quotation.converted", "quotation", id, nil, map[string]any{"number": number, "sale_id": saleID}); err != nil {
			return err
		}
		data["quotation_id"] = id
		raw, err := idem.Complete(ctx, tx, p.UserID, st2, httpx.Data(data))
		status, body = st2, raw
		return err
	})
	return status, body, err
}
