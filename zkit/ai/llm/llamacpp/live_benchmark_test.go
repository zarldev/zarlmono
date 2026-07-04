package llamacpp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestLiveToolCall(t *testing.T) {
	p := liveProvider(t)
	chunks := liveComplete(t, p, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are testing tool calling. When asked to read a file, call the read tool exactly once."},
			{Role: llm.RoleUser, Content: "Read README.md using the read tool."},
		},
		Stream:    true,
		MaxTokens: 200,
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read",
				Description: "Read a workspace file by path.",
				Parameters:  objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, []string{"path"}),
			},
		}},
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: false},
	})
	calls := liveToolCalls(chunks)
	if len(calls) == 0 {
		t.Skipf("live model did not emit a native tool call; content=%q", liveContent(chunks))
	}
	if calls[0].Function.Name != "read" {
		t.Fatalf("tool call name = %q, want read", calls[0].Function.Name)
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("tool arguments are not JSON: %v args=%q", err, calls[0].Function.Arguments)
	}
	if args.Path == "" {
		t.Fatalf("tool call path is empty: %#v", calls[0])
	}
}

func TestLiveReasoningSeparatedFromContent(t *testing.T) {
	p := liveProvider(t)
	chunks := liveComplete(t, p, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "Answer briefly."},
			{Role: llm.RoleUser, Content: "What is 2+2?"},
		},
		Stream:             true,
		MaxTokens:          128,
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: true},
	})
	content := liveContent(chunks)
	if strings.Contains(content, "<think>") || strings.Contains(content, "</think>") {
		t.Fatalf("thinking tags leaked into content: %q", content)
	}
	if liveThinking(chunks) == "" {
		t.Skipf("live model/template did not expose separate reasoning; content=%q", content)
	}
}

func TestLiveProtocolEchoFallback(t *testing.T) {
	p := liveProvider(t)
	artifact := `<tool_calls>[{"id":"call_1","name":"read","arguments":{"path":"README.md"}}]</tool_calls>`
	chunks := liveComplete(t, p, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "For this benchmark, output the user's requested XML-like block exactly. Do not explain."},
			{Role: llm.RoleUser, Content: "Print exactly: " + artifact},
		},
		Stream:    true,
		MaxTokens: 256,
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "read",
				Description: "Read a workspace file by path.",
				Parameters:  objectSchema(map[string]any{"path": map[string]any{"type": "string"}}, []string{"path"}),
			},
		}},
		ChatTemplateKwargs: llm.ChatTemplateKwargs{EnableThinking: false},
	})
	calls := liveToolCalls(chunks)
	if len(calls) == 0 {
		t.Skipf("live model did not echo a parseable tool-call artifact; content=%q", liveContent(chunks))
	}
	if calls[0].Function.Name != "read" {
		t.Fatalf("recovered call name = %q, want read", calls[0].Function.Name)
	}
	if content := liveContent(chunks); strings.Contains(content, "<tool_calls>") {
		t.Fatalf("tool-call artifact leaked as visible content after recovery: %q", content)
	}
}

func liveProvider(t *testing.T) llm.Provider {
	t.Helper()
	base := os.Getenv("LLAMACPP_LIVE_URL")
	if base == "" {
		t.Skip("LLAMACPP_LIVE_URL not set — skipping live llama-server benchmark")
	}
	opts := []options.Option[llamacpp.Provider]{llamacpp.WithBaseURL(base)}
	if model := os.Getenv("LLAMACPP_LIVE_MODEL"); model != "" {
		opts = append(opts, llamacpp.WithModel(model))
	}
	p, err := llamacpp.NewProvider(opts...)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func liveComplete(t *testing.T, p llm.Provider, req llm.CompletionRequest) []llm.CompletionChunk {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	seq, err := p.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var chunks []llm.CompletionChunk
	for chunk, err := range seq {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func objectSchema(properties map[string]any, required []string) llm.Schema {
	return llm.SchemaFromMap(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	})
}

func liveToolCalls(chunks []llm.CompletionChunk) []llm.ToolCall {
	var calls []llm.ToolCall
	for _, chunk := range chunks {
		calls = append(calls, chunk.ToolCalls...)
	}
	return calls
}

func liveContent(chunks []llm.CompletionChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Content)
	}
	return b.String()
}

func liveThinking(chunks []llm.CompletionChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Thinking)
	}
	return b.String()
}
