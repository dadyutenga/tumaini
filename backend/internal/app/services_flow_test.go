package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicesFlow(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	cash := e.cashAccount(t)
	staff, _ := e.user(t, "staff")
	manager, _ := e.user(t, "manager")

	// Service catalog with stages and required documents.
	cat := o.call(t, http.MethodPost, "/api/v1/service-catalog", map[string]any{
		"name": "Business registration", "standard_price": "300000", "government_fees": "50000",
		"stages":    []map[string]any{{"name": "Client intake", "est_days": 2}, {"name": "Name registration", "est_days": 3}, {"name": "TIN", "est_days": 5}},
		"documents": []map[string]any{{"name": "ID copy", "stage_position": 1}, {"name": "Proposed names", "stage_position": 2}},
	}, "")
	require.Equal(t, 201, cat.code, cat.raw)
	catID := str(cat.data["id"])
	require.Len(t, asList(cat.data["stages"]), 3)
	dup := o.call(t, http.MethodPost, "/api/v1/service-catalog", map[string]any{"name": "Business registration"}, "")
	require.Equal(t, 409, dup.code, dup.raw)
	pkg := o.call(t, http.MethodPost, "/api/v1/service-catalog", map[string]any{"name": "Startup package", "is_package": true, "standard_price": "500000", "package_services": []string{catID}}, "")
	require.Equal(t, 201, pkg.code, pkg.raw)
	require.Len(t, asList(pkg.data["package_services"]), 1)
	require.Equal(t, 403, staff.call(t, http.MethodPost, "/api/v1/service-catalog", map[string]any{"name": "Nope"}, "").code)

	// A client starts as a lead and becomes active with the first project.
	cl := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Mama Amina Shop", "phone": "0711000111", "source": "walk-in"}, "")
	require.Equal(t, 201, cl.code, cl.raw)
	clientID := str(cl.data["id"])
	require.Equal(t, "lead", str(cl.data["status"]))

	pr := o.call(t, http.MethodPost, "/api/v1/projects", map[string]any{"client_id": clientID, "service_id": catID, "start_date": "2026-11-16"}, "")
	require.Equal(t, 201, pr.code, pr.raw)
	projectID := str(pr.data["id"])
	stages := asList(pr.data["stages"])
	require.Len(t, stages, 3)
	require.Equal(t, "2026-11-18", str(stages[0]["deadline"]))
	require.Equal(t, "2026-11-21", str(stages[1]["deadline"]), "deadlines add up from the start date")
	docs := asList(pr.data["documents"])
	require.Len(t, docs, 2)
	require.Equal(t, "missing", str(docs[0]["status"]))
	require.Equal(t, "300000.00", dec(pr.data["total_price"]).StringFixed(2))
	got := o.call(t, http.MethodGet, "/api/v1/clients/"+clientID, nil, "")
	require.Equal(t, "active", str(got.data["status"]))
	staffProject := staff.call(t, http.MethodPost, "/api/v1/projects", map[string]any{"client_id": clientID, "service_id": catID, "start_date": "2026-11-16"}, "")
	require.Equal(t, 201, staffProject.code, staffProject.raw, "staff hold services.project.manage")

	// Stage changes record who and when; the last done stage completes the project.
	for i, st := range stages {
		sid := str(st["id"])
		up := o.call(t, http.MethodPut, "/api/v1/projects/"+projectID+"/stages/"+sid, map[string]any{"status": "done", "note": "ok"}, "")
		require.Equal(t, 200, up.code, up.raw)
		if i < 2 {
			require.Equal(t, "in_progress", str(up.data["status"]))
		} else {
			require.Equal(t, "completed", str(up.data["status"]))
		}
	}
	// A completed project cannot move back to in progress. A done stage may return to in progress,
	// and the project stays completed.
	reopen := o.call(t, http.MethodPut, "/api/v1/projects/"+projectID+"/stages/"+str(stages[2]["id"]), map[string]any{"status": "in_progress", "note": "Needs another look"}, "")
	require.Equal(t, 200, reopen.code, reopen.raw)
	require.Equal(t, "completed", str(reopen.data["status"]))

	// A document is marked received.
	doc := o.call(t, http.MethodPut, "/api/v1/client-documents/"+str(docs[0]["id"]), map[string]any{"status": "received"}, "")
	require.Equal(t, 200, doc.code, doc.raw)
	require.Equal(t, "received", str(doc.data["status"]))

	// Notes and an SMS update (no provider configured, so 503 channel_unavailable).
	note := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/notes", map[string]any{"kind": "call", "body": "Client confirmed the names"}, "")
	require.Equal(t, 201, note.code, note.raw)
	upd := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/updates", map[string]any{"message": "Your TIN is ready"}, "")
	require.Equal(t, 503, upd.code, upd.raw)
	require.Contains(t, upd.raw, "channel_unavailable")

	// A deposit before any invoice is held as a client deposit.
	pay1 := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/payments", map[string]any{"amount": "100000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, pay1.code, pay1.raw)
	require.Equal(t, "100000.00", dec(pay1.data["deposit"]).StringFixed(2))
	pay1ID := str(pay1.data["id"])
	require.Equal(t, "100000.00", accountBalance(t, o, cash))

	// The invoice for the whole project absorbs the deposit when it is issued.
	inv := o.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{"client_id": clientID, "project_id": projectID, "due_date": "2026-12-01",
		"installments": []map[string]any{{"due_date": "2026-11-20", "amount": "150000"}, {"due_date": "2026-12-01", "amount": "150000"}}}, "")
	require.Equal(t, 201, inv.code, inv.raw)
	invID := str(inv.data["id"])
	require.Equal(t, "300000.00", dec(inv.data["total"]).StringFixed(2))
	badInst := o.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{"client_id": clientID, "lines": []map[string]any{{"description": "x", "unit_price": "1000"}},
		"installments": []map[string]any{{"due_date": "2026-12-01", "amount": "500"}}}, "")
	require.Equal(t, 400, badInst.code, badInst.raw)
	again := o.call(t, http.MethodPost, "/api/v1/service-invoices", map[string]any{"client_id": clientID, "project_id": projectID}, "")
	require.Equal(t, 409, again.code, "work already on a draft invoice cannot be invoiced twice: "+again.raw)
	issue := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/issue", nil, "")
	require.Equal(t, 200, issue.code, issue.raw)
	require.Equal(t, "partial", str(issue.data["status"]))
	require.Equal(t, "100000.00", dec(issue.data["paid_total"]).StringFixed(2))
	require.Equal(t, "200000.00", dec(issue.data["balance"]).StringFixed(2))
	inst := asList(issue.data["installments"])
	require.Equal(t, "50000.00", dec(inst[0]["balance"]).StringFixed(2), "first installment of 150000 is 50000 short of the 100000 paid")
	require.Equal(t, "150000.00", dec(inst[1]["balance"]).StringFixed(2))
	require.Equal(t, 409, o.call(t, http.MethodPut, "/api/v1/service-invoices/"+invID, map[string]any{"client_id": clientID, "lines": []map[string]any{{"description": "x", "unit_price": "1"}}}, "").code)

	over := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{"amount": "250000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 409, over.code, over.raw)
	pay2 := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/payments", map[string]any{"amount": "200000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, pay2.code, pay2.raw)
	pay2ID := str(pay2.data["id"])
	paid := o.call(t, http.MethodGet, "/api/v1/service-invoices/"+invID, nil, "")
	require.Equal(t, "paid", str(paid.data["status"]))
	require.Equal(t, "300000.00", accountBalance(t, o, cash))
	// Retrying the same key does not post twice.
	k := key()
	retryA := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/payments", map[string]any{"amount": "1000", "method": "cash", "money_account_id": cash}, k)
	retryB := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/payments", map[string]any{"amount": "1000", "method": "cash", "money_account_id": cash}, k)
	require.Equal(t, 201, retryA.code, retryA.raw)
	require.Equal(t, str(retryA.data["id"]), str(retryB.data["id"]))
	require.Equal(t, "301000.00", accountBalance(t, o, cash))

	// Voiding the invoice payment reopens the invoice.
	fin := o.call(t, http.MethodPost, "/api/v1/payments/"+pay2ID+"/void", map[string]any{"reason": "mistake"}, key())
	require.Equal(t, 200, fin.code, fin.raw)
	vp := o.call(t, http.MethodPost, "/api/v1/service-payments/"+pay2ID+"/void", map[string]any{"reason": "wrong amount"}, "")
	require.Equal(t, 409, vp.code, vp.raw)
	reinv := o.call(t, http.MethodGet, "/api/v1/service-invoices/"+invID, nil, "")
	require.Equal(t, "partial", str(reinv.data["status"]))
	require.Equal(t, "101000.00", accountBalance(t, o, cash))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/service-invoices/"+invID+"/void", map[string]any{"reason": "cancel"}, "").code, "an invoice with payments cannot be voided")
	_ = pay1ID

	// Costs: one owed to a contractor, one paid now. Project profit is payments minus costs.
	ct := o.call(t, http.MethodPost, "/api/v1/contractors", map[string]any{"name": "Quick Print", "trade": "printing", "phone": "0755000222"}, "")
	require.Equal(t, 201, ct.code, ct.raw)
	ctID := str(ct.data["id"])
	owed := o.call(t, http.MethodPost, "/api/v1/service-costs", map[string]any{"project_id": projectID, "contractor_id": ctID, "kind": "contractor", "description": "Banners", "amount": "50000"}, key())
	require.Equal(t, 201, owed.code, owed.raw)
	require.Equal(t, "owed", str(owed.data["status"]))
	gov := o.call(t, http.MethodPost, "/api/v1/service-costs", map[string]any{"project_id": projectID, "kind": "government_fee", "description": "Registration fee", "amount": "20000", "money_account_id": cash}, key())
	require.Equal(t, 201, gov.code, gov.raw)
	require.Equal(t, "101000.00", accountBalance(t, o, cash))
	noAccount := o.call(t, http.MethodPost, "/api/v1/service-costs", map[string]any{"project_id": projectID, "kind": "other", "description": "x", "amount": "5"}, key())
	require.Equal(t, 400, noAccount.code, noAccount.raw)
	prj := o.call(t, http.MethodGet, "/api/v1/projects/"+projectID, nil, "")
	require.Equal(t, "50000.00", dec(prj.data["costs"]).StringFixed(2))
	require.Equal(t, "51000.00", dec(prj.data["profit"]).StringFixed(2), "101000 received minus the contractor cost")

	cts := o.call(t, http.MethodGet, "/api/v1/contractors/"+ctID, nil, "")
	require.Equal(t, "50000.00", dec(cts.data["owed"]).StringFixed(2))
	cpOver := o.call(t, http.MethodPost, "/api/v1/contractors/"+ctID+"/payments", map[string]any{"amount": "60000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 409, cpOver.code, cpOver.raw)
	cp := o.call(t, http.MethodPost, "/api/v1/contractors/"+ctID+"/payments", map[string]any{"amount": "30000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, cp.code, cp.raw)
	cts = o.call(t, http.MethodGet, "/api/v1/contractors/"+ctID, nil, "")
	require.Equal(t, "20000.00", dec(cts.data["owed"]).StringFixed(2))
	st := o.call(t, http.MethodGet, "/api/v1/contractors/"+ctID+"/statement", nil, "")
	require.Equal(t, 200, st.code, st.raw)
	require.Equal(t, "20000.00", dec(asList(st.data["lines"])[1]["balance"]).StringFixed(2))
	hasPaid := o.call(t, http.MethodPost, "/api/v1/service-costs/"+str(owed.data["id"])+"/void", map[string]any{"reason": "dup"}, "")
	require.Equal(t, 409, hasPaid.code, "a cost already part-paid cannot be voided: "+hasPaid.raw)
	voidGov := o.call(t, http.MethodPost, "/api/v1/service-costs/"+str(gov.data["id"])+"/void", map[string]any{"reason": "entered twice"}, "")
	require.Equal(t, 200, voidGov.code, voidGov.raw)
	require.Equal(t, "71000.00", accountBalance(t, o, cash))

	// A quotation becomes an invoice and a project when accepted.
	q := o.call(t, http.MethodPost, "/api/v1/service-quotations", map[string]any{"client_id": clientID, "valid_until": "2026-12-15", "discount": "10000",
		"lines": []map[string]any{{"service_id": catID}, {"description": "Rush fee", "quantity": "2", "unit_price": "5000"}}}, "")
	require.Equal(t, 201, q.code, q.raw)
	qID := str(q.data["id"])
	require.Equal(t, "310000.00", dec(q.data["subtotal"]).StringFixed(2))
	require.Equal(t, "300000.00", dec(q.data["total"]).StringFixed(2))
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/service-quotations/"+qID+"/send", nil, "").code)
	acc := o.call(t, http.MethodPost, "/api/v1/service-quotations/"+qID+"/accept", map[string]any{"create_project": true, "service_id": catID}, "")
	require.Equal(t, 200, acc.code, acc.raw)
	require.Equal(t, "accepted", str(acc.data["status"]))
	require.NotEmpty(t, str(acc.data["invoice_id"]))
	require.NotEmpty(t, str(acc.data["project_id"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/service-quotations/"+qID+"/accept", nil, "").code)
	pdf := o.call(t, http.MethodGet, "/api/v1/service-quotations/"+qID+"/pdf", nil, "")
	require.Equal(t, 200, pdf.code)
	require.True(t, strings.HasPrefix(pdf.raw, "%PDF"))
	voidInv := o.call(t, http.MethodPost, "/api/v1/service-invoices/"+str(acc.data["invoice_id"])+"/void", map[string]any{"reason": "client changed mind"}, "")
	require.Equal(t, 200, voidInv.code, voidInv.raw)
	require.Equal(t, 200, o.call(t, http.MethodGet, "/api/v1/service-invoices/"+invID+"/pdf", nil, "").code)

	stmt := o.call(t, http.MethodGet, "/api/v1/clients/"+clientID+"/statement", nil, "")
	require.Equal(t, 200, stmt.code, stmt.raw)
	require.Equal(t, "199000.00", dec(stmt.data["closing_balance"]).StringFixed(2), "300000 invoiced less 101000 paid")

	// Design job: version, client approval by link, print order and timer.
	dj := o.call(t, http.MethodPost, "/api/v1/design-jobs", map[string]any{"client_id": clientID, "project_id": projectID, "job_type": "logo", "title": "Logo for Mama Amina",
		"deadline": "2026-11-25", "price": "80000", "deposit": "20000", "revisions_included": 1}, "")
	require.Equal(t, 201, dj.code, dj.raw)
	djID := str(dj.data["id"])
	require.Equal(t, "new_brief", str(dj.data["status"]))
	nofile := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/versions", map[string]any{"notes": "empty"}, "")
	require.Equal(t, 400, nofile.code, nofile.raw)
	code, raw := upload(t, e.app.Fiber, o, "/api/v1/files", "logo.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	preview := regexpID(t, raw)
	v1 := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/versions", map[string]any{"file_id": preview, "link": "https://example.com/logo-v1.png"}, "")
	require.Equal(t, 201, v1.code, v1.raw)
	v1ID := str(v1.data["id"])
	require.Equal(t, 1, int(dec(v1.data["version_no"]).IntPart()))
	send := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/versions/"+v1ID+"/send", nil, "")
	require.Equal(t, 200, send.code, send.raw)
	token := strings.TrimPrefix(str(send.data["approval_path"]), "/approve/")
	require.NotEmpty(t, token)
	jobNow := o.call(t, http.MethodGet, "/api/v1/design-jobs/"+djID, nil, "")
	require.Equal(t, "with_client", str(jobNow.data["status"]))

	pub := &client{app: e.app.Fiber}
	page := pub.call(t, http.MethodGet, "/api/v1/public/design-approvals/"+token, nil, "")
	require.Equal(t, 200, page.code, page.raw)
	require.Equal(t, "Logo for Mama Amina", str(page.data["title"]))
	require.Equal(t, 404, pub.call(t, http.MethodGet, "/api/v1/public/design-approvals/not-a-real-token", nil, "").code)
	noComment := pub.call(t, http.MethodPost, "/api/v1/public/design-approvals/"+token+"/decision", map[string]any{"decision": "changes"}, "")
	require.Equal(t, 400, noComment.code, noComment.raw)
	chg := pub.call(t, http.MethodPost, "/api/v1/public/design-approvals/"+token+"/decision", map[string]any{"decision": "changes", "comment": "Make the text bigger"}, "")
	require.Equal(t, 200, chg.code, chg.raw)
	require.Equal(t, 409, pub.call(t, http.MethodPost, "/api/v1/public/design-approvals/"+token+"/decision", map[string]any{"decision": "approve"}, "").code, "a link decides once")
	jobNow = o.call(t, http.MethodGet, "/api/v1/design-jobs/"+djID, nil, "")
	require.Equal(t, "in_design", str(jobNow.data["status"]))
	require.Equal(t, 1, int(dec(jobNow.data["revisions_used"]).IntPart()))
	require.Len(t, asList(jobNow.data["feedback"]), 1)

	v2 := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/versions", map[string]any{"file_id": preview, "link": "https://example.com/logo-v2.png"}, "")
	require.Equal(t, 201, v2.code, v2.raw)
	require.Equal(t, 2, int(dec(v2.data["version_no"]).IntPart()))
	send2 := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/versions/"+str(v2.data["id"])+"/send", nil, "")
	require.Equal(t, 200, send2.code, send2.raw)
	token2 := strings.TrimPrefix(str(send2.data["approval_path"]), "/approve/")
	ok := pub.call(t, http.MethodPost, "/api/v1/public/design-approvals/"+token2+"/decision", map[string]any{"decision": "approve"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	jobNow = o.call(t, http.MethodGet, "/api/v1/design-jobs/"+djID, nil, "")
	require.Equal(t, "approved", str(jobNow.data["status"]))
	final := find(asList(jobNow.data["versions"]), "version_no", "2")
	require.Equal(t, true, final["is_final"])
	require.Equal(t, true, jobNow.data["revisions_over"] == false)

	po := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/print-orders", map[string]any{"contractor_id": ctID, "quantity": 100, "cost": "15000"}, "")
	require.Equal(t, 201, po.code, po.raw)
	jobNow = o.call(t, http.MethodGet, "/api/v1/design-jobs/"+djID, nil, "")
	require.Equal(t, "printing", str(jobNow.data["status"]))
	require.Equal(t, "15000.00", dec(jobNow.data["costs"]).StringFixed(2))
	rdy := o.call(t, http.MethodPut, "/api/v1/print-orders/"+str(po.data["id"]), map[string]any{"status": "ready"}, "")
	require.Equal(t, 200, rdy.code, rdy.raw)
	dlv := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/status", map[string]any{"status": "delivered"}, "")
	require.Equal(t, 200, dlv.code, dlv.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/status", map[string]any{"status": "in_design"}, "").code)
	jobPay := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/payments", map[string]any{"amount": "20000", "method": "cash", "money_account_id": cash}, key())
	require.Equal(t, 201, jobPay.code, jobPay.raw)
	// Service payment receipt (JSON + PDF) with Copy marking on reprint, and party_name resolution on /payments.
	rcpt := o.call(t, http.MethodGet, "/api/v1/service-payments/"+str(jobPay.data["id"])+"/receipt", nil, "")
	require.Equal(t, 200, rcpt.code, rcpt.raw)
	require.Equal(t, "Mama Amina Shop", str(rcpt.data["client_name"]))
	require.NotEmpty(t, str(rcpt.data["purpose"]))
	require.Equal(t, false, rcpt.data["copy"])
	rcptPDF := o.call(t, http.MethodGet, "/api/v1/service-payments/"+str(jobPay.data["id"])+"/receipt?format=pdf", nil, "")
	require.Equal(t, 200, rcptPDF.code)
	require.True(t, strings.HasPrefix(rcptPDF.raw, "%PDF-"))
	require.NotContains(t, rcptPDF.raw, "*** COPY ***")
	rcptAfter := o.call(t, http.MethodGet, "/api/v1/service-payments/"+str(jobPay.data["id"])+"/receipt", nil, "")
	require.Equal(t, 200, rcptAfter.code, rcptAfter.raw)
	require.Equal(t, true, rcptAfter.data["copy"])
	rcptPDF2 := o.call(t, http.MethodGet, "/api/v1/service-payments/"+str(jobPay.data["id"])+"/receipt?format=pdf", nil, "")
	require.Equal(t, 200, rcptPDF2.code)
	require.True(t, strings.HasPrefix(rcptPDF2.raw, "%PDF-"))
	require.Contains(t, rcptPDF2.raw, "COPY")
	allPays := o.call(t, http.MethodGet, "/api/v1/payments?party_type=customer", nil, "")
	require.Equal(t, 200, allPays.code, allPays.raw)
	require.NotEmpty(t, asList(allPays.body["data"]))
	require.Equal(t, "Mama Amina Shop", str(asList(allPays.body["data"])[0]["party_name"]))

	// Contractor statement PDF and print-orders list + cancellation voiding linked cost.
	ctPDF := o.call(t, http.MethodGet, "/api/v1/contractors/"+ctID+"/statement?format=pdf", nil, "")
	require.Equal(t, 200, ctPDF.code)
	require.True(t, strings.HasPrefix(ctPDF.raw, "%PDF-"))
	poList := o.call(t, http.MethodGet, "/api/v1/print-orders", nil, "")
	require.Equal(t, 200, poList.code, poList.raw)
	require.GreaterOrEqual(t, len(asList(poList.body["data"])), 1)

	tm := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/timer/start", nil, "")
	require.Equal(t, 200, tm.code, tm.raw)
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/timer/start", nil, "").code)
	require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+djID+"/timer/stop", nil, "").code)
	board := o.call(t, http.MethodGet, "/api/v1/design-jobs/board", nil, "")
	require.Equal(t, 200, board.code, board.raw)
	require.Len(t, asList(board.body["data"]), 7)

	// Branding stage link: several jobs at once; test extra revision charge & print order cancellation voiding cost.
	stageID := str(stages[0]["id"])
	multi := o.call(t, http.MethodPost, "/api/v1/projects/"+projectID+"/stages/"+stageID+"/design-jobs", map[string]any{"jobs": []map[string]any{{"job_type": "logo", "title": "Logo"}, {"job_type": "signage", "title": "Signboard"}}}, "")
	require.Equal(t, 201, multi.code, multi.raw)
	jobIDs := multi.data["job_ids"].([]any)
	require.Len(t, jobIDs, 2)
	// Extra revision charge increases price and revisions_included on the first branding job.
	firstJobID := str(jobIDs[0])
	ext := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+firstJobID+"/extra-revision-charge", map[string]any{"amount": "25000", "extra_revisions": 2, "note": "Extra logo concepts"}, "")
	require.Equal(t, 200, ext.code, ext.raw)
	require.Equal(t, "25000.00", dec(ext.data["price"]).StringFixed(2))
	for _, id := range jobIDs {
		jid := str(id)
		require.Equal(t, 201, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+jid+"/versions", map[string]any{"file_id": preview, "link": "https://example.com/x.png"}, "").code)
		require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+jid+"/status", map[string]any{"status": "with_client"}, "").code)
		require.Equal(t, 200, o.call(t, http.MethodPost, "/api/v1/design-jobs/"+jid+"/status", map[string]any{"status": "approved"}, "").code)
	}
	// Create a print order on the approved job and cancel it: linked cost must be voided.
	poCancel := o.call(t, http.MethodPost, "/api/v1/design-jobs/"+firstJobID+"/print-orders", map[string]any{"contractor_id": ctID, "quantity": 50, "cost": "10000"}, "")
	require.Equal(t, 201, poCancel.code, poCancel.raw)
	cancelledPO := o.call(t, http.MethodPut, "/api/v1/print-orders/"+str(poCancel.data["id"]), map[string]any{"status": "cancelled"}, "")
	require.Equal(t, 200, cancelledPO.code, cancelledPO.raw)
	firstJobAfter := o.call(t, http.MethodGet, "/api/v1/design-jobs/"+firstJobID, nil, "")
	require.Equal(t, "0.00", dec(firstJobAfter.data["costs"]).StringFixed(2), "cancelling print order must void linked contractor cost")

	prj = o.call(t, http.MethodGet, "/api/v1/projects/"+projectID, nil, "")
	require.Equal(t, "done", str(asList(prj.data["stages"])[0]["status"]))
	require.NotEmpty(t, asList(prj.data["stage_events"]), "project detail must include stage_events history")

	// Tasks, renewals and the daily reminder job.
	_, staffID := e.user(t, "staff")
	task := o.call(t, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Collect ID copy", "project_id": projectID, "assignee_id": staffID, "due_date": "2026-11-10"}, "")
	require.Equal(t, 201, task.code, task.raw)
	require.Equal(t, true, task.data["overdue"])
	late := o.call(t, http.MethodGet, "/api/v1/tasks?overdue=true", nil, "")
	require.Len(t, asList(late.body["data"]), 1)
	ren := o.call(t, http.MethodPost, "/api/v1/renewals", map[string]any{"client_id": clientID, "item": "Business licence", "due_date": "2026-12-16", "amount": "120000"}, "")
	require.Equal(t, 201, ren.code, ren.raw)
	renID := str(ren.data["id"])
	require.Equal(t, 30, int(dec(ren.data["days_left"]).IntPart()))
	soon := o.call(t, http.MethodGet, "/api/v1/renewals?within_days=30", nil, "")
	require.Len(t, asList(soon.body["data"]), 1)
	biz := o.call(t, http.MethodGet, "/api/v1/business", nil, "")
	require.Equal(t, 200, biz.code, biz.raw)
	require.NoError(t, e.app.Jobs.Enqueue(context.Background(), "service_reminders", map[string]any{}))
	e.runJobs(t)
	e.runJobs(t)
	notes := o.call(t, http.MethodGet, "/api/v1/notifications", nil, "")
	require.Equal(t, 200, notes.code, notes.raw)
	cnt := 0
	for _, n := range asList(notes.body["data"]) {
		if str(n["type"]) == "renewal_due" {
			cnt++
		}
	}
	require.Equal(t, 1, cnt, "one reminder per renewal and threshold")
	rn := o.call(t, http.MethodPost, "/api/v1/renewals/"+renID+"/renew", map[string]any{"next_due_date": "2027-12-10"}, "")
	require.Equal(t, 200, rn.code, rn.raw)
	require.Equal(t, "renewed", str(rn.data["status"]))
	require.NotEmpty(t, str(rn.data["next_id"]))
	require.Equal(t, 409, o.call(t, http.MethodPost, "/api/v1/renewals/"+renID+"/renew", nil, "").code)

	// Documents library and visibility limits.
	dl := o.call(t, http.MethodGet, "/api/v1/client-documents?client_id="+clientID+"&status=missing", nil, "")
	require.Equal(t, 200, dl.code, dl.raw)
	require.Len(t, asList(dl.body["data"]), 5, "missing documents across the client's projects")
	mp := manager.call(t, http.MethodGet, "/api/v1/projects", nil, "")
	require.Equal(t, 200, mp.code, mp.raw)
	require.GreaterOrEqual(t, len(asList(mp.body["data"])), 2)
	sp := staff.call(t, http.MethodGet, "/api/v1/projects", nil, "")
	require.Equal(t, 200, sp.code, sp.raw)
	require.NotEmpty(t, asList(sp.body["data"]), "staff see the projects they created")
	for _, row := range asList(sp.body["data"]) {
		require.NotEqual(t, projectID, str(row["id"]))
	}
	require.Equal(t, 404, staff.call(t, http.MethodGet, "/api/v1/projects/"+projectID, nil, "").code)
	require.Equal(t, 200, staff.call(t, http.MethodGet, "/api/v1/service-invoices", nil, "").code, "staff hold services.invoice.draft")
	require.Equal(t, 403, staff.call(t, http.MethodGet, "/api/v1/contractors", nil, "").code)

	dash := o.call(t, http.MethodGet, "/api/v1/services/dashboard", nil, "")
	require.Equal(t, 200, dash.code, dash.raw)
	require.NotNil(t, dash.data["projects_by_stage"])
	require.NotNil(t, dash.data["debts"])
	rep := o.call(t, http.MethodGet, "/api/v1/reports/projects", nil, "")
	require.Equal(t, 200, rep.code, rep.raw)
	require.NotEmpty(t, asList(rep.data["rows"]))
	drep := o.call(t, http.MethodGet, "/api/v1/reports/design-jobs", nil, "")
	require.Equal(t, 200, drep.code, drep.raw)
	require.Equal(t, 200, o.call(t, http.MethodGet, "/api/v1/reports/projects?format=csv", nil, "").code)

	// The public link keeps showing what was decided.
	decided := pub.call(t, http.MethodGet, "/api/v1/public/design-approvals/"+token2, nil, "")
	require.Equal(t, 200, decided.code, decided.raw)
	require.Equal(t, "approved", str(decided.data["status"]), "a decided link shows the recorded choice")
	require.NotNil(t, decided.data["decided_at"])
}
