package httpx

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type in struct {
	Name   string          `json:"name" validate:"required,max=5"`
	Amount decimal.Decimal `json:"amount" validate:"dpos"`
	Date   string          `json:"date" validate:"date"`
}

func run(t *testing.T, body, query string) (int, map[string]any) {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	app.Post("/", func(c fiber.Ctx) error {
		var v in
		if err := Bind(c, &v); err != nil {
			return err
		}
		from, to, err := Range(c)
		if err != nil {
			return err
		}
		return OK(c, fiber.Map{"from": from, "to": to}, nil)
	})
	req := httptest.NewRequest("POST", "/"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestBindAndValidate(t *testing.T) {
	code, _ := run(t, `{"name":"ok","amount":"5","date":"2026-11-16"}`, "")
	require.Equal(t, 200, code)
	code, _ = run(t, `{not json`, "")
	require.Equal(t, 400, code)
	code, body := run(t, `{"name":"toolong","amount":"0","date":"16/11/2026"}`, "")
	require.Equal(t, 422, code)
	fields := body["error"].(map[string]any)["fields"].(map[string]any)
	require.Equal(t, "max", fields["name"])
	require.Equal(t, "dpos", fields["amount"])
	require.Equal(t, "date", fields["date"])
	code, _ = run(t, ``, "")
	require.Equal(t, 400, code)
}

func TestRange(t *testing.T) {
	code, _ := run(t, `{"name":"ok","amount":"1"}`, "?from=2026-11-20&to=2026-11-01")
	require.Equal(t, 400, code)
	code, body := run(t, `{"name":"ok","amount":"1"}`, "?from=2026-11-01&to=2026-11-20")
	require.Equal(t, 200, code)
	require.Equal(t, "2026-11-01", body["data"].(map[string]any)["from"])
}

func TestSortWhitelist(t *testing.T) {
	allowed := map[string]string{"created_at": "s.created_at", "total": "s.total"}
	require.Equal(t, "s.total DESC", Sort("-total", allowed, "x"))
	require.Equal(t, "x", Sort("total; DROP TABLE x", allowed, "x"))
}
