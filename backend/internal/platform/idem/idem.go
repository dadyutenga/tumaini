package idem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"bms/internal/platform/httpx"
)

type keyType struct{}

type Info struct {
	Key    string
	Hash   []byte
	Method string
	Path   string
}

// New describes one request. The hash covers method, path and body, so a key reused for a different
// operation is refused instead of replaying the wrong response.
func New(key string, body []byte, method, path string) Info {
	h := sha256.New()
	h.Write([]byte(method + " " + path + "\n"))
	h.Write(body)
	return Info{Key: key, Hash: h.Sum(nil), Method: method, Path: path}
}

func With(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, keyType{}, info)
}

func From(ctx context.Context) (Info, bool) {
	v, ok := ctx.Value(keyType{}).(Info)
	return v, ok && v.Key != ""
}

type Replay struct {
	Status int
	Body   []byte
}

// Claim records the key in app.idempotency_keys inside the caller's transaction, or finds it.
// A replay is returned when the same key and request already completed.
func Claim(ctx context.Context, tx pgx.Tx) (*Replay, error) {
	info, ok := From(ctx)
	if !ok {
		return nil, nil
	}
	var hash []byte
	var status *int16
	var response []byte
	err := tx.QueryRow(ctx, `
		INSERT INTO app.idempotency_keys ( idempotency_key, membership_id, request_method, request_path, request_hash)
		VALUES ( $1, app.current_membership_id(), $2, $3, $4)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = app.idempotency_keys.idempotency_key
		RETURNING request_hash, response_status, response_body
	`, info.Key, info.Method, info.Path, info.Hash).Scan(&hash, &status, &response)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(hash, info.Hash) {
		return nil, httpx.Conflict("idempotency_mismatch", "This idempotency key was already used with a different request")
	}
	if status != nil && len(response) > 0 {
		return &Replay{Status: int(*status), Body: response}, nil
	}
	return nil, nil
}

// Complete stores the response against the key so a retry gets the same answer.
func Complete(ctx context.Context, tx pgx.Tx, status int, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	info, ok := From(ctx)
	if !ok {
		return raw, nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE app.idempotency_keys SET response_status = $2, response_body = $3
		WHERE idempotency_key = $1`, info.Key, status, raw)
	return raw, err
}
