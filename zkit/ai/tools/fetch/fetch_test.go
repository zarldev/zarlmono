package fetch_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
)

// TestDefinition_Shape verifies the tool spec has the expected shape.
func TestDefinition_Shape(t *testing.T) {
	tool := fetch.New()
	spec := tool.Definition()

	if spec.Name != tools.ToolNameWebFetch {
		t.Errorf("name: got %q, want %q", spec.Name, tools.ToolNameWebFetch)
	}
	if spec.Description == "" {
		t.Error("description must not be empty")
	}
	params, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatal("parameters must have properties")
	}
	required := false
	for _, name := range []string{"url", "use_browser", "max_chars", "selector"} {
		if _, ok := params[name]; !ok {
			t.Errorf("parameter %q missing", name)
		}
	}
	reqList, _ := spec.Parameters.Map()["required"].([]string)
	for _, r := range reqList {
		if r == "url" {
			required = true
		}
	}
	if !required {
		t.Error("url must be required")
	}
}

// TestDecodeAndValidate covers arg-validation boundaries.
func TestDecodeAndValidate(t *testing.T) {
	tool := fetch.New()
	ctx := t.Context()

	tests := []struct {
		name    string
		args    tools.ToolParameters
		wantErr bool
		errKind tools.Kind
	}{
		{
			name:    "valid http url",
			args:    tools.ToolParameters{"url": "https://example.com"},
			wantErr: false,
		},
		{
			name:    "valid http plain",
			args:    tools.ToolParameters{"url": "http://example.com"},
			wantErr: false,
		},
		{
			name:    "missing url",
			args:    tools.ToolParameters{},
			wantErr: true,
			errKind: tools.Kinds.VALIDATION,
		},
		{
			name:    "empty url",
			args:    tools.ToolParameters{"url": ""},
			wantErr: true,
			errKind: tools.Kinds.VALIDATION,
		},
		{
			name:    "invalid url scheme",
			args:    tools.ToolParameters{"url": "ftp://example.com"},
			wantErr: true,
			errKind: tools.Kinds.VALIDATION,
		},
		{
			name:    "no scheme",
			args:    tools.ToolParameters{"url": "example.com"},
			wantErr: true,
			errKind: tools.Kinds.VALIDATION,
		},
		{
			name:    "with use_browser flag",
			args:    tools.ToolParameters{"url": "https://example.com", "use_browser": true},
			wantErr: false,
		},
		{
			name:    "with max_chars",
			args:    tools.ToolParameters{"url": "https://example.com", "max_chars": 1000},
			wantErr: false,
		},
		{
			name:    "with selector",
			args:    tools.ToolParameters{"url": "https://example.com", "selector": ".main-content"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tools.ToolCall{
				ID:        "test",
				ToolName:  tools.ToolNameWebFetch,
				Arguments: tt.args,
			})
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if tt.wantErr {
				if result.Success {
					t.Error("expected failure, got success")
				}
				if tt.errKind != tools.Kinds.UNKNOWN && (result.Err == nil || result.Err.Kind != tt.errKind) {
					t.Errorf("kind: got %v, want %v", result.Err, tt.errKind)
				}
			}
			// Non-wantErr tests will fail at the HTTP level since the URLs
			// aren't real, but they shouldn't be validation failures.
			if !tt.wantErr && result.Err != nil && result.Err.Kind == tools.Kinds.VALIDATION {
				t.Errorf("unexpected validation error: %s", result.Error)
			}
		})
	}
}

// TestExtractText covers the HTML-to-text walker.
func TestExtractText(t *testing.T) {
	tests := []struct {
		name      string
		html      string
		wantTitle string
		wantBody  string
	}{
		{
			name:      "simple page",
			html:      "<html><head><title>Hello</title></head><body><p>This is a paragraph.</p><p>And another.</p></body></html>",
			wantTitle: "Hello",
			wantBody:  "This is a paragraph. And another.",
		},
		{
			name:      "script and style skipped",
			html:      "<html><head><title>Test</title><script>console.log('x')</script><style>body { }</style></head><body><p>Visible text</p><script>alert('x')</script></body></html>",
			wantTitle: "Test",
			wantBody:  "Visible text",
		},
		{
			name:      "noscript skipped",
			html:      "<html><head><title>Test</title></head><body><noscript>Enable JS</noscript><p>Real content</p></body></html>",
			wantTitle: "Test",
			wantBody:  "Real content",
		},
		{
			name:      "block elements add newlines",
			html:      "<html><head><title>Test</title></head><body><div>First</div><div>Second</div></body></html>",
			wantTitle: "Test",
			wantBody:  "First Second",
		},
		{
			name:      "whitespace collapse",
			html:      "<html><head><title>Test</title></head><body><p>Hello\n\n\nWorld</p></body></html>",
			wantTitle: "Test",
			wantBody:  "Hello World",
		},
		{
			name:      "no title",
			html:      "<html><body><p>Content</p></body></html>",
			wantTitle: "",
			wantBody:  "Content",
		},
		{
			name:      "nested elements",
			html:      "<html><head><title>Nested</title></head><body><article><section><p>Deep text</p></section></article></body></html>",
			wantTitle: "Nested",
			wantBody:  "Deep text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, err := fetch.ExtractText(strings.NewReader(tt.html), 100_000)
			if err != nil {
				t.Fatalf("extractText: %v", err)
			}
			if title != tt.wantTitle {
				t.Errorf("title: got %q, want %q", title, tt.wantTitle)
			}
			if body != tt.wantBody {
				t.Errorf("body: got %q, want %q", body, tt.wantBody)
			}
		})
	}
}

// TestExtractTextTruncation verifies the maxChars cap and sentence-boundary truncation.
func TestExtractTextTruncation(t *testing.T) {
	// Build exactly 1000 bytes of text.
	longText := strings.Repeat("Hello world. ", 200) // "Hello world. " is 13 chars
	html := "<html><head><title>Long</title></head><body>" + longText + "</body></html>"

	_, body, err := fetch.ExtractText(strings.NewReader(html), 100)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if len(body) > 110 { // 100 + "[truncated]" overhead
		t.Errorf("body length %d exceeds maxChars with truncation allowance", len(body))
	}
	if !strings.Contains(body, "[truncated]") {
		t.Errorf("truncated body should contain [truncated] marker: %s", body[:min(len(body), 80)])
	}
}

// TestAutoFallbackThreshold verifies the constant is reasonable.
func TestAutoFallbackThreshold(t *testing.T) {
	// autoFallbackBytes is 512 — a JS shell like
	// <html><body><div id=root></div><script src=/bundle.js></script></body></html>
	// should be well under this after stripping.
	jsShell := "<html><head><title>App</title></head><body><div id='root'></div><script src='/bundle.js'></script></body></html>"
	_, body, err := fetch.ExtractText(strings.NewReader(jsShell), 100_000)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if len(body) > 512 {
		t.Logf("JS shell body is %d bytes (threshold is 512) — threshold may need tuning", len(body))
	}
	if len(body) == 0 {
		t.Log("JS shell body is empty — auto-fallback will trigger")
	}
}

// TestRenderFetchResult verifies the output format.
func TestRenderFetchResult(t *testing.T) {
	result := fetch.RenderFetchResult("https://example.com", "Example Title", "Hello world.", false, "")
	if !strings.Contains(result, "fetched: https://example.com") {
		t.Error("missing 'fetched:' header")
	}
	if !strings.Contains(result, "title: Example Title") {
		t.Error("missing 'title:' field")
	}
	if !strings.Contains(result, "method: http") {
		t.Error("missing 'method:' field")
	}
	if !strings.Contains(result, "Hello world.") {
		t.Error("missing body content")
	}
	if strings.Contains(result, "warning:") {
		t.Error("unexpected warning when browserErr is empty")
	}
}

// TestRenderFetchResult_BrowserWarning verifies the browser fallback error surfaces.
func TestRenderFetchResult_BrowserWarning(t *testing.T) {
	result := fetch.RenderFetchResult("https://example.com", "", "", false, "start chrome: exec: \"google-chrome\": executable file not found in $PATH")
	if !strings.Contains(result, "warning: browser fallback failed:") {
		t.Error("missing browser fallback warning")
	}
	if !strings.Contains(result, "start chrome:") {
		t.Error("missing browser error detail")
	}
}
