package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	TOTPPeriod = 30
	TOTPDigits = 6
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret creates a 160-bit cryptographically random TOTP seed and its Base32 form.
func GenerateTOTPSecret() ([]byte, string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	return raw, totpEncoding.EncodeToString(raw), nil
}

// TOTPCode computes the RFC 6238 6-digit TOTP code for a given 30-second time step.
func TOTPCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}

// VerifyTOTP checks a 6-digit code within +-1 time step (90s window) and rejects replay
// of any step at or before lastUsedStep.
func VerifyTOTP(secret []byte, rawCode string, now time.Time, lastUsedStep *int64) (int64, bool) {
	code := strings.ReplaceAll(strings.TrimSpace(rawCode), " ", "")
	if len(code) != TOTPDigits {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	cur := now.Unix() / TOTPPeriod
	for _, skew := range []int64{-1, 0, 1} {
		step := cur + skew
		if step < 0 {
			continue
		}
		if lastUsedStep != nil && step <= *lastUsedStep {
			continue
		}
		want := TOTPCode(secret, step)
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// TOTPURL returns a standard otpauth:// URI for authenticator apps.
func TOTPURL(issuer, account, base32Secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	v := url.Values{}
	v.Set("secret", base32Secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// DeriveKey derives a 32-byte AES-256 key with domain separation.
func DeriveKey(secret, domain string) []byte {
	sum := sha256.Sum256([]byte(domain + ":" + secret))
	return sum[:]
}

// EncryptSecret encrypts plaintext with AES-256-GCM (nonce || ciphertext).
func EncryptSecret(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptSecret decrypts an AES-256-GCM payload produced by EncryptSecret.
func DecryptSecret(key, payload []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// Unambiguous Crockford-like alphabet (32 chars => 5 bits/char, 8 chars => 40 bits entropy).
const recoveryAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

// GenerateRecoveryCodes returns n random single-use recovery codes formatted XXXX-XXXX.
func GenerateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	buf := make([]byte, 8)
	for i := 0; i < n; i++ {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var b strings.Builder
		for j, v := range buf {
			if j == 4 {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// NormalizeRecoveryCode strips hyphens/spaces and uppercases a recovery code for hashing.
func NormalizeRecoveryCode(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}
