package runner_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// singleCallProvider emits exactly one tool call with the configured
// arguments string, then a final reply on the next iteration. Used to
// drive end-to-end tests of the dispatch JSON-parse path without
// going through the OpenAI SDK or the full conversation loop.
type singleCallProvider struct {
	iter     atomic.Int32
	toolName string
	rawArgs  string
	finalSay string
}

func (p *singleCallProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			yield(llm.CompletionChunk{
				ToolCalls: []llm.ToolCall{{
					ID:   "tc-1",
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      p.toolName,
						Arguments: p.rawArgs,
					},
				}},
			}, nil)
		default:
			yield(llm.CompletionChunk{Content: p.finalSay}, nil)
		}
	}, nil
}

func (p *singleCallProvider) Name() string { return "single-call" }

// argsCapturingTool records the Arguments map of every dispatched call.
// Used so tests can assert "the args reached me looking like X" after
// the repair pipeline ran.
type argsCapturingTool struct {
	mu   sync.Mutex
	seen []tools.ToolParameters
}

func (t *argsCapturingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "capture",
		Description: "test tool that records its arguments",
		Parameters: llm.Schema{
			Type:                 "object",
			Properties:           map[string]llm.Schema{},
			AdditionalProperties: true,
		},
	}
}

func (t *argsCapturingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	t.mu.Lock()
	t.seen = append(t.seen, call.Arguments)
	t.mu.Unlock()
	return &tools.ToolResult{Success: true, Data: "ok"}, nil
}

func TestDispatch_RepairsMalformedToolArguments(t *testing.T) {
	t.Parallel()
	// Literal newlines + missing closer — both common qwen3.6 emissions.
	raw := "{\"path\": \"foo.go\", \"content\": \"line one\nline two\""

	tool := &argsCapturingTool{}
	reg := tools.NewRegistry()
	reg.Register(tool)

	r := runner.New(
		runner.ClientFromProvider(&singleCallProvider{
			toolName: "capture",
			rawArgs:  raw,
			finalSay: "done",
		}),
		runner.WithTools(reg),
		runner.WithMaxIterations(3),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("Reason = %q, want completed", res.Reason)
	}

	tool.mu.Lock()
	defer tool.mu.Unlock()
	if len(tool.seen) != 1 {
		t.Fatalf("tool calls captured = %d, want 1", len(tool.seen))
	}
	args := tool.seen[0]
	if args["path"] != "foo.go" {
		t.Errorf("path = %v, want 'foo.go' (repair must preserve other fields)", args["path"])
	}
	if args["content"] != "line one\nline two" {
		t.Errorf("content = %q, want 'line one\\nline two' (literal newline must be repaired)", args["content"])
	}
}

func TestDispatch_FailsValidationOnUnrepairableArgs(t *testing.T) {
	t.Parallel()
	// Total garbage — no repair step can save this.
	raw := "this is not json at all, no objects, no brackets, just prose"

	tool := &argsCapturingTool{}
	reg := tools.NewRegistry()
	reg.Register(tool)

	r := runner.New(
		runner.ClientFromProvider(&singleCallProvider{
			toolName: "capture",
			rawArgs:  raw,
			finalSay: "done",
		}),
		runner.WithTools(reg),
		runner.WithMaxIterations(3),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	// The tool MUST NOT have been dispatched — repair failed, so
	// dispatch should have short-circuited to a Validation result.
	tool.mu.Lock()
	if got := len(tool.seen); got != 0 {
		tool.mu.Unlock()
		t.Errorf("tool dispatches = %d, want 0 (unrepairable args should short-circuit before Execute)", got)
		return
	}
	tool.mu.Unlock()

	// And the model must see a tool message carrying the failure —
	// otherwise it has no signal to correct its JSON next turn.
	var toolMsg *llm.Message
	for i := range res.Messages {
		if res.Messages[i].Role == "tool" && res.Messages[i].ToolCallID == "tc-1" {
			toolMsg = &res.Messages[i]
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message for tc-1 in task result")
	}
	if !strings.Contains(toolMsg.Content, "did not parse as JSON") {
		t.Errorf("tool message content = %q, want it to surface the parse-failure mode", toolMsg.Content)
	}
}
