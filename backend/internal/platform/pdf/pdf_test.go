package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManyRowsPaginateAndXrefIsConsistent(t *testing.T) {
	d := New()
	d.Heading("Statement", 14)
	d.KV("Customer", "Asha – Duka la Mama")
	rows := make([][]string, 200)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("2026-11-%02d", i%28+1), "Sale", "1,000.00"}
	}
	d.Table([]string{"Date", "Type", "Amount"}, []float64{1, 2, 1}, rows)
	raw := d.Bytes()
	require.Greater(t, bytes.Count(raw, []byte("/Type /Page ")), 1)
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(raw)
	require.NotNil(t, m)
	off, _ := strconv.Atoi(string(m[1]))
	require.True(t, bytes.HasPrefix(raw[off:], []byte("xref")), "startxref points at the xref table")
	for _, om := range regexp.MustCompile(`(\d{10}) 00000 n `).FindAllSubmatch(raw, -1) {
		o, _ := strconv.Atoi(string(om[1]))
		require.Regexp(t, `^\d+ 0 obj`, string(raw[o:o+12]))
	}
	require.Contains(t, string(raw), "Duka la Mama")
	require.Contains(t, string(raw), "Asha ? Duka", "characters outside Latin-1 become ?")
}
