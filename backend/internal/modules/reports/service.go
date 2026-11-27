// Package reports serves read-only reports and the dashboard from the approved BMS schema.
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
	Branches []uuid.UUID // nil means every location
	Query    map[string]string
	tz       string
	today    string
}

func (f Filter) Q(key string) string { return f.Query[key] }

// Int returns an integer query parameter with a default and bounds.
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

// branch adds "col = ANY(branches)" when the filter is limited to specific locations.
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

// tx runs fn in a REPEATABLE READ, READ ONLY actor transaction for the caller's tenant, with the
// tenant timezone and business date loaded into f so multi-query reports read one atomic MVCC snapshot.
func (s *Service) tx(ctx context.Context, p *auth.Principal, f *Filter, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.DB.InActorReadTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		f.tz = setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam")
		day, err := clock.BusinessDate(s.Clock.Now(), f.tz, setting.String(ctx, tx, "system.business_day_start", "00:00"))
		if err != nil {
			day = s.Clock.Now().UTC()
		}
		f.today = day.Format("2006-01-02")
		if _, err := tx.Exec(ctx, `SELECT set_config('TimeZone', $1, true)`, f.tz); err != nil {
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
	case int16:
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

// linesSQL is the sale-line ledger used by every sales report: completed sale lines dated
// on business_date, and completed return lines dated on r.business_date with quantities
// and money negated. Cost reduces only when returned goods were restocked.
func linesSQL(q *sqlq.Q, tenantID uuid.UUID, f Filter) string {
	t := q.Arg(tenantID)
	sc := "s.tenant_id = " + t + " AND s.status = 'completed'"
	rc := "r.tenant_id = " + t + " AND r.status = 'completed'"
	if f.From != "" {
		a := q.Arg(f.From)
		sc += " AND s.business_date >= " + a + "::date"
		rc += " AND r.business_date >= " + a + "::date"
	}
	if f.To != "" {
		a := q.Arg(f.To)
		sc += " AND s.business_date <= " + a + "::date"
		rc += " AND r.business_date <= " + a + "::date"
	}
	if f.Branches != nil {
		a := q.Arg(f.Branches)
		sc += " AND s.location_id = ANY(" + a + ")"
		rc += " AND r.location_id = ANY(" + a + ")"
	}
	return `(
		SELECT s.id AS sale_id, s.business_date AS d, s.location_id AS branch_id, sl.product_id, s.customer_id, s.sold_by,
		       sl.quantity AS qty, sl.quantity * sl.unit_price AS gross,
		       sl.line_discount + CASE WHEN s.subtotal > 0 THEN s.discount_amount * sl.line_total / s.subtotal ELSE 0 END AS disc,
		       CASE WHEN s.subtotal > 0 THEN sl.tax_amount * s.total / s.subtotal ELSE sl.tax_amount END AS tax,
		       sl.line_total - CASE WHEN s.subtotal > 0 THEN s.discount_amount * sl.line_total / s.subtotal ELSE 0 END AS total,
		       (sl.line_total - CASE WHEN s.subtotal > 0 THEN s.discount_amount * sl.line_total / s.subtotal ELSE 0 END)
		         - CASE WHEN s.subtotal > 0 THEN sl.tax_amount * s.total / s.subtotal ELSE sl.tax_amount END AS net,
		       sl.quantity * COALESCE(sl.unit_cost, 0) AS cost, 0::numeric AS ret
		FROM app.sales s
		JOIN app.sale_lines sl ON sl.tenant_id = s.tenant_id AND sl.sale_id = s.id
		WHERE ` + sc + `
		UNION ALL
		SELECT s.id, r.business_date, r.location_id, sl.product_id, s.customer_id, s.sold_by,
		       -rl.quantity, 0, 0,
		       -ROUND(sl.tax_amount * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2),
		       -ROUND(sl.line_total * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2),
		       -(ROUND(sl.line_total * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2)
		         - ROUND(sl.tax_amount * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2)),
		       -(CASE WHEN rl.restock THEN rl.quantity * COALESCE(sl.unit_cost, 0) ELSE 0 END),
		       ROUND(sl.line_total * (CASE WHEN s.subtotal > 0 THEN s.total / s.subtotal ELSE 1 END) * rl.quantity / sl.quantity, 2)
		FROM app.sale_returns r
		JOIN app.sale_return_lines rl ON rl.tenant_id = r.tenant_id AND rl.sale_return_id = r.id
		JOIN app.sale_lines sl ON sl.tenant_id = rl.tenant_id AND sl.id = rl.sale_line_id
		JOIN app.sales s ON s.tenant_id = r.tenant_id AND s.id = r.sale_id
		WHERE ` + rc + `
	) l`
}

// saleAgg is the common aggregate column list over linesSQL.
const saleAgg = `
	COUNT(DISTINCT l.sale_id) FILTER (WHERE l.ret = 0) AS sales_count,
	ROUND(COALESCE(SUM(l.qty), 0), 3) AS qty,
	ROUND(COALESCE(SUM(l.gross), 0), 2) AS gross_sales,
	ROUND(COALESCE(SUM(l.disc), 0), 2) AS discounts,
	ROUND(COALESCE(SUM(l.ret), 0), 2) AS returns,
	ROUND(COALESCE(SUM(l.tax), 0), 2) AS tax,
	ROUND(COALESCE(SUM(l.total), 0), 2) AS total,
	ROUND(COALESCE(SUM(l.net), 0), 2) AS net_sales,
	ROUND(COALESCE(SUM(l.cost), 0), 2) AS cost,
	ROUND(COALESCE(SUM(l.net), 0) - COALESCE(SUM(l.cost), 0), 2) AS gross_profit`

// canCost reports whether cost and profit may be shown to the caller.
func canCost(p *auth.Principal) bool {
	return p.Has("shop.product.view_cost") || p.Has("dashboard.business.view") || p.Has("reports.business.view")
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
