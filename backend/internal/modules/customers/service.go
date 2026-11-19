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
	"bms/internal/platform/ids"
	"bms/internal/platform/money"
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

type Customer struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Phone       *string         `json:"phone"`
	Email       *string         `json:"email"`
	Address     *string         `json:"address"`
	TaxNumber   *string         `json:"tax_number"`
	CreditLimit decimal.Decimal `json:"credit_limit"`
	TermsDays   int             `json:"payment_terms_days"`
	IsWalkIn    bool            `json:"is_walk_in"`
	IsActive    bool            `json:"is_active"`
	Balance     decimal.Decimal `json:"balance"`
	Credit      decimal.Decimal `json:"credit"`
	Notes       *string         `json:"notes"`
	CreatedAt   time.Time       `json:"created_at"`

	// Account summary, filled on the profile (FR-CUS-03).
	TotalPurchases *decimal.Decimal `json:"total_purchases,omitempty"`
	TotalPaid      *decimal.Decimal `json:"total_paid,omitempty"`
	Available      *decimal.Decimal `json:"available_credit,omitempty"`
	LastActivity   *time.Time       `json:"last_activity,omitempty"`
	SalesCount     *int             `json:"sales_count,omitempty"`
}

type Input struct {
	Name        string          `json:"name" validate:"max=200"`
	Phone       string          `json:"phone" validate:"max=40"`
	Email       string          `json:"email" validate:"omitempty,email"`
	Address     string          `json:"address" validate:"max=500"`
	TaxNumber   string          `json:"tax_number" validate:"max=60"`
	CreditLimit decimal.Decimal `json:"credit_limit" validate:"dgte0"`
	TermsDays   int             `json:"payment_terms_days" validate:"gte=0,lte=365"`
	Notes       string          `json:"notes" validate:"max=2000"`
}

const custCols = `c.id, c.name, c.phone, c.email::text, c.address, c.tax_number, c.credit_limit, c.payment_terms_days, c.is_walk_in, c.is_active, c.notes, c.created_at,
	COALESCE((SELECT SUM(s.balance) FROM sales s WHERE s.customer_id=c.id AND s.kind='sale' AND s.status IN ('completed','partially_refunded')),0),
	COALESCE((SELECT SUM(jl.credit - jl.debit) FROM journal_lines jl JOIN ledger_accounts la ON la.id=jl.ledger_account_id
	          WHERE jl.party_type='customer' AND jl.party_id=c.id AND la.code='2300'),0)`

func scanCustomer(row pgx.Row, c *Customer) error {
	return row.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.Address, &c.TaxNumber, &c.CreditLimit, &c.TermsDays, &c.IsWalkIn, &c.IsActive, &c.Notes, &c.CreatedAt, &c.Balance, &c.Credit)
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
		if qq := strings.TrimSpace(f.Q); qq != "" {
			q.Add("(c.name ILIKE '%' || ? || '%' OR c.phone ILIKE '%' || ? || '%' OR c.email::text ILIKE '%' || ? || '%')", qq, qq, qq)
		}
		q.If(f.Active != nil, "c.is_active = ?", f.Active)
		q.If(f.HasBalance, "EXISTS (SELECT 1 FROM sales s WHERE s.customer_id=c.id AND s.balance > 0 AND s.status IN ('completed','partially_refunded'))")
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM customers c`+q.Where(), q.Args...).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+custCols+` FROM customers c`+q.Where()+` ORDER BY c.is_walk_in DESC, c.name, c.id`+q.Page(page.PerPage, page.Offset), q.Args...)
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
	id := ids.New()
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		limit := money.Money(in.CreditLimit)
		// Setting a credit limit needs its own permission.
		if _, ok := p.Can("customers.credit_limit"); !ok {
			limit, in.TermsDays = money.Zero, 0
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO customers (id, business_id, name, phone, email, address, tax_number, credit_limit, payment_terms_days, notes, created_by)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::citext,NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11)
		`, id, p.BusinessID, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), in.Address, in.TaxNumber, limit, in.TermsDays, in.Notes, p.UserID)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "A customer with that phone already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.created", "customer", id, nil, in)
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
		var purchases, paid decimal.Decimal
		var n int
		var last *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT SUM(si.line_total) FROM sale_items si JOIN sales s ON s.id=si.sale_id
			                 WHERE s.customer_id=$1 AND s.kind='sale' AND s.status IN ('completed','partially_refunded','refunded')),0),
			       COALESCE((SELECT SUM(CASE WHEN direction='in' THEN amount ELSE -amount END) FROM payments
			                 WHERE party_type='customer' AND party_id=$1 AND status='posted'),0),
			       (SELECT count(*) FROM sales WHERE customer_id=$1 AND kind='sale' AND status IN ('completed','partially_refunded','refunded')),
			       GREATEST((SELECT max(created_at) FROM sales WHERE customer_id=$1 AND kind='sale' AND status NOT IN ('held','cancelled')),
			                (SELECT max(created_at) FROM payments WHERE party_type='customer' AND party_id=$1))
		`, id).Scan(&purchases, &paid, &n, &last); err != nil {
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
	err := scanCustomer(tx.QueryRow(ctx, `SELECT `+custCols+` FROM customers c WHERE c.id=$1`, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, httpx.NotFound("not_found", "Customer not found")
	}
	return c, err
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in Input) (Customer, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if before.IsWalkIn {
			return httpx.Conflict("invalid_status", "The walk-in customer cannot be edited")
		}
		_, err = tx.Exec(ctx, `
			UPDATE customers SET name=COALESCE(NULLIF($2,''), name), phone=COALESCE(NULLIF($3,''), phone),
			email=COALESCE(NULLIF($4,'')::citext, email), address=COALESCE(NULLIF($5,''), address),
			tax_number=COALESCE(NULLIF($6,''), tax_number), notes=COALESCE(NULLIF($7,''), notes)
			WHERE id=$1
		`, id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), in.Address, in.TaxNumber, in.Notes)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "A customer with that phone already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.updated", "customer", id, before, in)
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

func (s *Service) CreditLimit(ctx context.Context, p *auth.Principal, id uuid.UUID, limit decimal.Decimal, days int) error {
	if limit.IsNegative() || days < 0 || days > 365 {
		return httpx.BadRequest("invalid_body", "Credit limit cannot be negative and terms must be 0 to 365 days")
	}
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if before.IsWalkIn {
			return httpx.Conflict("invalid_status", "The walk-in customer cannot have credit")
		}
		if _, err := tx.Exec(ctx, `UPDATE customers SET credit_limit=$2, payment_terms_days=$3 WHERE id=$1`, id, money.Money(limit), days); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.credit_limit", "customer", id,
			map[string]any{"credit_limit": before.CreditLimit, "days": before.TermsDays}, map[string]any{"credit_limit": money.Money(limit), "days": days})
	})
}

// Deactivate hides a customer from new sales. A customer who still owes cannot be deactivated.
func (s *Service) Deactivate(ctx context.Context, p *auth.Principal, id uuid.UUID) (Customer, error) {
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		if c.IsWalkIn {
			return httpx.Conflict("invalid_status", "The walk-in customer cannot be deactivated")
		}
		if c.Balance.IsPositive() {
			return httpx.Conflict("has_balance", "This customer still owes "+c.Balance.StringFixed(2)+". Collect or write off the debt first")
		}
		if _, err := tx.Exec(ctx, `UPDATE customers SET is_active=false WHERE id=$1`, id); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.deactivated", "customer", id, map[string]any{"is_active": c.IsActive}, map[string]any{"is_active": false})
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

// Receive records a customer payment, allocated oldest sale first or as given. Any extra becomes customer credit (FR-CUS-07).
func (s *Service) Receive(ctx context.Context, p *auth.Principal, customerID uuid.UUID, in PayIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if in.Amount.LessThanOrEqual(money.Zero) {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
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
		var walk, active bool
		if err := tx.QueryRow(ctx, `SELECT is_walk_in, is_active FROM customers WHERE id=$1 FOR UPDATE`, customerID).Scan(&walk, &active); err != nil {
			return httpx.NotFound("not_found", "Customer not found")
		}
		if walk {
			return httpx.Conflict("customer_required", "Payments are recorded against a named customer")
		}
		var accActive bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM money_accounts WHERE id=$1`, in.MoneyAccountID).Scan(&accActive); err != nil || !accActive {
			return httpx.NotFound("not_found", "Money account not found or inactive")
		}
		amount := money.Money(in.Amount)
		type doc struct {
			id  uuid.UUID
			bal decimal.Decimal
			use decimal.Decimal
		}
		var docs []doc
		if len(in.Allocations) > 0 {
			sum := money.Zero
			seen := map[uuid.UUID]bool{}
			for _, a := range in.Allocations {
				if !a.Amount.IsPositive() || seen[a.SaleID] {
					return httpx.BadRequest("invalid_body", "Each allocation needs a different sale and a positive amount")
				}
				seen[a.SaleID] = true
				var bal decimal.Decimal
				if err := tx.QueryRow(ctx, `SELECT balance FROM sales WHERE id=$1 AND customer_id=$2 AND kind='sale' AND status IN ('completed','partially_refunded') FOR UPDATE`, a.SaleID, customerID).Scan(&bal); err != nil {
					return httpx.NotFound("not_found", "Sale to allocate to was not found for this customer")
				}
				use := money.Money(a.Amount)
				if use.GreaterThan(bal) {
					return httpx.Conflict("overallocation", "An allocation is larger than that sale's balance")
				}
				sum = sum.Add(use)
				docs = append(docs, doc{id: a.SaleID, bal: bal, use: use})
			}
			if sum.GreaterThan(amount) {
				return httpx.Conflict("overallocation", "Allocations add up to more than the payment")
			}
		} else {
			rows, err := tx.Query(ctx, `
				SELECT id, balance FROM sales
				WHERE customer_id=$1 AND kind='sale' AND balance>0 AND status IN ('completed','partially_refunded')
				ORDER BY business_date, completed_at, id FOR UPDATE
			`, customerID)
			if err != nil {
				return err
			}
			left := amount
			for rows.Next() {
				var d doc
				if err := rows.Scan(&d.id, &d.bal); err != nil {
					rows.Close()
					return err
				}
				if left.IsPositive() {
					d.use = decimal.Min(left, d.bal)
					left = left.Sub(d.use)
					docs = append(docs, d)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		date := in.Date
		if date == "" {
			var err error
			if date, err = s.bizDate(ctx, tx); err != nil {
				return err
			}
		}
		code, _, err := s.Ledger.AccountCodeForMoney(ctx, tx, in.MoneyAccountID)
		if err != nil {
			return err
		}
		res, err := payments.Insert(ctx, tx, payments.Input{
			BusinessID: p.BusinessID, BranchID: *p.BranchID, MoneyAccountID: in.MoneyAccountID, UserID: p.UserID,
			Direction: "in", Kind: "customer_payment", Method: in.Method, PartyType: "customer", PartyID: &customerID,
			Amount: amount, Reference: in.Reference, Date: date,
		})
		if err != nil {
			return err
		}
		applied := money.Zero
		allocs := []map[string]any{}
		for _, d := range docs {
			if !d.use.IsPositive() {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE sales SET paid_total=paid_total+$2, balance=balance-$2 WHERE id=$1`, d.id, d.use); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_allocations (id, business_id, payment_id, document_type, document_id, amount)
				VALUES ($1,$2,$3,'sale',$4,$5)
			`, ids.New(), p.BusinessID, res.ID, d.id, d.use); err != nil {
				return err
			}
			applied = applied.Add(d.use)
			allocs = append(allocs, map[string]any{"sale_id": d.id, "amount": d.use})
		}
		left := amount.Sub(applied)
		user := p.UserID
		lines := []ledger.Line{{Code: code, BranchID: p.BranchID, MoneyAccountID: &in.MoneyAccountID, PartyType: "customer", PartyID: &customerID, Debit: amount}}
		if applied.IsPositive() {
			lines = append(lines, ledger.Line{Code: "1100", BranchID: p.BranchID, PartyType: "customer", PartyID: &customerID, Credit: applied})
		}
		if left.IsPositive() {
			lines = append(lines, ledger.Line{Code: "2300", BranchID: p.BranchID, PartyType: "customer", PartyID: &customerID, Credit: left})
		}
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "customer_payment", res.ID, "Customer payment "+res.Number, &user, lines); err != nil {
			return err
		}
		out := map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "allocated": applied, "credit": left, "allocations": allocs, "date": date}
		if err := s.Audit.Record(ctx, tx, p, "customer.payment", "payment", res.ID, nil, map[string]any{"amount": amount, "customer_id": customerID, "allocations": allocs}); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

// Unpaid lists the customer's sales with a balance, oldest first, with age and due date.
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
		q := sqlq.New(id, today)
		if b := p.ScopeBranches("customers.view"); b != nil {
			q.Add("s.branch_id = ANY(?)", b)
		}
		rows, err := tx.Query(ctx, `
			SELECT s.id, s.number, s.branch_id, s.business_date::text, s.due_date::text, s.total, s.paid_total, s.written_off, s.balance,
			       ($2::date - s.business_date), CASE WHEN s.due_date IS NOT NULL AND s.due_date < $2::date THEN ($2::date - s.due_date) ELSE 0 END
			FROM sales s WHERE s.customer_id=$1 AND s.kind='sale' AND s.balance > 0 AND s.status IN ('completed','partially_refunded')`+q.And()+`
			ORDER BY s.business_date, s.created_at`, q.Args...)
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
			var tot, paid, wo, bal decimal.Decimal
			var age, overdue int
			if err := rows.Scan(&sid, &number, &branch, &date, &due, &tot, &paid, &wo, &bal, &age, &overdue); err != nil {
				return err
			}
			total = total.Add(bal)
			list = append(list, map[string]any{"sale_id": sid, "number": number, "branch_id": branch, "date": date, "due_date": due, "total": tot,
				"paid_total": paid, "written_off": wo, "balance": bal, "age_days": age, "days_overdue": overdue})
		}
		out = map[string]any{"customer_id": id, "sales": list, "total_balance": total}
		return rows.Err()
	})
	return out, err
}

// Statement lists sales, returns, payments, refunds and write-offs with running balance (FR-CUS-09).
// A positive balance is owed by the customer; negative is credit held for them.
func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID, from, to string) (map[string]any, error) {
	var out map[string]any
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.load(ctx, tx, id)
		if err != nil {
			return err
		}
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if to == "" {
			to = today
		}
		if from == "" {
			from = mustDate(to).AddDate(0, 0, -30).Format("2006-01-02")
		}
		tz := setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")
		const entries = `
			SELECT s.business_date AS d, s.created_at AS at, s.number AS ref, 'sale' AS kind, 'Sale' AS label,
			       (SELECT COALESCE(SUM(line_total),0) FROM sale_items WHERE sale_id=s.id) AS debit, 0::numeric AS credit
			FROM sales s WHERE s.customer_id=$1 AND s.kind='sale' AND s.status IN ('completed','partially_refunded','refunded')
			UNION ALL
			SELECT (r.completed_at AT TIME ZONE $2)::date, r.completed_at, r.number, 'return', 'Return: ' || r.reason, 0, r.refund_total
			FROM sale_returns r JOIN sales s ON s.id=r.sale_id WHERE s.customer_id=$1 AND r.status='completed'
			UNION ALL
			SELECT py.payment_date, py.created_at, py.number, CASE WHEN py.direction='in' THEN 'payment' ELSE 'refund' END,
			       CASE WHEN py.direction='in' THEN 'Payment (' || py.method || ')' ELSE 'Refund (' || py.method || ')' END,
			       CASE WHEN py.direction='out' THEN py.amount ELSE 0 END, CASE WHEN py.direction='in' THEN py.amount ELSE 0 END
			FROM payments py WHERE py.party_type='customer' AND py.party_id=$1 AND py.status='posted'
			UNION ALL
			SELECT w.write_off_date, w.created_at, 'Write-off', 'write_off', 'Bad debt written off: ' || w.reason, 0, w.amount
			FROM customer_write_offs w WHERE w.customer_id=$1`
		var opening decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(debit - credit),0) FROM (`+entries+`) e WHERE e.d < $3::date`, id, tz, from).Scan(&opening); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT d::text, ref, kind, label, debit, credit FROM (`+entries+`) e WHERE e.d BETWEEN $3::date AND $4::date ORDER BY e.d, e.at, e.ref`, id, tz, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		lines := []map[string]any{}
		running := opening
		debits, credits := money.Zero, money.Zero
		for rows.Next() {
			var d, ref, kind, label string
			var debit, credit decimal.Decimal
			if err := rows.Scan(&d, &ref, &kind, &label, &debit, &credit); err != nil {
				return err
			}
			running = running.Add(debit).Sub(credit)
			debits, credits = debits.Add(debit), credits.Add(credit)
			lines = append(lines, map[string]any{"date": d, "number": ref, "kind": kind, "description": label, "debit": debit, "credit": credit, "balance": running})
		}
		out = map[string]any{"customer": c, "from": from, "to": to, "opening_balance": opening, "lines": lines,
			"total_debit": debits, "total_credit": credits, "closing_balance": running}
		return rows.Err()
	})
	return out, err
}

func mustDate(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

// Debts is the aged receivables list for all customers in the caller's branches (FR-CUS-05).
func (s *Service) Debts(ctx context.Context, p *auth.Principal, key string) ([]map[string]any, error) {
	out := []map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		today, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		q := sqlq.New(today)
		if b := p.ScopeBranches(key); b != nil {
			q.Add("s.branch_id = ANY(?)", b)
		}
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.name, c.phone, c.credit_limit, COALESCE(SUM(s.balance),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE $1::date - s.business_date <= 30),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE $1::date - s.business_date BETWEEN 31 AND 60),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE $1::date - s.business_date BETWEEN 61 AND 90),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE $1::date - s.business_date > 90),0),
			  min(s.business_date)::text, count(*)
			FROM customers c JOIN sales s ON s.customer_id=c.id AND s.kind='sale' AND s.balance>0 AND s.status IN ('completed','partially_refunded')`+q.Where()+`
			GROUP BY c.id ORDER BY 5 DESC, c.name
		`, q.Args...)
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

// WriteOff clears bad debt oldest sale first, posting Dr 6200 Bad debts, Cr 1100 Receivable (FR-CUS-12).
// An amount of zero writes off the whole balance.
func (s *Service) WriteOff(ctx context.Context, p *auth.Principal, id uuid.UUID, in WriteOffIn) (int, []byte, error) {
	var status int
	var body []byte
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		var walk bool
		if err := tx.QueryRow(ctx, `SELECT is_walk_in FROM customers WHERE id=$1 FOR UPDATE`, id).Scan(&walk); err != nil || walk {
			return httpx.NotFound("not_found", "Customer not found")
		}
		q := sqlq.New(id)
		if b := p.ScopeBranches("customers.write_off"); b != nil {
			q.Add("branch_id = ANY(?)", b)
		}
		rows, err := tx.Query(ctx, `
			SELECT id, branch_id, balance FROM sales WHERE customer_id=$1 AND kind='sale' AND balance>0 AND status IN ('completed','partially_refunded')`+q.And()+`
			ORDER BY business_date, created_at FOR UPDATE`, q.Args...)
		if err != nil {
			return err
		}
		type doc struct {
			id, branch uuid.UUID
			bal, use   decimal.Decimal
		}
		var docs []doc
		owed := money.Zero
		for rows.Next() {
			var d doc
			if err := rows.Scan(&d.id, &d.branch, &d.bal); err != nil {
				rows.Close()
				return err
			}
			owed = owed.Add(d.bal)
			docs = append(docs, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if !owed.IsPositive() {
			return httpx.Conflict("invalid_status", "This customer owes nothing")
		}
		amount := money.Money(in.Amount)
		if amount.IsZero() {
			amount = owed
		}
		if amount.GreaterThan(owed) {
			return httpx.Conflict("invalid_status", "The write-off is more than the customer owes ("+owed.StringFixed(2)+")")
		}
		date, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		woID := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO customer_write_offs (id, business_id, customer_id, amount, reason, write_off_date, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			woID, p.BusinessID, id, amount, strings.TrimSpace(in.Reason), date, p.UserID); err != nil {
			return err
		}
		left := amount
		byBranch := map[uuid.UUID]decimal.Decimal{}
		var order []uuid.UUID
		for _, d := range docs {
			if !left.IsPositive() {
				break
			}
			use := decimal.Min(left, d.bal)
			left = left.Sub(use)
			if _, err := tx.Exec(ctx, `UPDATE sales SET written_off=written_off+$2, balance=balance-$2 WHERE id=$1`, d.id, use); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO customer_write_off_items (id, business_id, write_off_id, sale_id, amount) VALUES ($1,$2,$3,$4,$5)`, ids.New(), p.BusinessID, woID, d.id, use); err != nil {
				return err
			}
			if _, ok := byBranch[d.branch]; !ok {
				order = append(order, d.branch)
			}
			byBranch[d.branch] = byBranch[d.branch].Add(use)
		}
		var lines []ledger.Line
		for _, b := range order {
			b := b
			amt := byBranch[b]
			lines = append(lines,
				ledger.Line{Code: "6200", BranchID: &b, Debit: amt},
				ledger.Line{Code: "1100", BranchID: &b, PartyType: "customer", PartyID: &id, Credit: amt})
		}
		user := p.UserID
		if _, err := s.Ledger.Post(ctx, tx, p.BusinessID, date, "customer_write_off", woID, "Bad debt write-off", &user, lines); err != nil {
			return err
		}
		out := map[string]any{"id": woID, "customer_id": id, "amount": amount, "reason": in.Reason, "date": date, "remaining_balance": owed.Sub(amount)}
		if err := s.Audit.Record(ctx, tx, p, "customer.write_off", "customer", id, nil, out); err != nil {
			return err
		}
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(out))
		status, body = 201, raw
		return err
	})
	return status, body, err
}
