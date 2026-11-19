package setting

import (
	"context"
	"encoding/json"

	"bms/internal/platform/db"
)

func Raw(ctx context.Context, q db.Querier, key string) (json.RawMessage, error) {
	var raw []byte
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func Bool(ctx context.Context, q db.Querier, key string, def bool) bool {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

func String(ctx context.Context, q db.Querier, key, def string) string {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

func Int(ctx context.Context, q db.Querier, key string, def int) int {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v int
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

func Float(ctx context.Context, q db.Querier, key string, def float64) float64 {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return def
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil {
		return def
	}
	return v
}

// Strings reads a JSON array of strings; a missing or malformed value is empty.
func Strings(ctx context.Context, q db.Querier, key string) []string {
	raw, err := Raw(ctx, q, key)
	if err != nil {
		return nil
	}
	var v []string
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}
