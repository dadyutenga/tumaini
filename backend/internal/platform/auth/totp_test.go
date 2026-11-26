package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTOTPVerifyAndReplayProtection(t *testing.T) {
	secret, b32, err := GenerateTOTPSecret()
	require.NoError(t, err)
	require.Len(t, secret, 20)
	require.NotEmpty(t, b32)
	require.Contains(t, TOTPURL("Tumaini", "owner@demo.test", b32), "otpauth://totp/")

	now := time.Date(2026, 11, 27, 10, 0, 15, 0, time.UTC)
	step := now.Unix() / TOTPPeriod
	code := TOTPCode(secret, step)
	require.Len(t, code, 6)

	gotStep, ok := VerifyTOTP(secret, code, now, nil)
	require.True(t, ok)
	require.Equal(t, step, gotStep)

	// Replaying the same step must be rejected.
	_, ok = VerifyTOTP(secret, code, now, &gotStep)
	require.False(t, ok)

	// Next 30s step succeeds.
	nextNow := now.Add(30 * time.Second)
	nextCode := TOTPCode(secret, step+1)
	nextStep, ok := VerifyTOTP(secret, nextCode, nextNow, &gotStep)
	require.True(t, ok)
	require.Equal(t, step+1, nextStep)

	// Wrong code fails.
	_, ok = VerifyTOTP(secret, "000000", now, nil)
	if code != "000000" {
		require.False(t, ok)
	}
}

func TestEncryptDecryptSecretAndRecoveryCodes(t *testing.T) {
	key := DeriveKey("super-secret-jwt-key-at-least-32-chars", "mfa-totp-key:v1")
	plain := []byte("01234567890123456789")
	ct, err := EncryptSecret(key, plain)
	require.NoError(t, err)
	require.NotEqual(t, plain, ct)

	got, err := DecryptSecret(key, ct)
	require.NoError(t, err)
	require.Equal(t, plain, got)

	badKey := DeriveKey("another-key-0123456789012345678901234", "mfa-totp-key:v1")
	_, err = DecryptSecret(badKey, ct)
	require.Error(t, err)

	codes, err := GenerateRecoveryCodes(10)
	require.NoError(t, err)
	require.Len(t, codes, 10)
	require.Len(t, NormalizeRecoveryCode(codes[0]), 8)
}

func TestMFAChallengeTokenIsolation(t *testing.T) {
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	toks := Tokens{Secret: []byte("0123456789abcdef0123456789abcdef"), Access: 15 * time.Minute, Now: func() time.Time { return now }}
	uid, tid, mid, sid := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	challenge, err := toks.SignMFAChallenge(uid, &tid, "255700000001", now)
	require.NoError(t, err)
	// Challenge token must NEVER validate as a normal access token.
	_, err = toks.Verify(challenge)
	require.Error(t, err)
	claims, err := toks.VerifyMFAChallenge(challenge)
	require.NoError(t, err)
	require.Equal(t, uid.String(), claims.Subject)

	// Access token must NEVER validate as an MFA challenge token.
	access, err := toks.Sign(uid, tid, mid, sid, now)
	require.NoError(t, err)
	_, err = toks.VerifyMFAChallenge(access)
	require.Error(t, err)
}

func TestVerifyPasswordGuardsMalformedHashes(t *testing.T) {
	require.False(t, VerifyPassword("$argon2id$v=19$m=0,t=1,p=1$AAAA$AAAA", "secret"))
	require.False(t, VerifyPassword("$argon2id$v=19$m=8192,t=0,p=1$AAAA$AAAA", "secret"))
	require.False(t, VerifyPassword("$argon2id$v=19$m=8192,t=1,p=0$AAAA$AAAA", "secret"))
	require.False(t, VerifyPassword("$argon2id$v=19$m=8192,t=1,p=1$$", "secret"))
	_, err := HashPassword("secret-pass", Params{})
	require.Error(t, err)
}
