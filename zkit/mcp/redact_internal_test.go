package mcp

import (
	"strings"
	"testing"
)

// TestRedactSecrets guards the stderr redaction layer that closes
// the "MCP subprocess prints credentials to stderr → slog dumps
// them to ~/.zarlcode/cache/logs/" leak the adversarial review
// flagged (docs/adversarial-repo-review.md #5).
//
// Each case is one realistic stderr-line shape we want the
// redactor to mask BEFORE slog sees it. The "should NOT redact"
// cases guard against false positives that would garble normal
// debug output ("read 5 keys", "lookup table=foo").
func TestRedactSecrets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "API_KEY assignment",
			in:   "DEBUG: API_KEY=sk-1234567890",
			want: "DEBUG: API_KEY=REDACTED",
		},
		{
			name: "lowercase api_key colon form",
			in:   "starting with api_key: abc-secret-xyz",
			want: "starting with api_key: REDACTED",
		},
		{
			name: "OPENAI_API_KEY env-style",
			in:   "OPENAI_API_KEY=sk-proj-abcdef",
			want: "OPENAI_API_KEY=REDACTED",
		},
		{
			name: "secret_token shape",
			in:   "loaded secret_token=tok_1a2b3c4d",
			want: "loaded secret_token=REDACTED",
		},
		{
			name: "password assignment",
			in:   "db password=hunter2",
			want: "db password=REDACTED",
		},
		{
			name: "Authorization Bearer header",
			in:   "header Authorization: Bearer sk-LIVE-2025",
			want: "header Authorization: REDACTED",
		},
		{
			name: "passwd column form",
			in:   "passwd: changeme",
			want: "passwd: REDACTED",
		},
		{
			name: "access_key shape",
			in:   "AWS_ACCESS_KEY=AKIASOMETHING",
			want: "AWS_ACCESS_KEY=REDACTED",
		},
		{
			name: "already-redacted is idempotent",
			in:   "API_KEY=REDACTED",
			want: "API_KEY=REDACTED",
		},
		{
			name: "no redaction — plain English mentions key",
			in:   "read 5 keys from cache",
			want: "read 5 keys from cache",
		},
		{
			name: "no redaction — function name mentioning token",
			in:   "calling parseToken function",
			want: "calling parseToken function",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := redactSecrets(c.in)
			if got != c.want {
				t.Fatalf("redactSecrets(%q)\n  got:  %q\n  want: %q", c.in, got, c.want)
			}
		})
	}
}

// TestTruncateLine_PreservesShortAndCapsLong checks both branches
// of the per-line length cap: short lines pass through unchanged,
// over-budget lines get clipped with a visible "[truncated]"
// marker so log readers can tell something was elided.
func TestTruncateLine_PreservesShortAndCapsLong(t *testing.T) {
	t.Parallel()
	short := "short line"
	if got := truncateLine(short, 100); got != short {
		t.Fatalf("short line was altered: %q", got)
	}

	long := strings.Repeat("x", 5000)
	got := truncateLine(long, maxStderrLineBytes)
	if len(got) <= maxStderrLineBytes {
		t.Fatalf("expected cap to fire and append marker; got len=%d", len(got))
	}
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Fatalf("expected truncation marker suffix; got %q", got[len(got)-32:])
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 64)) {
		t.Fatalf("expected leading content preserved; prefix wrong")
	}
}
