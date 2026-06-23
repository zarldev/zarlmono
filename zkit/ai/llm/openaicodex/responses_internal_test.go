package openaicodex

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// collectChunks runs the SSE parser against a fixed string and
// gathers every chunk it emits. Returns the slice plus any parse
// error. The parser is driven natively via its iter.Seq2 yield; a
// yielded error is folded back onto the chunk's Error field so the
// assertions keep reading the pre-migration chunk shape.
func collectChunks(t *testing.T, payload string) ([]llm.CompletionChunk, error) {
	t.Helper()
	var out []llm.CompletionChunk
	err := parseSSEStream(strings.NewReader(payload), func(c llm.CompletionChunk, yerr error) bool {
		if yerr != nil {
			c.Error = yerr
		}
		out = append(out, c)
		return true
	})
	return out, err
}

func TestParseSSEStream_TextOnly(t *testing.T) {
	t.Parallel()
	// A minimal three-event response: two text deltas then completed.
	stream := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Hello "}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":4}}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3: %#v", len(chunks), chunks)
	}
	if chunks[0].Content != "Hello " {
		t.Errorf("chunk0 = %q, want %q", chunks[0].Content, "Hello ")
	}
	if chunks[1].Content != "world" {
		t.Errorf("chunk1 = %q, want %q", chunks[1].Content, "world")
	}
	last := chunks[2]
	if !last.Done || last.FinishReason != "stop" {
		t.Errorf("last chunk done/reason = %v/%q, want true/stop", last.Done, last.FinishReason)
	}
	if last.Usage == nil || last.Usage.PromptTokens != 10 || last.Usage.CachedTokens != 4 {
		t.Errorf("usage = %+v, want prompt=10 cached=4", last.Usage)
	}
}

func TestParseSSEStream_ReasoningRoutesToThinkingChannel(t *testing.T) {
	t.Parallel()
	// Reasoning summary deltas land on the out-of-band Thinking
	// channel; visible text stays on Content. The two channels must
	// stay disjoint — same bytes appearing on both would double in
	// the TUI's thinking pane.
	stream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Considering the question"}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":" carefully."}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"42"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var contents, thinking []string
	for _, c := range chunks {
		if c.Content != "" {
			contents = append(contents, c.Content)
		}
		if c.Thinking != "" {
			thinking = append(thinking, c.Thinking)
		}
	}
	wantThinking := []string{"Considering the question", " carefully."}
	if !reflect.DeepEqual(thinking, wantThinking) {
		t.Errorf("thinking = %v, want %v", thinking, wantThinking)
	}
	wantContent := []string{"42"}
	if !reflect.DeepEqual(contents, wantContent) {
		t.Errorf("content = %v, want %v", contents, wantContent)
	}
}

func TestParseSSEStream_ReasoningSummaryPartsSeparated(t *testing.T) {
	t.Parallel()
	// A multi-part summary: each part is bracketed by a *.part.added /
	// *.text.done pair and its deltas carry no leading separator. The
	// parser must inject a paragraph break at each part boundary so the
	// concatenated thinking reads "part one.\n\npart two." rather than
	// running together as "part one.part two."
	stream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_part.added","summary_index":0}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Checked CI."}`,
		``,
		`data: {"type":"response.reasoning_summary_text.done"}`,
		``,
		`data: {"type":"response.reasoning_summary_part.added","summary_index":1}`,
		``,
		`data: {"type":"response.reasoning_summary_text.delta","delta":"Now pushing the fix."}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var thinking strings.Builder
	for _, c := range chunks {
		thinking.WriteString(c.Thinking)
	}
	const want = "Checked CI.\n\nNow pushing the fix."
	if got := thinking.String(); got != want {
		t.Errorf("thinking = %q, want %q", got, want)
	}
}

func TestParseSSEStream_ToolCall(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_xyz","name":"search","arguments":""}}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"{\"q\":\""}`,
		``,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":"foo\"}"}`,
		``,
		`data: {"type":"response.completed","response":{"usage":{}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	var toolEvents []llm.ToolCall
	for _, c := range chunks {
		toolEvents = append(toolEvents, c.ToolCalls...)
	}
	if len(toolEvents) < 3 {
		t.Fatalf("expected at least 3 tool-call chunks (name + 2 arg deltas), got %d: %#v", len(toolEvents), toolEvents)
	}
	if toolEvents[0].Function.Name != "search" {
		t.Errorf("first event name = %q, want search", toolEvents[0].Function.Name)
	}
	if toolEvents[0].ID != "call_xyz" {
		t.Errorf("first event id = %q, want call_xyz", toolEvents[0].ID)
	}
	// All events should share the same ID so the runner can accumulate.
	for i, e := range toolEvents {
		if e.ID != "call_xyz" {
			t.Errorf("event %d id = %q, want call_xyz", i, e.ID)
		}
	}
	// Argument fragments concatenated should reconstruct the JSON.
	var args strings.Builder
	for _, e := range toolEvents {
		args.WriteString(e.Function.Arguments)
	}
	if args.String() != `{"q":"foo"}` {
		t.Errorf("reconstructed args = %q, want %q", args.String(), `{"q":"foo"}`)
	}
	last := chunks[len(chunks)-1]
	if last.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", last.FinishReason)
	}
}

func TestParseSSEStream_ToolCallArgDelivery(t *testing.T) {
	t.Parallel()
	const completed = `data: {"type":"response.completed","response":{"usage":{}}}`
	added := func(args string) string {
		return `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_xyz","name":"search","arguments":` + jsonStr(args) + `}}`
	}
	delta := func(d string) string {
		return `data: {"type":"response.function_call_arguments.delta","output_index":1,"item_id":"fc_1","delta":` + jsonStr(d) + `}`
	}
	done := func(args string) string {
		return `data: {"type":"response.function_call_arguments.done","output_index":1,"item_id":"fc_1","arguments":` + jsonStr(args) + `}`
	}

	cases := []struct {
		name   string
		events []string
	}{
		// The regression: arguments arrive ONLY on the done event (no deltas).
		// Before the fix the call dispatched with empty args.
		{"done only", []string{added(""), done(`{"q":"foo"}`)}},
		// Arguments delivered complete on the added event, no deltas/done.
		{"added carries full args", []string{added(`{"q":"foo"}`)}},
		// Normal streaming, with a redundant done — must NOT double-count.
		{"deltas then redundant done", []string{added(""), delta(`{"q":"`), delta(`foo"}`), done(`{"q":"foo"}`)}},
		// Partial deltas, done supplies the remainder.
		{"partial deltas completed by done", []string{added(""), delta(`{"q":"`), done(`{"q":"foo"}`)}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stream := strings.Join(append(interleaveBlankLines(tt.events), completed, ""), "\n")
			chunks, err := collectChunks(t, stream)
			if err != nil {
				t.Fatalf("parseSSEStream: %v", err)
			}
			var name string
			var args strings.Builder
			for _, c := range chunks {
				for _, e := range c.ToolCalls {
					if e.ID != "call_xyz" {
						t.Errorf("tool-call id = %q, want call_xyz", e.ID)
					}
					if e.Function.Name != "" {
						name = e.Function.Name
					}
					args.WriteString(e.Function.Arguments)
				}
			}
			if name != "search" {
				t.Errorf("tool-call name = %q, want search", name)
			}
			if args.String() != `{"q":"foo"}` {
				t.Errorf("reconstructed args = %q, want %q", args.String(), `{"q":"foo"}`)
			}
		})
	}
}

// jsonStr renders s as a JSON string literal (with quotes/escapes) for
// embedding in a test SSE payload.
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// interleaveBlankLines puts an empty line after each event so the SSE parser
// sees event boundaries.
func interleaveBlankLines(events []string) []string {
	out := make([]string, 0, len(events)*2)
	for _, e := range events {
		out = append(out, e, "")
	}
	return out
}

func TestParseSSEStream_Failure(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`data: {"type":"response.failed","response":{"error":{"message":"rate limit","code":"rate_limited"}}}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	last := chunks[len(chunks)-1]
	if last.Error == nil {
		t.Fatalf("last chunk should carry error, got: %+v", last)
	}
	if !strings.Contains(last.Error.Error(), "rate limit") {
		t.Errorf("error %q should mention rate limit", last.Error)
	}
}

func TestParseSSEStream_TruncatedStream(t *testing.T) {
	t.Parallel()
	// No completed event — parser should still emit a synthetic Done.
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		``,
	}, "\n")
	chunks, err := collectChunks(t, stream)
	if err != nil {
		t.Fatalf("parseSSEStream: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected text + synthetic Done, got %d", len(chunks))
	}
	if !chunks[len(chunks)-1].Done {
		t.Errorf("last chunk should be Done")
	}
}

func TestBuildRequest_BasicShape(t *testing.T) {
	t.Parallel()
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
		Stream: true,
	}
	body := buildRequest(req, "gpt-5.1-codex", "you are codex")
	if body.Model != "gpt-5.1-codex" {
		t.Errorf("model = %q", body.Model)
	}
	if body.Instructions != "you are codex" {
		t.Errorf("instructions = %q", body.Instructions)
	}
	if body.Store {
		t.Errorf("store should default false")
	}
	if !body.Stream {
		t.Errorf("stream should be forced true")
	}
	if len(body.Input) != 1 || body.Input[0].Type != "message" || body.Input[0].Role != "user" {
		t.Errorf("input shape wrong: %#v", body.Input)
	}
	if got := body.Input[0].Content[0]; got.Type != "input_text" || got.Text != "hello" {
		t.Errorf("input content wrong: %#v", got)
	}
	if body.Reasoning == nil || body.Reasoning.Effort != "medium" {
		t.Errorf("codex default reasoning effort should be medium, got %#v", body.Reasoning)
	}
}

func TestBuildRequest_AssistantWithToolCallsThenToolResult(t *testing.T) {
	t.Parallel()
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "search please"},
			{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      "search",
						Arguments: `{"q":"foo"}`,
					},
				}},
			},
			{Role: "tool", Content: `{"results":["bar"]}`, ToolCallID: "call_1"},
		},
	}
	body := buildRequest(req, "gpt-5.1-codex", "")
	if len(body.Input) != 3 {
		t.Fatalf("expected 3 input items (user/function_call/function_call_output), got %d", len(body.Input))
	}
	if body.Input[1].Type != "function_call" || body.Input[1].CallID != "call_1" || body.Input[1].Name != "search" {
		t.Errorf("function_call shape wrong: %#v", body.Input[1])
	}
	if body.Input[2].Type != "function_call_output" || body.Input[2].CallID != "call_1" || body.Input[2].Output == "" {
		t.Errorf("function_call_output shape wrong: %#v", body.Input[2])
	}
}

func TestBuildRequest_OptionsOverrides(t *testing.T) {
	t.Parallel()
	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
		Options: llm.ModelOptions{
			"reasoning_effort":  "high",
			"reasoning_summary": "concise",
			"text_verbosity":    "low",
			"tool_choice":       "required",
			"prompt_cache_key":  "sess-123",
		},
	}
	body := buildRequest(req, "gpt-5.2", "")
	if body.Reasoning.Effort != "high" || body.Reasoning.Summary != "concise" {
		t.Errorf("reasoning = %+v", body.Reasoning)
	}
	if body.Text == nil || body.Text.Verbosity != "low" {
		t.Errorf("text = %+v", body.Text)
	}
	if body.ToolChoice != "required" {
		t.Errorf("tool_choice = %q", body.ToolChoice)
	}
	if body.PromptCacheKey != "sess-123" {
		t.Errorf("prompt_cache_key = %q", body.PromptCacheKey)
	}
}

func TestBuildRequest_ToolsShape(t *testing.T) {
	t.Parallel()
	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "x"}},
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "do_thing",
				Description: "does a thing",
				Parameters:  llm.Schema{Type: "object"},
			},
		}},
	}
	body := buildRequest(req, "gpt-5.1-codex", "")
	if len(body.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(body.Tools))
	}
	tool := body.Tools[0]
	if tool.Type != "function" || tool.Name != "do_thing" || tool.Description != "does a thing" {
		t.Errorf("tool shape wrong: %#v", tool)
	}
	if body.ToolChoice != "auto" {
		t.Errorf("tool_choice default with tools = %q, want auto", body.ToolChoice)
	}
	// Verify the wire JSON omits the Chat-Completions { "function": {...} } wrapper.
	wireJSON, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(wireJSON), `"function":{`) {
		t.Errorf("responses tool should not have nested function wrapper: %s", wireJSON)
	}
}

func TestUserContentParts_MultimodalImage(t *testing.T) {
	t.Parallel()
	msg := llm.Message{
		Role: "user",
		Parts: []llm.ContentPart{
			llm.TextPart("look at this:"),
			llm.ImagePartFromURL("https://example.com/cat.png"),
		},
	}
	parts := userContentParts(msg)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].Type != "input_text" || parts[0].Text != "look at this:" {
		t.Errorf("text part wrong: %#v", parts[0])
	}
	if parts[1].Type != "input_image" || parts[1].ImageURL != "https://example.com/cat.png" {
		t.Errorf("image part wrong: %#v", parts[1])
	}
}

// Regression: gpt-5 family models default to medium reasoning effort
// so the Responses API emits reasoning_summary deltas. Without this
// the server returned an empty summary and zarlcode's transcript
// stayed empty of the thinking child — user-visible as "I see
// thinking for Qwen llama but not GPT 5.5".
func TestDefaultReasoningEffort_GPT5Family(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  reasoningEffort
	}{
		{"gpt-5", reasoningEffortMedium},
		{"gpt-5.5", reasoningEffortMedium},
		{"gpt-5-pro", reasoningEffortMedium},
		// -mini and -max still win their respective suffix branches
		// even within the gpt-5 family.
		{"gpt-5-mini", reasoningEffortLow},
		{"gpt-5-max", reasoningEffortHigh},
		// Non-gpt-5 models without an explicit suffix stay at the
		// server default (empty).
		{"gpt-4o", ""},
		{"o1-preview", ""},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			t.Parallel()
			if got := defaultReasoningEffort(c.model); got != c.want {
				t.Errorf("defaultReasoningEffort(%q) = %q, want %q", c.model, got, c.want)
			}
		})
	}
}

func TestBuildRequest_ForcesStreamTrue(t *testing.T) {
	t.Parallel()
	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
		Stream:   false,
	}
	body := buildRequest(req, "gpt-5.1-codex", "")
	if !body.Stream {
		t.Fatalf("stream = false, want true for Codex SSE parser")
	}
}
