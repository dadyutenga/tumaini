package payroll

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestMonthlyAcceptance(t *testing.T) {
	slip := Monthly(decimal.RequireFromString("600000"), decimal.RequireFromString("50000"), 26, 2, decimal.RequireFromString("50000"))
	require.True(t, slip.Daily.Equal(decimal.RequireFromString("23076.92")))
	require.True(t, slip.Absence.Equal(decimal.RequireFromString("46153.84")))
	require.True(t, slip.Gross.Equal(decimal.RequireFromString("603846.16")))
	require.True(t, slip.Net.Equal(decimal.RequireFromString("553846.16")))
}
