// Package readapi implements paginated read endpoints from module-owned SQL.
// Query inputs are bound parameters; SQL identifiers only come from resource definitions.
package readapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
	"bms/internal/platform/httpx"
	"bms/internal/platform/middleware"
)

type Filter struct{ Column, Kind string }
type Child struct{ Name, SQL string }
type Resource struct {
	Path, Permission, Select, From, Order, Search, Branch, Own, Employee, Date string
	Filters                                                                    map[string]Filter
	Children                                                                   []Child
	Sensitive                                                                  map[string][]string
	Detail                                                                     bool
	Self                                                                       bool
}

func Rows(ctx context.Context, tx db.Querier, sql string, args ...any) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, field := range rows.FieldDescriptions() {
			value := values[i]
			switch v := value.(type) {
			case [16]byte:
				value = uuid.UUID(v).String()
			case pgtype.Numeric:
				value, err = v.Value()
				if err != nil {
					return nil, err
				}
			case time.Time:
				if field.DataTypeOID == pgtype.DateOID {
					value = v.Format("2006-01-02")
				} else {
					value = v.Format(time.RFC3339Nano)
				}
			}
			row[field.Name] = value
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Scope applies the selected branch and the permission's record scope independently
// of tenant RLS. An all-branches request never expands a branch or own grant.
func Scope(p *auth.Principal, permission, branch, own, employee string, args *[]any) (string, error) {
	clauses := []string{"TRUE"}
	bind := func(value any) string { *args = append(*args, value); return fmt.Sprintf("$%d", len(*args)) }
	grant, ok := p.Can(permission)
	if permission == "" {
		grant = auth.Grant{Scope: "own"}
		ok = true
	}
	if !ok {
		return "", httpx.Forbidden("permission_denied", "You do not have permission to view these records")
	}
	if branch != "" {
		if p.BranchID != nil {
			clauses = append(clauses, branch+"="+bind(*p.BranchID))
		}
		if !p.IsOwner && grant.Scope != "all" {
			clauses = append(clauses, branch+"=ANY("+bind(p.BranchIDs)+"::uuid[])")
		}
	}
	if grant.Scope == "own" {
		if employee != "" {
			if p.EmployeeID == nil {
				return "", httpx.Conflict("employee_required", "Link this login to an employee first")
			}
			clauses = append(clauses, employee+"="+bind(*p.EmployeeID))
		} else {
			if own == "" {
				return "", httpx.Forbidden("permission_denied", "This resource does not support own-record access")
			}
			clauses = append(clauses, own+"="+bind(p.UserID))
		}
	}
	return strings.Join(clauses, " AND "), nil
}

func Register(r fiber.Router, database *db.DB, resources ...Resource) {
	for _, resource := range resources {
		if resource.Self {
			r.Get(resource.Path, resource.handler(database, false))
		} else {
			r.Get(resource.Path, middleware.Require(resource.Permission), resource.handler(database, false))
		}
		if resource.Detail {
			r.Get(resource.Path+"/:id", middleware.Require(resource.Permission), resource.handler(database, true))
		}
	}
}

func (res Resource) handler(database *db.DB, detail bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		p := middleware.Principal(c)
		if res.Self {
			res.Permission = ""
		}
		args := []any{}
		where, err := Scope(p, res.Permission, res.Branch, res.Own, res.Employee, &args)
		if err != nil {
			return err
		}
		bind := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
		var id uuid.UUID
		if detail {
			id, err = httpx.UUIDParam(c, "id")
			if err != nil {
				return err
			}
			where += " AND t.id=" + bind(id)
		}
		if q := strings.TrimSpace(c.Query("q")); q != "" && res.Search != "" {
			where += " AND (" + res.Search + ") ILIKE " + bind("%"+q+"%")
		}
		for key, filter := range res.Filters {
			raw := c.Query(key)
			if raw == "" {
				continue
			}
			var value any = raw
			switch filter.Kind {
			case "uuid":
				v, e := uuid.Parse(raw)
				if e != nil {
					return httpx.BadRequest("invalid_filter", key+" must be a UUID")
				}
				value = v
			case "bool":
				if raw != "true" && raw != "false" {
					return httpx.BadRequest("invalid_filter", key+" must be true or false")
				}
				value = raw == "true"
			}
			where += " AND " + filter.Column + "=" + bind(value)
		}
		if res.Date != "" {
			for _, f := range []struct{ key, op string }{{"from", ">="}, {"to", "<="}} {
				if raw := c.Query(f.key); raw != "" {
					if _, e := time.Parse("2006-01-02", raw); e != nil {
						return httpx.BadRequest("invalid_date", f.key+" must be YYYY-MM-DD")
					}
					where += " AND " + res.Date + "::date" + f.op + bind(raw) + "::date"
				}
			}
		}
		page := httpx.PageFrom(c)
		var total int
		var out []map[string]any
		err = database.InTx(c.Context(), p.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
			if !detail {
				if e := tx.QueryRow(ctx, "SELECT count(*) FROM "+res.From+" WHERE "+where, args...).Scan(&total); e != nil {
					return e
				}
			}
			query := "SELECT " + res.Select + " FROM " + res.From + " WHERE " + where
			if res.Order != "" {
				query += " ORDER BY " + res.Order
			}
			if !detail {
				query += " LIMIT " + bind(page.PerPage) + " OFFSET " + bind(page.Offset)
			}
			var e error
			out, e = Rows(ctx, tx, query, args...)
			if e != nil {
				return e
			}
			for _, row := range out {
				for permission, fields := range res.Sensitive {
					if _, ok := p.Can(permission); !ok {
						for _, field := range fields {
							delete(row, field)
						}
					}
				}
			}
			if detail {
				if len(out) == 0 {
					return httpx.NotFound("not_found", "Record not found")
				}
				for _, child := range res.Children {
					items, e := Rows(ctx, tx, child.SQL, id)
					if e != nil {
						return e
					}
					out[0][child.Name] = items
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if detail {
			return c.JSON(httpx.Data(out[0]))
		}
		return c.JSON(httpx.List(out, httpx.Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
	}
}
