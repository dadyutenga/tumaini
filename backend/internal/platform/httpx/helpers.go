package httpx

import (
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var validate = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "-" || name == "" {
			return f.Name
		}
		return name
	})
	// decimal.Decimal is a struct; validate it through its string form so
	// tags such as dpos (positive) and dgte0 work on money fields.
	_ = v.RegisterValidation("dpos", func(fl validator.FieldLevel) bool {
		d, ok := fl.Field().Interface().(decimal.Decimal)
		return ok && d.IsPositive()
	})
	_ = v.RegisterValidation("dgte0", func(fl validator.FieldLevel) bool {
		d, ok := fl.Field().Interface().(decimal.Decimal)
		return ok && !d.IsNegative()
	})
	_ = v.RegisterValidation("date", func(fl validator.FieldLevel) bool {
		s := fl.Field().String()
		if s == "" {
			return true
		}
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	})
	return v
}()

// Bind parses a JSON body into dst and runs struct-tag validation.
// A malformed body is 400 invalid_body; a field failure is 422 validation_failed.
func Bind(c fiber.Ctx, dst any) error {
	if len(c.Body()) == 0 {
		return BadRequest("invalid_body", "Request body is required")
	}
	if err := c.Bind().JSON(dst); err != nil {
		return BadRequest("invalid_body", "Request body is not valid JSON")
	}
	return Check(dst)
}

// BindOptional parses a body when one is sent. An empty body leaves dst unchanged.
func BindOptional(c fiber.Ctx, dst any) error {
	if len(c.Body()) == 0 {
		return Check(dst)
	}
	return Bind(c, dst)
}

// Check validates a struct already filled by the caller.
func Check(v any) error {
	if err := validate.Struct(v); err != nil {
		return Validation(err)
	}
	return nil
}

// Send writes a pre-serialised JSON body, as returned by idempotent services.
func Send(c fiber.Ctx, status int, body []byte, err error) error {
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(body)
}

// OK writes {"data": v} with 200, or returns err.
func OK(c fiber.Ctx, v any, err error) error {
	if err != nil {
		return err
	}
	return c.JSON(Data(v))
}

// Created writes {"data": v} with 201, or returns err.
func Created(c fiber.Ctx, v any, err error) error {
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(Data(v))
}

// Paged writes a list with pagination meta, or returns err.
func Paged(c fiber.Ctx, rows any, page Page, total int, err error) error {
	if err != nil {
		return err
	}
	return c.JSON(List(rows, Meta{Page: page.Page, PerPage: page.PerPage, Total: total}))
}

// QueryUUID returns nil when the parameter is absent and 400 when it is not a UUID.
func QueryUUID(c fiber.Ctx, key string) (*uuid.UUID, error) {
	raw := c.Query(key)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, BadRequest("invalid_query", key+" must be a UUID")
	}
	return &id, nil
}

// QueryDate returns "" when absent and 400 when not YYYY-MM-DD.
func QueryDate(c fiber.Ctx, key string) (string, error) {
	raw := c.Query(key)
	if raw == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", raw); err != nil {
		return "", BadRequest("invalid_query", key+" must be a date in YYYY-MM-DD form")
	}
	return raw, nil
}

// Range reads from and to as dates and rejects a range that ends before it starts.
func Range(c fiber.Ctx) (string, string, error) {
	from, err := QueryDate(c, "from")
	if err != nil {
		return "", "", err
	}
	to, err := QueryDate(c, "to")
	if err != nil {
		return "", "", err
	}
	if from != "" && to != "" && to < from {
		return "", "", BadRequest("invalid_query", "to must not be before from")
	}
	return from, to, nil
}

// QueryBool returns nil when absent.
func QueryBool(c fiber.Ctx, key string) *bool {
	switch strings.ToLower(c.Query(key)) {
	case "true", "1", "yes":
		v := true
		return &v
	case "false", "0", "no":
		v := false
		return &v
	}
	return nil
}

// SortFrom applies the whitelist in Sort to the sort query parameter.
func SortFrom(c fiber.Ctx, allowed map[string]string, fallback string) string {
	return Sort(c.Query("sort"), allowed, fallback)
}

// OKList writes a complete, unpaged collection with the standard meta block.
func OKList(c fiber.Ctx, v []map[string]any, err error) error {
	if err != nil {
		return err
	}
	if v == nil {
		v = []map[string]any{}
	}
	return c.JSON(List(v, Meta{Page: 1, PerPage: len(v), Total: len(v)}))
}
