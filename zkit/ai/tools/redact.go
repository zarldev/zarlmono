package tools

import "regexp"

var secretRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(api[_-]?key\s*[=:]\s*)[^\s'\"]{8,}`),
	regexp.MustCompile(`(?i)(token\s*[=:]\s*)[^\s'\"]{8,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// RedactSecrets removes common credential shapes before tool/process
// output is inserted into model-visible context or logs. It is a
// best-effort safety net, not a substitute for environment scrubbing.
func RedactSecrets(s string) string {
	out := s
	for _, re := range secretRedactors {
		out = re.ReplaceAllString(out, `${1}[REDACTED]`)
	}
	return out
}
