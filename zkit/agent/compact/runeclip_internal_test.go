package compact

import (
	"testing"
	"unicode/utf8"
)

func TestClipToRune(t *testing.T) {
	t.Parallel()
	// "héllo" — é is 2 bytes (0xC3 0xA9), so byte index 2 lands mid-rune.
	const s = "héllo"
	tests := []struct {
		n    int
		want string
	}{
		{0, ""},
		{1, "h"},
		{2, "h"},       // would split é → backs off to "h"
		{3, "hé"},      // é complete at byte 3
		{100, "héllo"}, // n past len → whole string
		{-1, ""},
	}
	for _, tt := range tests {
		got := clipToRune(s, tt.n)
		if got != tt.want {
			t.Errorf("clipToRune(%q, %d) = %q, want %q", s, tt.n, got, tt.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("clipToRune(%q, %d) = %q is not valid UTF-8", s, tt.n, got)
		}
	}
}
