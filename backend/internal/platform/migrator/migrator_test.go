package migrator

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// Runs against a disposable database; skipped unless MIGRATOR_TEST_URL points at an empty one
// owned by bms_owner, for example postgres://bms_owner:...@127.0.0.1:5433/bms_scratch.
func TestUpIsOrderedAtomicAndChecksummed(t *testing.T) {
	url := os.Getenv("MIGRATOR_TEST_URL")
	if url == "" {
		t.Skip("MIGRATOR_TEST_URL not set")
	}
	ctx := context.Background()
	files := fstest.MapFS{
		"m/000_boot.sql": {Data: []byte("CREATE SCHEMA IF NOT EXISTS mig_t;")},
		"m/010_a.sql":    {Data: []byte("CREATE TABLE mig_t.a (id int);")},
		"m/020_bad.sql":  {Data: []byte("CREATE TABLE mig_t.b (id int); SELECT 1/0;")},
	}
	applied, err := Up(ctx, url, files, "m")
	require.Error(t, err, "a failing file stops the run")
	require.Equal(t, []string{"000_boot", "010_a"}, applied)

	var exists bool
	c, err := connect(ctx, url)
	require.NoError(t, err)
	defer c.Close(ctx)
	require.NoError(t, c.QueryRow(ctx, "SELECT to_regclass('mig_t.b') IS NOT NULL").Scan(&exists))
	require.False(t, exists, "nothing of the failed file remains")

	st, err := Check(ctx, url, files, "m")
	require.NoError(t, err)
	require.Equal(t, []string{"020_bad"}, st.Pending)

	files["m/010_a.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE mig_t.a (id bigint);")}
	_, err = Up(ctx, url, files, "m")
	require.ErrorContains(t, err, "checksum mismatch")

	_, _ = c.Exec(ctx, "DROP SCHEMA mig_t CASCADE; DELETE FROM public.schema_migrations WHERE version IN ('000_boot','010_a')")
}
