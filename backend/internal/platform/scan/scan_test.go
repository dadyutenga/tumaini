package scan

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScanFailsWhenTheDaemonIsUnreachable(t *testing.T) {
	_, err := Clamd{Addr: "127.0.0.1:1", Timeout: time.Second}.Scan(context.Background(), []byte("x"))
	require.Error(t, err)
}

// Runs against a real clamd when CLAMD_TEST_ADDR is set, for example 127.0.0.1:3310.
func TestScanAgainstRealDaemon(t *testing.T) {
	addr := os.Getenv("CLAMD_TEST_ADDR")
	if addr == "" {
		t.Skip("CLAMD_TEST_ADDR is not set")
	}
	c := Clamd{Addr: addr}
	found, err := c.Scan(context.Background(), make([]byte, 10<<20)) // the largest upload
	require.NoError(t, err)
	require.Empty(t, found)
	// The industry test file, assembled here so that scanners do not flag this source file.
	eicar := `X5O!P%@AP[4\PZX54(P^)7CC)7}$` + `EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	found, err = c.Scan(context.Background(), []byte(eicar))
	require.NoError(t, err)
	require.Contains(t, found, "Eicar")
}
