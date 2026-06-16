package oauth_test

import (
	"io"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/oauth"
)

func TestRunLogin_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	// The dispatcher rejects unknown providers before touching the
	// service, so nil is fine here.
	err := oauth.RunLogin(t.Context(), nil, "anthropic-pro", strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %v, want 'not supported' for unknown provider", err)
	}
}
