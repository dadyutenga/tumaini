// Package phone normalises phone numbers to E.164, the form the database stores (app.phone_e164).
package phone

import (
	"errors"
	"regexp"
	"strings"
)

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// Normalize turns what a person types into E.164. A leading 0 is read as Tanzania (+255); 255... and
// +255... are accepted too.
func Normalize(raw string) (string, error) {
	s := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(s, "+"):
	case strings.HasPrefix(s, "00"):
		s = "+" + s[2:]
	case strings.HasPrefix(s, "0"):
		s = "+255" + s[1:]
	case strings.HasPrefix(s, "255"):
		s = "+" + s
	default:
		s = "+" + s
	}
	if !e164.MatchString(s) {
		return "", errors.New("phone number is not valid")
	}
	return s, nil
}

// Optional normalises a number that may be empty; empty stays empty.
func Optional(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return Normalize(raw)
}
