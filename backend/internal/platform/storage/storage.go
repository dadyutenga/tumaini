package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type File struct {
	Path string
	Size int64
}

type Store interface {
	Save(ctx context.Context, businessID, name string, r io.Reader, max int64) (File, error)
	Open(ctx context.Context, path string) (io.ReadCloser, error)
}

type Local struct{ Root string }

func NewLocal(root string) (*Local, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &Local{Root: root}, nil
}

func (l *Local) Save(_ context.Context, businessID, name string, r io.Reader, max int64) (File, error) {
	if max <= 0 {
		max = 10 << 20
	}
	buf, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return File{}, err
	}
	if int64(len(buf)) > max {
		return File{}, fmt.Errorf("file exceeds %d bytes", max)
	}
	sum := sha256.Sum256(buf)
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, filepath.Base(name))
	rel := filepath.Join(businessID, hex.EncodeToString(sum[:8])+"_"+safe)
	full := filepath.Join(l.Root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return File{}, err
	}
	if err := os.WriteFile(full, buf, 0o640); err != nil {
		return File{}, err
	}
	return File{Path: rel, Size: int64(len(buf))}, nil
}

func (l *Local) Open(_ context.Context, path string) (io.ReadCloser, error) {
	clean := filepath.Clean(path)
	if strings.Contains(clean, "..") {
		return nil, fmt.Errorf("invalid path")
	}
	return os.Open(filepath.Join(l.Root, clean))
}
