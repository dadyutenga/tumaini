package sales

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

// Hold saves a priced cart for later. Held carts reserve no stock and post nothing.
func (s *Service) Hold(ctx context.Context, p *auth.Principal, in CreateIn) (map[string]any, error) {
	if p.BranchID == nil {
		return nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if len(in.Items) == 0 {
		return nil, httpx.BadRequest("invalid_body", "Add at least one product")
	}
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		lines, tot, err := s.price(ctx, tx, p, in, false)
		if err != nil {
			return err
		}
		cust, err := s.resolveCustomer(ctx, tx, in.CustomerID, money.Zero)
		if err != nil {
			return err
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		label := strings.TrimSpace(in.Note)
		if len(label) > 60 {
			label = label[:60]
		}
		var id uuid.UUID
		if err := tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.sales ( business_unit_id, location_id, status, customer_id, sold_by, business_date,\n\t\t\t                       subtotal, discount_type, discount_value, discount_amount, tax_amount, total, held_label, notes)\n\t\t\tVALUES ( (SELECT business_unit_id FROM app.locations WHERE id = $1), $1, 'draft', $2, $3, $4::date,\n\t\t\t        $5, $6::app.discount_type, $7, $8, $9, $10, NULLIF($11,''), NULLIF($12,''))\n\t\t\tRETURNING id", *p.BranchID, cust, p.MembershipID, date,
			tot.Subtotal, tot.DiscountType, tot.DiscountValue, tot.Discount, tot.Tax, tot.Total, label, strings.TrimSpace(in.Note)).Scan(&id); err != nil {
			return err
		}
		if err := insertLines(ctx, tx, id, lines); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE app.sales SET status = 'held', held_at = now() WHERE id = $1", id); err != nil {
			return err
		}
		if err := s.Audit.Record(ctx, tx, p, "sale.held", "sale", id, nil, map[string]any{"total": tot.Total}); err != nil {
			return err
		}
		out = view(id, "", "held", lines, tot, money.Zero, tot.Total, money.Zero, canSeeCost(p))
		out["note"] = in.Note
		out["held_label"] = label
		out["customer_id"] = cust
		return nil
	})
	return out, err
}

// Held lists the caller's own held carts (or all held carts in the active location).
func (s *Service) Held(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("s.status = 'held'").Add("s.sold_by = ?", p.MembershipID)
		q.If(p.BranchID != nil, "s.location_id = ?", p.BranchID)
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.held_label, s.notes, s.customer_id, c.full_name::text, s.total, s.discount_type::text, s.discount_value,
			       s.created_at, s.location_id, (SELECT count(*) FROM app.sale_lines sl WHERE sl.sale_id = s.id)
			FROM app.sales s LEFT JOIN app.customers c ON c.id = s.customer_id`+q.Where()+
			` ORDER BY s.created_at DESC LIMIT 100`, q.Args...)
		if err != nil {
			return err
		}
		type heldRow struct {
			id, branch  uuid.UUID
			cust        *uuid.UUID
			label, note *string
			cname       *string
			discType    *string
			discVal     decimal.Decimal
			total       decimal.Decimal
			created     time.Time
			n           int
		}
		var list []heldRow
		for rows.Next() {
			var h heldRow
			if err := rows.Scan(&h.id, &h.label, &h.note, &h.cust, &h.cname, &h.total, &h.discType, &h.discVal, &h.created, &h.branch, &h.n); err != nil {
				rows.Close()
				return err
			}
			list = append(list, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range list {
			lrows, err := tx.Query(ctx, "\n\t\t\t\tSELECT product_id, quantity, line_discount FROM app.sale_lines\n\t\t\t\tWHERE sale_id = $1 ORDER BY line_no", h.id)
			if err != nil {
				return err
			}
			var items []ItemIn
			for lrows.Next() {
				var pid uuid.UUID
				var qty, ld decimal.Decimal
				if err := lrows.Scan(&pid, &qty, &ld); err != nil {
					lrows.Close()
					return err
				}
				it := ItemIn{ProductID: pid, Qty: qty}
				if ld.IsPositive() {
					it.Discount = &Discount{Type: "amount", Value: ld}
				}
				items = append(items, it)
			}
			lrows.Close()
			cart := CreateIn{CustomerID: h.cust, Items: items}
			if h.note != nil {
				cart.Note = *h.note
			}
			if h.discType != nil && h.discVal.IsPositive() {
				cart.Discount = &Discount{Type: *h.discType, Value: h.discVal}
			}
			out = append(out, map[string]any{
				"id": h.id, "held_label": h.label, "note": h.note, "customer_id": h.cust, "customer_name": h.cname,
				"total": h.total, "cart": cart, "created_at": h.created, "branch_id": h.branch, "item_count": h.n,
			})
		}
		return nil
	})
	return out, err
}

func (s *Service) DiscardHeld(ctx context.Context, p *auth.Principal, id uuid.UUID) error {
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM app.sales WHERE id = $1 AND status = 'held' AND sold_by = $2", id, p.MembershipID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Held cart not found")
		}
		return s.Audit.Record(ctx, tx, p, "sale.held_discarded", "sale", id, nil, nil)
	})
}

// Shop quotations are not part of the approved schema; quotations live in the services module
// (app.quotations / /service-quotations). These endpoints stay mounted and answer 410 / empty list.

type QuoteIn struct {
	CustomerID *uuid.UUID `json:"customer_id"`
	Items      []ItemIn   `json:"items"`
	Discount   *Discount  `json:"discount"`
	Note       string     `json:"note"`
	ValidUntil string     `json:"valid_until" validate:"omitempty,date"`
}

type QuoteFilter struct {
	Status     string
	CustomerID *uuid.UUID
	Q          string
	From, To   string
}

type ConvertIn struct {
	Payments []PayIn `json:"payments"`
	Note     string  `json:"note"`
}

func (s *Service) Quotations(ctx context.Context, p *auth.Principal, f QuoteFilter, page httpx.Page) ([]map[string]any, int, error) {
	return []map[string]any{}, 0, nil
}

func (s *Service) CreateQuotation(ctx context.Context, p *auth.Principal, in QuoteIn) (map[string]any, error) {
	return nil, removed("Shop quotations (use service quotations)")
}

func (s *Service) UpdateQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in QuoteIn) (map[string]any, error) {
	return nil, removed("Shop quotations (use service quotations)")
}

func (s *Service) Quotation(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Shop quotations (use service quotations)")
}

func (s *Service) QuotationDoc(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	return nil, removed("Shop quotations (use service quotations)")
}

func (s *Service) ConvertQuotation(ctx context.Context, p *auth.Principal, id uuid.UUID, in ConvertIn) (int, []byte, error) {
	return 0, nil, removed("Shop quotations (use service quotations)")
}
