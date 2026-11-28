package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/db"
)

// plaintextKeyID marks rows written before national IDs were encrypted: the "ciphertext" column holds
// the number itself and the HMAC column an unkeyed hash.
const plaintextKeyID = "local-v1"

// IDProtector encrypts national ID numbers (AES-256-GCM) and derives the keyed hash that finds
// duplicates without decrypting.
type IDProtector struct {
	KeyID  string
	encKey []byte
	macKey []byte
}

// NewIDProtector derives separate encryption and hashing keys from secret.
func NewIDProtector(secret, keyID string) IDProtector {
	return IDProtector{KeyID: keyID, encKey: auth.DeriveKey(secret, "pii-national-id:v1"), macKey: auth.DeriveKey(secret, "pii-national-id-hmac:v1")}
}

type protectedID struct {
	cipher []byte
	keyID  any
	hmac   []byte
	last4  any
}

// protect returns the column values for raw; every value is NULL when raw is blank.
func (k IDProtector) protect(raw string) (protectedID, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return protectedID{}, nil
	}
	if len(k.encKey) == 0 {
		return protectedID{}, errors.New("national ID encryption key is not configured")
	}
	cipher, err := auth.EncryptSecret(k.encKey, []byte(s))
	if err != nil {
		return protectedID{}, fmt.Errorf("encrypt national ID: %w", err)
	}
	mac := hmac.New(sha256.New, k.macKey)
	mac.Write([]byte(strings.ToUpper(s)))
	out := protectedID{cipher: cipher, keyID: k.KeyID, hmac: mac.Sum(nil)}
	if alnum := alnumOnly.ReplaceAllString(strings.ToUpper(s), ""); len(alnum) >= 4 {
		out.last4 = alnum[len(alnum)-4:]
	}
	return out, nil
}

// ProtectNationalIDs is the background job that encrypts rows stored in clear text by earlier
// releases. Each row is its own transaction: one that now duplicates a newer record is reported and
// left for a person to merge, without holding back the others.
func (s *Service) ProtectNationalIDs(ctx context.Context, _ json.RawMessage) error {
	var ids []uuid.UUID
	if err := s.globalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT id FROM app.customers WHERE national_id_key_id = $1", plaintextKeyID)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil {
		return err
	}
	for _, id := range ids {
		err := s.globalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var raw []byte
			err := tx.QueryRow(ctx, "SELECT national_id_ciphertext FROM app.customers WHERE id = $1 AND national_id_key_id = $2 FOR UPDATE", id, plaintextKeyID).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			v, err := s.IDs.protect(string(raw))
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "UPDATE app.customers SET national_id_ciphertext = $2, national_id_key_id = $3, national_id_hmac = $4 WHERE id = $1", id, v.cipher, v.keyID, v.hmac)
			return err
		})
		if db.IsUnique(err) {
			slog.Warn("national ID not re-encrypted: another customer has the same number", "customer_id", id)
			continue
		}
		if err != nil {
			return fmt.Errorf("protect national ID of customer %s: %w", id, err)
		}
	}
	return nil
}
