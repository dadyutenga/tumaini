package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct-horse", Params{Memory: 8192, Time: 1, Threads: 1})
	require.NoError(t, err)
	require.True(t, VerifyPassword(h, "correct-horse"))
	require.False(t, VerifyPassword(h, "wrong-horse"))
	require.False(t, VerifyPassword("garbage", "correct-horse"))
}

func TestPasswordAndPINRules(t *testing.T) {
	require.Error(t, PasswordOK("short"))
	require.Error(t, PasswordOK("password1"))
	require.NoError(t, PasswordOK("a-decent-passphrase"))
	require.Error(t, PINOK("12"))
	require.Error(t, PINOK("12a4"))
	require.NoError(t, PINOK("4321"))
}

func TestTokensUseTheConfiguredClock(t *testing.T) {
	signedAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	now := signedAt
	tok := Tokens{Secret: []byte("0123456789abcdef0123456789abcdef"), Access: 15 * time.Minute, Now: func() time.Time { return now }}
	raw, err := tok.Sign(uuid.New(), uuid.New(), uuid.New(), uuid.New(), signedAt)
	require.NoError(t, err)
	_, err = tok.Verify(raw)
	require.NoError(t, err, "a token is valid at the clock that signed it, whatever the wall time")
	now = signedAt.Add(16 * time.Minute)
	_, err = tok.Verify(raw)
	require.Error(t, err, "expired by the application clock")
	other := Tokens{Secret: []byte("another-secret-another-secret-xx"), Access: time.Minute, Now: tok.Now}
	now = signedAt
	_, err = other.Verify(raw)
	require.Error(t, err, "signature checked")
}

func TestScopeHelpers(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	limit := decimal.NewFromInt(100)
	p := &Principal{UserID: uuid.New(), BranchIDs: []uuid.UUID{a}, Perms: map[string]Grant{
		"sales.view": {Scope: "own"}, "stock.view": {Scope: "branch"}, "reports.sales": {Scope: "all"}, "expenses.create": {Scope: "branch", Limit: &limit},
	}}
	require.Equal(t, []uuid.UUID{a}, p.ScopeBranches("stock.view"))
	require.Nil(t, p.ScopeBranches("reports.sales"))
	require.Equal(t, p.UserID, *p.OwnOnly("sales.view"))
	require.Nil(t, p.OwnOnly("stock.view"))
	require.True(t, p.SeesBranch("stock.view", a))
	require.True(t, p.SeesBranch("stock.view", b), "no per-person location assignment in the approved model")
	require.True(t, p.SeesBranch("reports.sales", b))
	require.False(t, p.SeesBranch("missing.perm", a))
	p.BranchID = &b
	require.Equal(t, []uuid.UUID{b}, p.ScopeBranches("reports.sales"), "an active branch always narrows")
	g, ok := p.Can("expenses.create")
	require.True(t, WithinLimit(g, ok, decimal.NewFromInt(100)))
	require.False(t, WithinLimit(g, ok, decimal.NewFromInt(101)))
	require.False(t, WithinLimit(Grant{}, false, decimal.Zero))
	owner := &Principal{IsOwner: true, Perms: map[string]Grant{}}
	require.Nil(t, owner.ScopeBranches("anything"))
	require.Nil(t, owner.OwnOnly("sales.view"))
	none := &Principal{Perms: map[string]Grant{"stock.view": {Scope: "branch"}}}
	require.NotNil(t, none.ScopeBranches("stock.view"))
	require.Empty(t, none.ScopeBranches("stock.view"), "no branches means no rows, not all rows")
}
