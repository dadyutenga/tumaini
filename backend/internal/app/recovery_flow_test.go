package app_test

import (
	"context"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"bms/internal/platform/sender"
)

type recordingSender struct {
	mu   sync.Mutex
	sent []sender.Message
}

func (r *recordingSender) Send(_ context.Context, m sender.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

func (r *recordingSender) last(t *testing.T) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.sent)
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(r.sent[len(r.sent)-1].Body)
	require.NotEmpty(t, code)
	return code
}

func TestRecoveryForgotAndResetPassword(t *testing.T) {
	e := newEnv(t, defaultNow)
	anon := &client{app: e.app.Fiber}

	// Without a configured channel the endpoint says so, before any account lookup.
	off := anon.call(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"identifier": e.phone}, "")
	require.Equal(t, 503, off.code, off.raw)

	sms := &recordingSender{}
	e.app.Recovery.SMS = sms

	// Unknown accounts get the same answer and nothing is sent.
	unknown := anon.call(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"identifier": "2550000000000"}, "")
	require.Equal(t, 202, unknown.code, unknown.raw)
	require.Empty(t, sms.sent)

	res := anon.call(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"identifier": e.phone}, "")
	require.Equal(t, 202, res.code, res.raw)
	code := sms.last(t)
	require.Equal(t, sms.sent[0].To, e.phone)

	// A wrong code fails; a malformed one is a validation error; a weak password is refused.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	require.Equal(t, 400, anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": wrong, "new_password": "brand-new-pass"}, "").code)
	require.Equal(t, 422, anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": "12", "new_password": "brand-new-pass"}, "").code)
	require.Equal(t, 400, anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": code, "new_password": "short"}, "").code)

	ok := anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": code, "new_password": "brand-new-pass"}, "")
	require.Equal(t, 200, ok.code, ok.raw)
	// The old session ended and the code cannot be reused.
	require.Equal(t, 401, e.owner.call(t, http.MethodGet, "/api/v1/me", nil, "").code)
	require.Equal(t, 400, anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": code, "new_password": "another-pass-1"}, "").code)
	login(t, e.app.Fiber, e.phone, "brand-new-pass")

	// Five wrong guesses retire a code, so the right code no longer works.
	require.Equal(t, 202, anon.call(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"identifier": e.phone}, "").code)
	code = sms.last(t)
	for i := 0; i < 5; i++ {
		guess := "99999" + string(rune('0'+i))
		if guess == code {
			guess = "888888"
		}
		anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": guess, "new_password": "guessed-pass-1"}, "")
	}
	require.Equal(t, 400, anon.call(t, http.MethodPost, "/api/v1/auth/reset-password", map[string]any{"identifier": e.phone, "code": code, "new_password": "guessed-pass-1"}, "").code)
}
