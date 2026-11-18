// Package export turns tabular results into JSON, CSV, Excel or PDF responses.
// Report handlers build a Table and call Respond; ?format= selects the output.
package export

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"bms/internal/platform/httpx"
	"bms/internal/platform/pdf"
)

type Col struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type Table struct {
	Title    string           `json:"title"`
	Subtitle string           `json:"subtitle,omitempty"`
	Columns  []Col            `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Totals   map[string]any   `json:"totals,omitempty"`
}

// Respond writes the table as JSON {data: table+extra}, or as a file when
// format is csv, xlsx or pdf.
func Respond(c fiber.Ctx, t Table, extra map[string]any, err error) error {
	if err != nil {
		return err
	}
	if t.Rows == nil {
		t.Rows = []map[string]any{}
	}
	switch strings.ToLower(c.Query("format")) {
	case "", "json":
		out := map[string]any{"title": t.Title, "columns": t.Columns, "rows": t.Rows}
		if t.Subtitle != "" {
			out["subtitle"] = t.Subtitle
		}
		if t.Totals != nil {
			out["totals"] = t.Totals
		}
		for k, v := range extra {
			out[k] = v
		}
		return c.JSON(httpx.Data(out))
	case "csv":
		return File(c, slug(t.Title)+".csv", "text/csv; charset=utf-8", t.CSV())
	case "xlsx":
		raw, err := t.XLSX()
		if err != nil {
			return err
		}
		return File(c, slug(t.Title)+".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", raw)
	case "pdf":
		return File(c, slug(t.Title)+".pdf", "application/pdf", t.PDF())
	default:
		return httpx.BadRequest("invalid_query", "format must be json, csv, xlsx or pdf")
	}
}

// File sends an attachment.
func File(c fiber.Ctx, name, mime string, body []byte) error {
	c.Set("Content-Type", mime)
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	c.Set("Cache-Control", "no-store")
	return c.Send(body)
}

func (t Table) matrix() [][]string {
	out := make([][]string, 0, len(t.Rows)+1)
	for _, r := range t.Rows {
		out = append(out, t.line(r))
	}
	if len(t.Totals) > 0 {
		out = append(out, t.line(t.Totals))
	}
	return out
}

func (t Table) line(r map[string]any) []string {
	line := make([]string, len(t.Columns))
	for i, col := range t.Columns {
		line[i] = Cell(r[col.Key])
	}
	return line
}

func (t Table) header() []string {
	h := make([]string, len(t.Columns))
	for i, col := range t.Columns {
		h[i] = col.Label
	}
	return h
}

func (t Table) CSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(t.header())
	for _, ln := range t.matrix() {
		_ = w.Write(safeCSV(ln))
	}
	w.Flush()
	return buf.Bytes()
}

// safeCSV prefixes cells that a spreadsheet would treat as a formula.
func safeCSV(cells []string) []string {
	out := make([]string, len(cells))
	for i, s := range cells {
		if s != "" && strings.ContainsRune("=+@\t\r", rune(s[0])) {
			s = "'" + s
		}
		out[i] = s
	}
	return out
}

func (t Table) XLSX() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Report"
	_ = f.SetSheetName("Sheet1", sheet)
	_ = f.SetCellValue(sheet, "A1", t.Title)
	start := 2
	if t.Subtitle != "" {
		_ = f.SetCellValue(sheet, "A2", t.Subtitle)
		start = 3
	}
	for i, h := range t.header() {
		cell, _ := excelize.CoordinatesToCellName(i+1, start)
		_ = f.SetCellValue(sheet, cell, h)
	}
	rows := append([]map[string]any{}, t.Rows...)
	if len(t.Totals) > 0 {
		rows = append(rows, t.Totals)
	}
	for r, row := range rows {
		for i, col := range t.Columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, start+1+r)
			_ = f.SetCellValue(sheet, cell, xlsxValue(row[col.Key]))
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xlsxValue(v any) any {
	switch t := v.(type) {
	case decimal.Decimal:
		f, _ := t.Float64()
		return f
	case *decimal.Decimal:
		if t == nil {
			return ""
		}
		f, _ := t.Float64()
		return f
	case decimal.NullDecimal:
		if !t.Valid {
			return ""
		}
		f, _ := t.Decimal.Float64()
		return f
	case string:
		if t != "" && strings.ContainsRune("=+@", rune(t[0])) {
			return "'" + t
		}
		return t
	case nil:
		return ""
	default:
		return Cell(v)
	}
}

func (t Table) PDF() []byte {
	d := pdf.New()
	d.Heading(t.Title, 14)
	if t.Subtitle != "" {
		d.Text(t.Subtitle)
	}
	d.Space(6)
	d.Table(t.header(), nil, t.matrix())
	return d.Bytes()
}

// Cell formats one value for text output.
func Cell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	case decimal.Decimal:
		return t.StringFixed(2)
	case *decimal.Decimal:
		if t == nil {
			return ""
		}
		return t.StringFixed(2)
	case decimal.NullDecimal:
		if !t.Valid {
			return ""
		}
		return t.Decimal.StringFixed(2)
	case uuid.UUID:
		return t.String()
	case *uuid.UUID:
		if t == nil {
			return ""
		}
		return t.String()
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case *time.Time:
		if t == nil {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	case bool:
		if t {
			return "yes"
		}
		return "no"
	default:
		return fmt.Sprint(v)
	}
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "export"
	}
	return out
}
