package tools

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Each marker is a necessary literal in its pattern (one of the alternatives
// suffices). They only filter ASCII input; Unicode case folding stays with RE2.
type secretRedactor struct {
	pattern *regexp.Regexp
	markers []string
}

var secretRedactors = []secretRedactor{
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`), []string{"bearer"}},
	{regexp.MustCompile(`(?i)(authorization\s*:\s*basic\s+)[A-Za-z0-9+/=]+`), []string{"authorization"}},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|(?:access[_-]?|refresh[_-]?|auth[_-]?)?token|password|passphrase|client[_-]?secret)\b["']?\s*[=:]\s*)(?:"(?:\\.|[^"\\])*"?|'[^']*'?|[^\s'";]+)`), []string{"key", "token", "password", "passphrase", "secret"}},
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), []string{"akia"}},
	{regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`), []string{"gh"}},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), []string{"-----begin "}},
}

func (r secretRedactor) possible(lower string) bool {
	for _, marker := range r.markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func asciiSecretText(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// RedactSecrets removes common credential shapes before tool/process
// output is inserted into model-visible context or logs. It is a
// best-effort safety net, not a substitute for environment scrubbing.
func RedactSecrets(s string) string {
	ascii := asciiSecretText(s)
	lower := ""
	if ascii {
		lower = strings.ToLower(s)
	}
	out := s
	for _, redactor := range secretRedactors {
		if ascii && !redactor.possible(lower) {
			continue
		}
		redacted := redactor.pattern.ReplaceAllString(out, `${1}[REDACTED]`)
		if ascii && redacted != out {
			lower = strings.ToLower(redacted)
		}
		out = redacted
	}
	return out
}
