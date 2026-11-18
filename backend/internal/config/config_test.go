package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func base(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
}

func TestLoadDefaults(t *testing.T) {
	base(t)
	c, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", c.HTTPAddr)
	require.Equal(t, c.DatabaseURL, c.MigrateDatabaseURL)
}

func TestLoadRejectsMalformedValues(t *testing.T) {
	base(t)
	t.Setenv("ACCESS_TOKEN_TTL", "fifteen")
	t.Setenv("COOKIE_SECURE", "maybe")
	_, err := Load()
	require.ErrorContains(t, err, "ACCESS_TOKEN_TTL")
	require.ErrorContains(t, err, "COOKIE_SECURE")
}

func TestLoadRequiresSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "short")
	_, err := Load()
	require.ErrorContains(t, err, "JWT_SECRET")
}

func TestProductionGuards(t *testing.T) {
	base(t)
	t.Setenv("APP_ENV", "production")
	_, err := Load()
	require.ErrorContains(t, err, "COOKIE_SECURE")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("CORS_ORIGINS", "*")
	_, err = Load()
	require.ErrorContains(t, err, "CORS_ORIGINS")
	t.Setenv("CORS_ORIGINS", "https://app.example.com")
	_, err = Load()
	require.NoError(t, err)
	t.Setenv("SMTP_HOST", "smtp.example.com")
	_, err = Load()
	require.ErrorContains(t, err, "SMTP_FROM")
}
