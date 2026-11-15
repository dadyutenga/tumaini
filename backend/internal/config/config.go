package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration. Secrets come from the environment.
type Config struct {
	AppEnv            string
	HTTPAddr          string
	DatabaseURL       string
	MigrateDatabaseURL string
	JWTSecret         string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	CORSOrigins       []string
	CookieDomain      string
	CookieSecure      bool
	StorageDriver     string
	StoragePath       string
	DefaultTimezone   string
	DefaultCurrency   string
	LogLevel          string
	ArgonMemory       uint32
	ArgonTime         uint32
	ArgonThreads      uint8
	AutoMigrate       bool
}

func Load() (Config, error) {
	c := Config{
		AppEnv:             getenv("APP_ENV", "development"),
		HTTPAddr:           getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		MigrateDatabaseURL: os.Getenv("MIGRATE_DATABASE_URL"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
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
		AutoMigrate:        boolenv("AUTO_MIGRATE", false),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		return c, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if c.MigrateDatabaseURL == "" {
		c.MigrateDatabaseURL = c.DatabaseURL
	}
	return c, nil
}

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
	if err != nil {
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
