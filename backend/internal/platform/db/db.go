package db

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	// Pool connects as the API role (bms_api, member of bms_app): row-level security always applies.
	Pool *pgxpool.Pool
	// Jobs connects as the worker role (bms_jobs, member of bms_worker). It is used only for the few
	// operations the schema reserves for workers. Nil means "use Pool".
	Jobs *pgxpool.Pool
}

// Connect opens the API pool, and the worker pool when jobsURL is not empty.
func Connect(ctx context.Context, url string, jobsURL ...string) (*DB, error) {
	pool, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	d := &DB{Pool: pool}
	if len(jobsURL) > 0 && jobsURL[0] != "" {
		if d.Jobs, err = open(ctx, jobsURL[0]); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return d, nil
}

func open(ctx context.Context, url string) (*pgxpool.Pool, error) {
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
	return pool, nil
}

func (d *DB) Close() {
	d.Pool.Close()
	if d.Jobs != nil {
		d.Jobs.Close()
	}
}

// Querier is the subset of pgx used by repositories. A transaction and the pool both satisfy it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Actor is the request context the schema's row-level security and triggers read.
// Every field is optional; a zero value is simply not set.
type Actor struct {
	UserID       uuid.UUID
	MembershipID uuid.UUID
	RequestID    string
	ClientIP     string
}

func nilIfZero(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// setContext writes the app.* settings for this transaction only (set_config with is_local),
// which is what keeps them from leaking between requests on a pooled connection.
func setContext(ctx context.Context, tx pgx.Tx, a Actor) error {
	ip := a.ClientIP
	if net.ParseIP(ip) == nil {
		ip = ""
	}
	_, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true),
		set_config('app.membership_id', $2, true), set_config('app.request_id', $3, true), set_config('app.client_ip', $4, true)`,
		nilIfZero(a.UserID), nilIfZero(a.MembershipID), a.RequestID, ip)
	return err
}

func inTx(ctx context.Context, pool *pgxpool.Pool, a Actor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return inTxOpts(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, a, fn)
}

func inTxOpts(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, a Actor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setContext(ctx, tx, a); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InActorTx runs fn in a transaction with the full request context set. Use it for everything done on
// behalf of a signed-in member so created_by, updated_by and the audit trail are attributed.
func (d *DB) InActorTx(ctx context.Context, a Actor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return inTx(ctx, d.Pool, a, fn)
}

// InActorReadTx runs fn in a REPEATABLE READ, READ ONLY transaction so multi-query financial
// reports, statements, and summaries observe a single consistent MVCC snapshot without blocking
// concurrent writers or suffering torn / non-repeatable reads.
func (d *DB) InActorReadTx(ctx context.Context, a Actor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return inTxOpts(ctx, d.Pool, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, a, fn)
}

// InGlobalTx runs fn with no actor: for global identity and setup operations.
func (d *DB) InGlobalTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return inTx(ctx, d.Pool, Actor{}, fn)
}

// InJobsTx runs fn on the worker pool. Only code that the schema reserves for workers belongs here.
func (d *DB) InJobsTx(ctx context.Context, a Actor, fn func(ctx context.Context, tx pgx.Tx) error) error {
	pool := d.Jobs
	if pool == nil {
		pool = d.Pool
	}
	return inTx(ctx, pool, a, fn)
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
