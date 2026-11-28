package app_test

import (
	"context"
	"io/fs"
	"slices"
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
	// One person with a seat in a business provisioned the way the old release did it, to see that
	// nothing is lost: roles, permissions, limits, numbering and their access.
	_, err = db.Exec(ctx, `
		WITH person AS (INSERT INTO iam.users (full_name, phone) VALUES ('Existing Person', '+255700000123') RETURNING id)
		SELECT app.provision_tenant('first', 'First Traders', person.id) FROM person`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `UPDATE app.tenants SET timezone = 'Africa/Kampala', default_locale = 'en' WHERE slug = 'first'`)
	require.NoError(t, err)
	var changed time.Time
	require.NoError(t, db.QueryRow(ctx, `
		INSERT INTO app.employees (tenant_id, employee_no, full_name, position, start_date, updated_at)
		SELECT id, 'EMP-1', 'Existing Employee', 'Clerk', '2026-01-01', '2026-02-01T00:00:00Z' FROM app.tenants
		RETURNING updated_at`).Scan(&changed))
	_, err = db.Exec(ctx, `INSERT INTO app.tenants (slug, legal_name) VALUES ('second', 'Second Traders')`)
	require.NoError(t, err)

	_, err = migrator.Up(ctx, url, database.Migrations, "migrations")
	require.ErrorContains(t, err, "multi_tenant_data_present")
	var untouched bool
	require.NoError(t, db.QueryRow(ctx, `SELECT to_regclass('app.business_profile') IS NULL AND to_regclass('app.tenants') IS NOT NULL`).Scan(&untouched))
	require.True(t, untouched, "a refused upgrade changes nothing")

	_, err = db.Exec(ctx, `DELETE FROM app.tenants WHERE slug = 'second'`)
	require.NoError(t, err)
	counts, uniques := rowCounts(ctx, t, db), uniqueColumns(ctx, t, db)
	_, err = migrator.Up(ctx, url, database.Migrations, "migrations")
	require.NoError(t, err)

	after := rowCounts(ctx, t, db)
	var compared int
	for table, n := range counts {
		now, kept := after[table]
		// The tenants themselves and the removed sign-in features go; the audit trail may grow.
		if !kept || strings.HasPrefix(table, "audit.") {
			continue
		}
		require.Equal(t, n, now, "rows in %s", table)
		compared++
	}
	require.Greater(t, compared, 50, "most tables survive the upgrade")
	require.Positive(t, counts["app.role_permissions"])

	now := uniqueColumns(ctx, t, db)
	var rewritten int
	for _, u := range uniques {
		if _, kept := after[u.table]; !kept || !slices.Contains(u.columns, "tenant_id") {
			continue
		}
		want := slices.DeleteFunc(slices.Clone(u.columns), func(c string) bool { return c == "tenant_id" })
		if len(want) == 0 {
			continue // one row per tenant is one row in all
		}
		require.True(t, slices.ContainsFunc(now, func(n uniqueIndex) bool {
			return n.table == u.table && slices.Equal(n.columns, want)
		}), "%s lost its unique rule on %v", u.table, want)
		rewritten++
	}
	require.Greater(t, rewritten, 20, "the tenant's unique rules were found")

	var business, zone, locale string
	require.NoError(t, db.QueryRow(ctx, `SELECT name::text, timezone, default_locale FROM app.business_profile`).Scan(&business, &zone, &locale))
	require.Equal(t, []string{"First Traders", "Africa/Kampala", "en"}, []string{business, zone, locale})
	var units, primary string
	require.NoError(t, db.QueryRow(ctx, `
		SELECT string_agg(u.code, ',' ORDER BY u.code), max(u.code) FILTER (WHERE b.is_primary)
		FROM app.membership_business_units b JOIN app.business_units u ON u.id = b.business_unit_id`).Scan(&units, &primary))
	require.Equal(t, []string{"A,B", "A"}, []string{units, primary})
	var paidBy string
	var version int
	var updated time.Time
	require.NoError(t, db.QueryRow(ctx, `
		SELECT u.code, e.row_version, e.updated_at FROM app.employees e JOIN app.business_units u ON u.id = e.business_unit_id`).Scan(&paidBy, &version, &updated))
	require.Equal(t, "A", paidBy)
	require.Equal(t, 1, version, "giving people a business does not edit their records")
	require.True(t, changed.Equal(updated))
}

// rowCounts counts every table of the application, as a superuser so that row security hides none.
func rowCounts(ctx context.Context, t *testing.T, db *pgx.Conn) map[string]int64 {
	t.Helper()
	rows, err := db.Query(ctx, `SELECT format('%I.%I', schemaname, tablename) FROM pg_tables WHERE schemaname IN ('app', 'iam', 'audit')`)
	require.NoError(t, err)
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	out := map[string]int64{}
	for _, table := range tables {
		var n int64
		require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n), table)
		out[table] = n
	}
	return out
}

type uniqueIndex struct {
	table   string
	columns []string // sorted
}

// uniqueColumns lists what every unique index, constraints included, is unique on.
func uniqueColumns(ctx context.Context, t *testing.T, db *pgx.Conn) []uniqueIndex {
	t.Helper()
	rows, err := db.Query(ctx, `
		SELECT format('%I.%I', n.nspname, c.relname), array_agg(pg_get_indexdef(i.indexrelid, k, true))
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		CROSS JOIN LATERAL generate_series(1, i.indnkeyatts::int) k
		WHERE i.indisunique AND n.nspname IN ('app', 'iam', 'audit')
		GROUP BY i.indexrelid, n.nspname, c.relname`)
	require.NoError(t, err)
	defer rows.Close()
	var out []uniqueIndex
	for rows.Next() {
		var u uniqueIndex
		require.NoError(t, rows.Scan(&u.table, &u.columns))
		slices.Sort(u.columns)
		out = append(out, u)
	}
	require.NoError(t, rows.Err())
	return out
}
