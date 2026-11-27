// Package scan sends uploaded files to a ClamAV daemon before they are stored.
package scan

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"bms/internal/platform/httpx"
	"bms/internal/platform/storage"
)

// Clamd scans with the INSTREAM command of a clamd listening on Addr (host:port).
type Clamd struct {
	Addr    string
	Timeout time.Duration // zero means 30 seconds
}

// Scan returns the name of the signature found in content, or "" when it is clean.
func (c Clamd) Scan(ctx context.Context, content []byte) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return "", err
		}
	}
	w := bufio.NewWriter(conn)
	_, _ = w.WriteString("zINSTREAM\x00")
	for rest := content; len(rest) > 0; {
		n := min(len(rest), 64<<10)
		_ = binary.Write(w, binary.BigEndian, uint32(n))
		_, _ = w.Write(rest[:n])
		rest = rest[n:]
	}
	_ = binary.Write(w, binary.BigEndian, uint32(0))
	if err := w.Flush(); err != nil {
		return "", fmt.Errorf("send to clamd: %w", err)
	}
	reply, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString(0)
	if err != nil {
		return "", fmt.Errorf("read clamd reply: %w", err)
	}
	reply = strings.TrimSpace(strings.TrimRight(reply, "\x00"))
	switch {
	case strings.HasSuffix(reply, " OK"):
		return "", nil
	case strings.HasSuffix(reply, " FOUND"):
		return strings.TrimSuffix(strings.TrimPrefix(reply, "stream: "), " FOUND"), nil
	}
	return "", fmt.Errorf("clamd: %s", reply)
}

// Store saves a file only after Scanner has passed it. When the scanner cannot be reached the upload
// is refused: a scanner that is switched on must not be skipped silently.
type Store struct {
	storage.Store
	Scanner Clamd
}

func (s Store) Save(ctx context.Context, businessID, name string, r io.Reader, max int64) (storage.File, error) {
	content, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return storage.File{}, err
	}
	found, err := s.Scanner.Scan(ctx, content)
	if err != nil {
		slog.Error("virus scan unavailable", "tenant_id", businessID, "err", err)
		return storage.File{}, httpx.ServiceUnavailable("scan_unavailable", "Files cannot be checked right now. Try again in a few minutes.")
	}
	if found != "" {
		slog.Warn("upload rejected by virus scan", "tenant_id", businessID, "signature", found)
		return storage.File{}, httpx.New(422, "malware_detected", "That file was rejected by the virus scanner")
	}
	return s.Store.Save(ctx, businessID, name, bytes.NewReader(content), max)
}
