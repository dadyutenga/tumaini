package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/sender"
)

func TestOutboundMessagingFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	prod := e.product(t, "Widget", "10000", "50", "5000")

	// Create shop customer (zero balance initially) and walk-in sale.
	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Juma Shop", "phone": "+255712345601", "email": "juma@example.com", "credit_limit": "500000",
	}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])

	walkin := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": cash, "amount": "10000"}},
	}, key())
	require.Equal(t, 201, walkin.code, walkin.raw)
	walkinSaleID := str(walkin.data["id"])

	// Credit sale so customer has a balance.
	credit := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"customer_id": custID,
		"items":       []map[string]any{{"product_id": prod, "qty": "2"}},
		"payments":    []map[string]any{},
	}, key())
	require.Equal(t, 201, credit.code, credit.raw)
	creditSaleID := str(credit.data["id"])

	// Service client, project, renewal, and design job.
	cl := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{
		"name": "Neema Ltd", "phone": "+255712345602", "email": "neema@example.com", "status": "active",
	}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	clientID := str(cl.data["id"])

	pr := o.call(t, http.MethodPost, "/api/v1/projects", map[string]any{
		"client_id": clientID, "name": "BRELA Filing", "total_price": "150000",
	}, "")
	require.Equal(t, 201, pr.code, pr.raw)
	projectID := str(pr.data["id"])

	ren := o.call(t, http.MethodPost, "/api/v1/renewals", map[string]any{
		"client_id": clientID, "item": "Business License", "due_date": "2026-12-01",
	}, "")
	require.Equal(t, 201, ren.code, ren.raw)
	renewalID := str(ren.data["id"])

	inv := o.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{
		"client_id": clientID, "project_id": projectID, "due_date": "2026-12-01",
		"lines": []map[string]any{{"description": "BRELA", "qty": "1", "unit_price": "150000"}},
	}, "")
	require.Equal(t, 201, inv.code, inv.raw)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/service-invoices/"+str(inv.data["id"])+"/issue", nil, "").code)

	// 1. With no channels configured, every outbound send returns 503 channel_unavailable and inserts 0 rows.
	r503 := o.call(t, http.MethodPost, "/api/v1/sales/"+creditSaleID+"/send-receipt", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 503, r503.code, r503.raw)
	require.Contains(t, r503.raw, "channel_unavailable")

	s503 := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/statement/send", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 503, s503.code, s503.raw)

	d503 := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/debt-reminder", map[string]any{"channel": "whatsapp"}, "")
	require.Equal(t, 503, d503.code, d503.raw)

	rn503 := o.call(t, http.MethodPost, "/api/v1/renewals/"+renewalID+"/remind", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 503, rn503.code, rn503.raw)

	u503 := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/updates", map[string]any{"message": "Stage 1 done", "channel": "whatsapp"}, "")
	require.Equal(t, 503, u503.code, u503.raw)

	t503 := o.call(t, http.MethodPost, "/api/v1/notifications/test", map[string]any{"channel": "sms", "recipient": "+255712345601"}, "")
	require.Equal(t, 503, t503.code, t503.raw)

	countOutbound := func() (total, sent int) {
		_ = e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "\n\t\t\t\tSELECT count(*), count(*) FILTER (WHERE status = 'sent')\n\t\t\t\tFROM app.outbound_messages WHERE true").Scan(&total, &sent)
		})
		return
	}
	tot, _ := countOutbound()
	require.Equal(t, 0, tot, "503 calls must not write outbound rows")

	// 2. Enable LogSender (equivalent to OUTBOUND_LOG=1) for all three channels.
	e.app.Outbound.Channels = sender.Channels{
		Email:    &sender.LogSender{Channel: "email"},
		SMS:      &sender.LogSender{Channel: "sms"},
		WhatsApp: &sender.LogSender{Channel: "whatsapp"},
	}

	// Walk-in sale receipt returns 400 no_recipient.
	walkErr := o.call(t, http.MethodPost, "/api/v1/sales/"+walkinSaleID+"/send-receipt", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 400, walkErr.code, walkErr.raw)
	require.Contains(t, walkErr.raw, "no_recipient")

	// Zero-balance customer debt reminder returns 409 nothing_due.
	zeroCust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Paid Up", "phone": "+255712345609",
	}, "")
	require.Equal(t, 201, zeroCust.code, zeroCust.raw)
	zeroErr := o.call(t, http.MethodPost, "/api/v1/customers/"+str(zeroCust.data["id"])+"/debt-reminder", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 409, zeroErr.code, zeroErr.raw)
	require.Contains(t, zeroErr.raw, "nothing_due")

	// Send receipt with idempotency key — second click returns same row id without creating a duplicate.
	idem := key()
	rc1 := o.call(t, http.MethodPost, "/api/v1/sales/"+creditSaleID+"/send-receipt", map[string]any{"channel": "sms"}, idem)
	require.Equal(t, 200, rc1.code, rc1.raw)
	require.Equal(t, "queued", str(rc1.data["status"]))
	rc2 := o.call(t, http.MethodPost, "/api/v1/sales/"+creditSaleID+"/send-receipt", map[string]any{"channel": "sms"}, idem)
	require.Equal(t, 200, rc2.code, rc2.raw)
	require.Equal(t, str(rc1.data["id"]), str(rc2.data["id"]))

	// Send customer statement, client statement, debt reminders, renewal reminder, and project update.
	cs := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/statement/send", map[string]any{"channel": "email"}, key())
	require.Equal(t, 200, cs.code, cs.raw)

	cls := o.call(t, http.MethodPost, "/api/v1/clients/"+clientID+"/statement/send", map[string]any{"channel": "whatsapp"}, key())
	require.Equal(t, 200, cls.code, cls.raw)

	dr1 := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/debt-reminder", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 200, dr1.code, dr1.raw)
	dr2 := o.call(t, http.MethodPost, "/api/v1/customers/"+custID+"/debt-reminder", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 200, dr2.code, dr2.raw)
	require.Equal(t, str(dr1.data["id"]), str(dr2.data["id"]), "one debt reminder per day per customer per channel")

	cdr := o.call(t, http.MethodPost, "/api/v1/clients/"+clientID+"/debt-reminder", map[string]any{"channel": "whatsapp"}, "")
	require.Equal(t, 200, cdr.code, cdr.raw)

	rn := o.call(t, http.MethodPost, "/api/v1/renewals/"+renewalID+"/remind", map[string]any{"channel": "sms"}, "")
	require.Equal(t, 200, rn.code, rn.raw)
	require.Equal(t, true, rn.data["queued"])

	up := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/updates", map[string]any{"message": "Ready for pickup", "channel": "whatsapp"}, "")
	require.Equal(t, 200, up.code, up.raw)
	require.Equal(t, true, up.data["queued"])

	// Test notification succeeds via configured sender without creating an outbound row.
	tOK := o.call(t, http.MethodPost, "/api/v1/notifications/test", map[string]any{"channel": "whatsapp", "recipient": "+255712345601", "message": "Hello"}, "")
	require.Equal(t, 200, tOK.code, tOK.raw)

	// Drain the background worker — all 7 queued rows must move to 'sent'.
	require.NoError(t, e.app.Jobs.RunPending(context.Background()))
	tot, sent := countOutbound()
	require.Equal(t, 7, tot)
	require.Equal(t, 7, sent)
}
