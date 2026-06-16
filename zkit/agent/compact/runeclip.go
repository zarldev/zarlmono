package compact

import "unicode/utf8"

// clipToRune returns s truncated to at most n bytes without splitting a
// multi-byte UTF-8 rune: if byte n lands mid-rune it backs off to the previous
// rune boundary. Compaction truncates arbitrary tool/assistant content, which
// routinely carries multi-byte UTF-8 (non-ASCII identifiers, box-drawing,
// emoji); a raw s[:n] can leave a partial rune that some providers reject and
// that renders as U+FFFD in the next request.
func clipToRune(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n < 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
