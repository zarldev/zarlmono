package runner_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

func readBatchChunks(n int) [][]llm.CompletionChunk {
	calls := make([]llm.CompletionChunk, n)
	for i := range n {
		calls[i] = runnertest.ChunkToolCall(fmt.Sprintf("read-%d", i), "read", "{}")
	}
	return [][]llm.CompletionChunk{calls, {runnertest.ChunkText("done")}}
}

// The delay models independent I/O; this measures tool-batch latency, not model
// generation or end-to-end performance on a real repository.
func BenchmarkRunnerReadBatch(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		name := "sequential"
		if parallel {
			name = "default"
		}
		b.Run(name, func(b *testing.B) {
			read := tools.New(tools.ToolSpec{Name: "read", Description: "read fixture", WorkspaceAccess: tools.WorkspaceAccesses.READ},
				func(context.Context, map[string]any) (string, error) {
					time.Sleep(time.Millisecond)
					return "contents", nil
				})
			registry := tools.NewRegistry(read)
			chunks := readBatchChunks(8)
			b.ReportAllocs()
			for b.Loop() {
				var r *runner.Runner
				if parallel {
					r = runner.New(runnertest.NewClient(chunks), runner.WithTools(registry))
				} else {
					r = runner.New(runnertest.NewClient(chunks), runner.WithTools(registry), runner.WithToolConcurrency(1))
				}
				if result := r.Run(b.Context(), runner.TaskSpec{ID: "bench", Prompt: "read"}); result.Err != nil {
					b.Fatal(result.Err)
				}
			}
		})
	}
}

func TestDefaultReadParallelismIsBoundedAndKeepsResultOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    []options.Option[runner.Runner]
		peak    int
		elapsed time.Duration
	}{
		{"default", nil, 4, 2 * time.Second},
		{"explicit zero", []options.Option[runner.Runner]{runner.WithToolConcurrency(0)}, 1, 8 * time.Second},
		{"explicit one", []options.Option[runner.Runner]{runner.WithToolConcurrency(1)}, 1, 8 * time.Second},
		{"explicit two", []options.Option[runner.Runner]{runner.WithToolConcurrency(2)}, 2, 4 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mu sync.Mutex
				active, peak := 0, 0
				read := tools.New(tools.ToolSpec{Name: "read", Description: "read fixture", WorkspaceAccess: tools.WorkspaceAccesses.READ},
					func(context.Context, map[string]any) (string, error) {
						mu.Lock()
						active++
						peak = max(peak, active)
						mu.Unlock()
						// Simulated I/O completes in bounded waves under synctest.
						time.Sleep(time.Second)
						mu.Lock()
						active--
						mu.Unlock()
						return "contents", nil
					})
				opts := append([]options.Option[runner.Runner]{runner.WithTools(tools.NewRegistry(read))}, tc.opts...)
				r := runner.New(runnertest.NewClient(readBatchChunks(8)), opts...)
				start := time.Now()
				result := r.Run(t.Context(), runner.TaskSpec{Prompt: "read"})
				if result.Err != nil || peak != tc.peak || time.Since(start) != tc.elapsed {
					t.Fatalf("peak=%d elapsed=%s error=%v; want peak=%d elapsed=%s", peak, time.Since(start), result.Err, tc.peak, tc.elapsed)
				}
				assertReadResultOrder(t, result, 8)
			})
		})
	}
}

func assertReadResultOrder(t *testing.T, result runner.TaskResult, n int) {
	t.Helper()
	var ids []string
	for _, message := range result.Messages {
		if message.Role == llm.RoleTool {
			ids = append(ids, message.ToolCallID)
		}
	}
	want := make([]string, n)
	for i := range n {
		want[i] = fmt.Sprintf("read-%d", i)
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("result IDs = %v; want %v", ids, want)
	}
}

type readPhase struct {
	Phase int `json:"phase"`
	Delay int `json:"delay"`
}

func TestDefaultReadParallelismPreservesNonReadBarriers(t *testing.T) {
	for _, spec := range []tools.ToolSpec{
		{Name: "write", Mutates: true},
		{Name: "shell", AffectsWorkspace: true},
		{Name: "unclassified"},
		{Name: "no-workspace", WorkspaceAccess: tools.WorkspaceAccesses.NONE},
		{Name: "exclusive", WorkspaceAccess: tools.WorkspaceAccesses.WRITE},
		{Name: "mutating-read", WorkspaceAccess: tools.WorkspaceAccesses.READ, Mutates: true},
		{Name: "effectful-read", WorkspaceAccess: tools.WorkspaceAccesses.READ, AffectsWorkspace: true},
	} {
		t.Run(spec.Name.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var mu sync.Mutex
				phase, active, finished := 0, 0, 0
				read := tools.New(tools.ToolSpec{Name: "read", Description: "phase read", WorkspaceAccess: tools.WorkspaceAccesses.READ},
					func(_ context.Context, args readPhase) (int, error) {
						mu.Lock()
						if phase != args.Phase {
							t.Errorf("read phase=%d; want %d", phase, args.Phase)
						}
						active++
						mu.Unlock()
						time.Sleep(time.Duration(args.Delay) * time.Second)
						mu.Lock()
						defer mu.Unlock()
						if phase != args.Phase {
							t.Errorf("phase changed while read was active: %d", phase)
						}
						active--
						finished++
						return phase, nil
					})
				spec.Description = "ordered barrier"
				barrier := tools.New(spec, func(context.Context, map[string]any) (string, error) {
					mu.Lock()
					if active != 0 || finished != 2 {
						t.Errorf("barrier started with active=%d finished=%d", active, finished)
					}
					phase = -1
					mu.Unlock()
					time.Sleep(time.Second)
					mu.Lock()
					phase = 1
					mu.Unlock()
					return "changed", nil
				})
				client := runnertest.NewClient([][]llm.CompletionChunk{{
					runnertest.ChunkToolCall("before-slow", "read", `{"phase":0,"delay":2}`),
					runnertest.ChunkToolCall("before-fast", "read", `{"phase":0,"delay":1}`),
					runnertest.ChunkToolCall("barrier", spec.Name.String(), `{}`),
					runnertest.ChunkToolCall("after-slow", "read", `{"phase":1,"delay":2}`),
					runnertest.ChunkToolCall("after-fast", "read", `{"phase":1,"delay":1}`),
				}, {runnertest.ChunkText("done")}})
				start := time.Now()
				result := runner.New(client, runner.WithTools(tools.NewRegistry(read, barrier))).Run(t.Context(), runner.TaskSpec{Prompt: "read, change, read"})
				if result.Err != nil || finished != 4 || time.Since(start) != 5*time.Second {
					t.Fatalf("finished=%d elapsed=%s error=%v", finished, time.Since(start), result.Err)
				}
			})
		})
	}
}

func TestDefaultReadParallelismCancelsQueuedCallsAndJoinsActiveCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		release := make(chan struct{})
		var started, exited, writes atomic.Int32
		read := tools.New(tools.ToolSpec{Name: "read", Description: "blocked read", WorkspaceAccess: tools.WorkspaceAccesses.READ},
			func(ctx context.Context, _ map[string]any) (string, error) {
				started.Add(1)
				<-ctx.Done()
				<-release
				exited.Add(1)
				return "partial", ctx.Err()
			})
		write := tools.New(tools.ToolSpec{Name: "write", Description: "write", Mutates: true}, func(context.Context, map[string]any) (string, error) {
			writes.Add(1)
			return "changed", nil
		})
		chunks := readBatchChunks(8)
		chunks[0] = append(chunks[0], runnertest.ChunkToolCall("write", "write", "{}"))
		r := runner.New(runnertest.NewClient(chunks), runner.WithTools(tools.NewRegistry(read, write)))
		done := make(chan runner.TaskResult, 1)
		go func() { done <- r.Run(ctx, runner.TaskSpec{Prompt: "read"}) }()
		synctest.Wait()
		if started.Load() != 4 {
			t.Errorf("started=%d; want four", started.Load())
		}
		cancel()
		synctest.Wait()
		var result runner.TaskResult
		returned := false
		select {
		case result = <-done:
			returned = true
			t.Error("Run returned before active reads drained")
		default:
		}
		close(release)
		if !returned {
			result = <-done
		}
		if !errors.Is(result.Err, context.Canceled) || exited.Load() != 4 || started.Load() != 4 || writes.Load() != 0 {
			t.Fatalf("started=%d exited=%d writes=%d error=%v", started.Load(), exited.Load(), writes.Load(), result.Err)
		}
		var results int
		for _, message := range result.Messages {
			if message.Role == llm.RoleTool {
				results++
			}
		}
		if results != 9 {
			t.Fatalf("settled results=%d; want 9", results)
		}
	})
}

func TestDefaultReadParallelismResolvesMetadataAfterBarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := tools.NewRegistry()
		var active atomic.Int32
		future := func(readOnly bool) tools.Tool {
			access := tools.WorkspaceAccesses.WRITE
			if readOnly {
				access = tools.WorkspaceAccesses.READ
			}
			return tools.New(tools.ToolSpec{Name: "future", Description: "replaced tool", WorkspaceAccess: access},
				func(context.Context, map[string]any) (string, error) {
					if active.Add(1) != 1 {
						t.Error("stale read metadata allowed replaced write tools to overlap")
					}
					defer active.Add(-1)
					time.Sleep(time.Second)
					return "changed", nil
				})
		}
		if err := registry.Register(future(true)); err != nil {
			t.Fatal(err)
		}
		change := tools.New(tools.ToolSpec{Name: "register", Description: "replace tool", Mutates: true},
			func(context.Context, map[string]any) (string, error) {
				return "registered", registry.Register(future(false))
			})
		if err := registry.Register(change); err != nil {
			t.Fatal(err)
		}
		client := runnertest.NewClient([][]llm.CompletionChunk{{
			runnertest.ChunkToolCall("register", "register", "{}"),
			runnertest.ChunkToolCall("a", "future", "{}"),
			runnertest.ChunkToolCall("b", "future", "{}"),
		}, {runnertest.ChunkText("done")}})
		start := time.Now()
		result := runner.New(client, runner.WithTools(registry)).Run(t.Context(), runner.TaskSpec{Prompt: "replace, then use"})
		if result.Err != nil || time.Since(start) != 2*time.Second {
			t.Fatalf("elapsed=%s error=%v; want sequential replacements", time.Since(start), result.Err)
		}
	})
}
