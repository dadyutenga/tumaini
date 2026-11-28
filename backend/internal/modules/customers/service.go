package customers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/outbound"
	"bms/internal/kernel/payments"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/idem"
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
	"bms/internal/platform/phone"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB       *db.DB
	Audit    *audit.Service
	Outbound *outbound.Service
	Clock    clock.Clock
}

func New(database *db.DB, aud *audit.Service, clk clock.Clock) *Service {
	return &Service{DB: database, Audit: aud, Clock: clk}
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
	COALESCE((SELECT SUM(s.balance_due) FROM app.sales s WHERE s.customer_id=c.id AND s.status='completed'),0),
	COALESCE((SELECT SUM(pr.amount - COALESCE((SELECT SUM(a.amount) FROM app.payment_allocations a WHERE a.payment_received_id=pr.id),0))
	          FROM app.payments_received pr WHERE pr.customer_id=c.id AND pr.status='posted'),0)`

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
		q := sqlq.New()
		q.Add("c.is_shop_customer")
		// Without "view all customers" a person sees only the customers they registered.
		q.If(!p.Has("shop.customer.view_all"), "c.registered_by = ?", p.MembershipID)
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(c.full_name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%' OR c.email::text ILIKE '%' || ? || '%')", qq, qq, qq)
		}
		q.If(f.Active != nil, "(c.archived_at IS NULL) = ?", f.Active)
		q.If(f.HasBalance, "EXISTS (SELECT 1 FROM app.sales s WHERE s.customer_id=c.id AND s.balance_due > 0 AND s.status='completed')")
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
			INSERT INTO app.customers (id, full_name, business_name, phone, email, address, is_shop_customer, credit_limit, notes)
			VALUES ($1,$2,NULLIF($3,''),NULLIF($4,'')::app.phone_e164,NULLIF($5,'')::app.email,NULLIF($6,''),true,$7,NULLIF($8,''))`,
			id, strings.TrimSpace(in.Name), strings.TrimSpace(in.BusinessName), ph, strings.ToLower(strings.TrimSpace(in.Email)), in.Address, limit, in.Notes)
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
		c, err = s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if !p.Has("shop.customer.view_all") {
			var mine bool
			if err := tx.QueryRow(ctx, "SELECT registered_by = $2 FROM app.customers WHERE id = $1", id, p.MembershipID).Scan(&mine); err != nil || !mine {
				return httpx.NotFound("not_found", "Customer not found")
			}
		}
		var purchases, paid decimal.Decimal
		var n int
		var last *time.Time
		if err := tx.QueryRow(ctx, "\n\t\t\tSELECT COALESCE((SELECT SUM(s.total) FROM app.sales s WHERE s.customer_id=$1 AND s.status='completed'),0),\n\t\t\t       COALESCE((SELECT SUM(pr.amount) FROM app.payments_received pr WHERE pr.customer_id=$1 AND pr.status='posted'),0),\n\t\t\t       (SELECT count(*) FROM app.sales WHERE customer_id=$1 AND status='completed'),\n\t\t\t       GREATEST((SELECT max(created_at) FROM app.sales WHERE customer_id=$1 AND status NOT IN ('held','draft')),\n\t\t\t                (SELECT max(created_at) FROM app.payments_received WHERE customer_id=$1))\n\t\t", id).Scan(&purchases, &paid, &n, &last); err != nil {
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

func (s *Service) load(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Customer, error) {
	var c Customer
	err := scanCustomer(tx.QueryRow(ctx, `SELECT `+custCols+` FROM app.customers c WHERE c.id=$1 AND c.is_shop_customer`, id), &c)
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
		before, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "\n\t\t\tUPDATE app.customers SET full_name=COALESCE(NULLIF($1,''), full_name), business_name=COALESCE(NULLIF($2,''), business_name),\n\t\t\tphone=COALESCE(NULLIF($3,'')::app.phone_e164, phone), email=COALESCE(NULLIF($4,'')::app.email, email),\n\t\t\taddress=COALESCE(NULLIF($5,''), address), notes=COALESCE(NULLIF($6,''), notes)\n\t\t\tWHERE id=$7", strings.TrimSpace(in.Name), strings.TrimSpace(in.BusinessName), ph, strings.ToLower(strings.TrimSpace(in.Email)), in.Address, in.Notes, id)
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
		before, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE app.customers SET credit_limit=$2 WHERE id=$1", id, money.Money(limit)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.credit_limit", "customers", id,
			map[string]any{"credit_limit": before.CreditLimit}, map[string]any{"credit_limit": money.Money(limit)})
	})
}

// Deactivate archives a customer so they are hidden from new sales. A customer who still owes cannot be archived.
func (s *Service) Deactivate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Customer, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if c.Balance.IsPositive() {
			return httpx.Conflict("has_balance", "This customer still owes "+c.Balance.StringFixed(2)+". Collect the debt first")
		}
		if _, err := tx.Exec(ctx, "UPDATE app.customers SET archived_at=now() WHERE id=$1", id); err != nil {
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
		if rep, err := idem.Claim(ctx, tx); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		if _, err := s.load(ctx, tx, customerID); err != nil {
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
			sorted := append([]Allocation(nil), in.Allocations...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].SaleID.String() < sorted[j].SaleID.String() })
			seen := make(map[uuid.UUID]bool, len(sorted))
			for _, a := range sorted {
				if seen[a.SaleID] {
					return httpx.BadRequest("invalid_body", "A sale cannot be allocated more than once")
				}
				seen[a.SaleID] = true
				var bal decimal.Decimal
				if err := tx.QueryRow(ctx, "SELECT balance_due FROM app.sales WHERE id=$1 AND customer_id=$2 AND status='completed' FOR UPDATE", a.SaleID, customerID).Scan(&bal); err != nil {
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
			rows, err := tx.Query(ctx, "\n\t\t\t\tSELECT id, balance_due FROM app.sales WHERE customer_id=$1 AND status='completed' AND balance_due > 0\n\t\t\t\tORDER BY business_date, created_at FOR UPDATE", customerID)
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
			Business:   auth.Shop,
			LocationID: *p.BranchID,
			AccountID:  in.MoneyAccountID, CustomerID: &cust, Purpose: purpose,
			Method: method, Amount: in.Amount, Reference: ref, BusinessDate: date, Allocations: targets,
		})
		if err != nil {
			return err
		}
		credit := in.Amount.Sub(allocated)
		if err := s.Audit.Record(ctx, tx, p, "customer.payment_received", "payments_received", rec.ID, nil, map[string]any{"number": rec.Number, "amount": in.Amount, "allocated": allocated, "credit": credit}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, 201, httpx.Data(map[string]any{"id": rec.ID, "number": rec.Number, "amount": in.Amount, "allocated": allocated, "credit_added": credit}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

// Unpaid lists the customer's completed sales that still owe money.
func (s *Service) Unpaid(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := s.load(ctx, tx, id); err != nil {
			return err
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "\n\t\t\tSELECT s.id, coalesce(s.receipt_no,''), s.location_id, s.business_date::text, s.due_date::text, s.total, s.amount_paid, s.balance_due,\n\t\t\t       ($2::date - s.business_date), CASE WHEN s.due_date IS NOT NULL AND s.due_date < $2::date THEN ($2::date - s.due_date) ELSE 0 END\n\t\t\tFROM app.sales s WHERE s.customer_id=$1 AND s.status='completed' AND s.balance_due > 0\n\t\t\tORDER BY s.business_date, s.created_at", id, today)
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
		c, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if from == "" {
			from = "1970-01-01"
		}
		const union = `(
			SELECT s.business_date AS d, s.created_at AS at, 'sale' AS type, coalesce(s.receipt_no,'') AS number, 'Sale' AS description, s.total AS debit, 0::numeric AS credit
			  FROM app.sales s WHERE s.customer_id = $1 AND s.status = 'completed'
			UNION ALL
			SELECT pr.business_date, pr.created_at, 'payment', pr.receipt_no, 'Payment received', 0, pr.amount
			  FROM app.payments_received pr WHERE pr.customer_id = $1 AND pr.status = 'posted'
			UNION ALL
			SELECT pm.business_date, pm.created_at, 'refund', pm.payment_no, 'Refund paid', pm.amount, 0
			  FROM app.payments_made pm WHERE pm.customer_id = $1 AND pm.status = 'posted'
		) x`
		var opening decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(debit - credit), 0) FROM `+union+` WHERE d < $2::date`, id, from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT d::text, at, type, number, description, debit, credit FROM `+union+`
			WHERE d >= $2::date AND ($3 = '' OR d <= $3::date) ORDER BY d, at`, id, from, to)
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
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $1::date - s.business_date <= 30),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $1::date - s.business_date BETWEEN 31 AND 60),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $1::date - s.business_date BETWEEN 61 AND 90),0),
			  COALESCE(SUM(s.balance_due) FILTER (WHERE $1::date - s.business_date > 90),0),
			  min(s.business_date)::text, count(*)
			FROM app.customers c JOIN app.sales s ON s.customer_id = c.id AND s.balance_due > 0 AND s.status = 'completed'
			GROUP BY c.id ORDER BY 5 DESC, c.full_name`, today)
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

// SendStatement queues the customer's statement summary into app.outbound_messages.
func (s *Service) SendStatement(ctx context.Context, p *auth.Principal, id uuid.UUID, channel, from, to, idemKey string) (map[string]any, error) {
	if strings.TrimSpace(channel) == "" {
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
	stmt, err := s.Statement(ctx, p, id, from, to)
	if err != nil {
		return nil, err
	}
	c := stmt["customer"].(Customer)
	recipient := ""
	if ch == "email" {
		if c.Email != nil {
			recipient = *c.Email
		}
	} else if c.Phone != nil {
		recipient = *c.Phone
	}
	if len(strings.TrimSpace(recipient)) < 5 {
		return nil, httpx.BadRequest("no_recipient", "Customer has no phone number or email for this channel")
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
	body := fmt.Sprintf("Statement for %s (%s): closing balance %s.", c.Name, period, closing.StringFixed(2))
	suffix := strings.TrimSpace(idemKey)
	if suffix == "" {
		suffix = ids.New().String()
	}
	key := fmt.Sprintf("cust-stmt:%s:%s:%s:%s:%s", id.String(), ch, from, to, suffix)
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		row, err := s.Outbound.Enqueue(ctx, tx, outbound.Message{
			Channel:        ch,
			Recipient:      recipient,
			Subject:        "Account statement",
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

// SendDebtReminder queues a debt reminder for a shop customer with an unpaid balance.
func (s *Service) SendDebtReminder(ctx context.Context, p *auth.Principal, id uuid.UUID, channel string) (map[string]any, error) {
	if strings.TrimSpace(channel) == "" {
		channel = "sms"
	}
	ch, err := s.Outbound.CheckAvailable(channel)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if !c.Balance.IsPositive() {
			return httpx.Conflict("nothing_due", "This customer has no outstanding balance")
		}
		recipient := ""
		if ch == "email" {
			if c.Email != nil {
				recipient = *c.Email
			}
		} else if c.Phone != nil {
			recipient = *c.Phone
		}
		if len(strings.TrimSpace(recipient)) < 5 {
			return httpx.BadRequest("no_recipient", "Customer has no phone number or email for this channel")
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		body := outbound.RenderTemplate(ctx, tx, "notifications.sms_template_debt",
			"Reminder: {{name}}, your outstanding balance is {{balance}}.",
			map[string]string{
				"name":    c.Name,
				"balance": c.Balance.StringFixed(2),
			},
		)
		key := fmt.Sprintf("debt-reminder:shop:%s:%s:%s", id.String(), ch, today)
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

// WriteOff is not part of the approved model: a debt is settled by payment, or corrected by an
// approved transaction correction.
func (s *Service) WriteOff(ctx context.Context, p *auth.Principal, id uuid.UUID, in WriteOffIn) (int, []byte, error) {
	return 0, nil, httpx.New(410, "feature_removed", "Writing off a customer debt is not part of the approved accounting model")
}
