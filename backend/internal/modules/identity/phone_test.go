package identity

import "testing"

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"0712 345 678":     "+255712345678",
		"255712345678":     "+255712345678",
		"+255 712-345-678": "+255712345678",
		"00255712345678":   "+255712345678",
		"+254700111222":    "+254700111222",
	} {
		got, err := NormalizePhone(in)
		if err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "0", "+12"} {
		if _, err := NormalizePhone(bad); err == nil {
			t.Errorf("NormalizePhone(%q) should fail", bad)
		}
	}
}
