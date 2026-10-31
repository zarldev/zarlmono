package tools_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Keep the pre-optimization expressions as a compatibility oracle: the fast
// path must produce exactly the same bytes, not merely hide a few canaries.
var originalSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*basic\s+)[A-Za-z0-9+/=]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|(?:access[_-]?|refresh[_-]?|auth[_-]?)?token|password|passphrase|client[_-]?secret)\b["']?\s*[=:]\s*)(?:"(?:\\.|[^"\\])*"?|'[^']*'?|[^\s'";]+)`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
}

func originalRedactSecrets(text string) string {
	for _, pattern := range originalSecretPatterns {
		text = pattern.ReplaceAllString(text, `${1}[REDACTED]`)
	}
	return text
}

func FuzzRedactSecretsCompatibility(f *testing.F) {
	for _, input := range []string{
		"ordinary build output: package compiled\n",
		"Bearer CANARY", "Authorization: Basic CANARY", "AUTHORIZATION:\tBASIC CANARY",
		`api_key="CANARY"`, `api-key='CANARY'`, `APIKEY=CANARY`,
		"token=CANARY", "access_token=CANARY", "refresh-token=CANARY", "authtoken=CANARY",
		"password=CANARY", "passphrase=CANARY", "client_secret=CANARY",
		"paſſword=CANARY", "api_Key=CANARY", "é password=CANARY", "\xffpassword=CANARY",
		"AKIA1234567890ABCDEF", "ghp_abcdefghijklmnopqrstuvwxyz", "ghs_abcdefghijklmnopqrstuvwxyz",
		"-----BEGIN RSA PRIVATE KEY-----\nCANARY\n-----END RSA PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----\nCANARY",
		"password=\"Bearer CANARY\" token='api_key=CANARY'",
		strings.Repeat("synthetic progress\n", 4000) + "PASSWORD=CANARY",
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 128*1024 {
			t.Skip()
		}
		if got, want := tools.RedactSecrets(text), originalRedactSecrets(text); got != want {
			t.Fatal("optimized redaction changed compatibility output")
		}
	})
}

func BenchmarkRedactSecretsOutput(b *testing.B) {
	for _, sample := range []struct{ name, text string }{
		{"short", "building package: synthetic progress line\n"},
		{"preview", strings.Repeat("building package: synthetic progress line\n", 1500)},
		{"credential", strings.Repeat("building package: synthetic progress line\n", 1500) + "password=CANARY"},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.SetBytes(int64(len(sample.text)))
			b.ReportAllocs()
			for b.Loop() {
				_ = tools.RedactSecrets(sample.text)
			}
		})
	}
}
