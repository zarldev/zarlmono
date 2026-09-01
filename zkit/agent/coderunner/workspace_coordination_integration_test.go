package coderunner_test

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestConcurrentRunnerTasksCoordinateWorkspacePaths(t *testing.T) {
	t.Run("disjoint writes overlap", func(t *testing.T) {
		source := newBlockingWriteSource()
		coordinated := coderunner.CoordinateWorkspace(source, tools.NewWorkspaceCoordinator())
		firstDone := runToolTask(t, coordinated, "first", "zkit/a.go")
		secondDone := runToolTask(t, coordinated, "second", "zarlcode/b.go")

		entered := receiveEntries(t, source.entered, 2)
		if entered["first"] != "zkit/a.go" || entered["second"] != "zarlcode/b.go" {
			t.Fatalf("entered = %#v", entered)
		}
		close(source.release)
		assertTaskCompleted(t, firstDone)
		assertTaskCompleted(t, secondDone)
	})

	for _, tc := range []struct {
		name       string
		firstPath  string
		secondPath string
	}{
		{name: "same path serializes", firstPath: "zkit/a.go", secondPath: "zkit/a.go"},
		{name: "ancestor serializes descendant", firstPath: "zkit", secondPath: "zkit/agent/a.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newBlockingWriteSource()
			coordinated := coderunner.CoordinateWorkspace(source, tools.NewWorkspaceCoordinator())
			firstDone := runToolTask(t, coordinated, "first", tc.firstPath)
			first := receiveEntry(t, source.entered)
			if first.task != "first" || first.path != tc.firstPath {
				t.Fatalf("first entry = %#v", first)
			}

			waitSink := newWorkspaceWaitSink()
			secondDone := runToolTaskWithSink(t, coordinated, waitSink, "second", tc.secondPath)
			receiveWaitStarted(t, waitSink.started)
			select {
			case entry := <-source.entered:
				t.Fatalf("overlapping task entered before release: %#v", entry)
			default:
			}

			close(source.release)
			second := receiveEntry(t, source.entered)
			if second.task != "second" || second.path != tc.secondPath {
				t.Fatalf("second entry = %#v", second)
			}
			assertTaskCompleted(t, firstDone)
			assertTaskCompleted(t, secondDone)
		})
	}
}

func TestRunnerPublishesWorkspaceWaitLifecycle(t *testing.T) {
	source := newBlockingWriteSource()
	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.AcquirePaths("holder", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatal(err)
	}
	coordinated := coderunner.CoordinateWorkspace(source, coordinator)
	sink := newWorkspaceWaitSink()
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("call-waiter", "write_test", `{"path":"zkit/a.go"}`)},
		{runnertest.ChunkText("done")},
	})
	r := runner.New(client, runner.WithTools(coordinated), runner.WithMaxIterations(3), runner.WithSink(sink))
	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- r.Run(t.Context(), runner.TaskSpec{ID: "waiter", Prompt: "write zkit/a.go"})
	}()

	started := receiveWaitStarted(t, sink.started)
	if started.TaskID != "waiter" || started.ToolID != "call-waiter" || started.ToolName != "write_test" || started.BlockerCount != 1 {
		t.Fatalf("wait started = %#v", started)
	}
	holder.Release()
	entry := receiveEntry(t, source.entered)
	if entry.task != "waiter" || entry.path != "zkit/a.go" {
		t.Fatalf("entry = %#v", entry)
	}
	close(source.release)
	assertTaskCompleted(t, done)
	ended, ok := sink.FirstWorkspaceWaitEnded()
	if !ok || ended.ToolID != "call-waiter" || ended.Outcome != tools.WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED || ended.Duration < 0 {
		t.Fatalf("wait ended = %#v", ended)
	}
}

type blockingWriteSource struct {
	entered chan toolEntry
	release chan struct{}
	spec    tools.ToolSpec
}

type toolEntry struct {
	task string
	path string
}

func newBlockingWriteSource() *blockingWriteSource {
	return &blockingWriteSource{
		entered: make(chan toolEntry, 2),
		release: make(chan struct{}),
		spec: tools.ToolSpec{
			Name:            "write_test",
			Description:     "Test path-scoped write.",
			WorkspaceAccess: tools.WorkspaceAccesses.WRITE,
			WorkspaceScope:  tools.WorkspaceScopeArgument("path"),
			Mutates:         true,
		},
	}
}

func (s *blockingWriteSource) Tools(context.Context) iter.Seq[tools.Tool] {
	return func(yield func(tools.Tool) bool) { yield(blockingWriteTool{source: s}) }
}

func (s *blockingWriteSource) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	entry := toolEntry{task: string(taskscope.IDFrom(ctx)), path: call.Arguments.String("path", "")}
	select {
	case s.entered <- entry:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-s.release:
		return tools.Success(call.ID, "written"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type blockingWriteTool struct{ source *blockingWriteSource }

func (t blockingWriteTool) Definition() tools.ToolSpec { return t.source.spec }
func (t blockingWriteTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return t.source.Execute(ctx, call)
}

type workspaceWaitSink struct {
	*runnertest.Sink
	started chan runner.WorkspaceWaitStarted
}

func newWorkspaceWaitSink() *workspaceWaitSink {
	return &workspaceWaitSink{
		Sink:    runnertest.NewSink(),
		started: make(chan runner.WorkspaceWaitStarted, 1),
	}
}

func (s *workspaceWaitSink) OnWorkspaceWaitStarted(event runner.WorkspaceWaitStarted) {
	s.Sink.OnWorkspaceWaitStarted(event)
	s.started <- event
}

func runToolTask(t *testing.T, source tools.Source, id taskscope.ID, path string) <-chan runner.TaskResult {
	return runToolTaskWithSink(t, source, runnertest.NewSink(), id, path)
}

func runToolTaskWithSink(t *testing.T, source tools.Source, sink runner.EventSink, id taskscope.ID, path string) <-chan runner.TaskResult {
	t.Helper()
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("call-"+string(id), "write_test", fmt.Sprintf(`{"path":%q}`, path))},
		{runnertest.ChunkText("done")},
	})
	r := runner.New(client,
		runner.WithTools(source),
		runner.WithMaxIterations(3),
		runner.WithSink(sink),
	)
	done := make(chan runner.TaskResult, 1)
	go func() {
		done <- r.Run(t.Context(), runner.TaskSpec{ID: id, Prompt: "write " + path})
	}()
	return done
}

func receiveEntry(t *testing.T, entered <-chan toolEntry) toolEntry {
	t.Helper()
	select {
	case entry := <-entered:
		return entry
	case <-t.Context().Done():
		t.Fatal("tool did not enter")
		return toolEntry{}
	}
}

func receiveEntries(t *testing.T, entered <-chan toolEntry, count int) map[string]string {
	t.Helper()
	got := make(map[string]string, count)
	for range count {
		entry := receiveEntry(t, entered)
		got[entry.task] = entry.path
	}
	return got
}

func assertTaskCompleted(t *testing.T, done <-chan runner.TaskResult) {
	t.Helper()
	select {
	case result := <-done:
		if result.Reason != runner.TerminalCompleted || result.Err != nil {
			t.Fatalf("task result = %#v", result)
		}
	case <-t.Context().Done():
		t.Fatal("task did not complete")
	}
}

func receiveWaitStarted(t *testing.T, started <-chan runner.WorkspaceWaitStarted) runner.WorkspaceWaitStarted {
	t.Helper()
	select {
	case event := <-started:
		return event
	case <-t.Context().Done():
		t.Fatal("workspace wait did not start")
		return runner.WorkspaceWaitStarted{}
	}
}
