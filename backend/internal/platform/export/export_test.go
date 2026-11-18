package export

import (
	"bytes"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func table() Table {
	return Table{
		Title:   "Sales by product",
		Columns: []Col{{"name", "Product"}, {"qty", "Qty"}, {"total", "Total"}},
		Rows: []map[string]any{
			{"name": "Rice (5kg)", "qty": decimal.NewFromInt(3), "total": decimal.RequireFromString("7500")},
			{"name": "=HYPERLINK(\"x\")", "qty": decimal.NewFromInt(1), "total": decimal.RequireFromString("100.5")},
		},
		Totals: map[string]any{"name": "Total", "total": decimal.RequireFromString("7600.5")},
	}
}

func TestCSVEscapesFormulasAndAddsTotals(t *testing.T) {
	out := string(table().CSV())
	require.Contains(t, out, "Product,Qty,Total\n")
	require.Contains(t, out, "Rice (5kg),3.00,7500.00\n")
	require.Contains(t, out, `'=HYPERLINK`)
	require.Contains(t, out, "Total,,7600.50\n")
}

func TestXLSXHasNumbers(t *testing.T) {
	raw, err := table().XLSX()
	require.NoError(t, err)
	f, err := excelize.OpenReader(bytes.NewReader(raw))
	require.NoError(t, err)
	v, err := f.GetCellValue("Report", "C3")
	require.NoError(t, err)
	require.Equal(t, "7500", v)
	h, _ := f.GetCellValue("Report", "A2")
	require.Equal(t, "Product", h)
}

func TestPDFIsAPDF(t *testing.T) {
	raw := table().PDF()
	require.True(t, bytes.HasPrefix(raw, []byte("%PDF-1.4")))
	require.True(t, bytes.HasSuffix(raw, []byte("%%EOF\n")))
	require.Contains(t, string(raw), "Sales by product")
	require.Contains(t, string(raw), `Rice \(5kg\)`, "parentheses are escaped")
}
