package sales

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/sqlq"
)

type ListFilter struct {
	From, To   string
	Status     string
	CustomerID *uuid.UUID
	CashierID  *uuid.UUID
	Method     string
	Q          string
	Sort       string
}

var sortable = map[string]string{
	"created_at": "s.created_at", "business_date": "s.business_date", "total": "s.total",
	"number": "s.receipt_no", "balance": "s.balance_due",
}

// List returns non-held sales in the caller's scope. Without shop.sale.view_all a person sees only their own sales.
func (s *Service) List(ctx context.Context, p *auth.Principal, f ListFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("s.status <> 'held'")
		q.If(p.BranchID != nil, "s.location_id = ?", p.BranchID)
		q.If(!p.Has("shop.sale.view_all"), "s.sold_by = ?", p.MembershipID)
		q.If(f.From != "", "s.business_date >= ?::date", f.From)
		q.If(f.To != "", "s.business_date <= ?::date", f.To)
		q.If(f.Status != "", "s.status = ?::app.sale_status", f.Status)
		q.If(f.CustomerID != nil, "s.customer_id = ?", f.CustomerID)
		q.If(f.CashierID != nil, "(m.user_id = ? OR s.sold_by = ?)", f.CashierID, f.CashierID)
		if m := payments.Method(f.Method); f.Method != "" {
			if m == "" {
				return nil
			}
			q.Add("EXISTS (SELECT 1 FROM app.payment_allocations pa JOIN app.payments_received pr ON pr.id = pa.payment_received_id WHERE pa.sale_id = s.id AND pr.method = ?::app.payment_method AND pr.status = 'posted')", m)
		}
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(s.receipt_no ILIKE '%' || ? || '%' OR c.full_name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%')", qq, qq, qq)
		}
		from := ` FROM app.sales s
			LEFT JOIN app.customers c ON c.id = s.customer_id
			LEFT JOIN app.memberships m ON m.id = s.sold_by
			LEFT JOIN iam.users u ON u.id = m.user_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		order := httpx.Sort(f.Sort, sortable, "s.created_at DESC")
		rows, err := tx.Query(ctx, `
			SELECT s.id, COALESCE(s.receipt_no, ''), s.status::text, s.total, s.amount_paid, s.balance_due,
			       s.business_date::text, COALESCE(m.user_id, s.sold_by), s.customer_id, c.full_name::text,
			       u.full_name::text, s.location_id, s.discount_amount, s.tax_amount, s.created_at`+from+q.Where()+
			` ORDER BY `+order+`, s.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, cashier, branch uuid.UUID
			var customer *uuid.UUID
			var custName, cashierName *string
			var number, status, date string
			var totalD, paid, bal, disc, tax decimal.Decimal
			var created any
			if err := rows.Scan(&id, &number, &status, &totalD, &paid, &bal, &date, &cashier, &customer, &custName, &cashierName, &branch, &disc, &tax, &created); err != nil {
				return err
			}
			out = append(out, map[string]any{"id": id, "number": number, "status": status, "total": totalD, "paid_total": paid, "balance": bal,
				"business_date": date, "cashier_id": cashier, "cashier_name": cashierName, "customer_id": customer, "customer_name": custName,
				"branch_id": branch, "discount_total": disc, "tax_total": tax, "created_at": created})
		}
		return rows.Err()
	})
	return out, total, err
}

// loadSale reads a sale header and enforces own-sales scoping when shop.sale.view_all is absent.
func (s *Service) loadSale(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var number, status, date string
	var branch, soldBy uuid.UUID
	var customer, voidedBy *uuid.UUID
	var sub, disc, tax, total, paid, bal decimal.Decimal
	var note, dueDate, voidReason *string
	var created, completed, voidedAt any
	err := tx.QueryRow(ctx, "\n\t\tSELECT COALESCE(receipt_no, ''), status::text, location_id, customer_id, sold_by, business_date::text,\n\t\t       subtotal, discount_amount, tax_amount, total, amount_paid, balance_due, notes, due_date::text,\n\t\t       created_at, completed_at, voided_by, voided_at, void_reason\n\t\tFROM app.sales WHERE id = $1", id).
		Scan(&number, &status, &branch, &customer, &soldBy, &date, &sub, &disc, &tax, &total, &paid, &bal,
			&note, &dueDate, &created, &completed, &voidedBy, &voidedAt, &voidReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("not_found", "Sale not found")
	}
	if err != nil {
		return nil, err
	}
	if !p.Has("shop.sale.view_all") && soldBy != p.MembershipID {
		return nil, httpx.NotFound("not_found", "Sale not found")
	}
	var cashierUser uuid.UUID
	var custName, custPhone, cashierName, branchName, footer *string
	if err := tx.QueryRow(ctx, "\n\t\tSELECT COALESCE((SELECT user_id FROM app.memberships WHERE id = $2), $2),\n\t\t       (SELECT full_name::text FROM app.customers WHERE id = $1),\n\t\t       (SELECT phone::text FROM app.customers WHERE id = $1),\n\t\t       (SELECT u.full_name::text FROM app.memberships m JOIN iam.users u ON u.id = m.user_id WHERE m.id = $2),\n\t\t       (SELECT name::text FROM app.locations WHERE id = $3),\n\t\t       (SELECT receipt_footer FROM app.business_settings WHERE true)", customer, soldBy, branch).Scan(&cashierUser, &custName, &custPhone, &cashierName, &branchName, &footer); err != nil {
		return nil, err
	}
	items, costTotal, err := s.items(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id": id, "number": number, "kind": "sale", "status": status, "branch_id": branch, "customer_id": customer,
		"cashier_id": cashierUser, "sold_by": soldBy, "business_date": date, "subtotal": sub, "discount_total": disc,
		"tax_total": tax, "total": total, "paid_total": paid, "balance": bal, "change_due": decimal.Zero,
		"receipt_footer": footer, "prices_include_tax": true, "note": note, "due_date": dueDate,
		"created_at": created, "completed_at": completed, "voided_by": voidedBy, "voided_at": voidedAt, "void_reason": voidReason,
		"customer_name": custName, "customer_phone": custPhone, "cashier_name": cashierName, "branch_name": branchName,
		"items": items,
	}
	if canSeeCost(p) {
		out["cost_total"] = costTotal
		out["profit"] = total.Sub(tax).Sub(costTotal)
	}
	return out, nil
}

func (s *Service) items(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) ([]map[string]any, decimal.Decimal, error) {
	rows, err := tx.Query(ctx, "\n\t\tSELECT sl.id, sl.product_id, sl.product_name::text, sl.quantity, sl.unit_price, COALESCE(sl.unit_cost, 0),\n\t\t       sl.line_discount, sl.tax_rate_pct, sl.tax_amount, sl.line_total, sl.returned_quantity, sl.warranty_months,\n\t\t       COALESCE((SELECT string_agg(pu.serial_no, ', ' ORDER BY pu.serial_no)\n\t\t                 FROM app.sale_line_units slu\n\t\t                 JOIN app.product_units pu ON pu.id = slu.product_unit_id\n\t\t                 WHERE slu.sale_line_id = sl.id), '')\n\t\tFROM app.sale_lines sl\n\t\tWHERE sl.sale_id = $1\n\t\tORDER BY sl.line_no", id)
	if err != nil {
		return nil, decimal.Zero, err
	}
	defer rows.Close()
	showCost := canSeeCost(p)
	items := []map[string]any{}
	costTotal := decimal.Zero
	for rows.Next() {
		var itemID, pid uuid.UUID
		var name, serial string
		var warranty int
		var qty, price, ucost, d, rate, taxA, lineT, ret decimal.Decimal
		if err := rows.Scan(&itemID, &pid, &name, &qty, &price, &ucost, &d, &rate, &taxA, &lineT, &ret, &warranty, &serial); err != nil {
			return nil, decimal.Zero, err
		}
		var serialOut *string
		if serial != "" {
			sCopy := serial
			serialOut = &sCopy
		}
		item := map[string]any{"id": itemID, "product_id": pid, "product_name": name, "qty": qty, "unit_price": price,
			"discount": d, "tax_rate": rate, "tax_amount": taxA, "line_total": lineT, "qty_returned": ret,
			"serial_number": serialOut, "warranty_months": warranty}
		if showCost {
			item["unit_cost"] = ucost
			costTotal = costTotal.Add(qty.Mul(ucost).Round(2))
		}
		items = append(items, item)
	}
	return items, costTotal, rows.Err()
}

// Get returns a sale with items, payments, returns, approvals and its audit trail.
func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if m["payments"], err = salePayments(ctx, tx, id); err != nil {
			return err
		}
		returns := []map[string]any{}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT r.id, r.return_no, r.sale_id, COALESCE(s.receipt_no, ''), r.status::text, r.resolution::text,\n\t\t\t       r.reason, r.refund_amount, r.requested_by, r.decided_by, r.created_at, r.completed_at, r.location_id\n\t\t\tFROM app.sale_returns r JOIN app.sales s ON s.id = r.sale_id\n\t\t\tWHERE r.sale_id = $1 ORDER BY r.created_at", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			r, err := scanReturn(rows)
			if err != nil {
				rows.Close()
				return err
			}
			returns = append(returns, r)
		}
		rows.Close()
		m["returns"] = returns

		approvals := []map[string]any{}
		rows, err = tx.Query(ctx, "\n\t\t\tSELECT a.id, a.approval_type::text, a.status::text, a.requested_by, a.decided_by, u.full_name::text, a.decided_at, a.decision_note, a.created_at\n\t\t\tFROM app.approval_requests a\n\t\t\tLEFT JOIN app.memberships m ON m.id = a.decided_by\n\t\t\tLEFT JOIN iam.users u ON u.id = m.user_id\n\t\t\tWHERE a.sale_id = $1 ORDER BY a.created_at", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var aid, by uuid.UUID
			var decided *uuid.UUID
			var kind, status string
			var decider, note *string
			var decidedAt, created any
			if err := rows.Scan(&aid, &kind, &status, &by, &decided, &decider, &decidedAt, &note, &created); err != nil {
				rows.Close()
				return err
			}
			approvals = append(approvals, map[string]any{"id": aid, "kind": kind, "status": status, "requested_by": by, "decided_by": decided,
				"decided_by_name": decider, "decided_at": decidedAt, "note": note, "created_at": created})
		}
		rows.Close()
		m["approvals"] = approvals

		trail := []map[string]any{}
		rows, err = tx.Query(ctx, "\n\t\t\tSELECT a.action, a.actor_user_id, u.full_name::text, a.occurred_at\n\t\t\tFROM audit.audit_log a LEFT JOIN iam.users u ON u.id = a.actor_user_id\n\t\t\tWHERE (\n\t\t\t      (a.entity_table IN ('sale', 'app.sales') AND a.entity_id = $1)\n\t\t\t   OR (a.entity_table IN ('sale_return', 'app.sale_returns') AND a.entity_id IN (SELECT id FROM app.sale_returns WHERE sale_id = $1))\n\t\t\t)\n\t\t\tORDER BY a.occurred_at, a.seq", id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var action string
			var user *uuid.UUID
			var name *string
			var at any
			if err := rows.Scan(&action, &user, &name, &at); err != nil {
				return err
			}
			trail = append(trail, map[string]any{"action": action, "user_id": user, "user_name": name, "at": at})
		}
		m["audit"] = trail
		out = m
		return rows.Err()
	})
	return out, err
}

func salePayments(ctx context.Context, tx pgx.Tx, saleID uuid.UUID) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, "\n\t\tSELECT pr.id, pr.receipt_no, pr.purpose::text, pr.method::text, pa.amount, pr.reference,\n\t\t       pr.business_date::text, pr.status::text, pr.account_id, fa.name::text\n\t\tFROM app.payment_allocations pa\n\t\tJOIN app.payments_received pr ON pr.id = pa.payment_received_id\n\t\tJOIN app.financial_accounts fa ON fa.id = pr.account_id\n\t\tWHERE pa.sale_id = $1\n\t\tORDER BY pr.created_at, pr.id", saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, account uuid.UUID
		var number, purpose, method, date, status, accName string
		var amt decimal.Decimal
		var ref *string
		if err := rows.Scan(&id, &number, &purpose, &method, &amt, &ref, &date, &status, &account, &accName); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "direction": "in", "kind": purpose, "method": payments.MethodOut(method),
			"amount": amt, "reference": ref, "date": date, "status": status, "money_account_id": account, "money_account_name": accName})
	}
	return out, rows.Err()
}
