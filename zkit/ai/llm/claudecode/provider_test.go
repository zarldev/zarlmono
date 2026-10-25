package claudecode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
)

func newFixtureProvider(t *testing.T, output string) (*claudecode.Provider, string) {
	t.Helper()
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt")
	scriptPath := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat > \"$CLAUDE_TEST_PROMPT\"\ncat <<'OUTPUT'\n" + output + "\nOUTPUT\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	t.Setenv("CLAUDE_TEST_PROMPT", promptPath)
	p := claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "test-token"}}, claudecode.WithBinaryPath(scriptPath))

	return p, promptPath
}

func complete(t *testing.T, stream string, req llm.CompletionRequest) ([]llm.CompletionChunk, string) {
	t.Helper()
	p, promptPath := newFixtureProvider(t, stream)
	var chunks []llm.CompletionChunk
	for chunk, err := range p.Complete(t.Context(), req) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		chunks = append(chunks, chunk.Clone())
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	return chunks, string(prompt)
}

func collect(chunks []llm.CompletionChunk) (string, string, []llm.ToolCall) {
	var text, thought strings.Builder
	var calls []llm.ToolCall
	for _, chunk := range chunks {
		text.WriteString(chunk.Content)
		thought.WriteString(chunk.Thinking)
		calls = append(calls, chunk.ToolCalls...)
	}
	return text.String(), thought.String(), calls
}

func textEvent(text string) string {
	line, _ := json.Marshal(map[string]any{"type": "stream_event", "event": map[string]any{"delta": map[string]any{"type": "text_delta", "text": text}}})
	return string(line)
}

func TestProviderParsesStreamWithoutDuplicatingTerminalSnapshots(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"delta":{"type":"thinking_delta","thinking":"Let me "}}}`,
		`{"type":"stream_event","event":{"delta":{"type":"thinking_delta","thinking":"think."}}}`,
		textEvent("hello "), textEvent("world"),
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think."},{"type":"text","text":"hello world"}]}}`,
	}, "\n")
	chunks, _ := complete(t, stream, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	content, thinking, _ := collect(chunks)
	if content != "hello world" {
		t.Errorf("content = %q, want hello world", content)
	}
	if thinking != "Let me think." {
		t.Errorf("thinking = %q, want Let me think.", thinking)
	}
}

func TestProviderParsesNativeToolCallsAndDeduplicatesSnapshot(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"README.md\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"README.md"}}]}}`,
	}, "\n")
	chunks, _ := complete(t, stream, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "read"}}})
	_, _, calls := collect(chunks)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1: %#v", len(calls), calls)
	}
	if got := calls[0]; got.ID != "toolu_1" || got.Type != "function" || got.Function.Name != "read_file" || got.Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool call: %#v", got)
	}
	if got := chunks[len(chunks)-1].FinishReason; got != llm.FinishReasons.TOOLCALLS {
		t.Errorf("finish reason = %q, want %q", got, llm.FinishReasons.TOOLCALLS)
	}
}

func TestProviderParsesTextToolProtocol(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{name: "object with preamble", text: "Let me read it.\n" + `{"tool_calls":[{"id":"call_1","name":"read","arguments":{"path":"README.md"}},{"id":"call_2","name":"grep","arguments":{"pattern":"TODO"}}]}`, want: 2},
		{name: "assistant tag nested shape", text: `<assistant_tool_calls>[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]</assistant_tool_calls>`, want: 1},
		{name: "tool tag flat shape", text: `<tool_calls>[{"id":"call_1","name":"bash","arguments":{"command":"echo hi"}}]</tool_calls>`, want: 1},
		{name: "bare array", text: `[{"id":"call_1","type":"function","function":{"name":"grep","arguments":"{\"pattern\":\"TODO\"}"}}]`, want: 1},
	}
	tool := llm.Tool{Type: "function", Function: llm.ToolFunction{Name: "read"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, _ := complete(t, textEvent(tc.text), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "use tools"}}, Tools: []llm.Tool{tool}})
			content, _, calls := collect(chunks)
			if content != "" {
				t.Errorf("tool artifact leaked as content: %q", content)
			}
			if len(calls) != tc.want {
				t.Fatalf("tool calls = %d, want %d: %#v", len(calls), tc.want, calls)
			}
		})
	}
}

func TestProviderSuppressesTruncatedToolArtifact(t *testing.T) {
	truncated := `{"tool_calls":[{"function":{"name":"bash","arguments":"{\"command\":\"ls`
	chunks, _ := complete(t, textEvent(truncated), llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "use a tool"}},
		Tools:    []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "bash"}}},
	})
	content, _, calls := collect(chunks)
	if content != "" || len(calls) != 0 {
		t.Fatalf("truncated artifact produced content %q and calls %#v", content, calls)
	}
}

func TestProviderPromptIncludesRolesToolsAndHistory(t *testing.T) {
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "be brief"},
			{Role: llm.RoleUser, Content: "say hi"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "bash", Arguments: `{"command":"echo hi"}`}}}},
			{Role: llm.RoleTool, ToolCallID: "call_1", Content: "hi\n"},
		},
		Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "bash", Description: "run a command"}}},
	}
	_, prompt := complete(t, "", req)
	for _, want := range []string{
		"<system>\nbe brief\n</system>", "<user>\nsay hi\n</user>",
		"<assistant_tool_calls>\n[{\"id\":\"call_1\",\"name\":\"bash\",\"arguments\":{\"command\":\"echo hi\"}}]",
		"<tool_result tool_call_id=\"call_1\">\nhi\n\n</tool_result>", "<available_tools>", "Tool calling protocol:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q in:\n%s", want, prompt)
		}
	}
}

func TestProviderReportsUsage(t *testing.T) {
	cases := []struct {
		name   string
		stream string
		want   llm.Usage
	}{
		{name: "terminal result", stream: `{"type":"result","subtype":"success","usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":50}}`, want: llm.Usage{PromptTokens: 150, CompletionTokens: 50, TotalTokens: 200, CachedTokens: 30, CacheCreationTokens: 20}},
		{name: "assistant fallback", stream: `{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":5}}}`, want: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, _ := complete(t, tc.stream, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
			last := chunks[len(chunks)-1]
			if !last.UsageReported || last.Usage != tc.want {
				t.Errorf("usage = %#v reported=%v, want %#v", last.Usage, last.UsageReported, tc.want)
			}
		})
	}
}

func TestProviderHonorsDownstreamStop(t *testing.T) {
	p, _ := newFixtureProvider(t, textEvent("first")+"\n"+textEvent("second"))
	calls := 0
	p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})(func(_ llm.CompletionChunk, err error) bool {
		calls++
		if err != nil {
			t.Errorf("unexpected error after downstream stop: %v", err)
		}
		return false
	})
	if calls != 1 {
		t.Fatalf("yield calls = %d, want 1", calls)
	}
}

type recordingTokenSource struct {
	called bool
}

func (s *recordingTokenSource) Token(context.Context) (claudecode.Token, error) {
	s.called = true
	return claudecode.Token{Access: "should-not-be-used"}, nil
}

func TestProviderRejectsMediaBeforeTokenOrCLILaunch(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \"$CLAUDE_TEST_LAUNCHED\"\n"), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	t.Setenv("CLAUDE_TEST_LAUNCHED", marker)
	tokens := &recordingTokenSource{}
	provider := claudecode.NewProvider(tokens, claudecode.WithBinaryPath(script))
	var got error
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{
		Role: llm.RoleUser,
		Parts: []llm.ContentPart{
			llm.TextPart("inspect"),
			llm.ImagePartFromURL("https://example.invalid/image.png"),
		},
	}}}) {
		if chunk.Content != "" || chunk.Thinking != "" || len(chunk.ToolCalls) != 0 || chunk.FinishReason != llm.FinishReasons.UNKNOWN || chunk.UsageReported {
			t.Fatalf("error chunk = %+v, want zero", chunk)
		}
		got = err
	}
	if !errors.Is(got, llm.ErrModelNotSupported) {
		t.Fatalf("error = %v, want ErrModelNotSupported", got)
	}
	if tokens.called {
		t.Fatal("media rejection acquired an OAuth token")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CLI launch marker stat error = %v, want not exist", err)
	}
}

func TestProviderPreservesCancellationCauseAfterCLILaunch(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"stream_event\",\"event\":{\"delta\":{\"type\":\"text_delta\",\"text\":\"ready\"}}}'\nexec sleep 60\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	provider := claudecode.NewProvider(
		claudecode.StaticTokenSource{T: claudecode.Token{Access: "test-token"}},
		claudecode.WithBinaryPath(script),
	)
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("synthetic cancellation")
	defer cancel(cause)

	var got error
	yields := 0
	provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "wait"}}})(func(chunk llm.CompletionChunk, err error) bool {
		yields++
		if yields == 1 {
			if err != nil || chunk.Content != "ready" {
				t.Fatalf("first yield = (%+v, %v), want ready content", chunk, err)
			}
			cancel(cause)
			return true
		}
		if chunk.Content != "" || chunk.Thinking != "" || len(chunk.ToolCalls) != 0 || chunk.FinishReason != llm.FinishReasons.UNKNOWN || chunk.UsageReported {
			t.Fatalf("terminal error chunk = %+v, want zero", chunk)
		}
		got = err
		return true
	})
	if yields != 2 {
		t.Fatalf("yield calls = %d, want content then cancellation", yields)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("error = %v, want cancellation cause %v", got, cause)
	}
}
