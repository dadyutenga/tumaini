// Package pdf writes simple A4 text documents: headings, label/value pairs and
// tables. It uses the built-in Helvetica fonts, so no font files are embedded.
// It is enough for invoices, vouchers, payslips, statements and report exports.
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	pageW   = 595.0
	pageH   = 842.0
	margin  = 40.0
	lineGap = 4.0
)

type Doc struct {
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	y     float64
}

func New() *Doc {
	d := &Doc{}
	d.newPage()
	return d
}

func (d *Doc) newPage() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
	d.y = pageH - margin
}

func (d *Doc) ensure(h float64) {
	if d.y-h < margin {
		d.newPage()
	}
}

func (d *Doc) text(x, size float64, bold bool, s string) {
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(d.cur, "BT /%s %.1f Tf %.2f %.2f Td (%s) Tj ET\n", font, size, x, d.y, escape(s))
}

// Heading writes a bold line.
func (d *Doc) Heading(s string, size float64) {
	d.ensure(size + lineGap)
	d.y -= size
	d.text(margin, size, true, s)
	d.y -= lineGap
}

// Text writes a wrapped paragraph at 10pt.
func (d *Doc) Text(s string) {
	for _, ln := range wrap(s, width(pageW-2*margin, 10)) {
		d.ensure(10 + lineGap)
		d.y -= 10
		d.text(margin, 10, false, ln)
		d.y -= lineGap
	}
}

// KV writes "label: value" with the label in bold.
func (d *Doc) KV(label, value string) {
	d.ensure(10 + lineGap)
	d.y -= 10
	d.text(margin, 10, true, label)
	d.text(margin+150, 10, false, value)
	d.y -= lineGap
}

// Space adds vertical space.
func (d *Doc) Space(h float64) { d.y -= h }

// Rule draws a horizontal line.
func (d *Doc) Rule() {
	d.ensure(6)
	d.y -= 3
	fmt.Fprintf(d.cur, "0.5 w %.2f %.2f m %.2f %.2f l S\n", margin, d.y, pageW-margin, d.y)
	d.y -= 3
}

// Table writes a header row and body rows. Columns share the page width by
// the given weights; numeric-looking cells are right aligned.
func (d *Doc) Table(header []string, weights []float64, rows [][]string) {
	if len(weights) != len(header) {
		weights = make([]float64, len(header))
		for i := range weights {
			weights[i] = 1
		}
	}
	total := 0.0
	for _, w := range weights {
		total += w
	}
	avail := pageW - 2*margin
	widths := make([]float64, len(weights))
	for i, w := range weights {
		widths[i] = avail * w / total
	}
	row := func(cells []string, bold bool) {
		d.ensure(9 + lineGap)
		d.y -= 9
		x := margin
		for i, w := range widths {
			cell := ""
			if i < len(cells) {
				cell = clip(cells[i], width(w-4, 9))
			}
			cx := x
			if isNumeric(cell) {
				cx = x + w - 4 - float64(len(cell))*9*0.52
			}
			d.text(cx, 9, bold, cell)
			x += w
		}
		d.y -= lineGap
	}
	row(header, true)
	d.Rule()
	for _, r := range rows {
		row(r, false)
	}
}

// Bytes assembles the PDF file.
func (d *Doc) Bytes() []byte {
	var out bytes.Buffer
	offsets := []int{}
	obj := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(d.pages)
	// 1 catalog, 2 pages, 3 font regular, 4 font bold, then page/content pairs.
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, n)
	for i := 0; i < n; i++ {
		kids[i] = fmt.Sprintf("%d 0 R", 5+2*i)
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	for i, p := range d.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", pageW, pageH, 6+2*i))
		content := p.Bytes()
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content)+1, content))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}

// escape converts to WinAnsi bytes and escapes PDF string delimiters.
// Characters outside Latin-1 become '?'.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 32:
			continue
		case r < 128:
			b.WriteRune(r)
		case r < 256:
			fmt.Fprintf(&b, "\\%03o", r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

func width(points, size float64) int {
	n := int(points / (size * 0.52))
	if n < 1 {
		return 1
	}
	return n
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "."
}

func wrap(s string, max int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			if line == "" {
				line = w
			} else if len([]rune(line))+1+len([]rune(w)) <= max {
				line += " " + w
			} else {
				out = append(out, line)
				line = w
			}
		}
		out = append(out, line)
	}
	return out
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == ',' || r == '-' || r == '%':
		default:
			return false
		}
	}
	return digits > 0
}
