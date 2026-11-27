package audit

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
)

type Service struct{}

func New() *Service { return &Service{} }

// Record writes an explicit business event through audit.log_event. Row-level changes are captured
// by the database itself (audit.tg_capture triggers); Record is for events that are not a row change
// or that need a name, such as "user.password_reset". The transaction must have been opened with
// db.InActorTx so tenant, user, membership, request id and client address are attributed.
func (s *Service) Record(ctx context.Context, tx pgx.Tx, p *auth.Principal, action, entityType string, entityID uuid.UUID, before, after any) error {
	details := map[string]any{}
	if before != nil {
		details["before"] = redact(before)
	}
	if after != nil {
		details["after"] = redact(after)
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var entity any
	if entityID != uuid.Nil {
		entity = entityID
	}
	ua := p.UserAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}
	_, err = tx.Exec(ctx, `SELECT audit.log_event($1, NULLIF($2,''), $3::uuid, 'success', NULL, $4::jsonb, 'user', NULLIF($5,''))`,
		action, entityType, entity, raw, ua)
	return err
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func redact(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]string{"redacted": "unserialisable"}
	}
	var m any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return walk(m)
}

var sensitiveKeyFragments = []string{
	"password", "passphrase", "pin", "token", "secret",
	"api_key", "apikey", "authorization", "cookie", "credential", "bearer",
	"private_key", "otp", "totp", "recovery_code",
	"cvv", "cvc", "card_number", "iban",
}

func isSensitiveKey(lk string) bool {
	if lk == "pan" || strings.HasSuffix(lk, "_pan") || strings.HasSuffix(lk, "hash") {
		return true
	}
	for _, frag := range sensitiveKeyFragments {
		if strings.Contains(lk, frag) {
			return true
		}
	}
	return false
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			if isSensitiveKey(strings.ToLower(k)) {
				continue
			}
			out[k] = walk(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = walk(t[i])
		}
		return t
	default:
		return v
	}
}
