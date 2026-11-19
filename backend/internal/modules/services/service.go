// Package services is the services business: clients, catalog, projects,
// quotations, invoices, payments, costs, documents, tasks, renewals,
// contractors and design jobs. Money posts through the shared ledger.
package services

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/ledger"
	"bms/internal/kernel/notify"
	"bms/internal/platform/auth"
	"bms/internal/platform/clock"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/money"
	"bms/internal/platform/sender"
	"bms/internal/platform/setting"
	"bms/internal/platform/storage"
)

type Service struct {
	DB     *db.DB
	Ledger *ledger.Service
	Audit  *audit.Service
	Notify *notify.Service
	Clock  clock.Clock
	Store  storage.Store
	SMS    sender.Sender
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// tx runs fn in a tenant transaction and sets the business date for svc_today(), so
// "overdue" and "days left" follow the application clock and the business timezone.
func (s *Service) tx(ctx context.Context, businessID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.DB.InTx(ctx, businessID, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.bizDate(ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('app.today', $1, true)`, d); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func needBranch(p *auth.Principal) error {
	if p.BranchID == nil {
		return httpx.BadRequest("branch_required", "Choose a branch first")
	}
	return nil
}

func clean(s string) string { return strings.TrimSpace(s) }

// nz turns an empty string into NULL.
func nz(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

// rows runs a query and converts each row to a map keyed by column name.
// Numerics become decimals (JSON strings), dates YYYY-MM-DD, uuids strings.
func rows(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]map[string]any, error) {
	r, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	fields := r.FieldDescriptions()
	out := []map[string]any{}
	for r.Next() {
		vals, err := r.Values()
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, len(vals))
		for i, v := range vals {
			m[fields[i].Name] = convert(fields[i].DataTypeOID, v)
		}
		out = append(out, m)
	}
	return out, r.Err()
}

// one returns the first row or a not-found error.
func one(ctx context.Context, tx pgx.Tx, what, sql string, args ...any) (map[string]any, error) {
	list, err := rows(ctx, tx, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, httpx.NotFound("not_found", what+" not found")
	}
	return list[0], nil
}

func convert(oid uint32, v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case pgtype.Numeric:
		if !t.Valid || t.Int == nil {
			return nil
		}
		return decimal.NewFromBigInt(new(big.Int).Set(t.Int), t.Exp)
	case [16]uint8:
		return uuid.UUID(t)
	case time.Time:
		if oid == pgtype.DateOID {
			return t.Format("2006-01-02")
		}
		return t.UTC()
	}
	return v
}

func num(m map[string]any, k string) decimal.Decimal {
	if d, ok := m[k].(decimal.Decimal); ok {
		return d
	}
	return money.Zero
}

// exists reports whether a row of table with id is visible (RLS limits it to the business).
func exists(ctx context.Context, tx pgx.Tx, table string, id uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

// require404 returns a not-found error when the row is missing.
func require404(ctx context.Context, tx pgx.Tx, table, what string, id uuid.UUID) error {
	ok, err := exists(ctx, tx, table, id)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("not_found", what+" not found")
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// scopeOwn returns the user id when the permission is limited to own records.
func scopeOwn(p *auth.Principal, key string) *uuid.UUID { return p.OwnOnly(key) }

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func dateOrNil(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

func checkDate(field, s string) error {
	if s != "" && !validDate(s) {
		return httpx.BadRequest("invalid_body", field+" must be a date as YYYY-MM-DD")
	}
	return nil
}

func uuidOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}
