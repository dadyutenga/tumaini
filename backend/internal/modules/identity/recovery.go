package identity

import (
	"context"
	"crypto/hmac"
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

// resetHash is HMAC-SHA256(user id : code) keyed with the server pepper, the form
// iam.password_reset_requests.code_hash requires. A leaked table cannot be brute-forced offline
// without the pepper.
func (r *Recovery) resetHash(userID uuid.UUID, code string) []byte {
	m := hmac.New(sha256.New, r.Svc.Pepper)
	m.Write([]byte(userID.String() + ":" + code))
	return m.Sum(nil)
}

func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// Forgot creates a code and sends it to the identifier the user typed.
func (r *Recovery) Forgot(ctx context.Context, identifier, ip string) error {
	identifier = strings.TrimSpace(identifier)
	isEmail := IsEmail(identifier)
	channel, kind := r.SMS, "sms"
	if isEmail {
		channel, kind = r.Email, "email"
	}
	if channel == nil {
		return httpx.New(fiber.StatusServiceUnavailable, "recovery_unavailable", "Password recovery by this channel is not set up. Ask your administrator to reset your password.")
	}
	u, err := r.Svc.findUser(ctx, identifier)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && u.Disabled) {
		return nil // never reveal whether an account exists
	}
	if err != nil {
		return err
	}
	code, err := sixDigits()
	if err != nil {
		return err
	}
	now := r.Svc.Clock.Now()
	send := false
	err = r.Svc.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM iam.password_reset_requests WHERE user_id=$1 AND created_at > $2`, u.ID, now.Add(-time.Hour)).Scan(&recent); err != nil {
			return err
		}
		if recent >= resetMaxPerHour {
			return nil
		}
		// A new code replaces any earlier unused one.
		if _, err := tx.Exec(ctx, `UPDATE iam.password_reset_requests SET superseded_at=$2 WHERE user_id=$1 AND consumed_at IS NULL AND superseded_at IS NULL`, u.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.password_reset_requests (user_id, channel, code_hash, expires_at, created_at, requested_ip)
			VALUES ($1, $2::iam.otp_channel, $3, $4, $5, NULLIF($6,'')::inet)`,
			u.ID, kind, r.resetHash(u.ID, code), now.Add(resetTTL), now, ip); err != nil {
			return err
		}
		send = true
		return nil
	})
	if err != nil || !send {
		return err
	}
	r.Svc.event(ctx, "password_reset_requested", &u.ID, nil, identifier, nil, ip, "", "")
	msg := sender.Message{To: identifier, Subject: "Your password reset code", Body: "Your Tumaini password reset code is " + code + ". It expires in 15 minutes. If you did not ask for it, ignore this message."}
	if err := channel.Send(ctx, msg); err != nil {
		// Do not reveal delivery failures for a real account; log them for support.
		slog.Error("password reset delivery failed", "user_id", u.ID, "err", err)
	}
	return nil
}

var errInvalidCode = httpx.BadRequest("invalid_code", "The code is wrong or has expired. Ask for a new one.")

// Reset checks the code, sets the new password and ends every session.
func (r *Recovery) Reset(ctx context.Context, identifier, code, password, ip string) error {
	if err := auth.PasswordOK(password); err != nil {
		return httpx.BadRequest("weak_password", err.Error())
	}
	u, err := r.Svc.findUser(ctx, strings.TrimSpace(identifier))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && u.Disabled) {
		return errInvalidCode
	}
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password, r.Svc.Hash)
	if err != nil {
		return err
	}
	now := r.Svc.Clock.Now()
	matched := false
	err = r.Svc.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id uuid.UUID
		var stored []byte
		var attempts int
		err := tx.QueryRow(ctx, `
			SELECT id, code_hash, attempts FROM iam.password_reset_requests
			WHERE user_id=$1 AND consumed_at IS NULL AND superseded_at IS NULL AND expires_at > $2
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE`, u.ID, now).Scan(&id, &stored, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(stored, r.resetHash(u.ID, code)) != 1 {
			// Count the miss and retire the code after too many; this must commit.
			_, err := tx.Exec(ctx, `
				UPDATE iam.password_reset_requests SET attempts = attempts + 1,
				       superseded_at = CASE WHEN attempts + 1 >= $2 THEN $3::timestamptz ELSE NULL END WHERE id=$1`, id, resetMaxAttempts, now)
			return err
		}
		matched = true
		if _, err := tx.Exec(ctx, `UPDATE iam.password_reset_requests SET consumed_at=$2 WHERE id=$1`, id, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE iam.user_credentials SET password_hash=$2, password_changed_at=$3, must_change_password=false,
			       failed_attempts=0, last_failed_at=NULL, locked_until=NULL, updated_at=$3 WHERE user_id=$1`, u.ID, hash, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO iam.password_history (user_id, password_hash, created_at) VALUES ($1,$2,$3)`, u.ID, hash, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE iam.sessions SET revoked_at=$2, revoked_reason='password_changed' WHERE user_id=$1 AND revoked_at IS NULL`, u.ID, now)
		return err
	})
	if err != nil {
		return err
	}
	if !matched {
		return errInvalidCode
	}
	r.Svc.event(ctx, "password_reset_completed", &u.ID, nil, identifier, nil, ip, "", "")
	return nil
}

func (r *Recovery) ForgotHandler(c fiber.Ctx) error {
	var body struct {
		Identifier string `json:"identifier" validate:"required,max=200"`
	}
	if err := httpx.Bind(c, &body); err != nil {
		return err
	}
	if err := r.Forgot(c.Context(), body.Identifier, c.IP()); err != nil {
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
	if err := r.Reset(c.Context(), body.Identifier, body.Code, body.Password, c.IP()); err != nil {
		return err
	}
	return c.JSON(httpx.Data(fiber.Map{"status": "password_changed"}))
}
