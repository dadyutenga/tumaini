package app_test

import (
	"net/http"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func invD(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestInventoryFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	c := e.owner

	// Weighted average: 10 at 1,000 then 10 at 2,000 gives 1,500 (BR-05).
	rice := e.product(t, "Rice", "2500", "10", "1000")
	in2 := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "found", "items": []map[string]any{{"product_id": rice, "qty": "10", "unit_cost": "2000"}},
	}, key())
	require.Equal(t, 201, in2.code, in2.raw)

	bad := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "theft", "items": []map[string]any{{"product_id": rice, "qty": "1", "unit_cost": "1"}},
	}, key())
	require.Equal(t, 400, bad.code, bad.raw)
	invalid := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{"direction": "in", "reason": "found", "items": []map[string]any{}}, key())
	require.Equal(t, 422, invalid.code, invalid.raw)

	// Stock out leaves at average cost; internal use posts to an expense account.
	out := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "damaged", "items": []map[string]any{{"product_id": rice, "qty": "2"}},
	}, key())
	require.Equal(t, 201, out.code, out.raw)
	require.True(t, dec(out.data["total_value"]).Equal(invD("3000")), out.raw)
	use := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "internal_use", "items": []map[string]any{{"product_id": rice, "qty": "1"}},
	}, key())
	require.Equal(t, 201, use.code, use.raw)
	over := c.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "out", "reason": "theft", "items": []map[string]any{{"product_id": rice, "qty": "500"}},
	}, key())
	require.Equal(t, 409, over.code, over.raw)

	entries := c.call(t, http.MethodGet, "/api/v1/stock-entries?direction=out", nil, "")
	require.Equal(t, 200, entries.code, entries.raw)
	require.Len(t, asList(entries.body["data"]), 2)
	require.EqualValues(t, 2, entries.body["meta"].(map[string]any)["total"])
	entry := c.call(t, http.MethodGet, "/api/v1/stock-entries/"+str(out.data["id"]), nil, "")
	require.Equal(t, 200, entry.code, entry.raw)
	require.Len(t, asList(entry.data["items"]), 1)

	moves := c.call(t, http.MethodGet, "/api/v1/stock/movements?product_id="+rice, nil, "")
	require.Equal(t, 200, moves.code, moves.raw)
	require.Len(t, asList(moves.body["data"]), 4)
	card := c.call(t, http.MethodGet, "/api/v1/products/"+rice+"/stock-card", nil, "")
	require.Equal(t, 200, card.code, card.raw)
	rows := asList(card.body["data"])
	require.Len(t, rows, 4)
	require.True(t, dec(rows[3]["qty_after"]).Equal(invD("17")))

	val := c.call(t, http.MethodGet, "/api/v1/stock/valuation?as_of=2099-12-31", nil, "")
	require.Equal(t, 200, val.code, val.raw)
	require.True(t, dec(val.data["total"]).Equal(invD("25500")), val.raw) // 17 x 1,500
	past := c.call(t, http.MethodGet, "/api/v1/stock/valuation?as_of=2026-01-01", nil, "")
	require.Equal(t, 200, past.code, past.raw)
	require.True(t, dec(past.data["total"]).IsZero(), past.raw)

	// Adjustment above the Manager's 1,000,000 limit waits for approval (FR-INV-17).
	mgr, _ := e.user(t, "manager")
	gold := e.product(t, "Gold", "5000", "1000", "2000")
	adj := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Count", "items": []map[string]any{{"product_id": gold, "qty_counted": "0"}},
	}, "")
	require.Equal(t, 201, adj.code, adj.raw)
	posted := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+str(adj.data["id"])+"/post", nil, key())
	require.Equal(t, 202, posted.code, posted.raw)
	approve := c.call(t, http.MethodPost, "/api/v1/approvals/"+str(posted.data["approval_request_id"])+"/approve", map[string]any{"note": "ok"}, key())
	require.Equal(t, 200, approve.code, approve.raw)
	adjDetail := c.call(t, http.MethodGet, "/api/v1/stock-adjustments/"+str(adj.data["id"]), nil, "")
	require.Equal(t, "posted", str(adjDetail.data["status"]), adjDetail.raw)
	levels := c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	require.True(t, dec(find(asList(levels.body["data"]), "product_id", gold)["qty_on_hand"]).IsZero())

	// Within the limit, a draft can be edited, then posted at once.
	small := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{
		"reason": "Recount", "items": []map[string]any{{"product_id": rice, "qty_counted": "16"}},
	}, "")
	require.Equal(t, 201, small.code, small.raw)
	edited := mgr.call(t, http.MethodPut, "/api/v1/stock-adjustments/"+str(small.data["id"]), map[string]any{
		"reason": "Recount", "items": []map[string]any{{"product_id": rice, "qty_counted": "18"}},
	}, "")
	require.Equal(t, 200, edited.code, edited.raw)
	require.True(t, dec(edited.data["total_value"]).Equal(invD("1500")), edited.raw)
	ok := mgr.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+str(small.data["id"])+"/post", nil, key())
	require.Equal(t, 200, ok.code, ok.raw)
	again := mgr.call(t, http.MethodPut, "/api/v1/stock-adjustments/"+str(small.data["id"]), map[string]any{
		"reason": "x", "items": []map[string]any{{"product_id": rice, "qty_counted": "1"}},
	}, "")
	require.Equal(t, 409, again.code, again.raw)

	sheet := c.call(t, http.MethodPost, "/api/v1/stock-adjustments/count-sheet", map[string]any{}, "")
	require.Equal(t, 201, sheet.code, sheet.raw)
	require.Equal(t, true, sheet.data["is_stock_take"])
	require.Len(t, asList(sheet.data["items"]), 2)
	cancel := c.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+str(sheet.data["id"])+"/cancel", nil, "")
	require.Equal(t, 200, cancel.code, cancel.raw)
	postCancelled := c.call(t, http.MethodPost, "/api/v1/stock-adjustments/"+str(sheet.data["id"])+"/post", nil, key())
	require.Equal(t, 409, postCancelled.code, postCancelled.raw)
	list := c.call(t, http.MethodGet, "/api/v1/stock-adjustments?status=posted", nil, "")
	require.Equal(t, 200, list.code, list.raw)
	require.Len(t, asList(list.body["data"]), 2)

	// Transfer 5, edited to 4, received 3: one unit is a transfer loss and the Owner is told.
	br := c.call(t, http.MethodPost, "/api/v1/branches", map[string]any{"name": "Branch B", "code": "BB"}, "")
	require.Equal(t, 201, br.code, br.raw)
	branchB := str(br.data["id"])
	tr := c.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{"to_branch_id": branchB, "items": []map[string]any{{"product_id": rice, "qty": "5"}}}, "")
	require.Equal(t, 201, tr.code, tr.raw)
	trID := str(tr.data["id"])
	upd := c.call(t, http.MethodPut, "/api/v1/stock-transfers/"+trID, map[string]any{"to_branch_id": branchB, "items": []map[string]any{{"product_id": rice, "qty": "4"}}}, "")
	require.Equal(t, 200, upd.code, upd.raw)
	other := c.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{"to_branch_id": branchB, "items": []map[string]any{{"product_id": rice, "qty": "1"}}}, "")
	require.Equal(t, 201, other.code, other.raw)
	cancelTr := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+str(other.data["id"])+"/cancel", nil, "")
	require.Equal(t, 200, cancelTr.code, cancelTr.raw)
	disp := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+trID+"/dispatch", nil, key())
	require.Equal(t, 200, disp.code, disp.raw)
	cancelDisp := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+trID+"/cancel", nil, "")
	require.Equal(t, 409, cancelDisp.code, cancelDisp.raw)
	outList := c.call(t, http.MethodGet, "/api/v1/stock-transfers?direction=out", nil, "")
	require.Equal(t, 200, outList.code, outList.raw)
	require.Len(t, asList(outList.body["data"]), 2)

	cB := &client{app: c.app, token: c.token, branch: branchB}
	inList := cB.call(t, http.MethodGet, "/api/v1/stock-transfers?direction=in&status=dispatched", nil, "")
	require.Len(t, asList(inList.body["data"]), 1, inList.raw)
	recv := cB.call(t, http.MethodPost, "/api/v1/stock-transfers/"+trID+"/receive", map[string]any{"items": []map[string]any{{"product_id": rice, "qty_received": "3"}}}, key())
	require.Equal(t, 200, recv.code, recv.raw)
	require.Equal(t, "received_with_difference", str(recv.data["status"]))
	require.True(t, dec(recv.data["loss_value"]).Equal(invD("1500")), recv.raw)
	detail := c.call(t, http.MethodGet, "/api/v1/stock-transfers/"+trID, nil, "")
	require.Equal(t, 200, detail.code, detail.raw)
	require.True(t, dec(asList(detail.data["items"])[0]["qty_difference"]).Equal(invD("1")))
	bLevels := cB.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "")
	require.True(t, dec(find(asList(bLevels.body["data"]), "product_id", rice)["qty_on_hand"]).Equal(invD("3")))
	notes := c.call(t, http.MethodGet, "/api/v1/notifications", nil, "")
	require.NotEmpty(t, find(asList(notes.body["data"]), "type", "transfer_difference"), notes.raw)

	// A dispatched transfer can be returned in full; it cannot then be received (FR-INV-25).
	back := c.call(t, http.MethodPost, "/api/v1/stock-transfers", map[string]any{"to_branch_id": branchB, "items": []map[string]any{{"product_id": rice, "qty": "3"}}}, "")
	require.Equal(t, 201, back.code, back.raw)
	backID := str(back.data["id"])
	notYet := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+backID+"/return", nil, key())
	require.Equal(t, 409, notYet.code, notYet.raw)
	before := dec(find(asList(c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "").body["data"]), "product_id", rice)["qty_on_hand"])
	require.Equal(t, 200, c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+backID+"/dispatch", nil, key()).code)
	staffSender, _ := e.user(t, "staff")
	noPerm := staffSender.call(t, http.MethodPost, "/api/v1/stock-transfers/"+backID+"/return", nil, key())
	require.Equal(t, 403, noPerm.code, noPerm.raw)
	returned := c.call(t, http.MethodPost, "/api/v1/stock-transfers/"+backID+"/return", map[string]any{"reason": "Wrong branch"}, key())
	require.Equal(t, 200, returned.code, returned.raw)
	require.Equal(t, "returned", str(returned.data["status"]))
	require.True(t, dec(returned.data["value"]).Equal(invD("4500")), returned.raw)
	after := dec(find(asList(c.call(t, http.MethodGet, "/api/v1/stock/levels", nil, "").body["data"]), "product_id", rice)["qty_on_hand"])
	require.True(t, after.Equal(before), "stock back at the sender")
	recvReturned := cB.call(t, http.MethodPost, "/api/v1/stock-transfers/"+backID+"/receive", nil, key())
	require.Equal(t, 409, recvReturned.code, recvReturned.raw)

	// Permissions and branch scope.
	staff, _ := e.user(t, "staff")
	denied := staff.call(t, http.MethodPost, "/api/v1/stock-adjustments", map[string]any{"reason": "x", "items": []map[string]any{{"product_id": rice, "qty_counted": "1"}}}, "")
	require.Equal(t, 403, denied.code, denied.raw)
	deniedList := staff.call(t, http.MethodGet, "/api/v1/stock/movements", nil, "")
	require.Equal(t, 403, deniedList.code, deniedList.raw)
	bEntry := cB.call(t, http.MethodPost, "/api/v1/stock-entries", map[string]any{
		"direction": "in", "reason": "found", "items": []map[string]any{{"product_id": rice, "qty": "1", "unit_cost": "1500"}},
	}, key())
	require.Equal(t, 201, bEntry.code, bEntry.raw)
	hidden := mgr.call(t, http.MethodGet, "/api/v1/stock-entries/"+str(bEntry.data["id"]), nil, "")
	require.Equal(t, 404, hidden.code, hidden.raw)
	mgrMoves := mgr.call(t, http.MethodGet, "/api/v1/stock/movements?product_id="+rice, nil, "")
	for _, m := range asList(mgrMoves.body["data"]) {
		require.Equal(t, e.branch, str(m["branch_id"]))
	}
}
