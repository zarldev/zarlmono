package fetch_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
)

func TestURLPolicyRejectsInternalHosts(t *testing.T) {
	t.Parallel()

	blocked := []string{
		"http://localhost/",
		"http://localhost:8080/admin",
		"http://foo.localhost/",
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://10.1.2.3/",
		"http://192.168.0.1/",
		"http://0.0.0.0/",
		"http://[::ffff:127.0.0.1]/",
		"http://224.0.0.1/",
	}
	for _, rawURL := range blocked {
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()

			tool := fetch.New()
			t.Cleanup(func() { _ = tool.Close() })
			result, err := tool.Execute(t.Context(), tools.ToolCall{
				ID:       "blocked-url",
				ToolName: fetch.ToolName,
				Arguments: tools.ToolParameters{
					"url":         rawURL,
					"use_browser": true,
				},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Success {
				t.Fatalf("Execute(%q) succeeded, want URL policy rejection", rawURL)
			}
			if result.Err == nil || result.Err.Kind != tools.Kinds.VALIDATION {
				t.Fatalf("Execute(%q) error = %#v, want validation error", rawURL, result.Err)
			}
			if !strings.Contains(result.Error, "disallowed") && !strings.Contains(result.Error, "local") {
				t.Fatalf("Execute(%q) error %q does not explain URL policy rejection", rawURL, result.Error)
			}
		})
	}
}

func TestURLPolicyAllowsPublicIPLiterals(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{"http://8.8.8.8/", "https://1.1.1.1/", "http://93.184.216.34/"} {
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()

			tool := fetch.New()
			t.Cleanup(func() { _ = tool.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			result, err := tool.Execute(ctx, tools.ToolCall{
				ID:        "public-url",
				ToolName:  fetch.ToolName,
				Arguments: tools.ToolParameters{"url": rawURL},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Err != nil && result.Err.Kind == tools.Kinds.VALIDATION &&
				(strings.Contains(result.Error, "disallowed") || strings.Contains(result.Error, "local")) {
				t.Fatalf("Execute(%q) unexpectedly rejected by URL policy: %s", rawURL, result.Error)
			}
		})
	}
}
