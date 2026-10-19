package tools_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRedactSecretsDoesNotExposeShortQuotedOrTruncatedKeys(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"Bearer SHORT_CANARY",
		"Authorization: Basic BASIC_CANARY",
		`api_key="QUOTED_CANARY"`,
		"token=abc",
		"password='PASSWORD_CANARY'",
		"access_token=ACCESS_CANARY",
		"-----BEGIN PRIVATE KEY-----\nINCOMPLETE_KEY_CANARY\n",
		"-----BEGIN RSA PRIVATE KEY-----\nKEY_CANARY\n-----END RSA PRIVATE KEY-----\nafter",
	} {
		output := tools.RedactSecrets(input)
		for _, secret := range []string{"SHORT_CANARY", "BASIC_CANARY", "QUOTED_CANARY", "abc", "PASSWORD_CANARY", "ACCESS_CANARY", "INCOMPLETE_KEY_CANARY", "KEY_CANARY"} {
			if strings.Contains(output, secret) {
				t.Errorf("recognized credential remained in redacted output")
			}
		}
		if !strings.Contains(output, "[REDACTED]") {
			t.Errorf("recognized credential had no redaction marker")
		}
	}
}
