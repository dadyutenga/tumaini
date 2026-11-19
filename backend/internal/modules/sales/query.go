package sales

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

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

var sortable = map[string]string{"created_at": "s.created_at", "business_date": "s.business_date", "total": "s.total", "number": "s.number", "balance": "s.balance"}

// List returns posted sales in the caller's scope. Held carts and quotations are excluded.
func (s *Service) List(ctx context.Context, p *auth.Principal, f ListFilter, page httpx.Page) ([]map[string]any, int, error) {
	out := []map[string]any{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New()
		q.Add("s.kind = 'sale'").Add("s.status <> 'held'")
		if b := p.ScopeBranches("sales.view"); b != nil {
			q.Add("s.branch_id = ANY(?)", b)
		}
		if own := p.OwnOnly("sales.view"); own != nil {
			q.Add("s.cashier_id = ?", *own)
		}
		q.If(f.From != "", "s.business_date >= ?::date", f.From)
		q.If(f.To != "", "s.business_date <= ?::date", f.To)
		q.If(f.Status != "", "s.status = ?", f.Status)
		q.If(f.CustomerID != nil, "s.customer_id = ?", f.CustomerID)
		q.If(f.CashierID != nil, "s.cashier_id = ?", f.CashierID)
		q.If(f.Method != "", "EXISTS (SELECT 1 FROM payment_allocations pa JOIN payments py ON py.id=pa.payment_id WHERE pa.document_type='sale' AND pa.document_id=s.id AND py.method=? AND py.direction='in')", f.Method)
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(s.number ILIKE '%' || ? || '%' OR c.name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%')", qq, qq, qq)
		}
		from := ` FROM sales s LEFT JOIN customers c ON c.id=s.customer_id LEFT JOIN users u ON u.id=s.cashier_id`
		if err := tx.QueryRow(ctx, `SELECT count(*)`+from+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		order := httpx.Sort(f.Sort, sortable, "s.created_at DESC")
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.number, s.status, s.total, s.paid_total, s.balance, s.business_date::text, s.cashier_id,
			       s.customer_id, c.name, u.name, s.branch_id, s.discount_total, s.tax_total, s.created_at`+from+q.Where()+
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

// loadSale reads a sale header and enforces the caller's scope for key; outside scope is 404.
func (s *Service) loadSale(ctx context.Context, tx pgx.Tx, p *auth.Principal, key string, id uuid.UUID, kind string) (map[string]any, error) {
	var number, status, footer, date, k string
	var branch, cashier uuid.UUID
	var customer, employee, till, quotation, converted *uuid.UUID
	var sub, disc, tax, total, cost, paid, bal, change, written decimal.Decimal
	var note, dueDate, validUntil, voidReason *string
	var includeTax bool
	var prints int
	var created, completed, voidedAt any
	var voidedBy *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT number, kind, status, branch_id, customer_id, cashier_id, employee_id, till_session_id, business_date::text, subtotal, discount_total,
		       tax_total, total, cost_total, paid_total, balance, change_due, written_off, receipt_footer, prices_include_tax, note,
		       due_date::text, valid_until::text, receipt_prints, created_at, completed_at, voided_by, voided_at, void_reason, quotation_id, converted_sale_id
		FROM sales WHERE id=$1`, id).Scan(&number, &k, &status, &branch, &customer, &cashier, &employee, &till, &date, &sub, &disc,
		&tax, &total, &cost, &paid, &bal, &change, &written, &footer, &includeTax, &note, &dueDate, &validUntil, &prints, &created, &completed,
		&voidedBy, &voidedAt, &voidReason, &quotation, &converted)
	notFound := httpx.NotFound("not_found", "Sale not found")
	if kind == "quotation" {
		notFound = httpx.NotFound("not_found", "Quotation not found")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound
	}
	if err != nil {
		return nil, err
	}
	if k != kind || !p.SeesBranch(key, branch) {
		return nil, notFound
	}
	if own := p.OwnOnly(key); own != nil && cashier != *own {
		return nil, notFound
	}
	out := map[string]any{"id": id, "number": number, "kind": k, "status": status, "branch_id": branch, "customer_id": customer, "cashier_id": cashier,
		"employee_id": employee, "till_session_id": till, "business_date": date, "subtotal": sub, "discount_total": disc, "tax_total": tax,
		"total": total, "paid_total": paid, "balance": bal, "change_due": change, "written_off": written, "receipt_footer": footer,
		"prices_include_tax": includeTax, "note": note, "due_date": dueDate, "valid_until": validUntil, "receipt_prints": prints,
		"created_at": created, "completed_at": completed, "voided_by": voidedBy, "voided_at": voidedAt, "void_reason": voidReason,
		"quotation_id": quotation, "converted_sale_id": converted}
	if _, ok := p.Can("products.view_cost"); ok {
		out["cost_total"] = cost
		out["profit"] = total.Sub(tax).Sub(cost)
	}
	var custName, custPhone, cashierName, branchName *string
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT name FROM customers WHERE id=$1), (SELECT phone FROM customers WHERE id=$1),
		       (SELECT name FROM users WHERE id=$2), (SELECT name FROM branches WHERE id=$3)`, customer, cashier, branch).Scan(&custName, &custPhone, &cashierName, &branchName); err != nil {
		return nil, err
	}
	out["customer_name"], out["customer_phone"], out["cashier_name"], out["branch_name"] = custName, custPhone, cashierName, branchName
	items, err := s.items(ctx, tx, p, id)
	if err != nil {
		return nil, err
	}
	out["items"] = items
	return out, nil
}

func (s *Service) items(ctx context.Context, tx pgx.Tx, p *auth.Principal, id uuid.UUID) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, `
		SELECT si.id, si.product_id, si.product_name, si.qty, si.unit_price, si.unit_cost, si.discount, si.tax_rate, si.tax_amount, si.line_total, si.qty_returned, si.serial_number, COALESCE(pr.warranty_months, 0)
		FROM sale_items si LEFT JOIN products pr ON pr.id = si.product_id
		WHERE si.sale_id=$1 ORDER BY si.product_name, si.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	_, cost := p.Can("products.view_cost")
	items := []map[string]any{}
	for rows.Next() {
		var itemID, pid uuid.UUID
		var name string
		var serial *string
		var warranty int
		var qty, price, ucost, d, rate, taxA, line, ret decimal.Decimal
		if err := rows.Scan(&itemID, &pid, &name, &qty, &price, &ucost, &d, &rate, &taxA, &line, &ret, &serial, &warranty); err != nil {
			return nil, err
		}
		item := map[string]any{"id": itemID, "product_id": pid, "product_name": name, "qty": qty, "unit_price": price, "discount": d, "tax_rate": rate, "tax_amount": taxA, "line_total": line, "qty_returned": ret, "serial_number": serial, "warranty_months": warranty}
		if cost {
			item["unit_cost"] = ucost
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Get returns a sale with items, payments, returns, approvals and its audit trail (FR-SAL-20).
func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		m, err := s.loadSale(ctx, tx, p, "sales.view", id, "sale")
		if err != nil {
			return err
		}
		if m["payments"], err = salePayments(ctx, tx, id); err != nil {
			return err
		}
		returns := []map[string]any{}
		rows, err := tx.Query(ctx, `
			SELECT r.id, r.number, r.sale_id, s.number, r.status, r.reason, r.refund_method, r.refund_total, r.requested_by, r.approved_by, r.created_at, r.completed_at, r.branch_id
			FROM sale_returns r JOIN sales s ON s.id=r.sale_id WHERE r.sale_id=$1 ORDER BY r.created_at`, id)
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
		rows, err = tx.Query(ctx, `
			SELECT a.id, a.kind, a.status, a.requested_by, a.decided_by, du.name, a.decided_at, a.decision_note, a.created_at
			FROM approval_requests a LEFT JOIN users du ON du.id=a.decided_by
			WHERE a.subject_type='sale' AND a.subject_id=$1 ORDER BY a.created_at`, id)
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
			approvals = append(approvals, map[string]any{"id": aid, "kind": kind, "status": status, "requested_by": by, "decided_by": decided, "decided_by_name": decider, "decided_at": decidedAt, "note": note, "created_at": created})
		}
		rows.Close()
		m["approvals"] = approvals
		trail := []map[string]any{}
		rows, err = tx.Query(ctx, `
			SELECT a.action, a.user_id, u.name, a.created_at
			FROM audit_logs a LEFT JOIN users u ON u.id=a.user_id
			WHERE (a.entity_type='sale' AND a.entity_id=$1)
			   OR (a.entity_type='sale_return' AND a.entity_id IN (SELECT id FROM sale_returns WHERE sale_id=$1))
			ORDER BY a.created_at, a.id`, id)
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
	rows, err := tx.Query(ctx, `
		SELECT py.id, py.number, py.direction, py.kind, py.method, pa.amount, py.reference, py.payment_date::text, py.status, py.money_account_id, ma.name
		FROM payment_allocations pa JOIN payments py ON py.id=pa.payment_id JOIN money_accounts ma ON ma.id=py.money_account_id
		WHERE pa.document_type='sale' AND pa.document_id=$1 ORDER BY py.created_at, py.id`, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, account uuid.UUID
		var number, dir, kind, method, date, status, accName string
		var amt decimal.Decimal
		var ref *string
		if err := rows.Scan(&id, &number, &dir, &kind, &method, &amt, &ref, &date, &status, &account, &accName); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "number": number, "direction": dir, "kind": kind, "method": method, "amount": amt,
			"reference": ref, "date": date, "status": status, "money_account_id": account, "money_account_name": accName})
	}
	return out, rows.Err()
}
