package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
)

func TestSubscriptionQuotaExitIsTypedAndSanitized(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' \"You've hit your limit - resets later PRIVATE-CANARY\" >&2\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	p := claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}}, claudecode.WithBinaryPath(binary))
	var got error
	for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}) {
		if err != nil {
			got = err
		}
	}
	limit, ok := errors.AsType[*llm.RateLimitError](got)
	if !ok {
		t.Fatalf("got %T, want quota error", got)
	}
	if limit.Permanent || limit.Retryable || limit.RetryAfter != 0 || !limit.ResetAt.IsZero() {
		t.Fatal("invented reset/retry metadata")
	}
	if strings.Contains(limit.Error(), "PRIVATE-CANARY") {
		t.Fatal("private stderr leaked")
	}
}

func TestCLIContextWindowHonorsModelSelectionAndCaps(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CODE_DISABLE_1M_CONTEXT", "CLAUDE_CODE_AUTO_COMPACT_WINDOW"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		model string
		want  int
	}{{"sonnet", 200000}, {"claude-sonnet-4-6", 200000}, {"claude-opus-4-6", 200000}, {"claude-sonnet-4-6[1m]", 1000000}, {"claude-opus-4-7", 1000000}, {"claude-haiku-5-5", 1000000}, {"claude-haiku-4-5", 200000}} {
		if got := claudecode.ContextWindowFor(tc.model); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.model, got, tc.want)
		}
	}
	t.Setenv("ANTHROPIC_DEFAULT_SONNET_MODEL", "claude-sonnet-4-6[1m]")
	if got := claudecode.ContextWindowFor("sonnet"); got != 1000000 {
		t.Fatalf("pinned alias: %d", got)
	}
	t.Setenv("CLAUDE_CODE_DISABLE_1M_CONTEXT", "1")
	if got := claudecode.ContextWindowFor("sonnet"); got != 200000 {
		t.Fatalf("disabled: %d", got)
	}
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "64000")
	if got := claudecode.ContextWindowFor("sonnet"); got != 100000 {
		t.Fatalf("gateway cap: %d", got)
	}
}

func TestCLIContextWindowAutoCompactCapParsing(t *testing.T) {
	t.Setenv("CLAUDE_CODE_DISABLE_1M_CONTEXT", "")
	for _, tc := range []struct {
		name string
		cap  string
		want int
	}{
		{"thousands suffix", "200k", 200000},
		{"millions suffix", "1M", 1000000},
		{"numeric floor", "64000", 100000},
		{"small numeric floor", "200", 100000},
		{"numeric ceiling", "2000000", 1000000},
		{"garbage", "garbage", 1000000},
		{"zero", "0", 1000000},
		{"negative", "-1", 1000000},
		{"not a number", "NaN", 1000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", tc.cap)
			if got := claudecode.ContextWindowFor("claude-opus-4-7"); got != tc.want {
				t.Fatalf("cap %q: got %d, want %d", tc.cap, got, tc.want)
			}
		})
	}
}
