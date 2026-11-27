package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(code, message string) *Error { return New(fiber.StatusBadRequest, code, message) }
func Unauthorized(code, message string) *Error {
	return New(fiber.StatusUnauthorized, code, message)
}
func Forbidden(code, message string) *Error { return New(fiber.StatusForbidden, code, message) }
func NotFound(code, message string) *Error  { return New(fiber.StatusNotFound, code, message) }
func Conflict(code, message string) *Error  { return New(fiber.StatusConflict, code, message) }
func TooMany(code, message string) *Error   { return New(fiber.StatusTooManyRequests, code, message) }
func ServiceUnavailable(code, message string) *Error {
	return New(fiber.StatusServiceUnavailable, code, message)
}

func Validation(err error) *Error {
	fields := map[string]string{}
	var ves validator.ValidationErrors
	if errors.As(err, &ves) {
		for _, fe := range ves {
			fields[strings.ToLower(fe.Field())] = fe.Tag()
		}
	} else {
		fields["body"] = "invalid"
	}
	return &Error{Status: fiber.StatusUnprocessableEntity, Code: "validation_failed", Message: "Check the highlighted fields", Fields: fields}
}

// publicPrefix starts the unauthenticated link routes, /api/v1/public/<resource>/<token>[/...].
const publicPrefix = "/api/v1/public/"

// LogPath is the request path as it may be written to logs: the token of a public link is the
// credential for that link, so it is replaced.
func LogPath(c fiber.Ctx) string { return redactPath(c.Path()) }

func redactPath(path string) string {
	if len(path) <= len(publicPrefix) || !strings.EqualFold(path[:len(publicPrefix)], publicPrefix) {
		return path
	}
	parts := strings.Split(path[len(publicPrefix):], "/")
	if len(parts) > 1 && parts[1] != "" {
		parts[1] = ":token"
	}
	return path[:len(publicPrefix)] + strings.Join(parts, "/")
}

func ErrorHandler(c fiber.Ctx, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return c.Status(e.Status).JSON(fiber.Map{"error": e})
	}
	if e := FromDB(err); e != nil {
		return c.Status(e.Status).JSON(fiber.Map{"error": e})
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		if fe.Code >= fiber.StatusInternalServerError {
			slog.Error("fiber_internal", "err", err, "status", fe.Code, "path", LogPath(c), "request_id", c.GetRespHeader("X-Request-Id"))
			return c.Status(fe.Code).JSON(fiber.Map{"error": Error{Code: "internal_error", Message: "Something went wrong. Quote the request id if you contact support."}})
		}
		return c.Status(fe.Code).JSON(fiber.Map{"error": Error{Code: "http_error", Message: fe.Message}})
	}
	slog.Error("unhandled", "err", err, "path", LogPath(c), "request_id", c.GetRespHeader("X-Request-Id"))
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": Error{Code: "internal_error", Message: "Something went wrong. Quote the request id if you contact support."}})
}

func Data(v any) fiber.Map { return fiber.Map{"data": normalize(v)} }

type Meta struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
}

func List(v any, meta Meta) fiber.Map {
	return fiber.Map{"data": normalize(v), "meta": meta}
}

type Page struct {
	Page    int
	PerPage int
	Offset  int
}

func PageFrom(c fiber.Ctx) Page {
	page := queryInt(c, "page", 1)
	per := queryInt(c, "per_page", 25)
	if page < 1 {
		page = 1
	}
	if per < 1 {
		per = 25
	}
	if per > 100 {
		per = 100
	}
	return Page{Page: page, PerPage: per, Offset: (page - 1) * per}
}

func queryInt(c fiber.Ctx, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func UUIDParam(c fiber.Ctx, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil {
		return uuid.Nil, BadRequest("invalid_id", fmt.Sprintf("%s must be a UUID", name))
	}
	return id, nil
}

// Sort maps a whitelisted public field to a SQL column. The caller never interpolates raw input.
func Sort(raw string, allowed map[string]string, fallback string) string {
	if raw == "" {
		return fallback
	}
	desc := strings.HasPrefix(raw, "-")
	field := strings.TrimPrefix(raw, "-")
	col, ok := allowed[field]
	if !ok {
		return fallback
	}
	if desc {
		return col + " DESC"
	}
	return col + " ASC"
}
