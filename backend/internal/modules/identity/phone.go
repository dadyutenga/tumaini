package identity

import (
	"strings"

	"bms/internal/platform/phone"
)

// NormalizePhone turns what a person types into E.164 (see platform/phone).
func NormalizePhone(raw string) (string, error) { return phone.Normalize(raw) }

// IsEmail reports whether the identifier typed at sign-in is an email address.
func IsEmail(s string) bool { return strings.Contains(s, "@") }
