package purchasing

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/phone"
	"bms/internal/platform/sqlq"
)

type SupplierIn struct {
	Name          string `json:"name" validate:"required,max=200"`
	ContactPerson string `json:"contact_person" validate:"max=200"`
	Phone         string `json:"phone" validate:"max=40"`
	Email         string `json:"email" validate:"omitempty,email,max=200"`
	Address       string `json:"address" validate:"max=500"`
	TaxNumber     string `json:"tax_number" validate:"max=60"`
	GoodsSupplied string `json:"goods_supplied" validate:"max=1000"`
	Terms         int    `json:"payment_terms_days" validate:"gte=0,lte=365"`
}

// supplierBalanceSQL is what the business owes the supplier on non-cancelled purchases.
const supplierBalanceSQL = `COALESCE((SELECT SUM(p.balance_due) FROM app.purchases p
	WHERE p.supplier_id = s.id AND p.status <> 'cancelled'), 0)`

func (s *Service) CreateSupplier(ctx context.Context, p *auth.Principal, in SupplierIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return nil, err
	}
	var id uuid.UUID
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO app.suppliers ( name, contact_person, phone, email, address, tin, goods_supplied, payment_terms_days)
			VALUES ($1,NULLIF($2,''),NULLIF($3,'')::app.phone_e164,NULLIF($4,'')::app.email,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8)
			RETURNING id`, in.Name, strings.TrimSpace(in.ContactPerson), ph, strings.TrimSpace(in.Email),
			strings.TrimSpace(in.Address), strings.TrimSpace(in.TaxNumber), strings.TrimSpace(in.GoodsSupplied), in.Terms).Scan(&id)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "That supplier already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "supplier.created", "supplier", id, nil, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetSupplier(ctx, p, id)
}

func (s *Service) UpdateSupplier(ctx context.Context, p *auth.Principal, id uuid.UUID, in SupplierIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return nil, err
	}
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := supplierRow(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "\n\t\t\tUPDATE app.suppliers\n\t\t\tSET name = $2, contact_person = NULLIF($3,''), phone = NULLIF($4,'')::app.phone_e164, email = NULLIF($5,'')::app.email,\n\t\t\t    address = NULLIF($6,''), tin = NULLIF($7,''), goods_supplied = NULLIF($8,''), payment_terms_days = $9\n\t\t\tWHERE id = $1", id, in.Name, strings.TrimSpace(in.ContactPerson), ph, strings.TrimSpace(in.Email),
			strings.TrimSpace(in.Address), strings.TrimSpace(in.TaxNumber), strings.TrimSpace(in.GoodsSupplied), in.Terms)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "Another supplier already has that name")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "supplier.updated", "supplier", id, before, in)
	})
	if err != nil {
		return nil, err
	}
	return s.GetSupplier(ctx, p, id)
}

func (s *Service) DeactivateSupplier(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE app.suppliers SET status = 'inactive' WHERE id = $1", id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "supplier.deactivated", "supplier", id, map[string]any{"is_active": true}, map[string]any{"is_active": false})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "is_active": false}, nil
}

func supplierRow(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	var name string
	var contact, ph, email, address, tin, goods *string
	var terms *int
	var active bool
	var created time.Time
	err := tx.QueryRow(ctx, "\n\t\tSELECT name::text, contact_person, phone::text, email::text, address, tin, goods_supplied, payment_terms_days, status = 'active', created_at\n\t\tFROM app.suppliers WHERE id = $1", id).
		Scan(&name, &contact, &ph, &email, &address, &tin, &goods, &terms, &active, &created)
	if err != nil {
		return nil, httpx.NotFound("not_found", "Supplier not found")
	}
	t := 0
	if terms != nil {
		t = *terms
	}
	return map[string]any{"id": id, "name": name, "contact_person": contact, "phone": ph, "email": email, "address": address,
		"tax_number": tin, "goods_supplied": goods, "payment_terms_days": t, "is_active": active, "created_at": created}, nil
}

func (s *Service) ListSuppliers(ctx context.Context, p *auth.Principal, search string, active *bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(search != "", "(s.name ILIKE '%' || ? || '%' OR s.phone ILIKE '%' || ? || '%')", search, search)
		q.If(active != nil, "(s.status = 'active') = ?", active)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.suppliers s`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.name::text, s.contact_person, s.phone::text, s.email::text, COALESCE(s.payment_terms_days, 0),
			       s.status = 'active', `+supplierBalanceSQL+`
			FROM app.suppliers s`+q.Where()+` ORDER BY s.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var contact, ph, email *string
			var terms int
			var isActive bool
			var bal decimal.Decimal
			if err := rows.Scan(&id, &name, &contact, &ph, &email, &terms, &isActive, &bal); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "contact_person": contact, "phone": ph, "email": email,
				"payment_terms_days": terms, "is_active": isActive, "balance": bal})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

// GetSupplier returns the supplier profile with account summary and aging.
func (s *Service) GetSupplier(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		row, err := supplierRow(ctx, tx, id)
		if err != nil {
			return err
		}
		var purchased, paid, balance, advance decimal.Decimal
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT\n\t\t\t  COALESCE((SELECT SUM(total) FROM app.purchases WHERE supplier_id = $1 AND status <> 'cancelled'), 0),\n\t\t\t  COALESCE((SELECT SUM(amount) FROM app.payments_made WHERE supplier_id = $1 AND status = 'posted'), 0),\n\t\t\t  COALESCE((SELECT SUM(balance_due) FROM app.purchases WHERE supplier_id = $1 AND status <> 'cancelled'), 0),\n\t\t\t  COALESCE((SELECT SUM(pm.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_made_allocations a WHERE a.payment_made_id = pm.id), 0))\n\t\t\t            FROM app.payments_made pm WHERE pm.supplier_id = $1 AND pm.status = 'posted'), 0)\n\t\t", id).Scan(&purchased, &paid, &balance, &advance); err != nil {
			return err
		}
		aging, err := s.aging(ctx, tx, &id)
		if err != nil {
			return err
		}
		buckets := map[string]any{"0_30": money.Zero, "31_60": money.Zero, "61_90": money.Zero, "over_90": money.Zero}
		if len(aging) == 1 {
			for _, k := range []string{"0_30", "31_60", "61_90", "over_90"} {
				buckets[k] = aging[0][k]
			}
		}
		var last *time.Time
		_ = tx.QueryRow(ctx, "\n\t\t\tSELECT GREATEST(\n\t\t\t\t(SELECT max(created_at) FROM app.purchases WHERE supplier_id = $1),\n\t\t\t\t(SELECT max(created_at) FROM app.payments_made WHERE supplier_id = $1)\n\t\t\t)", id).Scan(&last)
		row["account"] = map[string]any{
			"total_purchased": purchased, "total_paid": paid, "balance": balance, "advance": advance,
			"aging": buckets, "last_activity": last,
		}
		row["balance"] = balance
		out = row
		return nil
	})
	return out, err
}

func (s *Service) today(ctx context.Context, tx pgx.Tx) string { return s.bizDate(ctx, tx) }

// aging buckets unpaid purchases by days since order date.
func (s *Service) aging(ctx context.Context, tx pgx.Tx, supplier *uuid.UUID) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, "\n\t\tSELECT s.id, s.name::text, s.phone::text,\n\t\t  COALESCE(SUM(p.balance_due) FILTER (WHERE $1::date - p.order_date <= 30), 0),\n\t\t  COALESCE(SUM(p.balance_due) FILTER (WHERE $1::date - p.order_date BETWEEN 31 AND 60), 0),\n\t\t  COALESCE(SUM(p.balance_due) FILTER (WHERE $1::date - p.order_date BETWEEN 61 AND 90), 0),\n\t\t  COALESCE(SUM(p.balance_due) FILTER (WHERE $1::date - p.order_date > 90), 0),\n\t\t  COALESCE(SUM(p.balance_due), 0),\n\t\t  COALESCE(SUM(p.balance_due) FILTER (WHERE p.due_date IS NOT NULL AND p.due_date < $1::date), 0)\n\t\tFROM app.purchases p JOIN app.suppliers s ON s.id = p.supplier_id\n\t\tWHERE p.status <> 'cancelled' AND p.balance_due > 0\n\t\t  AND ($2::uuid IS NULL OR p.supplier_id = $2)\n\t\tGROUP BY s.id ORDER BY SUM(p.balance_due) DESC, s.name\n\t", s.today(ctx, tx), supplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var name string
		var ph *string
		var a, b, c, d, t, overdue decimal.Decimal
		if err := rows.Scan(&id, &name, &ph, &a, &b, &c, &d, &t, &overdue); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"supplier_id": id, "name": name, "phone": ph, "0_30": a, "31_60": b, "61_90": c, "over_90": d, "total": t, "overdue": overdue})
	}
	return orEmpty(out), rows.Err()
}

// Debts is the aged payables list for all suppliers.
func (s *Service) Debts(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	var rows []map[string]any
	totals := map[string]decimal.Decimal{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rows, err = s.aging(ctx, tx, nil)
		return err
	})
	for _, r := range rows {
		for _, k := range []string{"0_30", "31_60", "61_90", "over_90", "total", "overdue"} {
			totals[k] = totals[k].Add(r[k].(decimal.Decimal))
		}
	}
	return map[string]any{"suppliers": rows, "totals": totals}, err
}

func (s *Service) Unpaid(ctx context.Context, p *auth.Principal, supplier uuid.UUID) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, supplier); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT id, purchase_no, supplier_invoice_ref, order_date::text, due_date::text, total, amount_paid, balance_due,\n\t\t\t       $2::date - order_date, (due_date IS NOT NULL AND due_date < $2::date)\n\t\t\tFROM app.purchases\n\t\t\tWHERE supplier_id = $1 AND status <> 'cancelled' AND balance_due > 0\n\t\t\tORDER BY order_date, purchase_no\n\t\t", supplier, s.today(ctx, tx))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var number, date string
			var ref, due *string
			var total, paid, bal decimal.Decimal
			var age int
			var overdue bool
			if err := rows.Scan(&id, &number, &ref, &date, &due, &total, &paid, &bal, &age, &overdue); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "supplier_ref": ref, "invoice_date": date, "due_date": due,
				"total": total, "paid_total": paid, "balance": bal, "age_days": age, "overdue": overdue})
		}
		return rows.Err()
	})
	return orEmpty(out), err
}

// Statement lists purchases and payments for the supplier with a running balance.
// Positive balance means the business owes the supplier.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, supplier uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		sup, err := supplierRow(ctx, tx, supplier)
		if err != nil {
			return err
		}
		opening := money.Zero
		if from != "" {
			if err := tx.QueryRow(ctx, "\n\t\t\t\tSELECT COALESCE((SELECT SUM(total) FROM app.purchases WHERE supplier_id = $1 AND status <> 'cancelled' AND order_date < $2::date), 0)\n\t\t\t\t     - COALESCE((SELECT SUM(amount) FROM app.payments_made WHERE supplier_id = $1 AND status = 'posted' AND business_date < $2::date), 0)\n\t\t\t", supplier, from).Scan(&opening); err != nil {
				return err
			}
		}
		q := sqlq.New(supplier)
		q.If(from != "", "e.dt >= ?::date", from)
		q.If(to != "", "e.dt <= ?::date", to)
		rows, err := tx.Query(ctx, `
			SELECT e.dt::text, e.kind, e.id, e.descr, e.debit, e.credit FROM (
				SELECT order_date AS dt, created_at AS ts, 'purchase' AS kind, id, 'Purchase ' || purchase_no AS descr,
				       0::numeric(18,2) AS debit, total AS credit
				FROM app.purchases WHERE supplier_id = $1 AND status <> 'cancelled'
				UNION ALL
				SELECT business_date AS dt, created_at AS ts, 'payment' AS kind, id, 'Payment ' || payment_no AS descr,
				       amount AS debit, 0::numeric(18,2) AS credit
				FROM app.payments_made WHERE supplier_id = $1 AND status = 'posted'
			) e`+q.Where()+` ORDER BY e.dt, e.ts, e.id`, q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		running := opening
		lines := []map[string]any{}
		for rows.Next() {
			var date, st, desc string
			var src uuid.UUID
			var dr, cr decimal.Decimal
			if err := rows.Scan(&date, &st, &src, &desc, &dr, &cr); err != nil {
				return err
			}
			running = running.Add(cr).Sub(dr)
			lines = append(lines, map[string]any{"date": date, "type": st, "document_id": src, "description": desc, "debit": dr, "credit": cr, "balance": running})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out = map[string]any{"supplier": sup, "from": from, "to": to, "opening_balance": opening, "lines": lines, "closing_balance": running}
		return nil
	})
	return out, err
}

// SupplierPurchases lists the supplier's purchases and goods receipts.
func (s *Service) SupplierPurchases(ctx context.Context, p *auth.Principal, supplier uuid.UUID) (map[string]any, error) {
	out := map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, supplier); err != nil {
			return err
		}
		var err error
		out["purchase_orders"], err = s.poRows(ctx, tx, POFilter{SupplierID: &supplier}, p.BranchID, 200, 0)
		if err != nil {
			return err
		}
		out["goods_receipts"], err = s.grnRows(ctx, tx, DocFilter{SupplierID: &supplier}, p.BranchID, 200, 0)
		if err != nil {
			return err
		}
		out["purchase_invoices"] = []map[string]any{}
		return nil
	})
	return out, err
}
