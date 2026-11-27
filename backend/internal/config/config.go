package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration. Secrets come from the environment.
type Config struct {
	AppEnv             string
	HTTPAddr           string
	DatabaseURL        string
	MigrateDatabaseURL string
	// JobsDatabaseURL connects as the worker role (bms_jobs); it is used only for tenant provisioning
	// and background jobs. Empty means the API role is used, which the schema does not allow to provision.
	JobsDatabaseURL string
	// SessionIdleTimeout ends a session that has not been used for this long.
	SessionIdleTimeout time.Duration
	// ReauthTTL is how long a recent primary credential check remains valid for sensitive account changes.
	ReauthTTL        time.Duration
	JWTSecret        string
	MFAEncryptionKey string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	CORSOrigins      []string
	CookieDomain     string
	CookieSecure     bool
	StorageDriver    string
	StoragePath      string
	DefaultTimezone  string
	DefaultCurrency  string
	LogLevel         string
	ArgonMemory      uint32
	ArgonTime        uint32
	ArgonThreads     uint8
	// TrustedProxies are the reverse proxies (IPs or CIDRs) whose X-Forwarded-For is believed.
	// Empty means the socket address is used, so rate limits and audit IPs cannot be spoofed.
	TrustedProxies     []string
	SMTPHost           string
	SMTPPort           int
	SMTPUser           string
	SMTPPassword       string
	SMTPFrom           string
	SMSWebhookURL      string
	SMSAPIKey          string
	SMSSender          string
	WhatsAppWebhookURL string
	WhatsAppAPIKey     string
	WhatsAppSender     string
	OutboundLog        bool
	// RateLimitScope separates the shared rate-limit budgets of deployments using one database.
	RateLimitScope string
	// ClamdAddr is the host:port of a ClamAV daemon. When set, every upload must pass it.
	ClamdAddr string
}

func Load() (Config, error) {
	bad = nil
	c := Config{
		AppEnv:             getenv("APP_ENV", "development"),
		HTTPAddr:           getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		MigrateDatabaseURL: os.Getenv("MIGRATE_DATABASE_URL"),
		JobsDatabaseURL:    os.Getenv("JOBS_DATABASE_URL"),
		SessionIdleTimeout: duration("SESSION_IDLE_TIMEOUT", 30*time.Minute),
		ReauthTTL:          duration("REAUTH_TTL", 10*time.Minute),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		MFAEncryptionKey:   os.Getenv("MFA_ENCRYPTION_KEY"),
		AccessTokenTTL:     duration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:    duration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		CORSOrigins:        splitCSV(getenv("CORS_ORIGINS", "http://localhost:5173")),
		CookieDomain:       os.Getenv("COOKIE_DOMAIN"),
		CookieSecure:       boolenv("COOKIE_SECURE", false),
		StorageDriver:      getenv("STORAGE_DRIVER", "local"),
		StoragePath:        getenv("STORAGE_PATH", "./data/files"),
		DefaultTimezone:    getenv("DEFAULT_TIMEZONE", "Africa/Dar_es_Salaam"),
		DefaultCurrency:    getenv("DEFAULT_CURRENCY", "TZS"),
		LogLevel:           getenv("LOG_LEVEL", "info"),
		ArgonMemory:        uint32(intenv("ARGON_MEMORY", 19456)),
		ArgonTime:          uint32(intenv("ARGON_TIME", 2)),
		ArgonThreads:       uint8(intenv("ARGON_THREADS", 1)),
		TrustedProxies:     splitCSV(os.Getenv("TRUSTED_PROXIES")),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           intenv("SMTP_PORT", 587),
		SMTPUser:           os.Getenv("SMTP_USER"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:           os.Getenv("SMTP_FROM"),
		SMSWebhookURL:      os.Getenv("SMS_WEBHOOK_URL"),
		SMSAPIKey:          os.Getenv("SMS_API_KEY"),
		SMSSender:          os.Getenv("SMS_SENDER_ID"),
		WhatsAppWebhookURL: os.Getenv("WHATSAPP_WEBHOOK_URL"),
		WhatsAppAPIKey:     os.Getenv("WHATSAPP_API_KEY"),
		WhatsAppSender:     os.Getenv("WHATSAPP_SENDER"),
		OutboundLog:        boolenv("OUTBOUND_LOG", false),
		RateLimitScope:     os.Getenv("RATE_LIMIT_SCOPE"),
		ClamdAddr:          os.Getenv("CLAMD_ADDR"),
	}
	if len(bad) > 0 {
		return c, fmt.Errorf("invalid configuration: %s", strings.Join(bad, "; "))
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if c.MFAEncryptionKey != "" && len(c.MFAEncryptionKey) < 32 {
		return c, fmt.Errorf("MFA_ENCRYPTION_KEY must be at least 32 characters when set")
	}
	if c.MigrateDatabaseURL == "" {
		c.MigrateDatabaseURL = c.DatabaseURL
	}
	if c.AccessTokenTTL <= 0 || c.AccessTokenTTL > time.Hour {
		return c, fmt.Errorf("ACCESS_TOKEN_TTL must be between 1s and 1h")
	}
	if c.RefreshTokenTTL < c.AccessTokenTTL || c.RefreshTokenTTL > 90*24*time.Hour {
		return c, fmt.Errorf("REFRESH_TOKEN_TTL must be longer than ACCESS_TOKEN_TTL and at most 90 days")
	}
	if c.SessionIdleTimeout < time.Minute {
		return c, fmt.Errorf("SESSION_IDLE_TIMEOUT must be at least 1m")
	}
	if c.ReauthTTL < time.Minute || c.ReauthTTL > time.Hour {
		return c, fmt.Errorf("REAUTH_TTL must be between 1m and 1h")
	}
	if c.ArgonMemory < 8192 || c.ArgonTime < 1 || c.ArgonThreads < 1 {
		return c, fmt.Errorf("ARGON_MEMORY must be at least 8192 and ARGON_TIME, ARGON_THREADS at least 1")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return c, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error")
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		return c, fmt.Errorf("DEFAULT_TIMEZONE %q is not a known timezone", c.DefaultTimezone)
	}
	if len(c.DefaultCurrency) != 3 || strings.ToUpper(c.DefaultCurrency) != c.DefaultCurrency {
		return c, fmt.Errorf("DEFAULT_CURRENCY must be a three-letter code such as TZS")
	}
	if c.StorageDriver != "local" {
		return c, fmt.Errorf("STORAGE_DRIVER %q is not supported; use local", c.StorageDriver)
	}
	if c.ClamdAddr != "" {
		if host, port, err := net.SplitHostPort(c.ClamdAddr); err != nil || host == "" || port == "" {
			return c, fmt.Errorf("CLAMD_ADDR must be host:port")
		}
	}
	if c.SMTPHost != "" && c.SMTPFrom == "" {
		return c, fmt.Errorf("SMTP_FROM is required when SMTP_HOST is set")
	}
	if c.AppEnv == "production" {
		if strings.EqualFold(c.LogLevel, "debug") {
			return c, fmt.Errorf("LOG_LEVEL=debug is not allowed in production")
		}
		if !c.CookieSecure {
			return c, fmt.Errorf("COOKIE_SECURE must be true in production")
		}
		lowerSecret := strings.ToLower(c.JWTSecret)
		for _, badSecret := range []string{"replace-with", "dev-only", "changeme"} {
			if strings.Contains(lowerSecret, badSecret) {
				return c, fmt.Errorf("JWT_SECRET still has an example or development value")
			}
			if c.MFAEncryptionKey != "" && strings.Contains(strings.ToLower(c.MFAEncryptionKey), badSecret) {
				return c, fmt.Errorf("MFA_ENCRYPTION_KEY still has an example or development value")
			}
		}
		for _, url := range []string{c.DatabaseURL, c.JobsDatabaseURL, c.MigrateDatabaseURL} {
			for _, devPass := range []string{"bms_api_dev", "bms_owner_dev", "bms_jobs_dev", "bms_app_dev", "postgres_dev"} {
				if strings.Contains(url, devPass) {
					return c, fmt.Errorf("database URL must not use default development credentials in production")
				}
			}
		}
		argonOK := (c.ArgonMemory >= 19456 && c.ArgonTime >= 2) || (c.ArgonMemory >= 47104 && c.ArgonTime >= 1)
		if !argonOK {
			return c, fmt.Errorf("Argon2id parameters are below OWASP minimums for production (need m>=19456,t>=2 or m>=47104,t>=1)")
		}
		for _, o := range c.CORSOrigins {
			if o == "*" {
				return c, fmt.Errorf("CORS_ORIGINS cannot be * when credentials are allowed")
			}
		}
	}
	return c, nil
}

// DataKey is the secret that data encrypted at rest (TOTP secrets, national ID numbers) is derived
// from, and the key id recorded beside it. Without MFA_ENCRYPTION_KEY it falls back to JWT_SECRET,
// in which case rotating JWT_SECRET makes that data unreadable.
func (c Config) DataKey() (secret, keyID string) {
	if c.MFAEncryptionKey != "" {
		return c.MFAEncryptionKey, "env-v1"
	}
	return c.JWTSecret, "derived-v1"
}

// bad collects malformed variables during Load so they fail start-up instead
// of silently falling back to defaults.
var bad []string

func MustLoad() Config {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func duration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		bad = append(bad, k+" is not a duration")
		return def
	}
	return d
}

func boolenv(k string, def bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		bad = append(bad, k+" is not true or false")
		return def
	}
	return b
}

func intenv(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		bad = append(bad, k+" is not a whole number")
		return def
	}
	return n
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
