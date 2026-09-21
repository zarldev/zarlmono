package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestModeDowngradeJoinsChildrenAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "settled"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				childStarted := make(chan struct{})
				releaseChild := make(chan struct{})
				release := sync.OnceFunc(func() { close(releaseChild) })
				defer release()
				pending := make(chan struct{}, 1)
				rootRequests, childRequests := 0, 0
				sink := &modeControlSink{finished: func(e runner.ToolCompleted) {
					if e.ToolName == string(engine.ToolNameSetMode) {
						pending <- struct{}{}
					}
				}}
				provider := modeProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
					return func(yield func(llm.CompletionChunk, error) bool) {
						if taskscope.DepthFrom(ctx) > 0 {
							childRequests++
							if requestHasTool(req, "set_mode") {
								t.Error("child can see parent control")
							}
							if childRequests == 1 {
								modeCalls(modeCall("escape", "build"))(yield)
								return
							}
							if !strings.Contains(modeResult(req.Messages, "escape"), "not available") {
								t.Errorf("child control result=%q", modeResult(req.Messages, "escape"))
							}
							close(childStarted)
							select {
							case <-releaseChild:
								modeCalls()(yield)
							case <-ctx.Done():
								yield(llm.CompletionChunk{}, ctx.Err())
							}
							return
						}
						rootRequests++
						switch rootRequests {
						case 1:
							modeCalls(namedModeCall("spawn", "agent_spawn", `{"prompt":"finish the fixture","mode":"implement"}`), modeCall("plan", "plan"))(yield)
						case 2:
							assertModeRequest(t, req, true)
							modeCalls(namedModeCall("await", "agent_await", `{}`))(yield)
						default:
							modeCalls()(yield)
						}
					}
				})
				live := modeLive(t, root, provider, engine.WithLiveSink(sink))
				live.SetLimits(0, 8, 4, 1)
				done := make(chan runner.TaskResult, 1)
				go func() { done <- live.RunHeadless(ctx, "delegate then investigate", 8) }()
				<-pending
				<-childStarted
				synctest.Wait()
				if live.AppliedMode().Plan || len(sink.changes) != 0 {
					t.Error("Plan published before child joined")
				}
				if cancelled {
					cancel()
				} else {
					release()
				}
				res := <-done
				if cancelled {
					if !errors.Is(res.Err, context.Canceled) || live.AppliedMode().Plan || len(sink.changes) != 0 {
						t.Fatalf("cancel result=%+v mode=%+v", res, live.AppliedMode())
					}
				} else if res.Err != nil || !live.AppliedMode().Plan || len(sink.changes) != 1 {
					t.Fatalf("settled result=%+v events=%+v", res, sink.changes)
				}
			})
		})
	}
}
