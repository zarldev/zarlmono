package runner_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestDispatchBarrierIsExclusiveUnderExplicitConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		makeTool := func(name tools.ToolName, barrier bool) tools.Tool {
			return tools.New(tools.ToolSpec{Name: name, DispatchBarrier: barrier, WorkspaceAccess: tools.WorkspaceAccesses.READ}, func(context.Context, map[string]any) (string, error) {
				mu.Lock()
				order = append(order, string(name)+" start")
				mu.Unlock()
				time.Sleep(time.Second)
				mu.Lock()
				order = append(order, string(name)+" end")
				mu.Unlock()
				return string(name), nil
			})
		}
		registry := tools.NewRegistry(makeTool("before", false), makeTool("control", true), makeTool("after", false))
		client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("1", "before", "{}"), runnertest.ChunkToolCall("2", "control", "{}"), runnertest.ChunkToolCall("3", "after", "{}")}, {runnertest.ChunkText("done")}})
		res := runner.New(client, runner.WithTools(registry), runner.WithToolConcurrency(8)).Run(t.Context(), runner.TaskSpec{Prompt: "test"})
		want := []string{"before start", "before end", "control start", "control end", "after start", "after end"}
		if res.Err != nil || !slices.Equal(order, want) {
			t.Fatalf("err=%v order=%v", res.Err, order)
		}
		var ids []string
		for _, m := range res.Messages {
			if m.Role == llm.RoleTool {
				ids = append(ids, m.ToolCallID)
			}
		}
		if !slices.Equal(ids, []string{"1", "2", "3"}) {
			t.Fatalf("results=%v", ids)
		}
	})
}

func TestIterationPromptRetainsTaskAndEmptySystemSlot(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(strconv.FormatBool(fail), func(t *testing.T) {
			calls := 0
			cause := errors.New("render fixture")
			prompt := runner.PromptFunc(func(ctx context.Context, _ runner.PromptVars) (string, error) {
				if taskscope.IDFrom(ctx) != "same-task" {
					t.Errorf("task changed: %s", taskscope.IDFrom(ctx))
				}
				calls++
				if calls == 1 {
					return "", nil
				}
				if fail {
					return "", cause
				}
				return "refreshed", nil
			})
			request := 0
			client := timingClient(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					request++
					want := ""
					if request > 1 {
						want = "refreshed"
					}
					if req.Messages[0].Role != llm.RoleSystem || req.Messages[0].Content != want || req.Messages[1].Content != "prior" {
						t.Errorf("history=%+v", req.Messages)
					}
					if request == 1 {
						yield(runnertest.ChunkToolCall("1", "noop", "{}"), nil)
					} else {
						yield(runnertest.ChunkText("done"), nil)
					}
				}
			})
			res := runner.New(client, runner.WithIterationPrompt(prompt)).Run(t.Context(), runner.TaskSpec{ID: "same-task", Prompt: "test", Context: []llm.Message{{Role: llm.RoleUser, Content: "prior"}}})
			if res.ID != "same-task" || calls != 2 {
				t.Fatalf("id=%s renders=%d", res.ID, calls)
			}
			if fail {
				if !errors.Is(res.Err, runner.ErrPromptRender) || !errors.Is(res.Err, cause) || request != 1 {
					t.Fatalf("result=%+v requests=%d", res, request)
				}
			} else if res.Err != nil || res.Iterations != 2 {
				t.Fatalf("result=%+v", res)
			}
		})
	}
}

type delayedHistoryFailure struct{}

func (delayedHistoryFailure) Append(context.Context, []runner.ReplayMessage) error { return nil }
func (delayedHistoryFailure) Request(context.Context, llm.CompletionRequest) error {
	time.Sleep(2 * time.Second)
	return errors.New("capture fixture")
}

func TestPreparationFailureTiming(t *testing.T) {
	for _, phase := range []string{"prompt", "history"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := runner.New(runnertest.NewClient(nil), runner.WithHistorySink(delayedHistoryFailure{}))
				if phase == "prompt" {
					r = runner.New(runnertest.NewClient(nil), runner.WithPrompt(runner.PromptFunc(func(context.Context, runner.PromptVars) (string, error) {
						time.Sleep(2 * time.Second)
						return "", errors.New("prompt fixture")
					})))
				}
				result := r.Run(t.Context(), runner.TaskSpec{Prompt: "test"})
				if result.Err == nil || result.Timing.RequestPreparationDuration != 2*time.Second || result.Duration != 2*time.Second || result.Timing.ProviderAttempts != 0 {
					t.Fatalf("result=%+v", result)
				}
			})
		})
	}
}
