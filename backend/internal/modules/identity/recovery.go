package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
	"bms/internal/platform/ids"
	"bms/internal/platform/sender"
)

// Recovery implements forgot password by one-time code (FR-AUTH-09).
// Responses never reveal whether an account exists.
type Recovery struct {
	Svc   *Service
	Email sender.Sender
	SMS   sender.Sender
}

const (
	resetTTL         = 15 * time.Minute
	resetMaxAttempts = 5
	resetMaxPerHour  = 3
)

func resetHash(userID uuid.UUID, code string) []byte {
	sum := sha256.Sum256([]byte(userID.String() + ":" + code))
	return sum[:]
}

func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

type resetUser struct {
	ID, BusinessID uuid.UUID
	Phone          *string
	Email          *string
	Active         bool
}

func (r *Recovery) find(ctx context.Context, identifier string) (*resetUser, error) {
	var u resetUser
	err := r.Svc.DB.Pool.QueryRow(ctx, `SELECT id, business_id, phone, email::text, is_active FROM auth_find_reset_user($1)`, identifier).
		Scan(&u.ID, &u.BusinessID, &u.Phone, &u.Email, &u.Active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !u.Active) {
		return nil, nil
	}
	return &u, err
}

// Forgot creates a code and sends it to the identifier the user typed.
func (r *Recovery) Forgot(ctx context.Context, identifier string) error {
	identifier = strings.TrimSpace(identifier)
	isEmail := strings.Contains(identifier, "@")
	channel := r.SMS
	if isEmail {
		channel = r.Email
	}
	if channel == nil {
		return httpx.New(fiber.StatusServiceUnavailable, "recovery_unavailable", "Password recovery by this channel is not set up. Ask the Owner to reset your password.")
	}
	u, err := r.find(ctx, identifier)
	if err != nil || u == nil {
		return err
	}
	code, err := sixDigits()
	if err != nil {
		return err
	}
	now := r.Svc.Clock.Now()
	send := false
	err = r.Svc.DB.InTx(ctx, u.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM password_resets WHERE user_id=$1 AND created_at > $2`, u.ID, now.Add(-time.Hour)).Scan(&recent); err != nil {
			return err
		}
		if recent >= resetMaxPerHour {
			return nil
		}
		// A new code replaces any earlier unused one.
		if _, err := tx.Exec(ctx, `UPDATE password_resets SET used_at=$2 WHERE user_id=$1 AND used_at IS NULL`, u.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO password_resets (id, business_id, user_id, code_hash, expires_at, created_at) VALUES ($1,$2,$3,$4,$5,$6)
		`, ids.New(), u.BusinessID, u.ID, resetHash(u.ID, code), now.Add(resetTTL), now); err != nil {
			return err
		}
		send = true
		return nil
	})
	if err != nil || !send {
		return err
	}
	msg := sender.Message{To: identifier, Subject: "Your password reset code", Body: "Your Tumaini password reset code is " + code + ". It expires in 15 minutes. If you did not ask for it, ignore this message."}
	if err := channel.Send(ctx, msg); err != nil {
		// Do not reveal delivery failures for a real account; log them for support.
		slog.Error("password reset delivery failed", "user_id", u.ID, "err", err)
	}
	return nil
}

var errInvalidCode = httpx.BadRequest("invalid_code", "The code is wrong or has expired. Ask for a new one.")

// Reset checks the code, sets the new password and ends every session.
func (r *Recovery) Reset(ctx context.Context, identifier, code, password string) error {
	if err := auth.PasswordOK(password); err != nil {
		return httpx.BadRequest("weak_password", err.Error())
	}
	u, err := r.find(ctx, strings.TrimSpace(identifier))
	if err != nil {
		return err
	}
	if u == nil {
		return errInvalidCode
	}
	hash, err := auth.HashPassword(password, r.Svc.Hash)
	if err != nil {
		return err
	}
	now := r.Svc.Clock.Now()
	matched := false
	err = r.Svc.DB.InTx(ctx, u.BusinessID, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var stored []byte
		var attempts int
		err := tx.QueryRow(ctx, `
			SELECT id, code_hash, attempts FROM password_resets
			WHERE user_id=$1 AND used_at IS NULL AND expires_at > $2
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE
		`, u.ID, now).Scan(&id, &stored, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(stored, resetHash(u.ID, code)) != 1 {
			// Count the miss and retire the code after too many; this must commit.
			_, err := tx.Exec(ctx, `UPDATE password_resets SET attempts = attempts + 1, used_at = CASE WHEN attempts + 1 >= $2 THEN $3::timestamptz ELSE NULL END WHERE id=$1`, id, resetMaxAttempts, now)
			return err
		}
		matched = true
		if _, err := tx.Exec(ctx, `UPDATE password_resets SET used_at=$2 WHERE id=$1`, id, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=false, locked_until=NULL WHERE id=$1`, u.ID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, u.ID); err != nil {
			return err
		}
		p := &auth.Principal{UserID: u.ID, BusinessID: u.BusinessID}
		return r.Svc.Audit.Record(ctx, tx, p, "user.password_recovered", "user", u.ID, nil, map[string]any{"id": u.ID})
	})
	if err != nil {
		return err
	}
	if !matched {
		return errInvalidCode
	}
	return nil
}

func (r *Recovery) ForgotHandler(c fiber.Ctx) error {
	var body struct {
		Identifier string `json:"identifier" validate:"required,max=200"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	if err := r.Forgot(c.Context(), body.Identifier); err != nil {
		return err
	}
	return c.Status(fiber.StatusAccepted).JSON(httpx.Data(fiber.Map{"status": "sent_if_account_exists"}))
}

func (r *Recovery) ResetHandler(c fiber.Ctx) error {
	var body struct {
		Identifier string `json:"identifier" validate:"required,max=200"`
		Code       string `json:"code" validate:"required,numeric,len=6"`
		Password   string `json:"new_password" validate:"required"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	if err := r.Reset(c.Context(), body.Identifier, body.Code, body.Password); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "password_changed"}))
}
