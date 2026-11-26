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
	t.Setenv("SMTP_HOST", "")

	// Default dev DB credentials and weak Argon2id parameters are rejected in production.
	t.Setenv("DATABASE_URL", "postgres://bms_api:bms_api_dev@127.0.0.1:5432/bms")
	_, err = Load()
	require.ErrorContains(t, err, "development credentials")
	t.Setenv("DATABASE_URL", "postgres://bms_api:real-secret@127.0.0.1:5432/bms")

	t.Setenv("ARGON_MEMORY", "8192")
	t.Setenv("ARGON_TIME", "1")
	_, err = Load()
	require.ErrorContains(t, err, "Argon2id")
	t.Setenv("ARGON_MEMORY", "65536")
	t.Setenv("ARGON_TIME", "3")
	_, err = Load()
	require.NoError(t, err)
}

func TestLogLevelAndDefaultsAreValidated(t *testing.T) {
	base(t)
	t.Setenv("LOG_LEVEL", "chatty")
	_, err := Load()
	require.ErrorContains(t, err, "LOG_LEVEL")
	t.Setenv("LOG_LEVEL", "DEBUG")
	_, err = Load()
	require.NoError(t, err, "the level is case-insensitive")
	t.Setenv("DEFAULT_TIMEZONE", "Nowhere/Land")
	_, err = Load()
	require.ErrorContains(t, err, "DEFAULT_TIMEZONE")
	t.Setenv("DEFAULT_TIMEZONE", "Africa/Nairobi")
	t.Setenv("DEFAULT_CURRENCY", "kes")
	_, err = Load()
	require.ErrorContains(t, err, "DEFAULT_CURRENCY")
	t.Setenv("DEFAULT_CURRENCY", "KES")
	c, err := Load()
	require.NoError(t, err)
	require.Equal(t, "Africa/Nairobi", c.DefaultTimezone)
	require.Equal(t, "KES", c.DefaultCurrency)
}
