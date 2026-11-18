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
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/sqlq"
)

type SupplierIn struct {
	Name          string `json:"name" validate:"required,max=200"`
	ContactPerson string `json:"contact_person" validate:"max=200"`
	Phone         string `json:"phone" validate:"max=40"`
	Email         string `json:"email" validate:"omitempty,email,max=200"`
	Address       string `json:"address" validate:"max=500"`
	TaxNumber     string `json:"tax_number" validate:"max=60"`
	Terms         int    `json:"payment_terms_days" validate:"gte=0,lte=365"`
}

// supplierBalanceSQL is what the business owes the supplier, from the ledger:
// accounts payable less supplier advances.
const supplierBalanceSQL = `COALESCE((SELECT SUM(jl.credit - jl.debit) FROM journal_lines jl JOIN ledger_accounts la ON la.id=jl.ledger_account_id
	WHERE jl.party_type='supplier' AND jl.party_id=%s AND la.code IN ('2000','1400')), 0)`

func balanceExpr(col string) string { return strings.Replace(supplierBalanceSQL, "%s", col, 1) }

func (s *Service) CreateSupplier(ctx context.Context, p *auth.Principal, in SupplierIn) (map[string]any, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := httpx.Check(in); err != nil {
		return nil, err
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO suppliers (id, business_id, name, contact_person, phone, email, address, tax_number, payment_terms_days, created_by)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')::citext,NULLIF($7,''),NULLIF($8,''),$9,$10)
		`, id, p.BusinessID, in.Name, in.ContactPerson, in.Phone, in.Email, in.Address, in.TaxNumber, in.Terms, p.UserID)
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		before, err := supplierRow(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE suppliers SET name=$2, contact_person=NULLIF($3,''), phone=NULLIF($4,''), email=NULLIF($5,'')::citext,
				address=NULLIF($6,''), tax_number=NULLIF($7,''), payment_terms_days=$8
			WHERE id=$1
		`, id, in.Name, in.ContactPerson, in.Phone, in.Email, in.Address, in.TaxNumber, in.Terms)
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE suppliers SET is_active=false WHERE id=$1`, id); err != nil {
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
	var contact, phone, email, address, taxNo *string
	var terms int
	var active bool
	var created time.Time
	err := tx.QueryRow(ctx, `
		SELECT name, contact_person, phone, email::text, address, tax_number, payment_terms_days, is_active, created_at FROM suppliers WHERE id=$1
	`, id).Scan(&name, &contact, &phone, &email, &address, &taxNo, &terms, &active, &created)
	if err != nil {
		return nil, httpx.NotFound("not_found", "Supplier not found")
	}
	return map[string]any{"id": id, "name": name, "contact_person": contact, "phone": phone, "email": email, "address": address,
		"tax_number": taxNo, "payment_terms_days": terms, "is_active": active, "created_at": created}, nil
}

func (s *Service) ListSuppliers(ctx context.Context, p *auth.Principal, search string, active *bool, page httpx.Page) ([]map[string]any, int, error) {
	var out []map[string]any
	var total int
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.If(search != "", "(s.name ILIKE '%' || ? || '%' OR s.phone ILIKE '%' || ? || '%')", search, search)
		q.If(active != nil, "s.is_active = ?", active)
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM suppliers s`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.name, s.contact_person, s.phone, s.email::text, s.payment_terms_days, s.is_active, `+balanceExpr("s.id")+`
			FROM suppliers s`+q.Where()+` ORDER BY s.name`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var contact, phone, email *string
			var terms int
			var isActive bool
			var bal decimal.Decimal
			if err := rows.Scan(&id, &name, &contact, &phone, &email, &terms, &isActive, &bal); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "name": name, "contact_person": contact, "phone": phone, "email": email,
				"payment_terms_days": terms, "is_active": isActive, "balance": bal})
		}
		return rows.Err()
	})
	return orEmpty(out), total, err
}

// GetSupplier returns the profile with the account summary of FR-SUP-03.
func (s *Service) GetSupplier(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		row, err := supplierRow(ctx, tx, id)
		if err != nil {
			return err
		}
		var purchased, paid, refunds, balance, advance decimal.Decimal
		if err := tx.QueryRow(ctx, `
			SELECT
			  COALESCE((SELECT SUM(total) FROM purchase_invoices WHERE supplier_id=$1 AND status='posted'),0),
			  COALESCE((SELECT SUM(amount) FROM payments WHERE party_type='supplier' AND party_id=$1 AND kind='supplier_payment' AND status='posted'),0),
			  COALESCE((SELECT SUM(amount) FROM payments WHERE party_type='supplier' AND party_id=$1 AND kind='supplier_refund' AND status='posted'),0),
			  `+balanceExpr("$1")+`,
			  COALESCE((SELECT SUM(jl.debit - jl.credit) FROM journal_lines jl JOIN ledger_accounts la ON la.id=jl.ledger_account_id
			    WHERE jl.party_type='supplier' AND jl.party_id=$1 AND la.code='1400'),0)
		`, id).Scan(&purchased, &paid, &refunds, &balance, &advance); err != nil {
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
		_ = tx.QueryRow(ctx, `SELECT max(created_at) FROM journal_lines jl JOIN journal_entries je ON je.id=jl.entry_id WHERE jl.party_type='supplier' AND jl.party_id=$1`, id).Scan(&last)
		row["account"] = map[string]any{
			"total_purchased": purchased, "total_paid": paid.Sub(refunds), "balance": balance, "advance": advance,
			"aging": buckets, "last_activity": last,
		}
		row["balance"] = balance
		out = row
		return nil
	})
	return out, err
}

func (s *Service) today(ctx context.Context, tx pgx.Tx) string { return s.bizDate(ctx, tx) }

// aging buckets unpaid posted invoices by days since invoice date.
func (s *Service) aging(ctx context.Context, tx pgx.Tx, supplier *uuid.UUID) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, `
		SELECT s.id, s.name, s.phone,
		  COALESCE(SUM(i.balance) FILTER (WHERE $1::date - i.invoice_date <= 30),0),
		  COALESCE(SUM(i.balance) FILTER (WHERE $1::date - i.invoice_date BETWEEN 31 AND 60),0),
		  COALESCE(SUM(i.balance) FILTER (WHERE $1::date - i.invoice_date BETWEEN 61 AND 90),0),
		  COALESCE(SUM(i.balance) FILTER (WHERE $1::date - i.invoice_date > 90),0),
		  COALESCE(SUM(i.balance),0),
		  COALESCE(SUM(i.balance) FILTER (WHERE i.due_date IS NOT NULL AND i.due_date < $1::date),0)
		FROM purchase_invoices i JOIN suppliers s ON s.id=i.supplier_id
		WHERE i.status='posted' AND i.balance > 0 AND ($2::uuid IS NULL OR i.supplier_id=$2)
		GROUP BY s.id ORDER BY SUM(i.balance) DESC, s.name
	`, s.today(ctx, tx), supplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id uuid.UUID
		var name string
		var phone *string
		var a, b, c, d, t, overdue decimal.Decimal
		if err := rows.Scan(&id, &name, &phone, &a, &b, &c, &d, &t, &overdue); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"supplier_id": id, "name": name, "phone": phone, "0_30": a, "31_60": b, "61_90": c, "over_90": d, "total": t, "overdue": overdue})
	}
	return orEmpty(out), rows.Err()
}

// Debts is the aged payables list for all suppliers (GET /suppliers/debts).
func (s *Service) Debts(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	var rows []map[string]any
	totals := map[string]decimal.Decimal{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
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
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, supplier); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, number, supplier_ref, invoice_date::text, due_date::text, total, paid_total, balance, $2::date - invoice_date,
			       (due_date IS NOT NULL AND due_date < $2::date)
			FROM purchase_invoices WHERE supplier_id=$1 AND status='posted' AND balance > 0 ORDER BY invoice_date, number
		`, supplier, s.today(ctx, tx))
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
			out = append(out, map[string]any{"id": id, "number": number, "supplier_ref": ref, "invoice_date": date, "due_date": due, "total": total, "paid_total": paid, "balance": bal, "age_days": age, "overdue": overdue})
		}
		return rows.Err()
	})
	return orEmpty(out), err
}

// Statement lists invoices, returns and payments for the supplier with a
// running balance (FR-SUP-06). Positive balance means the business owes.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, supplier uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		sup, err := supplierRow(ctx, tx, supplier)
		if err != nil {
			return err
		}
		opening := money.Zero
		if from != "" {
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(jl.credit - jl.debit),0) FROM journal_lines jl JOIN ledger_accounts la ON la.id=jl.ledger_account_id
				WHERE jl.party_type='supplier' AND jl.party_id=$1 AND la.code IN ('2000','1400') AND jl.entry_date < $2::date
			`, supplier, from).Scan(&opening); err != nil {
				return err
			}
		}
		q := sqlq.New(supplier)
		q.If(from != "", "jl.entry_date >= ?::date", from)
		q.If(to != "", "jl.entry_date <= ?::date", to)
		rows, err := tx.Query(ctx, `
			SELECT jl.entry_date::text, je.source_type, je.source_id, je.description, SUM(jl.debit), SUM(jl.credit)
			FROM journal_lines jl JOIN journal_entries je ON je.id=jl.entry_id JOIN ledger_accounts la ON la.id=jl.ledger_account_id
			WHERE jl.party_type='supplier' AND jl.party_id=$1 AND la.code IN ('2000','1400')`+q.And()+`
			GROUP BY je.id, jl.entry_date, je.source_type, je.source_id, je.description, je.created_at
			ORDER BY jl.entry_date, je.created_at
		`, q.Args...)
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

// Purchases lists the supplier's orders, receipts and invoices (FR-SUP-02).
func (s *Service) SupplierPurchases(ctx context.Context, p *auth.Principal, supplier uuid.UUID) (map[string]any, error) {
	out := map[string]any{}
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := supplierRow(ctx, tx, supplier); err != nil {
			return err
		}
		branches := p.ScopeBranches("po.view")
		var err error
		out["purchase_orders"], err = s.poRows(ctx, tx, POFilter{SupplierID: &supplier}, branches, 200, 0)
		if err != nil {
			return err
		}
		out["goods_receipts"], err = s.grnRows(ctx, tx, DocFilter{SupplierID: &supplier}, branches, 200, 0)
		if err != nil {
			return err
		}
		out["purchase_invoices"], err = s.invoiceRows(ctx, tx, DocFilter{SupplierID: &supplier}, branches, 200, 0)
		return err
	})
	return out, err
}
