// Package reports serves read-only reports and the dashboard. Every figure is
// computed in SQL from the posted records: sale lines (with their cost
// snapshot), stock movements, payments and journal lines.
package reports

import (
	"context"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/money"
	"bms/internal/platform/setting"
	"bms/internal/platform/sqlq"
)

type Service struct {
	DB    *db.DB
	Clock clock.Clock
}

func New(database *db.DB, clk clock.Clock) *Service { return &Service{DB: database, Clock: clk} }

// Filter is the common input of every report.
type Filter struct {
	From, To string      // YYYY-MM-DD, empty means unbounded
	Branches []uuid.UUID // nil means every branch
	Query    map[string]string
	tz       string
	today    string
}

func (f Filter) Q(key string) string { return f.Query[key] }

// Days returns an integer query parameter with a default and bounds.
func (f Filter) Int(key string, def, min, max int) int {
	n, err := strconv.Atoi(f.Query[key])
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// branch adds "col = ANY(branches)" when the filter is limited.
func (f Filter) branch(q *sqlq.Q, col string) {
	if f.Branches != nil {
		q.Add(col+" = ANY(?)", f.Branches)
	}
}

// dates adds the from/to bounds on a date expression.
func (f Filter) dates(q *sqlq.Q, expr string) {
	q.If(f.From != "", expr+" >= ?::date", f.From)
	q.If(f.To != "", expr+" <= ?::date", f.To)
}

func (f Filter) subtitle() string {
	switch {
	case f.From != "" && f.To != "":
		return f.From + " to " + f.To
	case f.From != "":
		return "From " + f.From
	case f.To != "":
		return "Up to " + f.To
	}
	return "All dates"
}

// tx runs fn in a read transaction for the caller's business, with the
// business timezone and business date loaded into f.
func (s *Service) tx(ctx context.Context, p *auth.Principal, f *Filter, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.DB.InTx(ctx, p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		f.tz = setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")
		day, err := clock.BusinessDate(s.Clock.Now(), f.tz, setting.String(ctx, tx, "system.business_day_start", "00:00"))
		if err != nil {
			day = s.Clock.Now().UTC()
		}
		f.today = day.Format("2006-01-02")
		if _, err := tx.Exec(ctx, `SET TRANSACTION READ ONLY`); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

// rows runs a query and converts each row to a map keyed by column name.
// Numerics become decimal.Decimal, dates YYYY-MM-DD strings, uuids uuid.UUID.
func rows(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]map[string]any, error) {
	r, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	fields := r.FieldDescriptions()
	out := []map[string]any{}
	for r.Next() {
		vals, err := r.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, v := range vals {
			m[fields[i].Name] = convert(fields[i].DataTypeOID, v)
		}
		out = append(out, m)
	}
	return out, r.Err()
}

func one(ctx context.Context, tx pgx.Tx, sql string, args ...any) (map[string]any, error) {
	list, err := rows(ctx, tx, sql, args...)
	if err != nil || len(list) == 0 {
		return map[string]any{}, err
	}
	return list[0], nil
}

func convert(oid uint32, v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case pgtype.Numeric:
		if !t.Valid || t.Int == nil {
			return nil
		}
		return decimal.NewFromBigInt(new(big.Int).Set(t.Int), t.Exp)
	case [16]uint8:
		return uuid.UUID(t)
	case time.Time:
		if oid == pgtype.DateOID {
			return t.Format("2006-01-02")
		}
		return t.UTC()
	}
	return v
}

// num reads a decimal from a row, treating missing values as zero.
func num(m map[string]any, key string) decimal.Decimal {
	switch v := m[key].(type) {
	case decimal.Decimal:
		return v
	case int64:
		return decimal.NewFromInt(v)
	case int32:
		return decimal.NewFromInt(int64(v))
	}
	return decimal.Zero
}

// totals sums the given columns over the rows.
func totals(list []map[string]any, label string, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		sum := decimal.Zero
		for _, r := range list {
			sum = sum.Add(num(r, k))
		}
		out[k] = sum
	}
	if label != "" {
		out["label"] = label
	}
	return out
}

// pct returns part/whole*100 rounded to two places, or nil when whole is zero.
func pct(part, whole decimal.Decimal) any {
	if whole.IsZero() {
		return nil
	}
	return money.Money(part.Div(whole).Mul(decimal.NewFromInt(100)))
}

// linesSQL is the sale-line ledger used by every sales report: sale lines dated
// on the sale's business date, and completed return lines dated on the return,
// with quantities and money negated. cost is the snapshot at sale time; a
// return line only reduces cost when the goods went back to stock.
func linesSQL(q *sqlq.Q, f Filter) string {
	tz := q.Arg(f.tz)
	retDate := "(r.completed_at AT TIME ZONE " + tz + ")::date"
	sc := "s.kind='sale' AND s.status IN ('completed','partially_refunded','refunded')"
	rc := "r.status='completed'"
	if f.From != "" {
		a := q.Arg(f.From)
		sc += " AND s.business_date >= " + a + "::date"
		rc += " AND " + retDate + " >= " + a + "::date"
	}
	if f.To != "" {
		a := q.Arg(f.To)
		sc += " AND s.business_date <= " + a + "::date"
		rc += " AND " + retDate + " <= " + a + "::date"
	}
	if f.Branches != nil {
		a := q.Arg(f.Branches)
		sc += " AND s.branch_id = ANY(" + a + ")"
		rc += " AND r.branch_id = ANY(" + a + ")"
	}
	return `(
		SELECT s.id AS sale_id, s.business_date AS d, s.branch_id, si.product_id, s.customer_id, s.cashier_id, s.employee_id,
		       si.qty, si.qty * si.unit_price AS gross, si.discount AS disc, si.tax_amount AS tax, si.line_total AS total,
		       si.line_total - si.tax_amount AS net, si.qty * si.unit_cost AS cost, 0::numeric AS ret
		FROM sales s JOIN sale_items si ON si.sale_id = s.id
		WHERE ` + sc + `
		UNION ALL
		SELECT s.id, ` + retDate + `, r.branch_id, si.product_id, s.customer_id, s.cashier_id, s.employee_id,
		       -ri.qty, 0, 0, -ROUND(si.tax_amount * ri.qty / si.qty, 2), -ri.amount,
		       -(ri.amount - ROUND(si.tax_amount * ri.qty / si.qty, 2)),
		       -(CASE WHEN ri.restock THEN ri.qty * ri.unit_cost ELSE 0 END), ri.amount
		FROM sale_returns r
		JOIN sale_return_items ri ON ri.return_id = r.id
		JOIN sale_items si ON si.id = ri.sale_item_id
		JOIN sales s ON s.id = r.sale_id
		WHERE ` + rc + `
	) l`
}

// saleAgg is the common aggregate column list over linesSQL.
const saleAgg = `
	COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count,
	ROUND(SUM(l.qty), 3) AS qty,
	ROUND(SUM(l.gross), 2) AS gross_sales,
	ROUND(SUM(l.disc), 2) AS discounts,
	ROUND(SUM(l.ret), 2) AS returns,
	ROUND(SUM(l.tax), 2) AS tax,
	ROUND(SUM(l.total), 2) AS total,
	ROUND(SUM(l.net), 2) AS net_sales,
	ROUND(SUM(l.cost), 2) AS cost,
	ROUND(SUM(l.net) - SUM(l.cost), 2) AS gross_profit`

// canCost reports whether cost and profit may be shown to the caller.
func canCost(p *auth.Principal) bool {
	if p.IsOwner {
		return true
	}
	if _, ok := p.Can("products.view_cost"); ok {
		return true
	}
	_, ok := p.Can("dashboard.view_profit")
	return ok
}

// stripCost removes cost and profit columns when the caller may not see them.
func stripCost(p *auth.Principal, cols []string) []string {
	if canCost(p) {
		return cols
	}
	hidden := map[string]bool{"cost": true, "gross_profit": true, "profit": true, "margin": true, "avg_cost": true, "value": true, "unit_cost": true}
	out := cols[:0:0]
	for _, c := range cols {
		if !hidden[c] {
			out = append(out, c)
		}
	}
	return out
}

func dropKeys(list []map[string]any, keys ...string) {
	for _, r := range list {
		for _, k := range keys {
			delete(r, k)
		}
	}
}
