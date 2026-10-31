package claudecode_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
)

func TestUnsuccessfulResultQuotaIsTypedAndSanitized(t *testing.T) {
	tests := []struct {
		name   string
		script string
		binary string
	}{
		{
			name: "result message",
			script: `#!/bin/sh
cat >/dev/null
cat <<'JSON'
{"type":"result","subtype":"error_during_execution","is_error":true,"result":"You've hit your limit PRIVATE-CANARY"}
JSON
exit 1
`,
		},
		{
			name: "stderr before generic result",
			script: `#!/bin/sh
cat >/dev/null
printf '%s\n' "You've hit your limit PRIVATE-CANARY" >&2
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"generic failure"}'
exit 1
`,
		},
	}

	// Keep fixture writes and subprocess execution serial. Quota classification
	// does not require executable creation to race another child fork.
	for i := range tests {
		tests[i].binary = filepath.Join(t.TempDir(), "claude")
		if err := os.WriteFile(tests[i].binary, []byte(tests[i].script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider := claudecode.NewProvider(
				claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}},
				claudecode.WithBinaryPath(tc.binary),
			)

			var got error
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
			}) {
				if err != nil {
					got = err
				}
			}

			limit, ok := errors.AsType[*llm.RateLimitError](got)
			if !ok {
				t.Fatalf("got %T (%v), want quota error", got, got)
			}
			if limit.Permanent || limit.Retryable {
				t.Fatalf("permanent=%v retryable=%v, want both false", limit.Permanent, limit.Retryable)
			}
			if strings.Contains(limit.Error(), "PRIVATE-CANARY") {
				t.Fatal("private quota detail leaked")
			}
		})
	}
}

func TestTerminalQuotaResultDrainsStderrAndBoundsStalledCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
cat >/dev/null
printf '%s\n' "You've hit your limit PRIVATE-CANARY" >&2
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"generic failure"}'
exec sleep 60
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := claudecode.NewProvider(
		claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}},
		claudecode.WithBinaryPath(binary),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var got error
	for _, err := range provider.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	}) {
		if err != nil {
			got = err
		}
	}

	limit, ok := errors.AsType[*llm.RateLimitError](got)
	if !ok {
		t.Fatalf("got %T (%v), want quota error", got, got)
	}
	if cause := context.Cause(ctx); cause != nil {
		t.Fatalf("terminal cleanup exceeded its bound: %v", cause)
	}
	if strings.Contains(limit.Error(), "PRIVATE-CANARY") {
		t.Fatal("private quota detail leaked")
	}
}

func TestMalformedQuotaDetailsCannotBypassResultFailure(t *testing.T) {
	tests := []struct {
		name   string
		result string
	}{
		{name: "object in errors", result: `{"type":"result","is_error":true,"errors":[{}]}`},
		{name: "numeric result", result: `{"type":"result","is_error":true,"result":123}`},
		{name: "string errors", result: `{"type":"result","is_error":true,"errors":"bad"}`},
		{name: "error subtype without is_error", result: `{"type":"result","subtype":"error_during_execution","result":123}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, _ := newFixtureProvider(t, tc.result)
			var got error
			successfulStops := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
			}) {
				if err != nil {
					got = err
				}
				if chunk.FinishReason == llm.FinishReasons.STOP {
					successfulStops++
				}
			}

			if got == nil {
				t.Fatal("malformed failure result completed without an error")
			}
			if successfulStops != 0 {
				t.Fatalf("successful STOP chunks = %d, want 0", successfulStops)
			}
			if _, ok := errors.AsType[*llm.RateLimitError](got); ok {
				t.Fatalf("malformed quota details produced a rate-limit error: %v", got)
			}
		})
	}
}
