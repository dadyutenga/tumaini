// Package migrator applies the versioned schema files in order, once each, with checksums.
//
// It keeps the contract of database/migrate.sh so either tool can operate on the same database:
// the version table is public.schema_migrations(version, checksum, applied_at), the bootstrap file
// (000_*) runs as the connecting role, every later file runs inside one transaction as bms_owner,
// and an applied file whose content changed stops the run.
package migrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Applied is one row of schema_migrations.
type Applied struct {
	Version  string
	Checksum string
}

// Status compares the files with the database.
type Status struct {
	Applied  []string
	Pending  []string
	Mismatch []string // applied, but the file has changed since
	Unknown  []string // recorded in the database but missing from the files
}

type file struct {
	version, sql, sum string
}

func load(fsys fs.FS, dir string) ([]file, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, file{version: strings.TrimSuffix(e.Name(), ".sql"), sql: string(b), sum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

const ensureTable = `CREATE TABLE IF NOT EXISTS public.schema_migrations (
  version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`

// advisory lock key: "bmsmig" as a number, so two deploys never migrate at once.
const lockKey = 0x626d736d6967

func appliedMap(ctx context.Context, c *pgx.Conn) (map[string]string, error) {
	rows, err := c.Query(ctx, "SELECT version, checksum FROM public.schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var v, s string
		if err := rows.Scan(&v, &s); err != nil {
			return nil, err
		}
		m[v] = s
	}
	return m, rows.Err()
}

// Up applies every pending file and returns the versions it applied.
// A file is applied atomically: if any statement fails nothing of that file remains and the run stops.
func Up(ctx context.Context, url string, fsys fs.FS, dir string) ([]string, error) {
	files, err := load(fsys, dir)
	if err != nil {
		return nil, err
	}
	conn, err := connect(ctx, url)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(lockKey)); err != nil {
		return nil, err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(lockKey)) //nolint:errcheck
	if _, err := conn.Exec(ctx, ensureTable); err != nil {
		return nil, err
	}
	done, err := appliedMap(ctx, conn)
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, f := range files {
		if sum, ok := done[f.version]; ok {
			if sum != f.sum {
				return applied, fmt.Errorf("checksum mismatch for applied migration %s", f.version)
			}
			continue
		}
		if err := apply(ctx, conn, f); err != nil {
			return applied, fmt.Errorf("migration %s failed (nothing of it was applied): %w", f.version, err)
		}
		applied = append(applied, f.version)
	}
	return applied, nil
}

func apply(ctx context.Context, c *pgx.Conn, f file) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !strings.HasPrefix(f.version, "000_") {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE bms_owner"); err != nil {
			return err
		}
	}
	// Whole file as one simple-protocol batch so function bodies keep their semicolons.
	if _, err := tx.Exec(ctx, f.sql); err != nil {
		return err
	}
	if !strings.HasPrefix(f.version, "000_") {
		if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO public.schema_migrations (version, checksum) VALUES ($1, $2)", f.version, f.sum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Check reports applied, pending, changed and unknown versions without changing anything.
func Check(ctx context.Context, url string, fsys fs.FS, dir string) (Status, error) {
	var st Status
	files, err := load(fsys, dir)
	if err != nil {
		return st, err
	}
	conn, err := connect(ctx, url)
	if err != nil {
		return st, err
	}
	defer conn.Close(ctx)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return st, err
	}
	done := map[string]string{}
	if exists {
		if done, err = appliedMap(ctx, conn); err != nil {
			return st, err
		}
	}
	known := map[string]bool{}
	for _, f := range files {
		known[f.version] = true
		sum, ok := done[f.version]
		switch {
		case !ok:
			st.Pending = append(st.Pending, f.version)
		case sum != f.sum:
			st.Mismatch = append(st.Mismatch, f.version)
		default:
			st.Applied = append(st.Applied, f.version)
		}
	}
	for v := range done {
		if !known[v] {
			st.Unknown = append(st.Unknown, v)
		}
	}
	sort.Strings(st.Unknown)
	return st, nil
}

// Verify fails unless every file is applied unchanged and nothing unknown is recorded.
func Verify(st Status) error {
	switch {
	case len(st.Pending) > 0:
		return fmt.Errorf("pending migrations: %s", strings.Join(st.Pending, ", "))
	case len(st.Mismatch) > 0:
		return fmt.Errorf("changed after being applied: %s", strings.Join(st.Mismatch, ", "))
	case len(st.Unknown) > 0:
		return fmt.Errorf("recorded but not in this build: %s", strings.Join(st.Unknown, ", "))
	}
	return nil
}

func connect(ctx context.Context, url string) (*pgx.Conn, error) {
	if url == "" {
		return nil, errors.New("MIGRATE_DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return pgx.ConnectConfig(ctx, cfg)
}
