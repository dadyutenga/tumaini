package httpx

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgconn"
)

// businessRules maps the SQLSTATEs the database raises for business-rule violations
// (database/migrations/010_foundation.sql) to API errors. The database message is written to be
// shown to the user, so BM codes pass it through; constraint errors never do, because they name
// internal tables and columns.
var businessRules = map[string]struct {
	status int
	code   string
}{
	"BM001": {fiber.StatusConflict, "invalid_transition"},
	"BM002": {fiber.StatusConflict, "day_closed"},
	"BM003": {fiber.StatusConflict, "insufficient_stock"},
	"BM004": {fiber.StatusConflict, "immutable_record"},
	"BM005": {fiber.StatusUnprocessableEntity, "amount_not_allowed"},
	"BM007": {fiber.StatusForbidden, "approval_required"},
	"BM008": {fiber.StatusConflict, "document_integrity"},
}

// FromDB turns a PostgreSQL error into an API error, or returns nil when err is not one we map.
func FromDB(err error) *Error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return nil
	}
	if m, ok := businessRules[pe.Code]; ok {
		return New(m.status, m.code, pe.Message)
	}
	switch pe.Code {
	case "23505":
		return Conflict("duplicate", "That record already exists")
	case "23503":
		return Conflict("reference_conflict", "This record is linked to other records or points to one that does not exist")
	case "23514", "22P02", "22003", "22007", "23502":
		return New(fiber.StatusUnprocessableEntity, "invalid_value", "A value is missing or not allowed")
	case "42501":
		return Forbidden("permission_denied", "You do not have permission to do that")
	}
	return nil
}
