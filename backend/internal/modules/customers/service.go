package customers

import (
	"context"
	"strings"

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
	Notes       *string         `json:"notes"`
}

type Input struct {
	Name        string          `json:"name"`
	Phone       string          `json:"phone"`
	Email       string          `json:"email"`
	Address     string          `json:"address"`
	TaxNumber   string          `json:"tax_number"`
	CreditLimit decimal.Decimal `json:"credit_limit"`
	TermsDays   int             `json:"payment_terms_days"`
	Notes       string          `json:"notes"`
}

func (s *Service) List(ctx context.Context, p *auth.Principal, q string) ([]Customer, error) {
	var out []Customer
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.name, c.phone, c.email::text, c.address, c.tax_number, c.credit_limit, c.payment_terms_days, c.is_walk_in, c.is_active, c.notes,
			       COALESCE((SELECT SUM(balance) FROM sales s WHERE s.customer_id=c.id AND s.status IN ('completed','partially_refunded')),0)
			FROM customers c
			WHERE ($1='' OR c.name ILIKE '%'||$1||'%' OR c.phone ILIKE '%'||$1||'%')
			ORDER BY c.is_walk_in, c.name
		`, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c Customer
			if err := rows.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.Address, &c.TaxNumber, &c.CreditLimit, &c.TermsDays, &c.IsWalkIn, &c.IsActive, &c.Notes, &c.Balance); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if out == nil {
		out = []Customer{}
	}
	return out, err
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input) (Customer, error) {
	if strings.TrimSpace(in.Name) == "" {
		return Customer{}, httpx.BadRequest("invalid_body", "Customer name is required")
	}
	id := ids.New()
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO customers (id, business_id, name, phone, email, address, tax_number, credit_limit, payment_terms_days, notes, created_by)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::citext,NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11)
		`, id, p.BusinessID, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), in.Address, in.TaxNumber, money.Money(in.CreditLimit), in.TermsDays, in.Notes, p.UserID)
		if db.IsUnique(err) {
			return httpx.Conflict("duplicate", "A customer with that phone already exists")
		}
		if err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "customer.created", "customer", id, nil, map[string]any{"name": in.Name})
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

func (s *Service) Get(ctx context.Context, p *auth.Principal, id uuid.UUID) (Customer, error) {
	list, err := s.List(ctx, p, "")
	if err != nil {
		return Customer{}, err
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return Customer{}, httpx.NotFound("not_found", "Customer not found")
}

func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, in Input) (Customer, error) {
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE customers SET name=COALESCE(NULLIF($2,''), name), phone=COALESCE(NULLIF($3,''), phone),
			email=COALESCE(NULLIF($4,'')::citext, email), address=COALESCE(NULLIF($5,''), address), notes=COALESCE(NULLIF($6,''), notes)
			WHERE id=$1 AND NOT is_walk_in
		`, id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Phone), strings.TrimSpace(in.Email), in.Address, in.Notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Customer not found")
		}
		return s.Audit.Record(ctx, tx, p, "customer.updated", "customer", id, nil, in)
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, p, id)
}

func (s *Service) CreditLimit(ctx context.Context, p *auth.Principal, id uuid.UUID, limit decimal.Decimal, days int) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE customers SET credit_limit=$2, payment_terms_days=$3 WHERE id=$1`, id, money.Money(limit), days)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.NotFound("not_found", "Customer not found")
		}
		return s.Audit.Record(ctx, tx, p, "customer.credit_limit", "customer", id, nil, map[string]any{"credit_limit": limit, "days": days})
	})
}

type PayIn struct {
	Amount         decimal.Decimal `json:"amount"`
	Method         string          `json:"method"`
	MoneyAccountID uuid.UUID       `json:"money_account_id"`
	Reference      string          `json:"reference"`
	Date           string          `json:"date"`
}

func (s *Service) Receive(ctx context.Context, p *auth.Principal, customerID uuid.UUID, in PayIn) (int, []byte, error) {
	if p.BranchID == nil {
		return 0, nil, httpx.BadRequest("branch_required", "Choose a branch first")
	}
	if in.Amount.LessThanOrEqual(money.Zero) {
		return 0, nil, httpx.BadRequest("invalid_body", "Amount must be greater than zero")
	}
	var status int
	var body []byte
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		if rep, err := idem.Claim(ctx, tx, p.BusinessID, p.UserID); err != nil || rep != nil {
			if rep != nil {
				status, body = rep.Status, rep.Body
			}
			return err
		}
		amount := money.Money(in.Amount)
		rows, err := tx.Query(ctx, `
			SELECT id, balance FROM sales
			WHERE customer_id=$1 AND balance>0 AND status IN ('completed','partially_refunded')
			ORDER BY business_date, completed_at
		`, customerID)
		if err != nil {
			return err
		}
		type doc struct {
			id  uuid.UUID
			bal decimal.Decimal
		}
		var docs []doc
		for rows.Next() {
			var d doc
			if err := rows.Scan(&d.id, &d.bal); err != nil {
				rows.Close()
				return err
			}
			docs = append(docs, d)
		}
		rows.Close()
		left := amount
		date := in.Date
		if date == "" {
			date = s.Clock.Now().Format("2006-01-02")
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
		for _, d := range docs {
			if !left.IsPositive() {
				break
			}
			use := left
			if use.GreaterThan(d.bal) {
				use = d.bal
			}
			if _, err := tx.Exec(ctx, `UPDATE sales SET paid_total=paid_total+$2, balance=balance-$2 WHERE id=$1`, d.id, use); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO payment_allocations (id, business_id, payment_id, document_type, document_id, amount)
				VALUES ($1,$2,$3,'sale',$4,$5)
			`, ids.New(), p.BusinessID, res.ID, d.id, use); err != nil {
				return err
			}
			applied = applied.Add(use)
			left = left.Sub(use)
		}
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
		_ = s.Audit.Record(ctx, tx, p, "customer.payment", "payment", res.ID, nil, map[string]any{"amount": amount, "customer_id": customerID})
		raw, err := idem.Complete(ctx, tx, p.UserID, 201, httpx.Data(map[string]any{"id": res.ID, "number": res.Number, "amount": amount, "allocated": applied, "credit": left}))
		status, body = 201, raw
		return err
	})
	return status, body, err
}

func (s *Service) Statement(ctx context.Context, p *auth.Principal, id uuid.UUID) (map[string]any, error) {
	c, err := s.Get(ctx, p, id)
	if err != nil {
		return nil, err
	}
	var lines []map[string]any
	err = s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT business_date::text, number, total, paid_total, balance, 'sale' FROM sales
			WHERE customer_id=$1 AND status IN ('completed','partially_refunded','refunded')
			UNION ALL
			SELECT payment_date::text, number, amount, amount, 0, 'payment' FROM payments
			WHERE party_type='customer' AND party_id=$1 AND status='posted'
			ORDER BY 1, 2
		`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var date, number, kind string
			var a, b, c decimal.Decimal
			if err := rows.Scan(&date, &number, &a, &b, &c, &kind); err != nil {
				return err
			}
			lines = append(lines, map[string]any{"date": date, "number": number, "kind": kind, "amount": a, "paid": b, "balance": c})
		}
		return rows.Err()
	})
	if lines == nil {
		lines = []map[string]any{}
	}
	return map[string]any{"customer": c, "lines": lines, "closing_balance": c.Balance}, err
}

func (s *Service) Debts(ctx context.Context, p *auth.Principal) ([]map[string]any, error) {
	var out []map[string]any
	err := s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT c.id, c.name, COALESCE(SUM(s.balance),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE current_date - s.business_date <= 30),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE current_date - s.business_date BETWEEN 31 AND 60),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE current_date - s.business_date BETWEEN 61 AND 90),0),
			  COALESCE(SUM(s.balance) FILTER (WHERE current_date - s.business_date > 90),0)
			FROM customers c JOIN sales s ON s.customer_id=c.id AND s.balance>0 AND s.status IN ('completed','partially_refunded')
			GROUP BY c.id ORDER BY 3 DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			var total, a, b, c, d decimal.Decimal
			if err := rows.Scan(&id, &name, &total, &a, &b, &c, &d); err != nil {
				return err
			}
			out = append(out, map[string]any{"customer_id": id, "name": name, "balance": total, "bucket_0_30": a, "bucket_31_60": b, "bucket_61_90": c, "bucket_over_90": d})
		}
		return rows.Err()
	})
	if out == nil {
		out = []map[string]any{}
	}
	return out, err
}
