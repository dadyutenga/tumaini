package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"bms/internal/platform/auth"
	"bms/internal/platform/httpx"
)

const mfaRecoveryCodeCount = 10

func (s *Service) mfaKey() ([]byte, string) {
	if len(s.MFAKey) == 32 {
		kid := s.MFAKeyID
		if kid == "" {
			kid = "env-v1"
		}
		return s.MFAKey, kid
	}
	return auth.DeriveKey(string(s.Auth.Secret), "mfa-totp-key:v1"), "derived-v1"
}

func (s *Service) recoveryCodeHash(userID uuid.UUID, raw string) []byte {
	norm := auth.NormalizeRecoveryCode(raw)
	m := hmac.New(sha256.New, s.Pepper)
	m.Write([]byte("mfa-recovery:" + userID.String() + ":" + norm))
	return m.Sum(nil)
}

func (s *Service) hasConfirmedMFA(ctx context.Context, userID uuid.UUID) bool {
	var ok bool
	_ = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM iam.mfa_factors
				 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NOT NULL AND disabled_at IS NULL
			)`, userID).Scan(&ok)
	})
	return ok
}

// isMFARecommended reports whether the caller holds a privileged account where MFA is recommended.
func isMFARecommended(p *auth.Principal) bool {
	return p.IsOwner || p.RoleRank >= 60 || p.Has("security.user.manage") || p.Has("hr.payroll.approve") || p.Has("finance.account.manage")
}

// verifyCurrentPasswordTx checks the user's current password inside an open global transaction
// while enforcing brute-force lockout protection.
func (s *Service) verifyCurrentPasswordTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, password string, now time.Time) error {
	var old string
	var attempts int
	var last *time.Time
	if err := tx.QueryRow(ctx, `SELECT password_hash, failed_attempts, last_failed_at FROM iam.user_credentials WHERE user_id = $1 FOR UPDATE`, userID).Scan(&old, &attempts, &last); err != nil {
		return err
	}
	if attempts >= lockoutThreshold && last != nil && now.Sub(*last) < lockoutFor {
		return httpx.TooMany("too_many_requests", "Too many wrong answers. Wait 15 minutes.")
	}
	if !auth.VerifyPassword(old, password) {
		windowStart := now.Add(-lockoutFor)
		if _, err := tx.Exec(ctx, `
			UPDATE iam.user_credentials
			   SET failed_attempts = CASE WHEN last_failed_at IS NULL OR last_failed_at < $3 THEN 1 ELSE failed_attempts + 1 END,
			       last_failed_at = $2,
			       updated_at = $2
			 WHERE user_id = $1`, userID, now, windowStart); err != nil {
			return err
		}
		return httpx.Unauthorized("invalid_credentials", "Current password is wrong")
	}
	return nil
}

// verifyMFAFactorTx verifies either a TOTP code (with step replay protection) or a single-use recovery code.
func (s *Service) verifyMFAFactorTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, code string, allowRecovery bool, now time.Time) (bool, error) {
	var factorID uuid.UUID
	var ct []byte
	var lastStep *int64
	err := tx.QueryRow(ctx, `
		SELECT id, secret_ciphertext, last_used_step
		  FROM iam.mfa_factors
		 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NOT NULL AND disabled_at IS NULL
		 FOR UPDATE`, userID).Scan(&factorID, &ct, &lastStep)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	key, _ := s.mfaKey()
	secret, err := auth.DecryptSecret(key, ct)
	if err != nil {
		return false, err
	}
	if step, ok := auth.VerifyTOTP(secret, code, now, lastStep); ok {
		if _, err := tx.Exec(ctx, `UPDATE iam.mfa_factors SET last_used_at = $2, last_used_step = $3 WHERE id = $1`, factorID, now, step); err != nil {
			return false, err
		}
		return true, nil
	}
	if !allowRecovery {
		return false, nil
	}
	h := s.recoveryCodeHash(userID, code)
	tag, err := tx.Exec(ctx, `
		UPDATE iam.mfa_recovery_codes
		   SET used_at = $3
		 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`, userID, h, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

type MFAStatusView struct {
	Enabled                bool       `json:"enabled"`
	FactorType             string     `json:"factor_type,omitempty"`
	ConfirmedAt            *time.Time `json:"confirmed_at,omitempty"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"`
	Recommended            bool       `json:"recommended"`
}

func (s *Service) MFAStatus(ctx context.Context, p *auth.Principal) (MFAStatusView, error) {
	out := MFAStatusView{Recommended: isMFARecommended(p)}
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var factorType string
		var confirmedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT factor_type::text, confirmed_at
			  FROM iam.mfa_factors
			 WHERE user_id = $1 AND confirmed_at IS NOT NULL AND disabled_at IS NULL
			 LIMIT 1`, p.UserID).Scan(&factorType, &confirmedAt)
		if err == nil {
			out.Enabled = true
			out.FactorType = factorType
			out.ConfirmedAt = confirmedAt
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM iam.mfa_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, p.UserID).Scan(&out.RecoveryCodesRemaining)
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return out, err
}

type MFASetupResult struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	Issuer     string `json:"issuer"`
}

// StartMFASetup verifies the user's current password and creates a pending TOTP factor.
func (s *Service) StartMFASetup(ctx context.Context, p *auth.Principal, currentPassword string) (MFASetupResult, error) {
	now := s.Clock.Now()
	secret, b32, err := auth.GenerateTOTPSecret()
	if err != nil {
		return MFASetupResult{}, err
	}
	key, keyID := s.mfaKey()
	ct, err := auth.EncryptSecret(key, secret)
	if err != nil {
		return MFASetupResult{}, err
	}
	var account string
	var failure error
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if ferr := s.verifyCurrentPasswordTx(ctx, tx, p.UserID, currentPassword, now); ferr != nil {
			failure = ferr
			return nil
		}
		var already bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM iam.mfa_factors
				 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NOT NULL AND disabled_at IS NULL
			)`, p.UserID).Scan(&already); err != nil {
			return err
		}
		if already {
			failure = httpx.Conflict("mfa_already_enabled", "Two-factor authentication is already enabled")
			return nil
		}
		// Retire any previous unconfirmed setup attempt before inserting the fresh seed.
		if _, err := tx.Exec(ctx, `
			UPDATE iam.mfa_factors SET disabled_at = $2
			 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NULL AND disabled_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO iam.mfa_factors (user_id, factor_type, secret_ciphertext, key_id, created_at)
			VALUES ($1, 'totp', $2, $3, $4)`, p.UserID, ct, keyID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT coalesce(email::text, phone::text, full_name::text) FROM iam.users WHERE id = $1`, p.UserID).Scan(&account)
	})
	if err != nil {
		return MFASetupResult{}, err
	}
	if failure != nil {
		return MFASetupResult{}, failure
	}
	const issuer = "Tumaini"
	return MFASetupResult{
		Secret:     b32,
		OTPAuthURL: auth.TOTPURL(issuer, account, b32),
		Issuer:     issuer,
	}, nil
}

type MFAConfirmResult struct {
	Enabled       bool     `json:"enabled"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// ConfirmMFASetup verifies the first 6-digit TOTP code, enables the factor, and returns one-time recovery codes.
func (s *Service) ConfirmMFASetup(ctx context.Context, p *auth.Principal, code string) (MFAConfirmResult, error) {
	now := s.Clock.Now()
	codes, err := auth.GenerateRecoveryCodes(mfaRecoveryCodeCount)
	if err != nil {
		return MFAConfirmResult{}, err
	}
	var failure error
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var factorID uuid.UUID
		var ct []byte
		qerr := tx.QueryRow(ctx, `
			SELECT id, secret_ciphertext FROM iam.mfa_factors
			 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NULL AND disabled_at IS NULL
			 FOR UPDATE`, p.UserID).Scan(&factorID, &ct)
		if errors.Is(qerr, pgx.ErrNoRows) {
			failure = httpx.BadRequest("mfa_not_pending", "Start two-factor setup first")
			return nil
		}
		if qerr != nil {
			return qerr
		}
		key, _ := s.mfaKey()
		secret, err := auth.DecryptSecret(key, ct)
		if err != nil {
			return err
		}
		step, ok := auth.VerifyTOTP(secret, code, now, nil)
		if !ok {
			failure = httpx.BadRequest("invalid_mfa_code", "Invalid authentication code")
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE iam.mfa_factors SET confirmed_at = $2, last_used_at = $2, last_used_step = $3 WHERE id = $1`,
			factorID, now, step); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.mfa_recovery_codes SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		for _, c := range codes {
			if _, err := tx.Exec(ctx, `INSERT INTO iam.mfa_recovery_codes (user_id, code_hash, created_at) VALUES ($1, $2, $3)`,
				p.UserID, s.recoveryCodeHash(p.UserID, c), now); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE iam.sessions SET mfa_verified_at = $2, reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now)
		return err
	})
	if err != nil {
		return MFAConfirmResult{}, err
	}
	if failure != nil {
		return MFAConfirmResult{}, failure
	}
	s.event(ctx, "mfa_enrolled", &p.UserID, &p.TenantID, "", &p.SessionID, p.IP, p.UserAgent, "")
	return MFAConfirmResult{Enabled: true, RecoveryCodes: codes}, nil
}

// DisableMFA requires both the current password and a valid TOTP or recovery code.
func (s *Service) DisableMFA(ctx context.Context, p *auth.Principal, currentPassword, code string) error {
	now := s.Clock.Now()
	var failure error
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if ferr := s.verifyCurrentPasswordTx(ctx, tx, p.UserID, currentPassword, now); ferr != nil {
			failure = ferr
			return nil
		}
		ok, err := s.verifyMFAFactorTx(ctx, tx, p.UserID, code, true, now)
		if err != nil {
			return err
		}
		if !ok {
			failure = httpx.Unauthorized("invalid_mfa_code", "Invalid authentication code")
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.mfa_factors SET disabled_at = $2 WHERE user_id = $1 AND disabled_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.mfa_recovery_codes SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now)
		return err
	})
	if err != nil {
		return err
	}
	if failure != nil {
		return failure
	}
	s.event(ctx, "mfa_disabled", &p.UserID, &p.TenantID, "", &p.SessionID, p.IP, p.UserAgent, "")
	return nil
}

// RegenerateRecoveryCodes requires the current password and a live TOTP code.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, p *auth.Principal, currentPassword, code string) ([]string, error) {
	now := s.Clock.Now()
	codes, err := auth.GenerateRecoveryCodes(mfaRecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	var failure error
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if ferr := s.verifyCurrentPasswordTx(ctx, tx, p.UserID, currentPassword, now); ferr != nil {
			failure = ferr
			return nil
		}
		ok, err := s.verifyMFAFactorTx(ctx, tx, p.UserID, code, false, now)
		if err != nil {
			return err
		}
		if !ok {
			failure = httpx.Unauthorized("invalid_mfa_code", "Invalid authentication code")
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.mfa_recovery_codes SET used_at = $2 WHERE user_id = $1 AND used_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		for _, c := range codes {
			if _, err := tx.Exec(ctx, `INSERT INTO iam.mfa_recovery_codes (user_id, code_hash, created_at) VALUES ($1, $2, $3)`,
				p.UserID, s.recoveryCodeHash(p.UserID, c), now); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		return nil, failure
	}
	return codes, nil
}

// VerifyMFALogin completes a two-factor sign-in challenge using a TOTP or single-use recovery code.
func (s *Service) VerifyMFALogin(ctx context.Context, mfaToken, code, ip, ua string) (SessionResult, error) {
	invalidMFA := httpx.Unauthorized("invalid_mfa_code", "Invalid or expired verification code")
	claims, err := s.Auth.VerifyMFAChallenge(strings.TrimSpace(mfaToken))
	if err != nil {
		return SessionResult{}, invalidMFA
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return SessionResult{}, invalidMFA
	}
	now := s.Clock.Now()
	var mustChange, disabled, ok bool
	var lockedUntil *time.Time
	err = s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT c.must_change_password, c.locked_until, u.status <> 'active'
			  FROM iam.users u JOIN iam.user_credentials c ON c.user_id = u.id
			 WHERE u.id = $1`, userID).Scan(&mustChange, &lockedUntil, &disabled); err != nil {
			return err
		}
		if disabled || (lockedUntil != nil && lockedUntil.After(now)) {
			return nil
		}
		var verr error
		ok, verr = s.verifyMFAFactorTx(ctx, tx, userID, code, true, now)
		return verr
	})
	if err != nil {
		return SessionResult{}, invalidMFA
	}
	if disabled {
		s.event(ctx, "login_failure", &userID, nil, claims.Identifier, nil, ip, ua, "disabled")
		return SessionResult{}, invalidMFA
	}
	if lockedUntil != nil && lockedUntil.After(now) {
		s.event(ctx, "mfa_challenge_failed", &userID, nil, claims.Identifier, nil, ip, ua, "locked")
		return SessionResult{}, invalidMFA
	}
	if !ok {
		s.event(ctx, "mfa_challenge_failed", &userID, nil, claims.Identifier, nil, ip, ua, "mfa_failed")
		s.failed(ctx, userID, claims.Identifier, ip, ua, now)
		return SessionResult{}, invalidMFA
	}
	tenantID, membershipID, err := s.pickMembership(ctx, userID, claims.TenantID)
	if err != nil {
		s.event(ctx, "login_failure", &userID, nil, claims.Identifier, nil, ip, ua, "membership_inactive")
		return SessionResult{}, invalidMFA
	}
	return s.completeLogin(ctx, userID, tenantID, membershipID, mustChange, claims.Identifier, ip, ua, &now, now)
}

type ReauthResult struct {
	ReauthenticatedAt time.Time `json:"reauthenticated_at"`
	ExpiresIn         int       `json:"expires_in"`
}

// Reauthenticate verifies primary credentials (password, plus MFA code if enrolled) and extends
// the current session's recent-authentication window for sensitive account changes.
func (s *Service) Reauthenticate(ctx context.Context, p *auth.Principal, password, mfaCode string) (ReauthResult, error) {
	now := s.Clock.Now()
	var failure error
	err := s.DB.InGlobalTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if ferr := s.verifyCurrentPasswordTx(ctx, tx, p.UserID, password, now); ferr != nil {
			failure = ferr
			return nil
		}
		var hasMFA bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM iam.mfa_factors
				 WHERE user_id = $1 AND factor_type = 'totp' AND confirmed_at IS NOT NULL AND disabled_at IS NULL
			)`, p.UserID).Scan(&hasMFA); err != nil {
			return err
		}
		if hasMFA {
			ok, err := s.verifyMFAFactorTx(ctx, tx, p.UserID, mfaCode, true, now)
			if err != nil {
				return err
			}
			if !ok {
				failure = httpx.Unauthorized("invalid_mfa_code", "Invalid authentication code")
				return nil
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE iam.user_credentials SET failed_attempts = 0, last_failed_at = NULL, locked_until = NULL, updated_at = $2 WHERE user_id = $1`, p.UserID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE iam.sessions SET reauthenticated_at = $2 WHERE id = $1`, p.SessionID, now)
		return err
	})
	if err != nil {
		return ReauthResult{}, err
	}
	if failure != nil {
		return ReauthResult{}, failure
	}
	return ReauthResult{
		ReauthenticatedAt: now,
		ExpiresIn:         int(s.reauthWindow().Seconds()),
	}, nil
}
