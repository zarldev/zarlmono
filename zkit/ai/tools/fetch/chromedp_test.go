package fetch_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
)

func TestWebFetchToolCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	tool := fetch.New()
	if err := tool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWebFetchToolBrowserIntegration(t *testing.T) {
	if os.Getenv("ZKIT_CHROME_INTEGRATION") == "" {
		t.Skip("set ZKIT_CHROME_INTEGRATION=1 to run Chrome integration tests")
	}

	chromePath := findChromeBinary(t)
	tool := fetch.New()
	tool.ConfigureChromeBinary(chromePath)
	t.Cleanup(func() { _ = tool.Close() })

	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:       "browser-fetch",
		ToolName: fetch.ToolName,
		Arguments: tools.ToolParameters{
			"url":         "https://example.com/",
			"use_browser": true,
			"selector":    "body",
			"max_chars":   1_000,
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}
	output, ok := result.Data.(string)
	if !ok {
		t.Fatalf("result data type = %T, want string", result.Data)
	}
	if !strings.Contains(output, "method: browser (chromedp)") {
		t.Fatalf("result did not use browser:\n%s", output)
	}
	if !strings.Contains(output, "Example Domain") {
		t.Fatalf("result missing rendered page content:\n%s", output)
	}
}

func findChromeBinary(t *testing.T) string {
	t.Helper()

	if configured := os.Getenv("ZKIT_CHROME_PATH"); configured != "" {
		return configured
	}
	for _, candidate := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Fatal("Chrome integration requested but no browser found; set ZKIT_CHROME_PATH")
	return ""
}
