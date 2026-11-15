package idem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/httpx"
)

type keyType struct{}

type Info struct {
	Key  string
	Hash []byte
}

func New(key string, body []byte) Info {
	sum := sha256.Sum256(body)
	return Info{Key: key, Hash: sum[:]}
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

// Claim locks the idempotency row inside the caller's transaction.
// A replay is returned when the same key and body already completed.
func Claim(ctx context.Context, tx pgx.Tx, businessID, userID uuid.UUID) (*Replay, error) {
	info, ok := From(ctx)
	if !ok {
		return nil, nil
	}
	var hash []byte
	var status *int
	var response []byte
	err := tx.QueryRow(ctx, `
		INSERT INTO idempotency_keys (business_id, user_id, key, request_hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, key) DO UPDATE SET key = idempotency_keys.key
		RETURNING request_hash, status_code, response
	`, businessID, userID, info.Key, info.Hash).Scan(&hash, &status, &response)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(hash, info.Hash) {
		return nil, httpx.Conflict("idempotency_mismatch", "This idempotency key was already used with a different request")
	}
	if status != nil && len(response) > 0 {
		return &Replay{Status: *status, Body: response}, nil
	}
	return nil, nil
}

func Complete(ctx context.Context, tx pgx.Tx, userID uuid.UUID, status int, body any) ([]byte, error) {
	info, ok := From(ctx)
	if !ok {
		raw, err := json.Marshal(body)
		return raw, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE idempotency_keys SET status_code = $3, response = $4
		WHERE user_id = $1 AND key = $2
	`, userID, info.Key, status, raw)
	return raw, err
}
