package auth

import (
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTokensUseInjectedClockAndRejectExpiry(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tokens := Tokens{Secret: []byte("0123456789abcdef0123456789abcdef"), Access: time.Minute, Now: func() time.Time { return now }}
	id := uuid.New()
	raw, err := tokens.Sign(id, uuid.New(), uuid.New(), now)
	require.NoError(t, err)
	claims, err := tokens.Verify(raw)
	require.NoError(t, err)
	require.Equal(t, id.String(), claims.Subject)
	now = now.Add(time.Minute)
	_, err = tokens.Verify(raw)
	require.Error(t, err)
}
