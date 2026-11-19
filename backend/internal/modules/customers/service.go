package customers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/money"
	"bms/internal/platform/phone"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB     *db.DB
	Ledger *ledger.Service
	Audit  *audit.Service
	Clock  clock.Clock
}

func New(database *db.DB, led *ledger.Service, aud *audit.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Ledger: led, Audit: aud, Clock: clk}
}

// Customer is a person or business that buys from the shop (app.customers with is_shop_customer).
// The same table holds services clients; the services module reads them as clients. Walk-in sales
// have no customer row, so there is no "walk-in customer" any more. Tax number and payment terms are
// not part of the approved model (a customer has a credit limit, and a national id kept encrypted).
type Customer struct {
	ID           uuid.UUID       `json:"id"`
	Name         string          `json:"name"`
	BusinessName *string         `json:"business_name"`
	Phone        *string         `json:"phone"`
	Email        *string         `json:"email"`
	Address      *string         `json:"address"`
	TaxNumber    *string         `json:"tax_number"`
	CreditLimit  decimal.Decimal `json:"credit_limit"`
	TermsDays    int             `json:"payment_terms_days"`
	IsWalkIn     bool            `json:"is_walk_in"`
	IsActive     bool            `json:"is_active"`
	Balance      decimal.Decimal `json:"balance"`
	Credit       decimal.Decimal `json:"credit"`
	Notes        *string         `json:"notes"`
	CreatedAt    time.Time       `json:"created_at"`

	// Account summary, filled on the profile.
	TotalPurchases *decimal.Decimal `json:"total_purchases,omitempty"`
	TotalPaid      *decimal.Decimal `json:"total_paid,omitempty"`
	Available      *decimal.Decimal `json:"available_credit,omitempty"`
	LastActivity   *time.Time       `json:"last_activity,omitempty"`
	SalesCount     *int             `json:"sales_count,omitempty"`
}

type Input struct {
	Name         string          `json:"name" validate:"max=200"`
	BusinessName string          `json:"business_name" validate:"max=200"`
	Phone        string          `json:"phone" validate:"max=40"`
	Email        string          `json:"email" validate:"omitempty,email"`
	Address      string          `json:"address" validate:"max=500"`
	TaxNumber    string          `json:"tax_number" validate:"max=60"`
	CreditLimit  decimal.Decimal `json:"credit_limit" validate:"dgte0"`
	TermsDays    int             `json:"payment_terms_days" validate:"gte=0,lte=365"`
	Notes        string          `json:"notes" validate:"max=2000"`
}

// custCols: balance is what the customer owes on completed sales; credit is money received from them
// that is not yet applied to any sale or invoice.
const custCols = `c.id, c.full_name::text, c.business_name, c.phone::text, c.email::text, c.address, c.credit_limit, c.notes, c.created_at, c.archived_at IS NULL,
	COALESCE((SELECT SUM(s.balance_due) FROM app.sales s WHERE s.tenant_id=c.tenant_id AND s.customer_id=c.id AND s.status='completed'),0),
	COALESCE((SELECT SUM(pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.tenant_id=pr.tenant_id AND a.payment_received_id=pr.id),0))
	          FROM app.payments_received pr WHERE pr.tenant_id=c.tenant_id AND pr.customer_id=c.id AND pr.status='posted'),0)`

func scanCustomer(row pgx.Row, c *Customer) error {
	var limit *decimal.Decimal
	err := row.Scan(&c.ID, &c.Name, &c.BusinessName, &c.Phone, &c.Email, &c.Address, &limit, &c.Notes, &c.CreatedAt, &c.IsActive, &c.Balance, &c.Credit)
	if limit != nil {
		c.CreditLimit = *limit
	}
	return err
}

type Filter struct {
	Q          string
	Active     *bool
	HasBalance bool
}

func (s *Service) List(ctx context.Context, p *auth.Principal, f Filter, page httpx.Page) ([]Customer, int, error) {
	out := []Customer{}
	total := 0
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		q := sqlq.New(p.TenantID)
		q.Add("c.tenant_id = $1 AND c.is_shop_customer")
		// Without "view all customers" a person sees only the customers they registered.
		q.If(!p.Has("shop.customer.view_all"), "c.registered_by = ?", p.MembershipID)
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(c.full_name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%' OR c.email::text ILIKE '%' || ? || '%')", qq, qq, qq)
		}
		q.If(f.Active != nil, "(c.archived_at IS NULL) = ?", f.Active)
		q.If(f.HasBalance, "EXISTS (SELECT 1 FROM app.sales s WHERE s.tenant_id=c.tenant_id AND s.customer_id=c.id AND s.balance_due > 0 AND s.status='completed')")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.customers c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+custCols+` FROM app.customers c`+q.Where()+` ORDER BY c.full_name, c.id`+q.Page(page.PerPage, page.Offset), q.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Customer
			if err := scanCustomer(rows, &c); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, total, err
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (Customer, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Customer{}, httpx.BadRequest("invalid_body", "Customer name is required")
	}
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return Customer{}, httpx.BadRequest("invalid_phone", err.Error())
	}
	id := uuid.New()
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var limit any
		// Setting a credit limit needs its own permission.
		if p.Has("shop.customer.manage") && in.CreditLimit.IsPositive() && p.Has("finance.debt.view") {
			limit = money.Money(in.CreditLimit)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO app.customers (id, tenant_id, full_name, business_name, phone, email, address, is_shop_customer, credit_limit, notes)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::app.phone_e164,NULLIF($6,'')::app.email,NULLIF($7,''),true,$8,NULLIF($9,''))`,
			id, p.TenantID, strings.TrimSpace(in.Name), strings.TrimSpace(in.BusinessName), ph, strings.ToLower(strings.TrimSpace(in.Email)), in.Address, limit, in.Notes)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "A customer with that phone already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.created", "customers", id, nil, in)
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

// Get returns the profile with the account summary.
func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (Customer, error) {
	var c Customer
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		c, err = s.load(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if !p.Has("shop.customer.view_all") {
			var mine bool
			if err := tx.QueryRow(ctx, `SELECT registered_by = $3 FROM app.customers WHERE tenant_id = $1 AND id = $2`, p.TenantID, id, p.MembershipID).Scan(&mine); err != nil || !mine {
				return httpx.NotFound("not_found", "Customer not found")
			}
		}
		var purchases, paid decimal.Decimal
		var n int
		var last *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT SUM(s.total) FROM app.sales s WHERE s.tenant_id=$1 AND s.customer_id=$2 AND s.status='completed'),0),
			       COALESCE((SELECT SUM(pr.amount) FROM app.payments_received pr WHERE pr.tenant_id=$1 AND pr.customer_id=$2 AND pr.status='posted'),0),
			       (SELECT count(*) FROM app.sales WHERE tenant_id=$1 AND customer_id=$2 AND status='completed'),
			       GREATEST((SELECT max(created_at) FROM app.sales WHERE tenant_id=$1 AND customer_id=$2 AND status NOT IN ('held','draft')),
			                (SELECT max(created_at) FROM app.payments_received WHERE tenant_id=$1 AND customer_id=$2))
		`, p.TenantID, id).Scan(&purchases, &paid, &n, &last); err != nil {
			return err
		}
		avail := c.CreditLimit.Sub(c.Balance)
		if avail.IsNegative() {
			avail = money.Zero
		}
		c.TotalPurchases, c.TotalPaid, c.SalesCount, c.LastActivity, c.Available = &purchases, &paid, &n, last, &avail
		return nil
	})
	return c, err
}

func (s *Service) load(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (Customer, error) {
	var c Customer
	err := scanCustomer(tx.QueryRow(ctx, `SELECT `+custCols+` FROM app.customers c WHERE c.tenant_id=$1 AND c.id=$2 AND c.is_shop_customer`, tenantID, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, httpx.NotFound("not_found", "Customer not found")
	}
	return c, err
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in Input) (Customer, error) {
	ph, err := phone.Optional(in.Phone)
	if err != nil {
		return Customer{}, httpx.BadRequest("invalid_phone", err.Error())
	}
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.load(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE app.customers SET full_name=COALESCE(NULLIF($2,''), full_name), business_name=COALESCE(NULLIF($3,''), business_name),
			phone=COALESCE(NULLIF($4,'')::app.phone_e164, phone), email=COALESCE(NULLIF($5,'')::app.email, email),
			address=COALESCE(NULLIF($6,''), address), notes=COALESCE(NULLIF($7,''), notes)
			WHERE tenant_id=$1 AND id=$8`,
			p.TenantID, strings.TrimSpace(in.Name), strings.TrimSpace(in.BusinessName), ph, strings.ToLower(strings.TrimSpace(in.Email)), in.Address, in.Notes, id)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "A customer with that phone already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.updated", "customers", id, before, in)
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

// CreditLimit sets how much the customer may owe. Payment terms are not part of the approved model, so
// days is validated and ignored.
func (s *Service) CreditLimit(ctx context.Context, p *auth.Principal, id uuid.UUID, limit decimal.Decimal, days int) error {
	if limit.IsNegative() || days < 0 || days > 365 {
		return httpx.BadRequest("invalid_body", "Credit limit cannot be negative and terms must be 0 to 365 days")
	}
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.load(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.customers SET credit_limit=$3 WHERE tenant_id=$1 AND id=$2`, p.TenantID, id, money.Money(limit)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.credit_limit", "customers", id,
			map[string]any{"credit_limit": before.CreditLimit}, map[string]any{"credit_limit": money.Money(limit)})
	})
}

// Deactivate archives a customer so they are hidden from new sales. A customer who still owes cannot be archived.
func (s *Service) Deactivate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Customer, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if c.Balance.IsPositive() {
			return httpx.Conflict("has_balance", "This customer still owes "+c.Balance.StringFixed(2)+". Collect the debt first")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.customers SET archived_at=now() WHERE tenant_id=$1 AND id=$2`, p.TenantID, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.deactivated", "customers", id, map[string]any{"is_active": c.IsActive}, map[string]any{"is_active": false})
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

type Allocation struct {
	SaleID uuid.UUID       `json:"sale_id"`
	Amount decimal.Decimal `json:"amount"`
}

type PayIn struct {
	Amount         decimal.Decimal `json:"amount" validate:"dpos"`
	Method         string          `json:"method" validate:"required,oneof=cash mobile_money bank"`
	MoneyAccountID uuid.UUID       `json:"money_account_id" validate:"required"`
	Reference      string          `json:"reference" validate:"max=120"`
	Date           string          `json:"date" validate:"omitempty,date"`
	Allocations    []Allocation    `json:"allocations"`
}

// Receive records a customer payment, allocated oldest sale first or as given. Whatever is not
// allocated stays with the customer as credit (received money not yet applied).
func (s *Service) Receive(ctx context.Context, p *auth.Principal, customerID uuid.UUID, in PayIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if err := httpx.Check(in); err != nil {
		return 0, nil, err
	}
	in.Amount = money.Money(in.Amount)
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.TenantID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if _, err := s.load(ctx, tx, p.TenantID, customerID); err != nil {
			return err
		}
		date := in.Date
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		// Targets: the sales the caller named, else the customer's open sales, oldest first.
		var targets []payments.Alloc
		allocated := money.Zero
		if len(in.Allocations) > 0 {
			for _, a := range in.Allocations {
				var bal decimal.Decimal
				if err := tx.QueryRow(ctx, `SELECT balance_due FROM app.sales WHERE tenant_id=$1 AND id=$2 AND customer_id=$3 AND status='completed'`, p.TenantID, a.SaleID, customerID).Scan(&bal); err != nil {
					return httpx.NotFound("not_found", "One of the sales was not found for this customer")
				}
				amt := money.Money(a.Amount)
				if !amt.IsPositive() || amt.GreaterThan(bal) {
					return httpx.BadRequest("invalid_body", "An allocation is more than the sale still owes")
				}
				sid := a.SaleID
				targets = append(targets, payments.Alloc{SaleID: &sid, Amount: amt})
				allocated = allocated.Add(amt)
			}
			if allocated.GreaterThan(in.Amount) {
				return httpx.BadRequest("invalid_body", "Allocations are more than the amount received")
			}
		} else {
			rows, err := tx.Query(ctx, `
				SELECT id, balance_due FROM app.sales WHERE tenant_id=$1 AND customer_id=$2 AND status='completed' AND balance_due > 0
				ORDER BY business_date, created_at FOR UPDATE`, p.TenantID, customerID)
			if err != nil {
				return err
			}
			left := in.Amount
			for rows.Next() && left.IsPositive() {
				var id uuid.UUID
				var bal decimal.Decimal
				if err := rows.Scan(&id, &bal); err != nil {
					rows.Close()
					return err
				}
				take := decimal.Min(bal, left)
				sid := id
				targets = append(targets, payments.Alloc{SaleID: &sid, Amount: take})
				allocated = allocated.Add(take)
				left = left.Sub(take)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		purpose := "debt_payment"
		if len(targets) == 0 {
			purpose = "advance"
		}
		method := payments.Method(in.Method)
		ref := strings.TrimSpace(in.Reference)
		if method != "cash" && ref == "" {
			return httpx.BadRequest("reference_required", "Enter the transaction reference for a bank or mobile-money payment")
		}
		cust := customerID
		rec, err := payments.Receive(ctx, tx, payments.ReceiveInput{
			TenantID: p.TenantID, LocationID: *p.BranchID, AccountID: in.MoneyAccountID, CustomerID: &cust, Purpose: purpose,
			Method: method, Amount: in.Amount, Reference: ref, BusinessDate: date, Allocations: targets,
		})
		if err != nil {
			return err
		}
		credit := in.Amount.Sub(allocated)
		if err := s.Audit.Record(ctx, tx, p, "customer.payment_received", "payments_received", rec.ID, nil, map[string]any{"number": rec.Number, "amount": in.Amount, "allocated": allocated, "credit": credit}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": rec.ID, "number": rec.Number, "amount": in.Amount, "allocated": allocated, "credit_added": credit}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

// Unpaid lists the customer's completed sales that still owe money.
func (s *Service) Unpaid(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.load(ctx, tx, p.TenantID, id); err != nil {
			return err
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, coalesce(s.receipt_no,''), s.location_id, s.business_date::text, s.due_date::text, s.total, s.amount_paid, s.balance_due,
			       ($3::date - s.business_date), CASE WHEN s.due_date IS NOT NULL AND s.due_date < $3::date THEN ($3::date - s.due_date) ELSE 0 END
			FROM app.sales s WHERE s.tenant_id=$1 AND s.customer_id=$2 AND s.status='completed' AND s.balance_due > 0
			ORDER BY s.business_date, s.created_at`, p.TenantID, id, today)
		if err != nil {
			return err
		}
		defer rows.Close()
		list := []map[string]any{}
		total := money.Zero
		for rows.Next() {
			var sid, branch uuid.UUID
			var number, date string
			var due *string
			var tot, paid, bal decimal.Decimal
			var age, overdue int
			if err := rows.Scan(&sid, &number, &branch, &date, &due, &tot, &paid, &bal, &age, &overdue); err != nil {
				return err
			}
			total = total.Add(bal)
			list = append(list, map[string]any{"sale_id": sid, "number": number, "branch_id": branch, "date": date, "due_date": due, "total": tot,
				"paid_total": paid, "written_off": money.Zero, "balance": bal, "age_days": age, "days_overdue": overdue})
		}
		out = map[string]any{"customer_id": id, "sales": list, "total_balance": total}
		return rows.Err()
	})
	return out, err
}

// Statement lists sales, payments and refunds with a running balance. A positive balance is owed by
// the customer; negative is credit held for them.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, p.TenantID, id)
		if err != nil {
			return err
		}
		if from == "" {
			from = "1970-01-01"
		}
		const union = `(
			SELECT s.business_date AS d, s.created_at AS at, 'sale' AS type, coalesce(s.receipt_no,'') AS number, 'Sale' AS description, s.total AS debit, 0::numeric AS credit
			  FROM app.sales s WHERE s.tenant_id = $1 AND s.customer_id = $2 AND s.status = 'completed'
			UNION ALL
			SELECT pr.business_date, pr.created_at, 'payment', pr.receipt_no, 'Payment received', 0, pr.amount
			  FROM app.payments_received pr WHERE pr.tenant_id = $1 AND pr.customer_id = $2 AND pr.status = 'posted' AND pr.purpose <> 'sale'
			UNION ALL
			SELECT pm.business_date, pm.created_at, 'refund', pm.payment_no, 'Refund paid', pm.amount, 0
			  FROM app.payments_made pm WHERE pm.tenant_id = $1 AND pm.customer_id = $2 AND pm.status = 'posted'
		) x`
		var opening decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(debit - credit), 0) FROM `+union+` WHERE d < $3::date`, p.TenantID, id, from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT d::text, at, type, number, description, debit, credit FROM `+union+`
			WHERE d >= $3::date AND ($4 = '' OR d <= $4::date) ORDER BY d, at`, p.TenantID, id, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		run := opening
		lines := []map[string]any{}
		for rows.Next() {
			var date, typ, number, desc string
			var at time.Time
			var debit, credit decimal.Decimal
			if err := rows.Scan(&date, &at, &typ, &number, &desc, &debit, &credit); err != nil {
				return err
			}
			run = run.Add(debit).Sub(credit)
			lines = append(lines, map[string]any{"date": date, "type": typ, "number": number, "description": desc, "debit": debit, "credit": credit, "balance": run, "created_at": at})
		}
		out = map[string]any{"customer": c, "from": from, "to": to, "opening_balance": opening, "closing_balance": run, "lines": lines}
		return rows.Err()
	})
	return out, err
}

// Debts lists customers who owe, with aging buckets.
func (s *Service) Debts(ctx context.Context, p *auth.Principal, key string) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.full_name::text, c.phone::text, coalesce(c.credit_limit, 0), COALESCE(SUM(s.balance_due),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $2::date - s.business_date <= 30),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $2::date - s.business_date BETWEEN 31 AND 60),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $2::date - s.business_date BETWEEN 61 AND 90),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $2::date - s.business_date > 90),0),
			  min(s.business_date)::text, count(*)
			FROM app.customers c JOIN app.sales s ON s.tenant_id = c.tenant_id AND s.customer_id = c.id AND s.balance_due > 0 AND s.status = 'completed'
			WHERE c.tenant_id = $1
			GROUP BY c.id ORDER BY 5 DESC, c.full_name`, p.TenantID, today)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name, oldest string
			var phone *string
			var n int
			var limit, total, a, b, c, d decimal.Decimal
			if err := rows.Scan(&id, &name, &phone, &limit, &total, &a, &b, &c, &d, &oldest, &n); err != nil {
				return err
			}
			out = append(out, map[string]any{"customer_id": id, "name": name, "phone": phone, "credit_limit": limit, "balance": total,
				"bucket_0_30": a, "bucket_31_60": b, "bucket_61_90": c, "bucket_over_90": d, "oldest_date": oldest, "unpaid_sales": n})
		}
		return rows.Err()
	})
	return out, err
}

type WriteOffIn struct {
	Amount decimal.Decimal `json:"amount" validate:"dgte0"`
	Reason string          `json:"reason" validate:"required,max=500"`
}

// WriteOff is not part of the approved model: a debt is settled by payment, or corrected by an
// approved transaction correction.
func (s *Service) WriteOff(ctx context.Context, p *auth.Principal, id uuid.UUID, in WriteOffIn) (int, []byte, error) {
	return 0, nil, httpx.New(410, "feature_removed", "Writing off a customer debt is not part of the approved accounting model")
}
