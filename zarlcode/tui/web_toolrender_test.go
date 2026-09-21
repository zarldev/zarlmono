package tui_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/fetch"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

func TestWebSearchPresentation(t *testing.T) {
	r := search.Result{Query: "Go contexts", Output: tools.OutputJSON,
		Hits:        []search.Hit{{Title: "Context guide", URL: "https://example.com/docs", Content: "Cancel ongoing work."}},
		Suggestions: []string{"context cancellation"},
	}
	original := r.String()
	for _, data := range []any{r, nil} {
		lines := tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, ToolName: "web_search", Text: original, Data: data})
		out := ansi.Strip(strings.Join(lines, "\n"))
		for _, want := range []string{"1 results · Go contexts", "1. Context guide", "https://example.com/docs", "Cancel ongoing work.", "Suggestions: context cancellation"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
		if strings.Contains(out, `"results"`) || r.String() != original {
			t.Fatal("rendered JSON or mutated underlying result")
		}
	}
	empty := search.Result{Query: "missing", Suggestions: []string{"try again"}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(80, "web_search", "", empty), "\n"))
	if !strings.Contains(out, "(no results)") || !strings.Contains(out, "try again") {
		t.Fatalf("empty search: %s", out)
	}
}

func TestWebToolTextPresentation(t *testing.T) {
	text := fetch.RenderFetchResult("https://example.com", "Example page", "Readable body", false, "browser unavailable")
	encoded, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{text, string(encoded)} {
		out := ansi.Strip(strings.Join(tui.RenderContent(100, tui.Content{Kind: tui.ContentToolResult, ToolName: "web_fetch", Text: input}), "\n"))
		for _, want := range []string{"https://example.com", "Example page", "Readable body", "method: http", "warning: browser fallback failed: browser unavailable"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
		if strings.Contains(out, `\n`) {
			t.Fatalf("escaped JSON string rendered: %s", out)
		}
	}
	for _, name := range []string{"web_fetch", "web_search", "computer_observe", "unknown"} {
		out := ansi.Strip(strings.Join(tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, ToolName: name, Text: "connection refused", Data: struct{}{}}), "\n"))
		if strings.TrimSpace(out) != "connection refused" {
			t.Errorf("%s changed fallback: %q", name, out)
		}
	}
}

func TestComputerObservationPresentation(t *testing.T) {
	obs := computer.Observation{
		Surface:     computer.SurfaceInfo{Kind: computer.SurfaceKinds.BROWSER, Title: "Example page", URL: "https://example.com", Width: 1440, Height: 900},
		VisibleText: "Welcome to the page", Screenshot: &computer.ObservationImage{MIMEType: "image/png"},
		Targets: []computer.TargetDescriptor{{ID: "save", Role: "button", Name: "Save"}},
		Hints:   []string{"surface stable"}, Raw: map[string]any{"backend": "details"},
	}
	encoded, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []any{obs, nil} {
		out := ansi.Strip(strings.Join(tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, ToolName: "computer_observe", Text: string(encoded), Data: data}), "\n"))
		for _, want := range []string{"Example page", "https://example.com", "1440 × 900", "Screenshot attached", "Welcome to the page", "save · button Save", "surface stable", "Raw metadata available"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
		if strings.Contains(out, `"surface"`) {
			t.Fatalf("JSON observation: %s", out)
		}
	}
}

func TestWebToolPresentationWidths(t *testing.T) {
	r := search.Result{Query: strings.Repeat("query", 50), Hits: []search.Hit{{Title: strings.Repeat("界", 100), URL: "https://example.com/" + strings.Repeat("x", 200), Content: strings.Repeat("excerpt ", 100)}}}
	for _, width := range []int{8, 24, 60} {
		lines := tui.RenderContent(width, tui.Content{Kind: tui.ContentToolResult, ToolName: "web_search", Data: r, BodyPrefix: "    "})
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d: wide line %q", width, line)
			}
		}
	}
}

func screenshotPart(t *testing.T, img image.Image) llm.ContentPart {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return llm.ImagePartFromDataURI("data:image/png;base64,"+base64.StdEncoding.EncodeToString(buf.Bytes()), "image/png")
}

func TestToolScreenshotPreview(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	img := image.NewNRGBA(image.Rect(0, 0, 32, 16))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	parts := []llm.ContentPart{screenshotPart(t, img)}
	before := llm.CloneContentParts(parts)
	for _, width := range []int{12, 40, 100} {
		lines := tui.RenderContent(width, tui.Content{Kind: tui.ContentToolResult, ToolName: "computer_observe", Text: "observation text", Parts: parts, BodyPrefix: "    "})
		out := ansi.Strip(strings.Join(lines, "\n"))
		if !strings.Contains(out, "▀") || strings.Contains(out, "base64") {
			t.Fatalf("missing preview or leaked encoded image: %s", out)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d: wide image line", width)
			}
		}
	}
	if !reflect.DeepEqual(parts, before) {
		t.Fatal("presentation mutated original attachments")
	}
}

func TestToolScreenshotPreviewFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name string
		part llm.ContentPart
		want string
	}{
		{"remote", llm.ImagePartFromURL("http://127.0.0.1:1/never-fetch"), "no embedded image"},
		{"invalid", llm.ImagePartFromDataURI("data:image/png;base64,invalid", "image/png"), "preview unavailable"},
		{"unsupported", llm.ImagePartFromDataURI("data:image/svg+xml;base64,PHN2Zz4=", "image/svg+xml"), "unsupported image format"},
		{"bytes", llm.ImagePartFromDataURI("data:image/png;base64,"+strings.Repeat("A", 4*1024*1024), "image/png"), "exceeds 4 MiB"},
		{"pixels", screenshotPart(t, image.NewGray(image.Rect(0, 0, 2500, 2000))), "exceeds 4 million pixels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := ansi.Strip(strings.Join(tui.RenderContent(100, tui.Content{Kind: tui.ContentToolResult, Text: "metadata survives", Parts: []llm.ContentPart{tc.part}}), "\n"))
			if !strings.Contains(out, tc.want) || !strings.Contains(out, "metadata survives") || strings.Contains(out, "base64") {
				t.Fatalf("bad fallback: %s", out)
			}
		})
	}
}

func TestComputerScreenshotInTimeline(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	for _, text := range []string{"observation text", ""} {
		t.Run(text, func(t *testing.T) {
			m := tui.New()
			step(t, m, window(120, 50))
			step(t, m, teasink.ToolStartedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe", Parameters: map[string]any{"include_screenshot": true}})
			step(t, m, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "observe", ToolName: "computer_observe", FormattedResult: text, Parts: []llm.ContentPart{screenshotPart(t, image.NewGray(image.Rect(0, 0, 16, 8)))}})
			step(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
			step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // group
			step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
			step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // result
			out := ansi.Strip(m.View().Content)
			if !strings.Contains(out, "▀") || !strings.Contains(out, text) {
				t.Fatalf("timeline missing screenshot or text: %s", out)
			}
			step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // collapse result
			if strings.Contains(ansi.Strip(m.View().Content), "▀") {
				t.Fatal("collapsed screenshot remains visible")
			}
		})
	}
}

func TestWebSearchSerializedEmptyResults(t *testing.T) {
	for _, text := range []string{`{"query":"none","results":null}`, `{"Query":"none","Hits":[],"Output":"json"}`} {
		out := ansi.Strip(strings.Join(tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, ToolName: "web_search", Text: text}), "\n"))
		if !strings.Contains(out, "(no results)") || strings.Contains(out, `"query"`) {
			t.Fatalf("empty serialized result: %s", out)
		}
	}
}

func TestWebResultStripsExternalTerminalControls(t *testing.T) {
	r := search.Result{Query: "safe\x1b]52;c;Y2xpcGJvYXJk\a query\r\u009b", Hits: []search.Hit{{Title: "title\x1b[2J", Content: "text\x00\tend"}}}
	out := strings.Join(tui.RenderTypedToolResult(80, "web_search", "", r), "\n")
	for _, control := range []string{"\x1b]52", "\x1b[2J", "\r", "\u009b", "\x00"} {
		if strings.Contains(out, control) {
			t.Errorf("external terminal control survived: %q", control)
		}
	}
	if !strings.Contains(ansi.Strip(out), "text    end") {
		t.Fatalf("lost ordinary text: %q", out)
	}
}

func TestToolScreenshotPreviewCountBound(t *testing.T) {
	part := screenshotPart(t, image.NewGray(image.Rect(0, 0, 160, 80)))
	parts := []llm.ContentPart{part, part, part, part, part, part}
	lines := tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, Text: "metadata survives", Parts: parts, MaxLines: 40})
	out := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Count(out, "Image ·") != 4 || !strings.Contains(out, "Additional images omitted") || !strings.Contains(out, "metadata survives") || len(lines) > 40 {
		t.Fatalf("unbounded previews or missing metadata: %s", out)
	}
}

func TestWebAndComputerResultsStripBidiControls(t *testing.T) {
	controls := "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
	url := "https://example.com/" + controls + "actual-path"
	for _, data := range []any{
		search.Result{Hits: []search.Hit{{Title: "page" + controls, URL: url}}},
		computer.Observation{Surface: computer.SurfaceInfo{URL: url}, VisibleText: "visible" + controls + " text"},
	} {
		out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(80, "", "", data), "\n"))
		if strings.ContainsAny(out, controls) || !strings.Contains(out, "https://example.com/actual-path") {
			t.Fatalf("bidi controls survived or URL changed: %q", out)
		}
	}
}
