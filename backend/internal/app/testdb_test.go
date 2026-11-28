package app_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// The databases the suite was started with, read before any test changes the environment.
// MIGRATE_DATABASE_URL must be a superuser and names the migrated database that is copied.
var (
	templateURL = envOr("MIGRATE_DATABASE_URL", "postgres://postgres:postgres_dev@127.0.0.1:5433/bms?sslmode=disable")
	apiURL      = envOr("DATABASE_URL", "postgres://bms_api:bms_api_dev@127.0.0.1:5433/bms?sslmode=disable")
	jobsURL     = envOr("JOBS_DATABASE_URL", "postgres://bms_jobs:bms_jobs_dev@127.0.0.1:5433/bms?sslmode=disable")
)

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func withDatabase(t *testing.T, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// freshDatabase gives a test a database of its own, because an installation holds exactly one
// business. It copies the migrated database, points the app's environment at the copy and drops it
// when the test ends. Call it before the app is created, so the app closes first.
func freshDatabase(t *testing.T) {
	t.Helper()
	parsed, err := url.Parse(templateURL)
	require.NoError(t, err)
	template := strings.TrimPrefix(parsed.Path, "/")
	name := "bms_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin := withDatabase(t, templateURL, "postgres")

	run := func(statement string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(ctx) }()
		_, err = conn.Exec(ctx, statement)
		return err
	}
	create := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize() + " TEMPLATE " + pgx.Identifier{template}.Sanitize()
	for attempt := 0; ; attempt++ {
		err = run(create)
		var pe *pgconn.PgError
		// 55006: somebody is still connected to the template. It clears in a moment.
		if errors.As(err, &pe) && pe.Code == "55006" && attempt < 50 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}
	require.NoError(t, err, "copy the test database")
	t.Cleanup(func() {
		if err := run("DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})

	t.Setenv("DATABASE_URL", withDatabase(t, apiURL, name))
	t.Setenv("JOBS_DATABASE_URL", withDatabase(t, jobsURL, name))
	t.Setenv("MIGRATE_DATABASE_URL", withDatabase(t, templateURL, name))
}
