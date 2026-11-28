package org

import (
	"context"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/phone"
)

// BusinessUnit is one of the two businesses and what it prints about itself. An empty field means
// the business profile's value is used.
type BusinessUnit struct {
	ID            uuid.UUID  `json:"id"`
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	Kind          string     `json:"kind"`
	DisplayName   *string    `json:"display_name"`
	LogoFileID    *uuid.UUID `json:"logo_file_id"`
	Phone         *string    `json:"phone"`
	Address       *string    `json:"address"`
	TIN           *string    `json:"tin"`
	ReceiptHeader *string    `json:"receipt_header"`
	ReceiptFooter *string    `json:"receipt_footer"`
}

// BusinessUnitInput changes the fields it names; an empty string clears one back to the profile's.
type BusinessUnitInput struct {
	DisplayName   *string `json:"display_name"`
	Phone         *string `json:"phone"`
	Address       *string `json:"address"`
	TIN           *string `json:"tin"`
	ReceiptHeader *string `json:"receipt_header"`
	ReceiptFooter *string `json:"receipt_footer"`
}

const unitSelect = `
	SELECT id, code, name::text, kind::text, display_name::text, logo_file_id, phone::text, address, tin, receipt_header, receipt_footer
	FROM app.business_units`

func scanUnit(row pgx.CollectableRow) (u BusinessUnit, err error) {
	err = row.Scan(&u.ID, &u.Code, &u.Name, &u.Kind, &u.DisplayName, &u.LogoFileID, &u.Phone, &u.Address, &u.TIN, &u.ReceiptHeader, &u.ReceiptFooter)
	return u, err
}

// BusinessUnits lists the businesses the caller works in.
func (s *Service) BusinessUnits(ctx context.Context, p *auth.Principal) ([]BusinessUnit, error) {
	out := []BusinessUnit{}
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, unitSelect+` WHERE status = 'active' AND code = ANY($1) ORDER BY code`, auth.UnitCodes(p.Units))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanUnit)
		return err
	})
	return out, err
}

func (s *Service) businessUnit(ctx context.Context, tx pgx.Tx, p *auth.Principal, code string) (BusinessUnit, error) {
	if !p.MayUseBusiness(code) {
		return BusinessUnit{}, httpx.NotFound("not_found", "Business not found")
	}
	rows, err := tx.Query(ctx, unitSelect+` WHERE code = $1`, code)
	if err != nil {
		return BusinessUnit{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanUnit)
}

// UpdateBusinessUnit changes what one business prints about itself.
func (s *Service) UpdateBusinessUnit(ctx context.Context, p *auth.Principal, code string, in BusinessUnitInput) (BusinessUnit, error) {
	trim := func(v *string) *string {
		if v == nil {
			return nil
		}
		t := strings.TrimSpace(*v)
		return &t
	}
	in.DisplayName, in.Phone, in.Address, in.TIN = trim(in.DisplayName), trim(in.Phone), trim(in.Address), trim(in.TIN)
	in.ReceiptHeader, in.ReceiptFooter = trim(in.ReceiptHeader), trim(in.ReceiptFooter)
	for field, limit := range map[string]struct {
		value *string
		max   int
	}{
		"display_name": {in.DisplayName, 200}, "address": {in.Address, 500}, "tin": {in.TIN, 40},
		"receipt_header": {in.ReceiptHeader, 500}, "receipt_footer": {in.ReceiptFooter, 500},
	} {
		if limit.value != nil && len([]rune(*limit.value)) > limit.max {
			return BusinessUnit{}, invalid(map[string]string{field: "is too long"})
		}
	}
	if in.Phone != nil && *in.Phone != "" {
		normal, err := phone.Normalize(*in.Phone)
		if err != nil {
			return BusinessUnit{}, invalid(map[string]string{"phone": err.Error()})
		}
		in.Phone = &normal
	}
	var out BusinessUnit
	err := s.DB.InActorTx(ctx, p.Actor(), func(ctx context.Context, tx pgx.Tx) error {
		before, err := s.businessUnit(ctx, tx, p, code)
		if err != nil {
			return err
		}
		// A field that was not sent ($n NULL) keeps its value; an empty one is cleared.
		if _, err := tx.Exec(ctx, `
			UPDATE app.business_units SET
			  display_name   = CASE WHEN $2::text IS NULL THEN display_name   ELSE nullif($2, '') END,
			  phone          = CASE WHEN $3::text IS NULL THEN phone          ELSE nullif($3, '')::app.phone_e164 END,
			  address        = CASE WHEN $4::text IS NULL THEN address        ELSE nullif($4, '') END,
			  tin            = CASE WHEN $5::text IS NULL THEN tin            ELSE nullif($5, '') END,
			  receipt_header = CASE WHEN $6::text IS NULL THEN receipt_header ELSE nullif($6, '') END,
			  receipt_footer = CASE WHEN $7::text IS NULL THEN receipt_footer ELSE nullif($7, '') END
			WHERE code = $1`, code, in.DisplayName, in.Phone, in.Address, in.TIN, in.ReceiptHeader, in.ReceiptFooter); err != nil {
			return err
		}
		if out, err = s.businessUnit(ctx, tx, p, code); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "business_unit.updated", "business_units", out.ID, before, out)
	})
	return out, err
}

// SaveUnitLogo stores the logo one business prints.
func (s *Service) SaveUnitLogo(ctx context.Context, p *auth.Principal, code, name string, r io.Reader) (string, error) {
	if !p.MayUseBusiness(code) {
		return "", httpx.NotFound("not_found", "Business not found")
	}
	id, err := s.store(ctx, p, name, r, 2<<20, true, "public", func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		var unit uuid.UUID
		if err := tx.QueryRow(ctx, `UPDATE app.business_units SET logo_file_id = $2 WHERE code = $1 RETURNING id`, code, id).Scan(&unit); err != nil {
			return err
		}
		return s.Audit.Record(ctx, tx, p, "business_unit.logo_changed", "business_units", unit, nil, map[string]any{"file_id": id})
	})
	return id.String(), err
}
