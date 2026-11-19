// Package setting reads tenant settings by their product key ("payroll.pay_day").
//
// Where the approved schema has a typed column (app.tenant_settings, app.tenants,
// app.document_sequences) the key is served from it; every other key lives in
// app.tenant_setting_values. Callers do not need to know which, so modules read settings the same
// way they always did. Row-level security limits every read to the caller's tenant.
package setting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"bms/internal/platform/db"
)

// Column is where a typed setting lives.
type Column struct {
	Table, Column string
}

// Typed maps a product key to its typed column. The map is fixed code, never request input, so the
// identifiers can be placed in SQL.
var Typed = map[string]Column{
	"tax.prices_include_tax":         {"app.tenant_settings", "prices_include_tax"},
	"system.allow_negative_stock":    {"app.tenant_settings", "allow_negative_stock"},
	"system.session_timeout_minutes": {"app.tenant_settings", "session_idle_minutes"},
	"system.default_low_stock":       {"app.tenant_settings", "low_stock_default"},
	"invoice.footer":                 {"app.tenant_settings", "invoice_footer"},
	"invoice.terms":                  {"app.tenant_settings", "quotation_terms"},
	"receipt.footer":                 {"app.tenant_settings", "receipt_footer"},
	"currency.code":                  {"app.tenant_settings", "currency"},
	"system.timezone":                {"app.tenants", "timezone"},
	"system.language":                {"app.tenants", "default_locale"},
}

// ErrNotSet means the tenant has no value for the key; callers fall back to their default.
var ErrNotSet = errors.New("setting not set")

func Raw(ctx context.Context, q db.Querier, key string) (json.RawMessage, error) {
	var raw []byte
	var err error
	switch {
	case key == "invoice.prefix":
		var prefix string
		err = q.QueryRow(ctx, `SELECT prefix FROM app.document_sequences WHERE tenant_id = app.current_tenant_id() AND doc_type = 'invoice'`).Scan(&prefix)
		if err == nil {
			raw, _ = json.Marshal(strings.TrimSuffix(prefix, "-"))
		}
	case Typed[key].Column != "":
		c := Typed[key]
		keyCol := "tenant_id"
		if c.Table == "app.tenants" {
			keyCol = "id"
		}
		// c comes from the fixed Typed map above.
		err = q.QueryRow(ctx, `SELECT to_jsonb(`+c.Column+`) FROM `+c.Table+` WHERE `+keyCol+` = app.current_tenant_id()`).Scan(&raw)
	default:
		err = q.QueryRow(ctx, `SELECT value FROM app.tenant_setting_values WHERE tenant_id = app.current_tenant_id() AND key = $1`, key).Scan(&raw)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotSet
	}
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, ErrNotSet
	}
	return raw, nil
}

// Defaults are what a tenant sees for a key it has never changed.
var Defaults = map[string]any{
	"attendance.restrict_clock_in": false, "attendance.allowed_networks": []string{},
	"currency.symbol": "TZS", "currency.decimals": 2, "currency.thousands_separator": ",",
	"invoice.due_days": 0, "invoice.quotation_valid_days": 14,
	"receipt.header": "", "receipt.show_logo": true, "receipt.paper_width": 80, "receipt.show_cashier": true, "receipt.show_tax": true,
	"payroll.pay_day": 25, "payroll.working_days": 26, "payroll.standard_hours": 8, "payroll.overtime_multiplier": 1.5,
	"payroll.late_grace_minutes": 10, "payroll.lateness_deducts": false,
	"system.date_format": "02 Jan 2006", "system.business_day_start": "00:00", "system.till_difference_threshold": 1000,
	"sales.discount_limit_percent":    0,
	"notifications.supplier_due_days": 7, "notifications.salary_due_days": 3,
	"payment.methods": map[string]any{
		"cash": map[string]any{"enabled": true}, "bank": map[string]any{"enabled": true},
		"mobile_money": map[string]any{"enabled": true}, "credit": map[string]any{"enabled": true},
	},
}

func Bool(ctx context.Context, q db.Querier, key string, def bool) bool {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

func String(ctx context.Context, q db.Querier, key, def string) string {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

func Int(ctx context.Context, q db.Querier, key string, def int) int {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return def
	}
	return int(f)
}

func Float(ctx context.Context, q db.Querier, key string, def float64) float64 {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

// Strings reads a JSON array of strings; a missing or malformed value is empty.
func Strings(ctx context.Context, q db.Querier, key string) []string {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return nil
	}
	var v []string
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

// All returns every setting grouped by prefix: defaults, then values stored in the key/value table,
// then typed columns, so the most specific source wins.
func All(ctx context.Context, q db.Querier) (map[string]map[string]any, error) {
	flat := map[string]any{}
	for k, v := range Defaults {
		flat[k] = v
	}
	rows, err := q.Query(ctx, `SELECT key, value FROM app.tenant_setting_values WHERE tenant_id = app.current_tenant_id()`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		flat[key] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	keys := []string{"invoice.prefix"}
	for k := range Typed {
		keys = append(keys, k)
	}
	for _, k := range keys {
		raw, err := Raw(ctx, q, k)
		if errors.Is(err, ErrNotSet) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		flat[k] = v
	}
	out := map[string]map[string]any{}
	for k, v := range flat {
		group, name, _ := strings.Cut(k, ".")
		if out[group] == nil {
			out[group] = map[string]any{}
		}
		out[group][name] = v
	}
	return out, nil
}
