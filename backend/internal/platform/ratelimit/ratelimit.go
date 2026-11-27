// Package ratelimit keeps rate-limit counters in PostgreSQL, so that every API process shares one
// budget and a restart does not reset it.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"time"

	"bms/internal/platform/db"
)

// DB counts hits in iam.rate_limits. Scope separates deployments that share a database.
type DB struct {
	DB    *db.DB
	Scope string
}

// Hit records one request against key and returns the count in the current fixed window and the
// seconds until that window ends. The database clock decides, so processes agree.
func (c DB) Hit(ctx context.Context, key string, window time.Duration) (hits, retryAfter int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bucket := sha256.Sum256([]byte(c.Scope + "\n" + key))
	err = c.DB.Pool.QueryRow(ctx, `
		INSERT INTO iam.rate_limits AS r (bucket, window_start, hits) VALUES ($1, now(), 1)
		ON CONFLICT (bucket) DO UPDATE SET
		  window_start = CASE WHEN r.window_start <= now() - make_interval(secs => $2) THEN now() ELSE r.window_start END,
		  hits         = CASE WHEN r.window_start <= now() - make_interval(secs => $2) THEN 1 ELSE r.hits + 1 END
		RETURNING hits, GREATEST(1, ceil(extract(epoch FROM window_start + make_interval(secs => $2) - now())))::int`,
		bucket[:], window.Seconds()).Scan(&hits, &retryAfter)
	return hits, retryAfter, err
}
