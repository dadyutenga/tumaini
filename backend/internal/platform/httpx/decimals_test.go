package httpx

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type line struct {
	ID       uuid.UUID           `json:"id"`
	Name     string              `json:"name"`
	Price    decimal.Decimal     `json:"price"`
	Cost     *decimal.Decimal    `json:"cost,omitempty"`
	Qty      decimal.NullDecimal `json:"qty"`
	Note     *string             `json:"note"`
	Hidden   string              `json:"-"`
	internal int
	At       time.Time       `json:"at"`
	Raw      json.RawMessage `json:"raw"`
}

type withEmbedded struct {
	line
	Extra decimal.Decimal `json:"extra"`
}

func enc(t *testing.T, v any) string {
	b, err := json.Marshal(Data(v))
	require.NoError(t, err)
	return string(b)
}

func TestDecimalsKeepTheirScale(t *testing.T) {
	money := decimal.RequireFromString("1000").Round(2)
	require.Equal(t, `{"data":{"total":"1000.00"}}`, enc(t, map[string]any{"total": money}))
	require.Equal(t, `{"data":"3.000"}`, enc(t, decimal.RequireFromString("3.000")))
	require.Equal(t, `{"data":"1200.0000"}`, enc(t, decimal.RequireFromString("1200.0000")))
	require.Equal(t, `{"data":"5"}`, enc(t, decimal.NewFromInt(5)), "an integer-scale decimal stays an integer")
	require.Equal(t, `{"data":"0.00"}`, enc(t, decimal.Zero.Round(2)))
	require.Equal(t, `{"data":"0.00"}`, enc(t, decimal.Zero), "a zero with no scale prints as money")
	require.Equal(t, `{"data":"0.000"}`, enc(t, decimal.RequireFromString("0.000")), "a zero that has a scale keeps it")
	require.Equal(t, `{"data":"-12.50"}`, enc(t, decimal.RequireFromString("-12.5").Round(2)))
}

func TestStructsKeepOrderOmitemptyAndCustomTypes(t *testing.T) {
	id := uuid.MustParse("0192f0d5-0000-7000-8000-000000000001")
	at := time.Date(2026, 11, 16, 5, 20, 0, 0, time.UTC)
	cost := decimal.RequireFromString("4.5000")
	l := line{ID: id, Name: "Rice", Price: decimal.RequireFromString("2500").Round(2), Cost: &cost,
		Qty: decimal.NullDecimal{Decimal: decimal.RequireFromString("3").Round(3), Valid: true}, Hidden: "secret", At: at, Raw: json.RawMessage(`{"a":1}`)}
	require.Equal(t,
		`{"data":{"id":"0192f0d5-0000-7000-8000-000000000001","name":"Rice","price":"2500.00","cost":"4.5000","qty":"3.000","note":null,"at":"2026-11-16T05:20:00Z","raw":{"a":1}}}`,
		enc(t, l))

	l.Cost = nil
	l.Qty = decimal.NullDecimal{}
	got := enc(t, []line{l})
	require.NotContains(t, got, `"cost"`, "omitempty drops a nil pointer")
	require.Contains(t, got, `"qty":null`)
	require.NotContains(t, got, "secret")
}

func TestEmbeddedAndNestedValues(t *testing.T) {
	e := withEmbedded{line: line{Name: "x", Price: decimal.RequireFromString("1").Round(2)}, Extra: decimal.RequireFromString("2").Round(2)}
	got := enc(t, map[string]any{"rows": []withEmbedded{e}, "none": nil, "empty": []string{}, "ptr": &e.Extra})
	require.Contains(t, got, `"price":"1.00"`, "embedded struct fields are flattened")
	require.Contains(t, got, `"extra":"2.00"`)
	require.Contains(t, got, `"none":null`)
	require.Contains(t, got, `"empty":[]`)
	require.Contains(t, got, `"ptr":"2.00"`)
	var nilMap map[string]any
	require.Equal(t, `{"data":null}`, enc(t, nilMap))
	require.Equal(t, `{"data":null}`, enc(t, nil))
}

func TestListWrapsMeta(t *testing.T) {
	b, err := json.Marshal(List([]map[string]any{{"n": decimal.RequireFromString("1").Round(2)}}, Meta{Page: 1, PerPage: 25, Total: 1}))
	require.NoError(t, err)
	require.JSONEq(t, `{"data":[{"n":"1.00"}],"meta":{"page":1,"per_page":25,"total":1}}`, string(b))
}
