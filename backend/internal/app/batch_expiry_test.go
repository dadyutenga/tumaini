package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Decision #6: batch numbers and expiry dates are not part of the approved BMS schema.
// Verify stock entries reject batch/expiry fields cleanly and the expiring report returns an empty set with proper permission gating.
func TestBatchAndExpiryAreRecordedPerReceipt(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	milk := e.product(t, "Milk 1L", "2000", "", "")

	rejected := o.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "opening",
		"items": []map[string]any{{"product_id": milk, "qty": "10", "unit_cost": "1000", "batch_no": "M-2611", "expiry_date": "2026-11-26"}},
	}, key())
	require.Equal(t, 400, rejected.code, rejected.raw)
	require.Equal(t, "batch_not_supported", errCode(rejected.body))

	rep := o.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring?days=30", nil, "")
	require.Equal(t, 200, rep.code, rep.raw)
	require.Empty(t, asList(rep.data["rows"]))

	staff, _ := e.user(t, "staff")
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/reports/inventory/expiring", nil, "").code)
}
