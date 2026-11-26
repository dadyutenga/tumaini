package org

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/setting"
)

// TaxIn is a create or update body. On update every field is optional, so renaming
// a rate cannot reset its percentage or its default flag.
type TaxIn struct {
	Name      *string          `json:"name"`
	Rate      *decimal.Decimal `json:"rate"`
	IsDefault *bool            `json:"is_default"`
	IsActive  *bool            `json:"is_active"`
}

var hundred = decimal.NewFromInt(100)

func (s *Service) SaveTax(ctx context.Context, p *auth.Principal, id *uuid.UUID, in TaxIn) (TaxRate, error) {
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	fields := map[string]string{}
	if (id == nil || in.Name != nil) && name == "" {
		fields["name"] = "required"
	}
	if id == nil && in.Rate == nil {
		fields["rate"] = "required"
	}
	if in.Rate != nil && (in.Rate.IsNegative() || in.Rate.GreaterThan(hundred)) {
		fields["rate"] = "must be between 0 and 100"
	}
	if len(fields) > 0 {
		return TaxRate{}, invalid(fields)
	}
	var outID uuid.UUID
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		if id == nil {
			outID = uuid.New()
			wantDefault := in.IsDefault != nil && *in.IsDefault
			if wantDefault {
				if _, err := tx.Exec(ctx, `UPDATE app.tax_rates SET is_default=false WHERE tenant_id=$1 AND is_default`, p.TenantID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.tax_rates (id, tenant_id, name, rate_pct, is_default) VALUES ($1,$2,$3,$4,$5)`,
				outID, p.TenantID, name, *in.Rate, wantDefault); err != nil {
				if db.IsUnique(err) {
					return httpx.Conflict("duplicate", "A tax rate with that name already exists")
				}
				return err
			}
			return s.Audit.Record(ctx, tx, p, "tax_rate.created", "tax_rate", outID, nil, map[string]any{"name": name, "rate": in.Rate, "is_default": wantDefault})
		}
		outID = *id
		var wasDefault, wasActive bool
		var beforeName string
		var beforeRate decimal.Decimal
		if err := tx.QueryRow(ctx, `SELECT name::text, rate_pct, is_default, status = 'active' FROM app.tax_rates WHERE id=$1 FOR UPDATE`, outID).Scan(&beforeName, &beforeRate, &wasDefault, &wasActive); err != nil {
			return httpx.NotFound("not_found", "Tax rate not found")
		}
		active := wasActive
		if in.IsActive != nil {
			active = *in.IsActive
		}
		isDefault := wasDefault
		if in.IsDefault != nil {
			isDefault = *in.IsDefault
		}
		if wasDefault && !isDefault {
			return httpx.Conflict("invalid_status", "This is the default rate. Make another rate the default instead.")
		}
		if isDefault && !active {
			return httpx.Conflict("invalid_status", "The default tax rate must stay active")
		}
		if isDefault && !wasDefault {
			if _, err := tx.Exec(ctx, `UPDATE app.tax_rates SET is_default=false WHERE tenant_id=$1 AND is_default`, p.TenantID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.tax_rates SET name=COALESCE(NULLIF($2,''), name), rate_pct=COALESCE($3, rate_pct), is_default=$4,
			       status=CASE WHEN $5 THEN 'active' ELSE 'inactive' END::app.record_status WHERE id=$1`,
			outID, name, in.Rate, isDefault, active); err != nil {
			if db.IsUnique(err) {
				return httpx.Conflict("duplicate", "A tax rate with that name already exists")
			}
			return err
		}
		return s.Audit.Record(ctx, tx, p, "tax_rate.updated", "tax_rate", outID,
			map[string]any{"name": beforeName, "rate": beforeRate, "is_default": wasDefault, "is_active": wasActive}, in)
	})
	if err != nil {
		return TaxRate{}, err
	}
	var t TaxRate
	err = s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name::text, rate_pct, is_default, status = 'active' FROM app.tax_rates WHERE id=$1`, outID).
			Scan(&t.ID, &t.Name, &t.Rate, &t.IsDefault, &t.IsActive)
	})
	return t, err
}

// paymentMethods are the methods a business can switch on, and the money account types each may map to.
var paymentMethods = map[string][]string{
	"cash":         {"cash", "petty_cash"},
	"bank":         {"bank"},
	"mobile_money": {"mobile_money"},
	"credit":       nil, // credit is not paid into an account
}

// UpdatePaymentMethods enables or disables methods and maps each to a money account (FR-SET-04).
// Only the methods in the body change; the rest keep their settings.
func (s *Service) UpdatePaymentMethods(ctx context.Context, p *auth.Principal, body any) error {
	in, ok := body.(map[string]any)
	if !ok || len(in) == 0 {
		return httpx.BadRequest("invalid_body", "Send an object keyed by payment method")
	}
	fields := map[string]string{}
	type change struct {
		enabled *bool
		account *uuid.UUID
		clear   bool
	}
	changes := map[string]change{}
	for method, raw := range in {
		types, known := paymentMethods[method]
		if !known {
			fields[method] = "unknown payment method"
			continue
		}
		cfg, ok := raw.(map[string]any)
		if !ok {
			fields[method] = "must be an object"
			continue
		}
		var c change
		for k, v := range cfg {
			switch k {
			case "enabled":
				b, isBool := v.(bool)
				if !isBool {
					fields[method+".enabled"] = "must be true or false"
					continue
				}
				c.enabled = &b
			case "money_account_id":
				if types == nil {
					fields[method+".money_account_id"] = "credit does not use a money account"
					continue
				}
				if v == nil {
					c.clear = true
					continue
				}
				str, isStr := v.(string)
				id, err := uuid.Parse(str)
				if !isStr || err != nil {
					fields[method+".money_account_id"] = "must be a UUID"
					continue
				}
				c.account = &id
			default:
				fields[method+"."+k] = "unknown setting"
			}
		}
		changes[method] = c
	}
	if len(fields) > 0 {
		return invalid(fields)
	}
	return s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		cur := map[string]any{}
		if def, err := json.Marshal(setting.Defaults["payment.methods"]); err == nil {
			_ = json.Unmarshal(def, &cur)
		}
		if raw, err := setting.Raw(ctx, tx, "payment.methods"); err == nil {
			_ = json.Unmarshal(raw, &cur)
		}
		before, _ := json.Marshal(cur)
		for method, c := range changes {
			entry, _ := cur[method].(map[string]any)
			if entry == nil {
				entry = map[string]any{"enabled": true}
			}
			if c.enabled != nil {
				entry["enabled"] = *c.enabled
			}
			if c.clear {
				delete(entry, "money_account_id")
			}
			if c.account != nil {
				var typ string
				var active bool
				if err := tx.QueryRow(ctx, `SELECT kind::text, status = 'active' FROM app.financial_accounts WHERE id=$1`, *c.account).Scan(&typ, &active); err != nil {
					return invalid(map[string]string{method + ".money_account_id": "account not found"})
				}
				if !active {
					return invalid(map[string]string{method + ".money_account_id": "account is inactive"})
				}
				allowed := false
				for _, t := range paymentMethods[method] {
					allowed = allowed || t == typ
				}
				if !allowed {
					return invalid(map[string]string{method + ".money_account_id": "a " + typ + " account cannot be used for " + method})
				}
				entry["money_account_id"] = c.account.String()
			}
			cur[method] = entry
		}
		out, err := json.Marshal(cur)
		if err != nil {
			return err
		}
		if err := s.writeSetting(ctx, tx, p, "payment.methods", json.RawMessage(out)); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "payment_methods.updated", "settings", p.TenantID, json.RawMessage(before), json.RawMessage(out))
	})
}
