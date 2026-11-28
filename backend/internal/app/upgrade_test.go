package app_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"bms/database"
	"bms/internal/platform/migrator"
)

// Upgrading an installation with data must keep its business, and must refuse to merge two. Row
// security restricts the table owner that runs migrations, so a migration can read nothing without
// failing; an empty database cannot show that.
func TestUpgradeToOneBusiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := "bms_u_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	server, err := pgx.Connect(ctx, withDatabase(t, templateURL, "postgres"))
	require.NoError(t, err)
	t.Cleanup(func() {
		done, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := server.Exec(done, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
		_ = server.Close(done)
	})
	_, err = server.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	url := withDatabase(t, templateURL, name)

	// The schema as it was before the single-business migrations.
	earlier := fstest.MapFS{}
	files, err := fs.Glob(database.Migrations, "migrations/*.sql")
	require.NoError(t, err)
	for _, f := range files {
		if f < "migrations/340" {
			raw, err := fs.ReadFile(database.Migrations, f)
			require.NoError(t, err)
			earlier[f] = &fstest.MapFile{Data: raw}
		}
	}
	_, err = migrator.Up(ctx, url, earlier, "migrations")
	require.NoError(t, err)

	db, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	defer func() { _ = db.Close(ctx) }()
	_, err = db.Exec(ctx, `INSERT INTO app.tenants (slug, legal_name, timezone, default_locale)
		VALUES ('first', 'First Traders', 'Africa/Kampala', 'en'), ('second', 'Second Traders', 'Africa/Nairobi', 'sw')`)
	require.NoError(t, err)

	_, err = migrator.Up(ctx, url, database.Migrations, "migrations")
	require.ErrorContains(t, err, "multi_tenant_data_present")
	var untouched bool
	require.NoError(t, db.QueryRow(ctx, `SELECT to_regclass('app.business_profile') IS NULL AND to_regclass('app.tenants') IS NOT NULL`).Scan(&untouched))
	require.True(t, untouched, "a refused upgrade changes nothing")

	_, err = db.Exec(ctx, `DELETE FROM app.tenants WHERE slug = 'second'`)
	require.NoError(t, err)
	_, err = migrator.Up(ctx, url, database.Migrations, "migrations")
	require.NoError(t, err)

	var business, zone, locale string
	require.NoError(t, db.QueryRow(ctx, `SELECT name::text, timezone, default_locale FROM app.business_profile`).Scan(&business, &zone, &locale))
	require.Equal(t, []string{"First Traders", "Africa/Kampala", "en"}, []string{business, zone, locale})
}
