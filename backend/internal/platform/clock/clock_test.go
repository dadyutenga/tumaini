package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBusinessDateUsesTimezoneAndDayStart(t *testing.T) {
	// 22:30 UTC on the 16th is 01:30 on the 17th in Dar es Salaam (UTC+3).
	now := time.Date(2026, 11, 16, 22, 30, 0, 0, time.UTC)
	d, err := BusinessDate(now, "Africa/Dar_es_Salaam", "00:00")
	require.NoError(t, err)
	require.Equal(t, "2026-11-17", d.Format("2006-01-02"))
	// With the business day starting at 04:00, 01:30 still belongs to the 16th (BR-15).
	d, err = BusinessDate(now, "Africa/Dar_es_Salaam", "04:00")
	require.NoError(t, err)
	require.Equal(t, "2026-11-16", d.Format("2006-01-02"))
	_, err = BusinessDate(now, "Not/AZone", "00:00")
	require.Error(t, err)
}
