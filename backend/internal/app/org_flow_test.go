package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func fieldsOf(body map[string]any) map[string]any {
	e, _ := body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	return f
}

func TestSettingsAreValidatedAndAudited(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	mgr, _ := e.user(t, "manager")

	// Only settings.manage may change them; viewing needs settings.view.
	require.Equal(t, 403, mgr.call(t, http.MethodPut, "/api/v1/settings/receipt", map[string]any{"footer": "x"}, "").code)
	require.Equal(t, 200, mgr.call(t, http.MethodGet, "/api/v1/settings", nil, "").code)

	// A change is stored, returned, and audited with old and new value (FR-SET-10).
	ok := o.call(t, http.MethodPut, "/api/v1/settings/receipt", map[string]any{"footer": "Karibu tena", "paper_width": 58}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	require.Equal(t, "Karibu tena", str(ok.data["receipt"].(map[string]any)["footer"]))
	logs := o.call(t, http.MethodGet, "/api/v1/audit-logs?action=settings.updated", nil, "")
	var found bool
	for _, row := range asList(logs.body["data"]) {
		after, _ := row["after"].(map[string]any)
		before, _ := row["before"].(map[string]any)
		if str(after["key"]) == "receipt.footer" {
			found = true
			require.Equal(t, "Thank you", str(before["value"]), "the old value is recorded")
			require.Equal(t, "Karibu tena", str(after["value"]))
		}
	}
	require.True(t, found, "settings.updated with before and after for receipt.footer")

	// Bad values are field errors and change nothing, even when mixed with good ones.
	bad := o.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"timezone": "Mars/Olympus", "language": "en"}, "")
	require.Equal(t, 422, bad.code, bad.raw)
	require.Contains(t, str(fieldsOf(bad.body)["timezone"]), "not a known timezone")
	for name, body := range map[string]map[string]any{
		"system":   {"allow_negative_stock": "yes", "business_day_start": "25:00", "session_timeout_minutes": 1, "language": "fr"},
		"payroll":  {"working_days": "twenty", "overtime_multiplier": 0.5, "pay_day": 32, "late_grace_minutes": 1.5},
		"receipt":  {"paper_width": 70, "show_logo": 1},
		"currency": {"code": "tz", "decimals": 9},
		"invoice":  {"due_days": -1, "unknown_key": 1},
	} {
		res := o.call(t, http.MethodPut, "/api/v1/settings/"+name, body, "")
		require.Equal(t, 422, res.code, name+": "+res.raw)
		require.GreaterOrEqual(t, len(fieldsOf(res.body)), len(body)-0, name+" reports every bad field: "+res.raw)
	}
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/settings/nonsense", map[string]any{"a": 1}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{}, "").code)
	after := o.call(t, http.MethodGet, "/api/v1/settings", nil, "")
	require.Equal(t, "Africa/Dar_es_Salaam", str(after.data["system"].(map[string]any)["timezone"]), "the rejected request changed nothing")
	require.Equal(t, 26, int(dec(after.data["payroll"].(map[string]any)["working_days"]).IntPart()))

	// A valid timezone and currency also update the business record and take effect.
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/system", map[string]any{"timezone": "Africa/Nairobi", "business_day_start": "04:00"}, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPut, "/api/v1/settings/currency", map[string]any{"code": "KES"}, "").code)
	biz := o.call(t, http.MethodGet, "/api/v1/business", nil, "")
	require.Equal(t, "Africa/Nairobi", str(biz.data["timezone"]))
	require.Equal(t, "KES", str(biz.data["currency"]))

	// Notification rules (FR-SET-08): which events use which channel.
	rules := o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": map[string]any{
		"low_stock": map[string]any{"email": true, "sms": false}, "export_ready": map[string]any{"email": true},
	}, "supplier_due_days": 5}, "")
	require.Equal(t, 200, rules.code, rules.raw)
	got := rules.data["notifications"].(map[string]any)["rules"].(map[string]any)
	require.Equal(t, true, got["low_stock"].(map[string]any)["email"])
	require.Equal(t, true, got["low_stock"].(map[string]any)["in_app"], "untouched channels keep their value")
	require.Equal(t, true, got["export_ready"].(map[string]any)["email"], "a type without a row yet is created")
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": map[string]any{"made_up": map[string]any{"email": true}}}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": map[string]any{"low_stock": map[string]any{"email": "yes"}}}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{"rules": map[string]any{"low_stock": map[string]any{"pigeon": true}}}, "").code)

	// Another business keeps its own settings.
	other := newEnv(t, defaultNow)
	require.Equal(t, "Thank you", str(other.owner.call(t, http.MethodGet, "/api/v1/settings", nil, "").data["receipt"].(map[string]any)["footer"]))
}

func TestBusinessProfileAndPaymentMethods(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	staff, _ := e.user(t, "staff")
	mgr, _ := e.user(t, "manager")

	upd := o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"legal_name": "Tumaini Traders Ltd", "tax_number": "TIN-123", "phone": "0700111222", "address": "Mtaa wa Uhuru"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "Tumaini Traders Ltd", str(upd.data["legal_name"]))
	renamed := o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"name": "Tumaini Shop"}, "")
	require.Equal(t, "Tumaini Traders Ltd", str(renamed.data["legal_name"]), "fields not sent are kept")
	require.Equal(t, 403, mgr.call(t, http.MethodPut, "/api/v1/business", map[string]any{"name": "Hacked"}, "").code)
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/business", nil, "").code)
	// The timezone must exist; an invalid one would stop every sale from being dated.
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"timezone": "Nowhere/Land"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"currency": "shillings"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"email": "not-an-email"}, "").code)
	tz := o.call(t, http.MethodPut, "/api/v1/business", map[string]any{"timezone": "Africa/Kampala"}, "")
	require.Equal(t, 200, tz.code, tz.raw)
	require.Equal(t, "Africa/Kampala", str(o.call(t, http.MethodGet, "/api/v1/settings", nil, "").data["system"].(map[string]any)["timezone"]), "the business row and the settings agree")

	// Payment methods map to money accounts of a matching type.
	cash := e.cashAccount(t)
	bank := o.call(t, http.MethodPost, "/api/v1/money-accounts", map[string]any{"name": "CRDB", "type": "bank", "opening_balance": "0"}, "")
	require.Equal(t, 201, bank.code, bank.raw)
	ok := o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"cash": map[string]any{"money_account_id": cash}, "bank": map[string]any{"enabled": true, "money_account_id": str(bank.data["id"])}}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	methods := o.call(t, http.MethodGet, "/api/v1/payment-methods", nil, "")
	require.Equal(t, cash, str(methods.data["cash"].(map[string]any)["money_account_id"]))
	require.Equal(t, true, methods.data["mobile_money"].(map[string]any)["enabled"], "methods not in the body keep their settings")
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"cash": map[string]any{"money_account_id": str(bank.data["id"])}}, "").code, "a bank account cannot take cash")
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"bitcoin": map[string]any{"enabled": true}}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"credit": map[string]any{"money_account_id": cash}}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"bank": map[string]any{"enabled": "yes"}}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"bank": map[string]any{"money_account_id": key()}}, "").code)
	require.Equal(t, 400, o.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{}, "").code)
	require.Equal(t, 403, mgr.call(t, http.MethodPut, "/api/v1/payment-methods", map[string]any{"cash": map[string]any{"enabled": false}}, "").code)
}

func TestTaxRatesKeepOneDefaultAndUpdateInPlace(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	rates := asList(o.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "").body["data"])
	vat := find(rates, "name", "VAT")
	exempt := find(rates, "name", "Exempt")
	require.Equal(t, true, exempt["is_default"])

	// Validation.
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "", "rate": "5"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "Neg", "rate": "-1"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "Huge", "rate": "150"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "No rate"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "VAT", "rate": "18"}, "").code)

	// Renaming must not reset the percentage, the default flag or the active flag.
	ren := o.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(vat["id"]), map[string]any{"name": "VAT 18%"}, "")
	require.Equal(t, 200, ren.code, ren.raw)
	require.Equal(t, "18.0000", str(ren.data["rate"]))
	require.Equal(t, true, ren.data["is_active"])
	require.Equal(t, false, ren.data["is_default"])
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/tax-rates/"+key(), map[string]any{"name": "Ghost"}, "").code)

	// Exactly one default: making VAT the default moves the flag; the default cannot just be unset or switched off.
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(exempt["id"]), map[string]any{"is_default": false}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(exempt["id"]), map[string]any{"is_active": false}, "").code)
	moved := o.call(t, http.MethodPut, "/api/v1/tax-rates/"+str(vat["id"]), map[string]any{"is_default": true}, "")
	require.Equal(t, 200, moved.code, moved.raw)
	defaults := 0
	for _, r := range asList(o.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "").body["data"]) {
		if r["is_default"] == true {
			defaults++
		}
	}
	require.Equal(t, 1, defaults)
	// A new product picks up the default rate; the new rate applies to the next sale.
	p := o.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Taxed", "sell_price": "1180"}, "")
	require.Equal(t, 201, p.code, p.raw)
	require.Equal(t, str(vat["id"]), str(p.data["tax_rate_id"]))
	created := o.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "Reduced", "rate": "8", "is_default": true}, "")
	require.Equal(t, 201, created.code, created.raw)
	require.Equal(t, true, created.data["is_default"])
	var defaultNames []string
	for _, r := range asList(o.call(t, http.MethodGet, "/api/v1/tax-rates", nil, "").body["data"]) {
		if r["is_default"] == true {
			defaultNames = append(defaultNames, str(r["name"]))
		}
	}
	require.Equal(t, []string{"Reduced"}, defaultNames, "creating a default rate moves the flag")
	staff, _ := e.user(t, "staff")
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/tax-rates", map[string]any{"name": "Nope", "rate": "1"}, "").code)
}

func TestBranchesRulesAndScope(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	b := o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "  Mwanza  ", "code": "mwz", "phone": "0700"}, "")
	require.Equal(t, 201, b.code, b.raw)
	require.Equal(t, "Mwanza", str(b.data["name"]))
	require.Equal(t, "MWZ", str(b.data["code"]))
	bID := str(b.data["id"])

	// Validation: name and a plain short code, a unique name and code, a real manager.
	require.Equal(t, 400, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "", "code": "X"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Bad code", "code": "A-B/1"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Too long", "code": "ABCDEFGHIJ"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Other", "code": "MWZ"}, "").code)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Mwanza", "code": "MW2"}, "").code)
	require.Equal(t, 422, o.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Ghost mgr", "code": "GM", "manager_id": key()}, "").code)

	// A manager of the main branch cannot see, edit or close a branch they are not in; the Owner can.
	mgr, _ := e.user(t, "manager")
	require.Equal(t, 404, mgr.call(t, http.MethodGet, "/api/v1/branches/"+bID, nil, "").code)
	require.Equal(t, 404, mgr.call(t, http.MethodPut, "/api/v1/branches/"+bID, map[string]any{"name": "Hijacked"}, "").code)
	require.Equal(t, 404, mgr.call(t, http.MethodPost, "/api/v1/branches/"+bID+"/deactivate", nil, "").code)
	require.Equal(t, 404, mgr.call(t, http.MethodGet, "/api/v1/branches/"+bID+"/staff", nil, "").code)
	upd := o.call(t, http.MethodPut, "/api/v1/branches/"+bID, map[string]any{"address": "Rock City"}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	require.Equal(t, "Mwanza", str(upd.data["name"]), "fields not sent are kept")
	require.Equal(t, 404, o.call(t, http.MethodPut, "/api/v1/branches/"+key(), map[string]any{"name": "Ghost"}, "").code)

	// The main branch stays; an unknown id is a 404, not a misleading conflict.
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/branches/"+e.branch+"/deactivate", nil, "").code)
	require.Equal(t, 404, o.call(t, http.MethodPost, "/api/v1/branches/"+key()+"/deactivate", nil, "").code)

	// A branch with stock cannot be closed; an open draft transfer blocks it too; an empty one can.
	ob := &client{app: e.app.Fiber, token: o.token, branch: bID}
	pid := str(ob.call(t, http.MethodPost, "/api/v1/products", map[string]any{"name": "Stocked", "sell_price": "10"}, "").data["id"])
	in := ob.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "in", "reason": "opening", "items": []map[string]any{{"product_id": pid, "qty": "2", "unit_cost": "5"}}}, key())
	require.Equal(t, 201, in.code, in.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/branches/"+bID+"/deactivate", nil, "").code)
	out := ob.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "out", "reason": "damaged", "items": []map[string]any{{"product_id": pid, "qty": "2"}}}, key())
	require.Equal(t, 201, out.code, out.raw)
	draft := ob.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{"to_branch_id": e.branch, "items": []map[string]any{{"product_id": pid, "qty": "1"}}}, "")
	if draft.code == 201 {
		blocked := o.call(t, http.MethodPost, "/api/v1/branches/"+bID+"/deactivate", nil, "")
		require.Equal(t, 409, blocked.code, "a draft transfer is an open document: "+blocked.raw)
		require.Equal(t, 200, ob.call(t, http.MethodPost, "/api/v1/stock-transfers/"+str(draft.data["id"])+"/cancel", nil, "").code)
	}
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/branches/"+bID+"/deactivate", nil, "").code)
	list := o.call(t, http.MethodGet, "/api/v1/branches", nil, "")
	require.Equal(t, false, find(asList(list.body["data"]), "id", bID)["is_active"])
}

// DEFAULT_TIMEZONE and DEFAULT_CURRENCY decide what a new business starts with.
func TestNewBusinessesGetTheConfiguredDefaults(t *testing.T) {
	t.Setenv("DEFAULT_TIMEZONE", "Africa/Nairobi")
	t.Setenv("DEFAULT_CURRENCY", "KES")
	e := newEnv(t, defaultNow)
	biz := e.owner.call(t, http.MethodGet, "/api/v1/business", nil, "")
	require.Equal(t, "Africa/Nairobi", str(biz.data["timezone"]))
	require.Equal(t, "KES", str(biz.data["currency"]))
	settings := e.owner.call(t, http.MethodGet, "/api/v1/settings", nil, "")
	require.Equal(t, "Africa/Nairobi", str(settings.data["system"].(map[string]any)["timezone"]))
	require.Equal(t, "KES", str(settings.data["currency"].(map[string]any)["code"]))
}
