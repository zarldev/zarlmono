package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/options"
)

type modeProvider func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (modeProvider) Name() string { return "offline-mode-fixture" }
func (p modeProvider) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return p(ctx, req)
}

func modeCalls(calls ...llm.ToolCall) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if len(calls) == 0 {
			yield(llm.CompletionChunk{Content: "Task complete.", FinishReason: llm.FinishReasons.STOP}, nil)
			return
		}
		yield(llm.CompletionChunk{ToolCalls: calls}, nil)
	}
}
func modeCall(id, mode string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: "set_mode", Arguments: fmt.Sprintf(`{"mode":%q,"reason":"fixture transition"}`, mode)}}
}
func namedModeCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: name, Arguments: args}}
}
func requestHasTool(req llm.CompletionRequest, name string) bool {
	for _, tool := range req.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}
func assertModeRequest(t *testing.T, req llm.CompletionRequest, plan bool) {
	t.Helper()
	if !requestHasTool(req, "set_mode") || requestHasTool(req, "write") == plan {
		t.Errorf("mode=%v tools=%+v", plan, req.Tools)
	}
	if len(req.Messages) == 0 || strings.Contains(req.Messages[0].Content, "**PLAN mode**") != plan {
		t.Errorf("prompt disagrees with mode=%v", plan)
	}
	for _, tool := range req.Tools {
		if tool.Function.Name == "set_mode" {
			schema := tool.Function.Parameters
			if !slices.Equal(schema.Required, []string{"mode", "reason"}) || len(schema.Properties["mode"].Enum) != 2 {
				t.Errorf("mode schema=%+v", schema)
			}
		}
	}
}
func modeResult(messages []llm.Message, id string) string {
	for _, msg := range messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID == id {
			return msg.Content
		}
	}
	return ""
}
func modeLive(t *testing.T, root string, provider llm.Provider, opts ...options.Option[engine.LiveRunner]) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(provider, ws, "fixture", opts...)
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return live
}

func TestAutonomousModesContinueOneTask(t *testing.T) {
	for _, headless := range []bool{false, true} {
		t.Run(strconv.FormatBool(headless), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			request := 0
			var task taskscope.ID
			provider := modeProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					if task == "" {
						task = taskscope.IDFrom(ctx)
					}
					if task != taskscope.IDFrom(ctx) {
						t.Error("transition changed task identity")
					}
					request++
					var stream llm.CompletionStream
					switch request {
					case 1:
						assertModeRequest(t, req, false)
						stream = modeCalls(modeCall("plan", "plan"), namedModeCall("suppressed", "write", `{"path":"old-policy.txt","content":"forbidden"}`))
					case 2:
						assertModeRequest(t, req, true)
						if !strings.Contains(modeResult(req.Messages, "suppressed"), "mode transition pending") {
							t.Errorf("missing truthful suppressed result: %+v", req.Messages)
						}
						stream = modeCalls(modeCall("build", "build"), namedModeCall("suppressed-program", "program", `{"script":"emit(1)"}`))
					case 3:
						assertModeRequest(t, req, false)
						if !strings.Contains(modeResult(req.Messages, "suppressed-program"), "mode transition pending") {
							t.Error("program ran in old-policy batch")
						}
						stream = modeCalls(namedModeCall("write", "write", `{"path":"implemented.txt","content":"done"}`))
					default:
						stream = modeCalls()
					}
					stream(yield)
				}
			})
			live := modeLive(t, root, provider)
			if headless {
				res := live.RunHeadless(t.Context(), "Create implemented.txt; plan briefly then implement.", 8)
				if res.Err != nil || res.ID != task || res.Iterations != 4 || res.Timing.ProviderAttempts != 4 {
					t.Fatalf("result=%+v", res)
				}
			} else if err := live.RunTurn(t.Context(), "Create implemented.txt; plan briefly then implement."); err != nil {
				t.Fatal(err)
			}
			if request != 4 || live.AppliedMode().Plan || live.AppliedMode().Generation != 2 {
				t.Fatalf("requests=%d mode=%+v", request, live.AppliedMode())
			}
			if _, err := os.Stat(filepath.Join(root, "old-policy.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("suppressed write: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(root, "implemented.txt"))
			if err != nil || string(data) != "done" {
				t.Fatalf("implementation=%q err=%v", data, err)
			}
		})
	}
}

func TestModeCeilingAndNestedControl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	request := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		request++
		assertModeRequest(t, req, true)
		if request == 1 {
			return modeCalls(modeCall("build", "build"), namedModeCall("write", "write", `{"path":"forbidden.txt","content":"no"}`), namedModeCall("nested", "program", `{"script":"emit(call(\"set_mode\", {\"mode\":\"build\",\"reason\":\"escape\"}))"}`))
		}
		if !strings.Contains(modeResult(req.Messages, "build"), "inspect-only authority ceiling") {
			t.Error("ceiling not enforced")
		}
		if !strings.Contains(modeResult(req.Messages, "write"), "not callable") {
			t.Error("write not blocked")
		}
		if strings.Contains(modeResult(req.Messages, "nested"), "transition pending") {
			t.Error("nested control admitted")
		}
		return modeCalls()
	})
	live := modeLive(t, root, provider, engine.WithReadOnlyTasks())
	live.SetPlanMode(false)
	res := live.RunHeadless(t.Context(), "review only", 5)
	if res.Err != nil || !live.AppliedMode().Plan {
		t.Fatalf("result=%+v mode=%+v", res, live.AppliedMode())
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write escaped: %v", err)
	}
}

func TestModeBudgetNoopsConflictsAndReplan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	request := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		request++
		switch request {
		case 1:
			return modeCalls(modeCall("noop", "build"), modeCall("plan1", "plan"), modeCall("duplicate", "plan"), modeCall("conflict", "build"))
		case 2:
			if !strings.Contains(modeResult(req.Messages, "noop"), "already in requested mode") || !strings.Contains(modeResult(req.Messages, "duplicate"), "already pending") || !strings.Contains(modeResult(req.Messages, "conflict"), "conflicting") {
				t.Errorf("control results=%+v", req.Messages)
			}
			return modeCalls(modeCall("build1", "build"))
		case 3:
			return modeCalls(modeCall("plan2", "plan"))
		case 4:
			return modeCalls(modeCall("build2", "build"))
		case 5:
			return modeCalls(modeCall("budget", "plan"), modeCall("noop2", "build"))
		default:
			if !strings.Contains(modeResult(req.Messages, "budget"), "four mode changes") || !strings.Contains(modeResult(req.Messages, "noop2"), "already in requested mode") {
				t.Errorf("budget results=%+v", req.Messages)
			}
			return modeCalls()
		}
	})
	live := modeLive(t, t.TempDir(), provider)
	res := live.RunHeadless(t.Context(), "investigate then implement; replan as needed", 8)
	if res.Err != nil || res.Iterations != 6 || live.AppliedMode().Generation != 4 {
		t.Fatalf("result=%+v mode=%+v", res, live.AppliedMode())
	}
}

type modeControlSink struct {
	liveRecordingSink
	finished func(runner.ToolCompleted)
	changes  []engine.ModeChanged
}

func (s *modeControlSink) OnToolCompleted(_ context.Context, e runner.ToolCompleted) {
	if s.finished != nil {
		s.finished(e)
	}
}
func (s *modeControlSink) ModeChanged(e engine.ModeChanged) { s.changes = append(s.changes, e) }

func TestPendingModeInvalidatedByOverrideOrCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelled), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := 0
			sink := &modeControlSink{}
			provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
				request++
				if request == 1 {
					return modeCalls(modeCall("plan", "plan"))
				}
				assertModeRequest(t, req, false)
				return modeCalls()
			})
			live := modeLive(t, t.TempDir(), provider, engine.WithLiveSink(sink))
			sink.finished = func(e runner.ToolCompleted) {
				if e.ToolName == string(engine.ToolNameSetMode) {
					if cancelled {
						cancel()
					} else {
						live.SetPlanMode(false)
					}
				}
			}
			res := live.RunHeadless(ctx, "test pending transition", 4)
			if cancelled && !errors.Is(res.Err, context.Canceled) {
				t.Fatalf("result=%+v", res)
			}
			if !cancelled && res.Err != nil {
				t.Fatal(res.Err)
			}
			if live.AppliedMode().Plan || len(sink.changes) != 0 {
				t.Fatalf("stale transition applied: %+v", sink.changes)
			}
			// A subsequent root run cannot revive the abandoned pending transition.
			res = live.RunHeadless(t.Context(), "next task", 2)
			if res.Err != nil || live.AppliedMode().Plan {
				t.Fatalf("next task=%+v", res)
			}
		})
	}
}

func TestSmallTaskStaysInBuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	live := modeLive(t, t.TempDir(), modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		assertModeRequest(t, req, false)
		return modeCalls()
	}))
	res := live.RunHeadless(t.Context(), "answer briefly", 3)
	if res.Err != nil || res.Iterations != 1 || live.AppliedMode().Generation != 0 {
		t.Fatalf("result=%+v", res)
	}
}

var _ tools.ToolName = engine.ToolNameSetMode
