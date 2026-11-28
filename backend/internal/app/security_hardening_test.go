package app_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"bms/internal/platform/db"
	"bms/internal/platform/jobs"
)

// ownerActor is the database request context a compromised API process would set to act as the Owner.
func (e *env) ownerActor(t *testing.T) db.Actor {
	t.Helper()
	a := db.Actor{}
	require.NoError(t, e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		// Registration creates the Owner's membership first.
		return tx.QueryRow(ctx, `SELECT user_id, id FROM app.memberships ORDER BY created_at LIMIT 1`).Scan(&a.UserID, &a.MembershipID)
	}))
	return a
}

// mustRefuse runs one statement as the API database role and requires PostgreSQL to refuse it with a
// privilege error (42501) or a business-rule error (BM004 or BM001).
func (e *env) mustRefuse(t *testing.T, a db.Actor, name, sql string, args ...any) {
	t.Helper()
	var affected int64
	err := e.app.DB.InActorTx(context.Background(), a, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, sql, args...)
		affected = tag.RowsAffected()
		if err != nil {
			return err
		}
		return errors.New("rollback: statement was accepted") // never keep a successful attack
	})
	var pe *pgconn.PgError
	require.Truef(t, errors.As(err, &pe), "%s: the database accepted it (%d rows): %v", name, affected, err)
	require.Containsf(t, []string{"42501", "BM004", "BM001"}, pe.Code, "%s: unexpected refusal %s", name, pe.Message)
}

func TestCompromisedAppRole_CannotRewriteFinalRecords(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	till := e.cashAccount(t)
	prod := e.product(t, "Router", "100000", "10", "60000")

	cust := o.call(t, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Debtor", "phone": uniquePhone(), "credit_limit": "500000"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	custID := str(cust.data["id"])
	sale := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": prod, "qty": "2"}}, "customer_id": custID, "due_date": "2026-12-16",
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "50000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)

	closing := o.call(t, http.MethodPost, "/api/v1/day-closings", map[string]any{"date": "2026-11-16", "counted_cash": "50000", "note": "Counted"}, "")
	require.Equal(t, 201, closing.code, closing.raw)

	workWeek(t, o)
	emp := employee(t, o, "Asha", "100000", nil)
	loan := o.call(t, http.MethodPost, "/api/v1/loans", map[string]any{"employee_id": emp, "principal": "30000", "monthly_deduction": "10000"}, key())
	require.Equal(t, 201, loan.code, loan.raw)

	a := e.ownerActor(t)
	// Internal posting helpers are not callable, so the ledger cannot be reversed behind a payment's back.
	e.mustRefuse(t, a, "call reverse_ledger", `SELECT app.reverse_ledger(NULL, 'account_id', $1, current_date, 'x')`, uuid.MustParse(till))
	e.mustRefuse(t, a, "call accrue_sale_commissions", `SELECT app.accrue_sale_commissions($1, $1)`, uuid.Nil)
	e.mustRefuse(t, a, "call reverse_return_commissions", `SELECT app.reverse_return_commissions($1, $1)`, uuid.Nil)

	for _, c := range []struct{ name, txnType, direction string }{
		{"siphon with other_payment", "other_payment", "out"},
		{"second opening balance", "opening_balance", "in"},
		{"opening balance outflow", "opening_balance", "out"},
	} {
		e.mustRefuse(t, a, c.name, `
			INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id, location_id, business_date, description)
			SELECT account_id, $2::app.txn_direction, 1, $3::app.txn_type, business_unit_id, location_id, '2026-11-17', 'forged'
			FROM app.financial_transactions WHERE account_id = $1 LIMIT 1`, uuid.MustParse(till), c.direction, c.txnType)
	}

	e.mustRefuse(t, a, "reassign who took the money", `UPDATE app.payments_received SET received_by = NULL WHERE true`)
	e.mustRefuse(t, a, "renumber a receipt", `UPDATE app.payments_received SET receipt_no = receipt_no || 'X' WHERE true`)
	e.mustRefuse(t, a, "re-date a payment", `UPDATE app.payments_received SET received_at = received_at - interval '1 year' WHERE true`)
	e.mustRefuse(t, a, "re-date a sale", `UPDATE app.sales SET completed_at = completed_at - interval '1 year' WHERE true`)
	e.mustRefuse(t, a, "change closed cash count", `UPDATE app.day_closings SET counted_cash = 1 WHERE true`)
	e.mustRefuse(t, a, "delete closed day lines", `DELETE FROM app.day_closing_lines WHERE true`)
	e.mustRefuse(t, a, "shrink a loan", `UPDATE app.employee_advances SET principal = 1, monthly_deduction = 1 WHERE true`)
	e.mustRefuse(t, a, "write off a loan", `UPDATE app.employee_advances SET status = 'written_off' WHERE true`)
	e.mustRefuse(t, a, "archive a debtor", `UPDATE app.customers SET archived_at = now() WHERE id = $1`, uuid.MustParse(custID))

	// Every SECURITY DEFINER function the API role can execute runs with the owner's rights, so the
	// list is closed: a new one must be reviewed and added here.
	var callable []string
	require.NoError(t, e.app.DB.InActorTx(context.Background(), a, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT n.nspname || '.' || p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE p.prosecdef AND n.nspname IN ('app', 'iam', 'audit', 'public') AND p.prorettype <> 'trigger'::regtype
			  AND has_function_privilege(p.oid, 'execute') ORDER BY 1`)
		if err != nil {
			return err
		}
		callable, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	}))
	require.Equal(t, []string{
		"app.apply_advance_cash_repayment", "app.authority_limit", "app.has_permission",
		"app.is_self_employee", "audit.log_event",
	}, callable)
}

// A customer who owes on a service invoice must not disappear through the shop customer screen, the
// client status route or a client edit, each of which used to check a different balance or none.
func TestArchivingCannotHideReceivables(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	till := e.cashAccount(t)
	prod := e.product(t, "Cable", "5000", "10", "2000")
	cust := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Owes Us", "phone": uniquePhone(), "status": "active"}, "")
	require.Equal(t, 201, cust.code, cust.raw)
	id := str(cust.data["id"])
	sale := o.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items": []map[string]any{{"product_id": prod, "qty": "1"}}, "customer_id": id, "due_date": "2026-12-16",
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "1000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)

	// The client API only counts service invoices; the shop debt must still stop it.
	res := o.call(t, http.MethodPost, "/api/v1/clients/"+id+"/deactivate", nil, "")
	require.Equal(t, 409, res.code, res.raw)
	res = o.call(t, http.MethodPut, "/api/v1/clients/"+id, map[string]any{"name": "Owes Us", "status": "inactive"}, "")
	require.Equal(t, 409, res.code, res.raw)
}

// National ID numbers are encrypted at rest and never copied into the audit trail in clear text.
func TestNationalIDEncryptedAndKeptOutOfAudit(t *testing.T) {
	e := newEnv(t, defaultNow)
	o := e.owner
	const nida = "19900101-12345-00001-27"
	res := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Private Person", "phone": uniquePhone(), "id_number": nida}, "")
	require.Equal(t, 201, res.code, res.raw)
	id := uuid.MustParse(str(res.data["id"]))
	require.NotContains(t, res.raw, nida)

	// The same number is still recognised as a duplicate, however it is capitalised.
	dup := o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Same Person", "phone": uniquePhone(), "id_number": " " + nida + " "}, "")
	require.Equal(t, 409, dup.code, dup.raw)

	read := func() (cipher []byte, keyID string) {
		require.NoError(t, e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT national_id_ciphertext, national_id_key_id FROM app.customers WHERE id = $1`, id).Scan(&cipher, &keyID)
		}))
		return
	}
	cipher, keyID := read()
	require.False(t, bytes.Contains(cipher, []byte(nida)), "stored value must not contain the number")
	require.NotEqual(t, "local-v1", keyID)

	var leaked int
	require.NoError(t, e.app.DB.InActorTx(context.Background(), e.ownerActor(t), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM audit.audit_log WHERE (coalesce(new_values::text,'') LIKE '%' || $1 || '%' OR coalesce(old_values::text,'') LIKE '%' || $1 || '%')", nida).Scan(&leaked)
	}))
	require.Zero(t, leaked)

	// Rows written in clear text by earlier releases are re-encrypted by the daily job.
	require.NoError(t, e.app.DB.InGlobalTx(context.Background(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE app.customers SET national_id_ciphertext = convert_to($2, 'UTF8'), national_id_key_id = 'local-v1',
			national_id_hmac = sha256(convert_to(upper($2), 'UTF8')) WHERE id = $1`, id, nida)
		return err
	}))
	require.NoError(t, e.app.Jobs.Enqueue(context.Background(), jobs.ProtectNationalIDs, map[string]any{}))
	require.NoError(t, e.app.Jobs.RunPending(context.Background()))
	cipher, keyID = read()
	require.False(t, bytes.Contains(cipher, []byte(nida)))
	require.NotEqual(t, "local-v1", keyID)
	dup = o.call(t, http.MethodPost, "/api/v1/clients", map[string]any{"name": "Same Person", "phone": uniquePhone(), "id_number": nida}, "")
	require.Equal(t, 409, dup.code, dup.raw)
}

// Fiber routes case-insensitively, so the limiter must not give each spelling of a path its own budget,
// and guessing public link tokens must share one budget.
func TestRateLimitCannotBeSidesteppedByPathSpelling(t *testing.T) {
	e := newEnv(t, defaultNow)
	c := &client{app: e.app.Fiber}
	spellings := []string{"/api/v1/auth/login", "/api/v1/auth/Login", "/api/v1/auth/LOGIN", "/API/v1/auth/login", "/api/v1/auth/login/"}
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		res := c.call(t, http.MethodPost, spellings[i%len(spellings)], map[string]any{"identifier": uniquePhone(), "password": "wrong-password"}, "")
		limited = res.code == http.StatusTooManyRequests
	}
	require.True(t, limited, "40 sign-in attempts spread over path spellings must hit the 30/minute limit")

	limited = false
	for i := 0; i < 40 && !limited; i++ {
		limited = c.call(t, http.MethodGet, "/api/v1/public/design-approvals/"+uuid.NewString(), nil, "").code == http.StatusTooManyRequests
	}
	require.True(t, limited, "token guessing must be limited across different tokens")
}

// The request log records the status the client actually received and never a public link token.
func TestRequestLogStatusAndTokenRedaction(t *testing.T) {
	e := newEnv(t, defaultNow)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &client{app: e.app.Fiber}
	bad := c.call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "wrong-password"}, "")
	require.Equal(t, 401, bad.code, bad.raw)
	const token = "super-secret-approval-link-token"
	for _, p := range []string{"/api/v1/public/design-approvals/" + token, "/API/v1/Public/Design-Approvals/" + token + "/file"} {
		require.Equal(t, 404, c.call(t, http.MethodGet, p, nil, "").code)
	}

	logs := buf.String()
	require.NotContains(t, logs, token)
	require.Contains(t, logs, ":token")
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `"path":"/api/v1/auth/login"`) {
			require.Contains(t, line, `"status":401`)
		}
	}
}

// Uploading the same file again must never remove the bytes behind an earlier attachment.
func TestRepeatUploadKeepsEarlierFile(t *testing.T) {
	e := newEnv(t, defaultNow)
	code, raw := upload(t, e.app.Fiber, e.owner, "/api/v1/files", "receipt.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	first := regexpID(t, raw)
	code, raw = upload(t, e.app.Fiber, e.owner, "/api/v1/files", "receipt.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)

	req, err := http.NewRequest(http.MethodGet, "/api/v1/files/"+first, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+e.owner.token)
	resp, err := e.app.Fiber.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, string(body))
	require.Equal(t, pngBytes, body)
}
