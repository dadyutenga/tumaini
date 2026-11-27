package audit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactStripsCredentialsAndPaymentSecrets(t *testing.T) {
	in := map[string]any{
		"amount":         "5000000",
		"reason":         "Supplier settlement",
		"password":       "hunter2",
		"smtp_password":  "mail-pass",
		"access_token":   "jwt-abc",
		"webhook_secret": "whsec_123",
		"sms_api_key":    "sk_live_999",
		"authorization":  "Bearer secret",
		"card_number":    "4111111111111111",
		"cvv":            "123",
		"pan":            "4111111111111111",
		"totp_code":      "654321",
		"recovery_codes": []string{"ABCD-1234"},
		"password_hash":  "$argon2id$...",
		"nested": map[string]any{
			"safe_ref":    "PAY-001",
			"private_key": "-----BEGIN PRIVATE KEY-----",
			"pin":         "9999",
		},
	}

	got, ok := redact(in).(map[string]any)
	require.True(t, ok)
	require.Equal(t, "5000000", got["amount"])
	require.Equal(t, "Supplier settlement", got["reason"])
	for _, k := range []string{
		"password", "smtp_password", "access_token", "webhook_secret",
		"sms_api_key", "authorization", "card_number", "cvv", "pan",
		"totp_code", "recovery_codes", "password_hash",
	} {
		_, exists := got[k]
		require.False(t, exists, "sensitive key %q must be stripped from audit payload", k)
	}
	nested := got["nested"].(map[string]any)
	require.Equal(t, "PAY-001", nested["safe_ref"])
	require.NotContains(t, nested, "private_key")
	require.NotContains(t, nested, "pin")
}
