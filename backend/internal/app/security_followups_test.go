package app_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
	"bms/internal/platform/jobs"
)

// restart builds a second application from the current environment, as a restart or a second
// instance of the API would.
func restart(t *testing.T) *app.App {
	t.Helper()
	cfg, err := config.Load()
	require.NoError(t, err)
	a, err := app.New(context.Background(), cfg, clock.Fixed{T: defaultNow})
	require.NoError(t, err)
	t.Cleanup(a.Close)
	return a
}

// The sign-in budget is shared: restarting the API or asking another instance does not renew it.
func TestRateLimitIsSharedAcrossInstances(t *testing.T) {
	e := newEnv(t, defaultNow)
	attempt := func(c *client) int {
		return c.call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": uniquePhone(), "password": "wrong-password"}, "").code
	}
	first := &client{app: e.app.Fiber}
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		limited = attempt(first) == http.StatusTooManyRequests
	}
	require.True(t, limited)
	second := &client{app: restart(t).Fiber}
	require.Equal(t, http.StatusTooManyRequests, attempt(second), "a new instance must see the spent budget")

	// Old windows are cleared by the daily cleanup without disturbing current ones.
	require.NoError(t, e.app.Jobs.Enqueue(context.Background(), jobs.SessionCleanup, map[string]any{}))
	require.NoError(t, e.app.Jobs.RunPending(context.Background()))
	require.Equal(t, http.StatusTooManyRequests, attempt(second))
}

func TestFullExportIsRateLimited(t *testing.T) {
	e := newEnv(t, defaultNow)
	last := 0
	for i := 0; i < 6; i++ {
		last = e.owner.call(t, http.MethodPost, "/api/v1/backups/export", nil, key()).code
	}
	require.Equal(t, http.StatusTooManyRequests, last)
}

// A correction needs the approval raised for that entry, and that approval posts once.
func TestLedgerCorrectionUsesItsOwnApprovalOnce(t *testing.T) {
	e := newEnv(t, defaultNow)
	till := e.cashAccount(t)
	prod := e.product(t, "Lamp", "10000", "5", "5000")
	sale := e.owner.call(t, http.MethodPost, "/api/v1/sales", map[string]any{
		"items":    []map[string]any{{"product_id": prod, "qty": "1"}},
		"payments": []map[string]any{{"method": "cash", "money_account_id": till, "amount": "10000"}},
	}, key())
	require.Equal(t, 201, sale.code, sale.raw)
	_, managerUser := e.user(t, "manager")
	owner := e.ownerActor(t)

	const correction = `
		INSERT INTO app.financial_transactions (account_id, direction, amount, txn_type, business_unit_id, location_id,
		       business_date, description, reverses_transaction_id, approval_request_id)
		SELECT account_id, 'out', 1, 'correction', business_unit_id, location_id, business_date, 'Correction', id, $2
		FROM app.financial_transactions WHERE id = $1`
	errRollback := errors.New("rollback")
	err := e.app.DB.InActorTx(context.Background(), owner, func(ctx context.Context, tx pgx.Tx) error {
		var entry, requester, approval uuid.UUID
		require.NoError(t, tx.QueryRow(ctx, "SELECT id FROM app.financial_transactions WHERE txn_type = 'customer_receipt'").Scan(&entry))
		require.NoError(t, tx.QueryRow(ctx, "SELECT id FROM app.memberships WHERE user_id = $1", managerUser).Scan(&requester))
		require.NoError(t, tx.QueryRow(ctx, "\n\t\t\tINSERT INTO app.approval_requests (approval_type, reason, requested_by, financial_transaction_id, requested_amount)\n\t\t\tVALUES ('transaction_correction', 'Wrong amount', $1, $2, 1) RETURNING id", requester, entry).Scan(&approval))
		_, err := tx.Exec(ctx, `UPDATE app.approval_requests SET status = 'approved', decision_note = 'Checked' WHERE id = $1`, approval)
		require.NoError(t, err)

		attempt := func(with uuid.UUID) string {
			sp, err := tx.Begin(ctx)
			require.NoError(t, err)
			_, err = sp.Exec(ctx, correction, entry, with)
			if err == nil {
				require.NoError(t, sp.Commit(ctx))
				return ""
			}
			require.NoError(t, sp.Rollback(ctx))
			var pe *pgconn.PgError
			require.ErrorAs(t, err, &pe)
			return pe.Code
		}
		require.Equal(t, "BM007", attempt(uuid.New()), "another approval id")
		require.Equal(t, "", attempt(approval), "the approved correction")
		require.Equal(t, "23505", attempt(approval), "the same approval again")
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)
}

// Changing a password behind the application leaves an audit record, without the secret.
func TestIdentityChangesAreAudited(t *testing.T) {
	e := newEnv(t, defaultNow)
	a := e.ownerActor(t)
	var rows int
	var leaked bool
	require.NoError(t, e.app.DB.InActorTx(context.Background(), a, func(ctx context.Context, tx pgx.Tx) error {
		// What a compromised API process could do directly: swap in another well-formed hash.
		if _, err := tx.Exec(ctx, `UPDATE iam.user_credentials SET password_hash = password_hash || 'x' WHERE user_id = $1`, a.UserID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "\n\t\t\tSELECT count(*), coalesce(bool_or(coalesce(new_values::text, '') || coalesce(old_values::text, '') LIKE '%argon2%'), false)\n\t\t\tFROM audit.audit_log WHERE entity_table = 'user_credentials' AND entity_id = $1\n\t\t\t  AND 'password_hash' = ANY (changed_columns)", a.UserID).Scan(&rows, &leaked)
	}))
	require.Equal(t, 1, rows)
	require.False(t, leaked)
}

// A failed sign-in counter is not an identity change and must not flood the trail.
func TestFailedSignInIsNotAnIdentityChange(t *testing.T) {
	e := newEnv(t, defaultNow)
	count := func() (n int) {
		require.NoError(t, e.app.DB.InActorTx(context.Background(), e.ownerActor(t), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT count(*) FROM audit.audit_log WHERE entity_table = 'user_credentials'").Scan(&n)
		}))
		return
	}
	before := count()
	c := &client{app: e.app.Fiber}
	require.Equal(t, 401, c.call(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"identifier": e.phone, "password": "wrong-password"}, "").code)
	require.Equal(t, before, count())
}

func archive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, n := range names {
		f, err := w.Create(n)
		require.NoError(t, err)
		_, err = f.Write([]byte("content"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestOnlyMacroFreeOfficeArchivesAreAccepted(t *testing.T) {
	e := newEnv(t, defaultNow)
	for _, c := range []struct {
		name string
		body []byte
		want int
	}{
		{"report.docx", archive(t, "[Content_Types].xml", "word/document.xml"), 201},
		{"stock.xlsx", archive(t, "[Content_Types].xml", "xl/workbook.xml"), 201},
		{"macro.docx", archive(t, "[Content_Types].xml", "word/document.xml", "word/vbaProject.bin"), 415},
		{"payload.zip", archive(t, "setup.exe"), 415},
		{"lookalike.zip", archive(t, "word/readme.txt"), 415},
		{"broken.zip", []byte("PK\x03\x04 not really a zip"), 415},
	} {
		code, raw := upload(t, e.app.Fiber, e.owner, "/api/v1/files", c.name, "application/zip", c.body)
		require.Equal(t, c.want, code, "%s: %s", c.name, raw)
	}
}

// fakeClamd speaks enough of INSTREAM to report any content containing "infected".
func fakeClamd(t *testing.T) (addr string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				if cmd, err := r.ReadString(0); err != nil || cmd != "zINSTREAM\x00" {
					return
				}
				var body bytes.Buffer
				for {
					var n uint32
					if binary.Read(r, binary.BigEndian, &n) != nil {
						return
					}
					if n == 0 {
						break
					}
					if _, err := io.CopyN(&body, r, int64(n)); err != nil {
						return
					}
				}
				reply := "stream: OK\x00"
				if strings.Contains(body.String(), "infected") {
					reply = "stream: Test.Signature FOUND\x00"
				}
				_, _ = conn.Write([]byte(reply))
			}()
		}
	}()
	return l.Addr().String(), func() { _ = l.Close() }
}

func TestUploadsPassTheVirusScannerWhenConfigured(t *testing.T) {
	e := newEnv(t, defaultNow)
	addr, stop := fakeClamd(t)
	t.Cleanup(stop)
	t.Setenv("CLAMD_ADDR", addr)
	scanned := restart(t)
	owner := &client{app: scanned.Fiber, token: e.owner.token, branch: e.branch}

	code, raw := upload(t, scanned.Fiber, owner, "/api/v1/files", "clean.png", "image/png", pngBytes)
	require.Equal(t, 201, code, raw)
	bad := append(append([]byte{}, pngBytes...), []byte("infected")...)
	code, raw = upload(t, scanned.Fiber, owner, "/api/v1/files", "bad.png", "image/png", bad)
	require.Equal(t, 422, code, raw)
	require.Contains(t, raw, "malware_detected")
	require.NotContains(t, raw, "Test.Signature")

	// Switched on but unreachable: the upload is refused, never stored unchecked.
	stop()
	code, raw = upload(t, scanned.Fiber, owner, "/api/v1/files", "later.png", "image/png", pngBytes)
	require.Equal(t, 503, code, raw)
	require.NotContains(t, raw, addr)
}
