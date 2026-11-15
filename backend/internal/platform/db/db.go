package db

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Connect(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() { d.Pool.Close() }

// Querier is the subset of pgx used by repositories. A transaction and the pool both satisfy it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// InTx starts a transaction and sets the tenant for row-level security.
// businessID may be uuid.Nil only for queries that do not touch tenant tables.
func (d *DB) InTx(ctx context.Context, businessID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if businessID != uuid.Nil {
		if _, err := tx.Exec(ctx, "select set_config('app.business_id', $1, true)", businessID.String()); err != nil {
			return err
		}
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func IsUnique(err error) bool {
	var pe *pgconn.PgError
	return err != nil && (errorAs(err, &pe) && pe.Code == "23505")
}

func errorAs(err error, target **pgconn.PgError) bool {
	return asPg(err, target)
}

func asPg(err error, target **pgconn.PgError) bool {
	if err == nil {
		return false
	}
	var pe *pgconn.PgError
	if ok := errorIsPg(err, &pe); ok {
		*target = pe
		return true
	}
	return false
}

func errorIsPg(err error, pe **pgconn.PgError) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if e, ok := err.(*pgconn.PgError); ok {
			*pe = e
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func PgCode(err error) string {
	var pe *pgconn.PgError
	if asPg(err, &pe) {
		return pe.Code
	}
	return ""
}

func Fmt(err error) string { return fmt.Sprint(err) }
