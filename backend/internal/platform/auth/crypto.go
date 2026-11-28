package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

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
