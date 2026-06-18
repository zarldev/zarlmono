package claudecode

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestTextDeltaFromStreamEvent(t *testing.T) {
	line := `{"type":"stream_event","event":{"delta":{"type":"text_delta","text":"hello"}}}`
	if got := textDeltaFromLine(line); got != "hello" {
		t.Fatalf("textDeltaFromLine() = %q, want hello", got)
	}
}

// TestStreamDoesNotDuplicateFinalText locks the fix for the terminal
// `assistant` event (which repeats the whole message) being re-emitted on top
// of the incremental text_delta lines — which used to double the content.
func TestStreamDoesNotDuplicateFinalText(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"delta":{"type":"text_delta","text":"hello "}}}`,
		`{"type":"stream_event","event":{"delta":{"type":"text_delta","text":"world"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`,
	}, "\n")
	var b strings.Builder
	yield := func(c llm.CompletionChunk, _ error) bool {
		b.WriteString(c.Content)
		return true
	}
	if _, _, err := parseStream(strings.NewReader(stream), yield, newToolCallState(), false); err != nil {
		t.Fatalf("parseStream: %v", err)
	}
	if got := b.String(); got != "hello world" {
		t.Fatalf("content = %q, want %q (terminal assistant event was re-emitted)", got, "hello world")
	}
}

func TestCompleteDoesNotYieldErrorAfterConsumerStops(t *testing.T) {
	p := &Provider{tokens: StaticTokenSource{T: Token{Access: "token"}}, binaryPath: "definitely-missing-claude-for-test"}
	stream, err := p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	calls := 0
	for range stream {
		calls++
		break
	}
	if calls != 1 {
		t.Fatalf("yield calls = %d, want 1", calls)
	}
}

func TestBuildPromptIncludesRoles(t *testing.T) {
	prompt := buildPrompt(llm.CompletionRequest{Messages: []llm.Message{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hi"},
	}})
	if want := "<system>\nbe brief\n</system>"; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing %q in %q", want, prompt)
	}
	if want := "<user>\nhi\n</user>"; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing %q in %q", want, prompt)
	}
}

func TestBuildPromptIncludesToolResults(t *testing.T) {
	prompt := buildPrompt(llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Type: toolCallTypeFunction, Function: llm.ToolCallFunction{Name: "bash", Arguments: `{"command":"echo hi"}`}}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "hi\n"},
	}})
	if want := "<assistant_tool_calls>"; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing assistant tool call block %q in:\n%s", want, prompt)
	}
	if want := `"name":"bash"`; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing tool call name %q in:\n%s", want, prompt)
	}
	if want := `"arguments":"{\"command\":\"echo hi\"}"`; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing tool call arguments %q in:\n%s", want, prompt)
	}
	if want := "<tool_result tool_call_id=\"call_1\">\nhi\n\n</tool_result>"; !strings.Contains(prompt, want) {
		t.Fatalf("prompt missing tool result block %q in:\n%s", want, prompt)
	}
}

func TestToolCallFromStreamingEvents(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"README.md\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
	}, "\n")
	var calls []llm.ToolCall
	yield := func(c llm.CompletionChunk, _ error) bool {
		calls = append(calls, c.ToolCalls...)
		return true
	}
	if _, _, err := parseStream(strings.NewReader(stream), yield, newToolCallState(), false); err != nil {
		t.Fatalf("parseStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].ID != "toolu_1" || calls[0].Function.Name != "read_file" ||
		calls[0].Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected call: %#v", calls[0])
	}
}

func TestToolCallFromAssistantMessage(t *testing.T) {
	state := newToolCallState()
	calls := state.toolCallsFromLine(
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"grep","input":{"pattern":"TODO"}}]}}`,
	)
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].Function.Arguments != `{"pattern":"TODO"}` {
		t.Fatalf("arguments = %q", calls[0].Function.Arguments)
	}
	if dup := state.toolCallsFromLine(
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"grep","input":{"pattern":"TODO"}}]}}`,
	); len(
		dup,
	) != 0 {
		t.Fatalf("duplicate emitted: %#v", dup)
	}
}

func TestParseToolProtocol(t *testing.T) {
	calls := parseToolProtocol(`{"tool_calls":[{"id":"call_1","name":"read","arguments":{"path":"README.md"}}]}`)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].ID != "call_1" || calls[0].Function.Name != "read" ||
		calls[0].Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected call: %#v", calls[0])
	}
}

// TestParseToolProtocolAssistantToolCallsBlock locks parsing of the inline
// <assistant_tool_calls> framing the model copies from the prompt's rendered
// history instead of the documented {"tool_calls":...} protocol — these used to
// leak into the transcript as prose.
func TestParseToolProtocolAssistantToolCallsBlock(t *testing.T) {
	calls := parseToolProtocol(
		`<assistant_tool_calls>[{"id":"call_r2","type":"function","function":{"name":"read","arguments":"{\"path\":\"VOICE.md\"}"}}] </assistant_tool_calls>`,
	)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_r2" || calls[0].Function.Name != "read" ||
		calls[0].Function.Arguments != `{"path":"VOICE.md"}` {
		t.Fatalf("unexpected call: %#v", calls[0])
	}
}

// TestParseToolProtocolAssistantToolCallsWithPreamble covers the model wrapping
// the block in prose, which is the common leak shape.
func TestParseToolProtocolAssistantToolCallsWithPreamble(t *testing.T) {
	calls := parseToolProtocol(strings.Join([]string{
		"Sure, let me look at that file.",
		`<assistant_tool_calls>[{"id":"call_r2","type":"function","function":{"name":"read","arguments":"{\"path\":\"VOICE.md\"}"}}]</assistant_tool_calls>`,
	}, "\n"))
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1: %#v", len(calls), calls)
	}
	if calls[0].Function.Arguments != `{"path":"VOICE.md"}` {
		t.Fatalf("arguments = %q", calls[0].Function.Arguments)
	}
}

// TestParseToolProtocolBareArray covers the OpenAI-style array emitted without
// the wrapping tags.
func TestParseToolProtocolBareArray(t *testing.T) {
	calls := parseToolProtocol(
		`[{"id":"call_1","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"TODO\"}"}}]`,
	)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Function.Name != "grep" ||
		calls[0].Function.Arguments != `{"pattern":"TODO"}` {
		t.Fatalf("unexpected call: %#v", calls[0])
	}
}

// TestParseStreamCatchesInlineAssistantToolCalls is the end-to-end guard: when
// tools are present, the model's inline <assistant_tool_calls> text must surface
// as a tool call rather than transcript content.
func TestParseStreamCatchesInlineAssistantToolCalls(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"delta":{"type":"text_delta","text":"<assistant_tool_calls>[{\"id\":\"call_r2\",\"type\":\"function\","}}}`,
		`{"type":"stream_event","event":{"delta":{"type":"text_delta","text":"\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"VOICE.md\\\"}\"}}]</assistant_tool_calls>"}}}`,
	}, "\n")
	var chunks []llm.CompletionChunk
	yield := func(c llm.CompletionChunk, _ error) bool {
		chunks = append(chunks, c)
		return true
	}
	if _, _, err := parseStream(strings.NewReader(stream), yield, newToolCallState(), true); err != nil {
		t.Fatalf("parseStream: %v", err)
	}
	var calls []llm.ToolCall
	for _, c := range chunks {
		if c.Content != "" {
			t.Fatalf("inline tool call leaked as content: %q", c.Content)
		}
		calls = append(calls, c.ToolCalls...)
	}
	if len(calls) != 1 || calls[0].Function.Name != "read" ||
		calls[0].Function.Arguments != `{"path":"VOICE.md"}` {
		t.Fatalf("unexpected calls: %#v", calls)
	}
}

func TestParseToolProtocolWithPreamble(t *testing.T) {
	calls := parseToolProtocol(strings.Join([]string{
		"Let me read the relevant files.",
		`{"tool_calls":[{"id":"call_1","name":"read","arguments":{"path":"zkit/agent/runner/drain.go"}},{"id":"call_2","name":"read","arguments":{"path":"zkit/ai/llm/claudecode/provider.go"}}]}`,
	}, "\n"))
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2: %#v", len(calls), calls)
	}
	if calls[0].ID != "call_1" || calls[0].Function.Name != "read" ||
		calls[0].Function.Arguments != `{"path":"zkit/agent/runner/drain.go"}` {
		t.Fatalf("unexpected first call: %#v", calls[0])
	}
	if calls[1].ID != "call_2" || calls[1].Function.Name != "read" ||
		calls[1].Function.Arguments != `{"path":"zkit/ai/llm/claudecode/provider.go"}` {
		t.Fatalf("unexpected second call: %#v", calls[1])
	}
}
