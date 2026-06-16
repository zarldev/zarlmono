package tools_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRedactSecrets(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456",
		"api_key=sk-test-secret-value",
		"token: supersecrettokenvalue",
		"aws AKIA1234567890ABCDEF",
		"github ghp_abcdefghijklmnopqrstuvwxyz123456",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
	}, "\n")
	got := tools.RedactSecrets(input)
	for _, leaked := range []string{
		"abcdefghijklmnopqrstuvwxyz123456",
		"sk-test-secret-value",
		"supersecrettokenvalue",
		"AKIA1234567890ABCDEF",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"secret\n-----END PRIVATE KEY-----",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q in:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redacted output missing marker: %s", got)
	}
}
