package sqlq

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuilderNumbersPlaceholdersInOrder(t *testing.T) {
	q := New("fixed")
	q.Add("a = ?", 1).If(false, "skipped = ?", 2).If(true, "b BETWEEN ? AND ?", 3, 4)
	require.Equal(t, " WHERE a = $2 AND b BETWEEN $3 AND $4", q.Where())
	require.Equal(t, " LIMIT $5 OFFSET $6", q.Page(25, 50))
	require.Equal(t, []any{"fixed", 1, 3, 4, 25, 50}, q.Args)
}

func TestEmptyBuilder(t *testing.T) {
	q := New()
	require.Equal(t, "", q.Where())
	require.Equal(t, "", q.And())
	q.Add("x IS NULL")
	require.Equal(t, " AND x IS NULL", q.And())
}

func TestUserInputIsNeverInterpolated(t *testing.T) {
	q := New().Add("name = ?", "'; DROP TABLE users; --")
	require.Equal(t, " WHERE name = $1", q.Where())
	require.Equal(t, "'; DROP TABLE users; --", q.Args[0])
}
