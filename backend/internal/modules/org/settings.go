package org

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
)

// A settings value that is the wrong type, or a timezone that does not exist,
// would break every sale, attendance record and scheduled job of the business.
// So each key has a checker, and unknown keys are refused.

type check func(v any) (any, error)

func isBool(v any) (any, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("must be true or false")
	}
	return b, nil
}

func text(max int) check {
	return func(v any) (any, error) {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("must be text")
		}
		if len(s) > max {
			return nil, fmt.Errorf("must be at most %d characters", max)
		}
		return strings.TrimSpace(s), nil
	}
}

func whole(min, max int) check {
	return func(v any) (any, error) {
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return nil, fmt.Errorf("must be a whole number")
		}
		if int(f) < min || int(f) > max {
			return nil, fmt.Errorf("must be between %d and %d", min, max)
		}
		return int(f), nil
	}
}

func number(min, max float64) check {
	return func(v any) (any, error) {
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("must be a number")
		}
		if f < min || f > max {
			return nil, fmt.Errorf("must be between %g and %g", min, max)
		}
		return f, nil
	}
}

func oneOf(options ...any) check {
	return func(v any) (any, error) {
		for _, o := range options {
			// JSON numbers arrive as float64, so integer options are compared numerically.
			if n, isInt := o.(int); isInt {
				if f, ok := v.(float64); ok && f == float64(n) {
					return n, nil
				}
				continue
			}
			if o == v {
				return o, nil
			}
		}
		return nil, fmt.Errorf("must be one of %v", options)
	}
}

var invoicePrefix = regexp.MustCompile(`^[A-Z]{1,6}$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
var clockTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func pattern(re *regexp.Regexp, msg string) check {
	return func(v any) (any, error) {
		s, ok := v.(string)
		if !ok || !re.MatchString(s) {
			return nil, fmt.Errorf("%s", msg)
		}
		return s, nil
	}
}

func timezone(v any) (any, error) {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil, fmt.Errorf("must be a timezone name such as Africa/Dar_es_Salaam")
	}
	if _, err := time.LoadLocation(s); err != nil {
		return nil, fmt.Errorf("%q is not a known timezone", s)
	}
	return s, nil
}

// networks accepts up to 20 IP addresses or CIDR ranges and stores them normalised.
func networks(v any) (any, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("must be a list of IP addresses or CIDR ranges")
	}
	if len(list) > 20 {
		return nil, fmt.Errorf("at most 20 entries")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("every entry must be text")
		}
		s = strings.TrimSpace(s)
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n.String())
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", s)
		}
		out = append(out, ip.String())
	}
	return out, nil
}

var settingsSpec = map[string]map[string]check{
	// FR-ATT-12: with restrict_clock_in on and at least one network listed, staff can only
	// clock themselves in from those addresses.
	"attendance": {"restrict_clock_in": isBool, "allowed_networks": networks},
	"tax":        {"prices_include_tax": isBool},
	"currency": {
		"code": pattern(currencyCode, "must be a three-letter code such as TZS"), "symbol": text(8),
		"decimals": whole(0, 4), "thousands_separator": oneOf(",", ".", " ", ""),
	},
	"invoice": {"prefix": pattern(invoicePrefix, "use 1 to 6 capital letters"), "terms": text(500), "footer": text(500), "due_days": whole(0, 365), "quotation_valid_days": whole(1, 365)},
	"receipt": {"header": text(200), "footer": text(200), "show_logo": isBool, "paper_width": oneOf(58, 80), "show_cashier": isBool, "show_tax": isBool},
	"payroll": {
		"pay_day": whole(1, 31), "working_days": whole(1, 31), "standard_hours": number(1, 24), "overtime_multiplier": number(1, 5),
		"late_grace_minutes": whole(0, 240), "lateness_deducts": isBool,
	},
	"system": {
		"timezone": timezone, "date_format": text(30), "language": oneOf("en", "sw"), "allow_negative_stock": isBool,
		"business_day_start": pattern(clockTime, "must be a time such as 00:00 or 04:30"), "session_timeout_minutes": whole(5, 240),
		"till_difference_threshold": number(0, 1e12), "default_low_stock": whole(0, 100000),
	},
	"sales": {"discount_limit_percent": number(0, 100)},
	"notifications": {
		"supplier_due_days": whole(0, 90), "salary_due_days": whole(0, 30),
		"sms_template_receipt": text(500), "sms_template_debt": text(500),
		"sms_template_renewal": text(500), "whatsapp_template_update": text(500),
	},
}

// NotificationTypes are the events whose channels an admin can configure: the values of the
// app.notification_type enum.
var NotificationTypes = []string{
	"low_stock", "debt_due", "task_overdue", "task_assigned", "renewal_due", "salary_due",
	"approval_waiting", "approval_decided", "design_brief_assigned", "design_feedback",
	"design_deadline", "design_approved", "print_ready", "account_locked", "day_closed",
	"project_stage_changed", "payment_received",
}

func invalid(fields map[string]string) error {
	return &httpx.Error{Status: fiber.StatusUnprocessableEntity, Code: "validation_failed", Message: "Check the highlighted fields", Fields: fields}
}

// Settings returns every setting grouped, with the notification rules under notifications.rules.
func (s *Service) Settings(ctx context.Context, p *auth.Principal) (map[string]any, error) {
	out := map[string]any{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		grouped, err := setting.All(ctx, tx)
		if err != nil {
			return err
		}
		rules, err := s.rules(ctx, tx)
		if err != nil {
			return err
		}
		if grouped["notifications"] == nil {
			grouped["notifications"] = map[string]any{}
		}
		grouped["notifications"]["rules"] = rules
		for k, v := range grouped {
			out[k] = v
		}
		return nil
	})
	return out, err
}

func (s *Service) rules(ctx context.Context, tx pgx.Tx) (map[string]any, error) {
	out := map[string]any{}
	for _, t := range NotificationTypes {
		out[t] = map[string]any{"enabled": true, "in_app": true, "email": false, "sms": false, "whatsapp": false, "days_before": nil}
	}
	rows, err := tx.Query(ctx, `SELECT notification_type::text, enabled, in_app, email, sms, whatsapp FROM app.notification_rules`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var typ string
		var enabled, inApp, email, sms, wa bool
		if err := rows.Scan(&typ, &enabled, &inApp, &email, &sms, &wa); err != nil {
			rows.Close()
			return nil, err
		}
		out[typ] = map[string]any{"enabled": enabled, "in_app": inApp, "email": email, "sms": sms, "whatsapp": wa, "days_before": nil}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range NotificationTypes {
		if raw, err := setting.Raw(ctx, tx, "notifications.days_before_"+t); err == nil {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				out[t].(map[string]any)["days_before"] = n
			}
		}
	}
	return out, nil
}

// writeSetting stores one validated value where it belongs: a typed column, the invoice numbering
// prefix, or the key/value table.
func (s *Service) writeSetting(ctx context.Context, tx pgx.Tx, p *auth.Principal, key string, val any) error {
	switch {
	case key == "invoice.prefix":
		_, err := tx.Exec(ctx, `UPDATE app.document_sequences SET prefix = $2 WHERE tenant_id = $1 AND doc_type = 'invoice'`, p.TenantID, fmt.Sprint(val)+"-")
		return err
	case setting.Typed[key].Column != "":
		c := setting.Typed[key]
		keyCol := "tenant_id"
		if c.Table == "app.tenants" {
			keyCol = "id"
		}
		// c comes from the fixed setting.Typed map, never from the request.
		if _, err := tx.Exec(ctx, `UPDATE `+c.Table+` SET `+c.Column+` = $2 WHERE `+keyCol+` = $1`, p.TenantID, val); err != nil {
			return err
		}
		if key == "currency.code" {
			_, err := tx.Exec(ctx, `UPDATE app.tenants SET default_currency = $2 WHERE id = $1`, p.TenantID, val)
			return err
		}
		return nil
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app.tenant_setting_values (tenant_id, key, value, updated_by) VALUES ($1,$2,$3,$4)
		ON CONFLICT (tenant_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		p.TenantID, key, raw, p.MembershipID)
	return err
}

// UpdateSettings validates every value before writing any, so a bad request changes nothing.
func (s *Service) UpdateSettings(ctx context.Context, p *auth.Principal, group string, body map[string]any) error {
	spec, ok := settingsSpec[group]
	if !ok {
		return httpx.BadRequest("invalid_body", "Unknown settings group")
	}
	if len(body) == 0 {
		return httpx.BadRequest("invalid_body", "Send at least one setting")
	}
	fields := map[string]string{}
	clean := map[string]any{}
	var rules map[string]map[string]any
	for name, val := range body {
		if group == "notifications" && name == "rules" {
			r, err := parseRules(val)
			if err != nil {
				fields["rules"] = err.Error()
				continue
			}
			rules = r
			continue
		}
		fn, ok := spec[name]
		if !ok {
			fields[name] = "unknown setting"
			continue
		}
		v, err := fn(val)
		if err != nil {
			fields[name] = err.Error()
			continue
		}
		clean[name] = v
	}
	if len(fields) > 0 {
		return invalid(fields)
	}
	names := make([]string, 0, len(clean))
	for n := range clean {
		names = append(names, n)
	}
	sort.Strings(names)
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		for _, name := range names {
			val := clean[name]
			key := group + "." + name
			var before any
			if old, err := setting.Raw(ctx, tx, key); err == nil {
				before = json.RawMessage(old)
			}
			if err := s.writeSetting(ctx, tx, p, key, val); err != nil {
				return err
			}
			// Every change is logged with its old and new value.
			if err := s.Audit.Record(ctx, tx, p, "settings.updated", "settings", p.TenantID,
				map[string]any{"key": key, "value": before}, map[string]any{"key": key, "value": val}); err != nil {
				return err
			}
		}
		if len(rules) > 0 {
			return s.saveRules(ctx, tx, p, rules)
		}
		return nil
	})
}

func parseRules(v any) (map[string]map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return nil, fmt.Errorf("must be an object keyed by event type")
	}
	known := map[string]bool{}
	for _, t := range NotificationTypes {
		known[t] = true
	}
	out := map[string]map[string]any{}
	for typ, raw := range m {
		if !known[typ] {
			return nil, fmt.Errorf("%q is not a notification type", typ)
		}
		rule, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", typ)
		}
		clean := map[string]any{}
		for k, val := range rule {
			switch k {
			case "enabled", "in_app", "email", "sms", "whatsapp":
				b, err := isBool(val)
				if err != nil {
					return nil, fmt.Errorf("%s.%s %v", typ, k, err)
				}
				clean[k] = b
			case "days_before":
				if val == nil {
					clean[k] = nil
					continue
				}
				n, err := whole(0, 90)(val)
				if err != nil {
					return nil, fmt.Errorf("%s.days_before %v", typ, err)
				}
				clean[k] = n
			default:
				return nil, fmt.Errorf("%s.%s is not a rule setting", typ, k)
			}
		}
		out[typ] = clean
	}
	return out, nil
}

func (s *Service) saveRules(ctx context.Context, tx pgx.Tx, p *auth.Principal, rules map[string]map[string]any) error {
	types := make([]string, 0, len(rules))
	for t := range rules {
		types = append(types, t)
	}
	sort.Strings(types)
	oldRules, err := s.rules(ctx, tx)
	if err != nil {
		return err
	}
	for _, typ := range types {
		r := rules[typ]
		if _, err := tx.Exec(ctx, `INSERT INTO app.notification_rules (tenant_id, notification_type) VALUES ($1,$2::app.notification_type) ON CONFLICT DO NOTHING`, p.TenantID, typ); err != nil {
			return err
		}
		for _, col := range []string{"enabled", "in_app", "email", "sms", "whatsapp"} {
			if v, ok := r[col]; ok {
				// col comes from the fixed list above, never from the request.
				if _, err := tx.Exec(ctx, `UPDATE app.notification_rules SET `+col+` = $3, updated_at = now(), updated_by = $4 WHERE tenant_id = $1 AND notification_type = $2::app.notification_type`,
					p.TenantID, typ, v, p.MembershipID); err != nil {
					return err
				}
			}
		}
		if v, set := r["days_before"]; set {
			key := "notifications.days_before_" + typ
			if v == nil {
				if _, err := tx.Exec(ctx, `DELETE FROM app.tenant_setting_values WHERE tenant_id = $1 AND key = $2`, p.TenantID, key); err != nil {
					return err
				}
			} else if err := s.writeSetting(ctx, tx, p, key, v); err != nil {
				return err
			}
		}
	}
	return s.Audit.Record(ctx, tx, p, "settings.updated", "settings", p.TenantID, map[string]any{"key": "notifications.rules", "value": oldRules}, map[string]any{"key": "notifications.rules", "value": rules})
}
