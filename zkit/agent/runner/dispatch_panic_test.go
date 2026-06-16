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

// panicTool's Execute always panics — a stand-in for a nil deref, a
// buggy MCP server, or a bad dynamic tool.
type panicTool struct{ name string }

func (t panicTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tools.ToolName(t.name),
		Description: "always panics",
		Parameters:  llm.Schema{Type: "object"},
	}
}

func (t panicTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	panic("boom: tool blew up")
}

// panicSeqProvider calls the panicking tool on iteration 1, then on
// iteration 2 records whether the recovered failure was threaded back as
// a tool message and replies to finish the run.
type panicSeqProvider struct {
	iter atomic.Int32
	tool string

	mu          sync.Mutex
	sawPanicMsg bool
}

func (p *panicSeqProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
				ID:       "tc-panic",
				Type:     "function",
				Function: llm.ToolCallFunction{Name: p.tool, Arguments: `{}`},
			}}}, nil)
		default:
			for _, m := range req.Messages {
				if strings.Contains(m.Content, "panicked") {
					p.mu.Lock()
					p.sawPanicMsg = true
					p.mu.Unlock()
				}
			}
			yield(llm.CompletionChunk{Content: "recovered, moving on"}, nil)
		}
	}, nil
}

func (p *panicSeqProvider) Name() string { return "panic-seq" }

func (p *panicSeqProvider) sawPanic() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawPanicMsg
}

// A panicking tool must not crash the runner: the panic is recovered, a
// failure result is threaded back to the model as a tool message, and the
// run completes normally. Asserted for both the sequential (concurrency 1)
// and parallel (concurrency > 1) dispatch paths, since the recover lives
// in the per-tool execution goroutine shared by both.
func TestRunnerRecoversPanickingTool(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		concurrency int
	}{
		{"sequential", 1},
		{"parallel", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg := tools.NewRegistry()
			reg.Register(panicTool{name: "blow_up"})
			prov := &panicSeqProvider{tool: "blow_up"}

			r := runner.New(
				runner.ClientFromProvider(prov),
				runner.WithTools(reg),
				runner.WithMaxIterations(4),
				runner.WithToolConcurrency(tc.concurrency),
			)

			res := r.Run(t.Context(), runner.TaskSpec{
				ID:     taskscope.ID(uuid.NewString()),
				Prompt: "go",
			})
			if res.Err != nil {
				t.Fatalf("Run returned error after a tool panic: %v", res.Err)
			}
			if res.Reason != runner.TerminalCompleted {
				t.Fatalf("Reason = %q, want completed (the loop must survive the panic)", res.Reason)
			}
			if !prov.sawPanic() {
				t.Error("the recovered panic was not threaded back to the model as a tool failure")
			}
		})
	}
}
