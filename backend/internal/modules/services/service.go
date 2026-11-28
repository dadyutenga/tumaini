// Package services is the services business: clients, catalog, projects,
// quotations, invoices, payments, costs, documents, tasks, renewals,
// contractors and design jobs, backed by the approved BMS schema.
package services

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"bms/internal/kernel/audit"
	"bms/internal/kernel/business"
	"bms/internal/kernel/notify"
	"bms/internal/kernel/outbound"
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
	DB       *db.DB
	Audit    *audit.Service
	Notify   *notify.Service
	Outbound *outbound.Service
	Clock    clock.Clock
	Store    storage.Store
	SMS      sender.Sender
	// IDs encrypts national ID numbers before they are stored.
	IDs IDProtector
}

func (s *Service) bizDate(ctx context.Context, tx pgx.Tx) (string, error) {
	d, err := clock.BusinessDate(s.Clock.Now(), setting.String(ctx, tx, "system.timezone", "Africa/Dar_es_Salaam"), setting.String(ctx, tx, "system.business_day_start", "00:00"))
	if err != nil {
		return "", err
	}
	return d.Format("2006-01-02"), nil
}

// tx runs fn in an actor-attributed transaction.
func (s *Service) tx(ctx context.Context, p *auth.Principal, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.DB.InActorTx(ctx, p.Actor(), fn)
}

// globalTx runs fn in a transaction with no signed-in actor (public links, background jobs).
func (s *Service) globalTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return s.DB.InGlobalTx(ctx, fn)
}

func needBranch(p *auth.Principal) error {
	if p.BranchID == nil {
		return httpx.BadRequest("branch_required", "Choose a branch first")
	}
	return nil
}

// servicesBU returns the business's professional_services business unit, falling back to any active BU.
func (s *Service) servicesBU(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tSELECT id FROM app.business_units\n\t\tWHERE status = 'active'\n\t\tORDER BY (kind = 'professional_services') DESC, created_at\n\t\tLIMIT 1").Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("services business unit: %w", err)
	}
	return id, nil
}

// resolveMembership resolves an API user or membership UUID to a membership UUID.
func (s *Service) resolveMembership(ctx context.Context, tx pgx.Tx, id *uuid.UUID) (*uuid.UUID, error) {
	if id == nil {
		return nil, nil
	}
	var mid uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tSELECT id FROM app.memberships\n\t\tWHERE (id = $1 OR user_id = $1)\n\t\tORDER BY (id = $1) DESC LIMIT 1", *id).Scan(&mid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httpx.NotFound("not_found", "Team member not found")
	}
	if err != nil {
		return nil, err
	}
	return &mid, nil
}

// membershipUserID returns the iam.users.id for a membership, or uuid.Nil if not found.
func (s *Service) membershipUserID(ctx context.Context, tx pgx.Tx, membershipID uuid.UUID) uuid.UUID {
	var uid uuid.UUID
	_ = tx.QueryRow(ctx, "SELECT user_id FROM app.memberships WHERE id = $1", membershipID).Scan(&uid)
	return uid
}

// defaultCustomService returns (or lazily creates) a fallback "CUSTOM" service for rows where
// the approved schema requires service_id NOT NULL (projects, quotation_lines) and the caller omitted it.
func (s *Service) defaultCustomService(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, "\n\t\tINSERT INTO app.services (code, name, category, standard_price, status)\n\t\tVALUES ('CUSTOM', 'Custom Service', 'other', 0, 'active')\n\t\tON CONFLICT (code) DO UPDATE SET code = app.services.code\n\t\tRETURNING id").Scan(&id)
	return id, err
}

// header is what the services business prints on its documents.
func (s *Service) header(ctx context.Context, tx pgx.Tx) (business.Header, error) {
	return business.HeaderFor(ctx, tx, auth.Services)
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

// exists reports whether a row of table with id exists.
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
func isDup(err error) bool    { return db.IsUnique(err) }

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

func uuidOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

var alnumOnly = regexp.MustCompile(`[^A-Z0-9]+`)

func serviceCodeFromName(name string) string {
	s := alnumOnly.ReplaceAllString(strings.ToUpper(strings.TrimSpace(name)), "_")
	s = strings.Trim(s, "_")
	if len(s) > 22 {
		s = s[:22]
	}
	if len(s) < 2 {
		s = "SVC"
	}
	return s
}
