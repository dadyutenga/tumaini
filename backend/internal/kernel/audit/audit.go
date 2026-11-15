package audit

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/ids"
)

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) Record(ctx context.Context, tx pgx.Tx, p *auth.Principal, action, entityType string, entityID uuid.UUID, before, after any) error {
	var b, a []byte
	var err error
	if before != nil {
		if b, err = json.Marshal(redact(before)); err != nil {
			return err
		}
	}
	if after != nil {
		if a, err = json.Marshal(redact(after)); err != nil {
			return err
		}
	}
	var branch any
	if p.BranchID != nil {
		branch = *p.BranchID
	}
	var ip any
	if p.IP != "" {
		ip = p.IP
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (id, business_id, branch_id, user_id, action, entity_type, entity_id, before, after, ip, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::inet,$11)
	`, ids.New(), p.BusinessID, branch, p.UserID, action, entityType, entityID, nullJSON(b), nullJSON(a), ip, p.RequestID)
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

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "password") || strings.Contains(lk, "pin") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") || strings.HasSuffix(lk, "hash") {
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
