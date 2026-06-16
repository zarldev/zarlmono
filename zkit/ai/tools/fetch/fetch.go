// Package fetch provides a web_fetch tool the agent can call to retrieve
// page content. Fast path: plain HTTP GET via zhttp.Client. Fallback:
// headless Chrome via chromedp for JavaScript-heavy pages.
//
// The package lives alongside pkg/ai/tools/search/ — both are
// external-endpoint tools rather than workspace-bound code tools.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// ToolName is the registered name surfaced to the LLM.
const ToolName tools.ToolName = tools.ToolNameWebFetch

// DefaultMaxChars caps the returned body when the caller doesn't specify
// max_chars. 50k chars is ~12k tokens — safe for even small context windows
// while carrying enough detail for most articles.
const DefaultMaxChars = 50_000

// HardMaxChars is the upper bound on max_chars. Prevents the LLM from
// asking for 1M chars and busting the context window.
const HardMaxChars = 200_000

// fetchTimeout is the per-call ceiling on the HTTP round trip. Similar
// to the search tool's requestTimeout — generous because real-world
// sites can be slow on first byte.
const fetchTimeout = 15 * time.Second

// autoFallbackBytes is the threshold that triggers the chromedp fallback.
// If the HTTP GET produces ≤ this many bytes of text after HTML stripping,
// the page is likely a JS shell (empty <body>, <noscript> placeholder) and
// needs browser rendering.
const autoFallbackBytes = 512

// maxBodyBytes caps how much of a response we read into memory before HTML
// extraction. Without it a malicious/compromised page can stream a
// multi-GB body and OOM the process (max_chars only bounds the *output*,
// after the full DOM is already built).
const maxBodyBytes = 16 << 20 // 16 MiB

// WebFetchTool fetches page content via HTTP GET with an automatic
// chromedp fallback for JS-heavy pages. Construct with [New]; the
// returned tool is safe for concurrent Execute calls.
//
// Use [WebFetchTool.WithChromeBinPath] to set an explicit Chrome/Chromium
// binary path for the browser fallback. When unset, chromedp searches the
// standard platform paths.
type WebFetchTool struct {
	client        *zhttp.Client
	chromeBinPath string
}

// FetchArgs is the typed argument struct decoded from ToolCall.Arguments.
type FetchArgs struct {
	URL        string `json:"url"                     doc:"The URL to fetch. Must use http or https scheme."`
	UseBrowser bool   `json:"use_browser,omitempty"   doc:"Force chromedp browser rendering for JavaScript-heavy pages. Default false — the tool auto-falls-back if HTTP GET returns empty/JS-shell content."`
	MaxChars   int    `json:"max_chars,omitempty"     doc:"Max characters to return. Default 50000, capped at 200000."`
	Selector   string `json:"selector,omitempty"      doc:"Optional CSS selector to extract only matching element text. Chromedp-only; ignored on the HTTP GET path."`
}

// New returns a WebFetchTool with the default HTTP client (retry,
// backoff, 15s timeout). No configuration needed — the tool always
// registers and surfaces errors at Execute time.
func New() *WebFetchTool {
	return &WebFetchTool{
		client: zhttp.NewClient(
			zhttp.WithTimeout(fetchTimeout),
			zhttp.WithRetryPolicy(zhttp.NoRetry()),
			// SSRF guard: validate the actual dialed IP at connect time so
			// a model-controlled URL can't reach internal/loopback hosts,
			// even via DNS rebinding or a redirect to an internal address.
			zhttp.WithTransport(guardedTransport()),
		),
	}
}

// WithChromeBinPath sets the absolute path to a Chrome or Chromium binary
// for the chromedp browser fallback. When empty (the default), chromedp
// searches standard platform paths via exec.LookPath.
func (t *WebFetchTool) WithChromeBinPath(path string) *WebFetchTool {
	t.chromeBinPath = path
	return t
}

// Definition is the LLM-facing spec.
func (t *WebFetchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolName,
		Description: "Fetch a web page and return its text content. Tries fast HTTP GET first; " +
			"falls back to headless Chrome for JavaScript-heavy pages. " +
			"Use this for reading documentation, articles, or any page content.",
		Parameters: tools.SchemaFor[FetchArgs](),
	}
}

// Execute runs the fetch: HTTP GET first, chromedp fallback if the page
// looks JS-heavy. Errors follow the standard three-bucket classification
// (validation / transient / fatal) via tools.Failure.
func (t *WebFetchTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, failure := decodeAndValidate(call)
	if failure != nil {
		return failure, nil
	}
	// Pre-flight SSRF check: gives a clear error before any connection and
	// guards the chromedp path (which does its own DNS and bypasses the
	// transport dial gate). The dial gate in guardedTransport closes the
	// rebinding/redirect window for the HTTP path.
	if err := guardURLHost(ctx, args.URL); err != nil {
		return tools.Failure(call.ID, tools.Validation("web_fetch", err.Error())), nil
	}
	maxChars := clampMaxChars(args.MaxChars)

	// Fast path: HTTP GET.
	title, body, err := t.httpFetch(ctx, args.URL, maxChars)
	usedBrowser := false
	var browserErr string
	switch {
	case err != nil && args.UseBrowser:
		// If the HTTP path failed and the model asked for browser, try chromedp.
		title, body, err = t.browserFetch(ctx, args.URL, args.Selector, maxChars)
		if err != nil {
			return tools.Failure(call.ID, err), nil
		}
		usedBrowser = true
	case err != nil:
		return tools.Failure(call.ID, err), nil
	case args.UseBrowser:
		// Model explicitly asked for browser — override HTTP success.
		bTitle, bBody, bErr := t.browserFetch(ctx, args.URL, args.Selector, maxChars)
		if bErr == nil {
			title, body, usedBrowser = bTitle, bBody, true
		} else {
			browserErr = bErr.Error()
		}
		// On chromedp failure, keep the HTTP result — it's better than nothing.
	case len(body) <= autoFallbackBytes:
		// Auto-fallback: HTTP body is suspiciously small.
		bTitle, bBody, bErr := t.browserFetch(ctx, args.URL, args.Selector, maxChars)
		if bErr == nil {
			title, body, usedBrowser = bTitle, bBody, true
		} else {
			browserErr = bErr.Error()
		}
		// On chromedp failure, keep the HTTP result — it might be a
		// genuinely small page (e.g. a plain-text endpoint).
	}

	return tools.Success(call.ID, RenderFetchResult(args.URL, title, body, usedBrowser, browserErr)), nil
}

// httpFetch does a plain HTTP GET, extracts text from the HTML response,
// and returns title + body. Uses the zhttp.Client for retry/backoff.
func (t *WebFetchTool) httpFetch(ctx context.Context, rawURL string, maxChars int) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", tools.Fatal("web_fetch", fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", "zarlcode/web_fetch (HTTP)")
	req.Header.Set("Accept", "text/html,text/plain,*/*")

	res, err := t.client.Do(ctx, req)
	if err != nil {
		return "", "", tools.Transient("web_fetch", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		if res.StatusCode >= 500 {
			return "", "", tools.Transient("web_fetch", fmt.Errorf("%s", res.Status))
		}
		return "", "", tools.Validation("web_fetch", fmt.Sprintf("HTTP %s", res.Status))
	}

	title, body, err := ExtractText(io.LimitReader(res.Body, maxBodyBytes), maxChars)
	if err != nil {
		return "", "", tools.Fatal("web_fetch", fmt.Errorf("extract text: %w", err))
	}
	return title, body, nil
}

// browserFetch launches headless Chrome via chromedp, navigates to
// rawURL, waits for the page to settle, and extracts text content.
func (t *WebFetchTool) browserFetch(ctx context.Context, rawURL, sel string, maxChars int) (string, string, error) {
	return fetchWithBrowser(ctx, rawURL, sel, maxChars, t.chromeBinPath)
}

// decodeAndValidate decodes the tool call arguments and runs pre-flight
// checks. Returns the typed args on success, or a populated failure
// ToolResult ready to return to the runner.
func decodeAndValidate(call tools.ToolCall) (FetchArgs, *tools.ToolResult) {
	var args FetchArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return args, tools.Failure(call.ID, derr)
	}
	if args.URL == "" {
		return args, tools.Failure(call.ID, tools.Validation("web_fetch", "url is required"))
	}
	u, err := url.Parse(args.URL)
	if err != nil {
		return args, tools.Failure(call.ID, tools.Validation("web_fetch", fmt.Sprintf("invalid url: %v", err)))
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return args, tools.Failure(call.ID, tools.Validation("web_fetch", fmt.Sprintf("unsupported scheme %q — only http and https are allowed", u.Scheme)))
	}
	return args, nil
}

// clampMaxChars applies the default + hard cap. Zero (unset) becomes the
// default; negative values become the default; over-large values become
// the hard cap.
func clampMaxChars(n int) int {
	if n <= 0 {
		return DefaultMaxChars
	}
	if n > HardMaxChars {
		return HardMaxChars
	}
	return n
}

// RenderFetchResult formats the fetch output as labelled plaintext.
// browserErr, when non-empty, is appended as a warning line so the
// caller can see that the browser fallback was attempted but failed.
func RenderFetchResult(rawURL, title string, body string, usedBrowser bool, browserErr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "fetched: %s\n", rawURL)
	if title != "" {
		fmt.Fprintf(&b, "title: %s\n", title)
	}
	fmt.Fprintf(&b, "content-length: %d bytes\n", len(body))
	if usedBrowser {
		b.WriteString("method: browser (chromedp)\n")
	} else {
		b.WriteString("method: http\n")
	}
	if browserErr != "" {
		fmt.Fprintf(&b, "warning: browser fallback failed: %s\n", browserErr)
	}
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	return strings.TrimRight(b.String(), "\n")
}
