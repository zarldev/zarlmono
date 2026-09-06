package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	model "github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
	"github.com/zarldev/zarlmono/zkit/db"
)

type requestRecordingProvider struct {
	requests []llm.CompletionRequest
}

func (p *requestRecordingProvider) Complete(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
	p.requests = append(p.requests, req)
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
	}
}

func (*requestRecordingProvider) Name() string { return "openai-codex" }

type blockingProvider struct{ started chan struct{} }

func (p *blockingProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.started)
		<-ctx.Done()
		yield(llm.CompletionChunk{}, ctx.Err())
	}
}

func (*blockingProvider) Name() string { return "blocking" }

type stubbornProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p *stubbornProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.started)
		<-p.release
		yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
	}
}

func (*stubbornProvider) Name() string { return "stubborn" }

type gatedToolProvider struct {
	ready   chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func (p *gatedToolProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call > 1 {
		return func(yield func(llm.CompletionChunk, error) bool) {
			yield(llm.CompletionChunk{Content: "done", FinishReason: llm.FinishReasons.STOP}, nil)
		}
	}
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.ready)
		<-p.release
		yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
			ID:   "write-1",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      string(code.ToolNameWrite),
				Arguments: `{"path":"blocked.txt","content":"must not be written"}`,
			},
		}}}, nil)
	}
}

func (*gatedToolProvider) Name() string { return "gated-tool" }

func TestWithLiveSinkRejectsNil(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("WithLiveSink(nil) did not panic")
		}
	}()
	_ = engine.WithLiveSink(nil)
}

func TestLiveRunnerBuildsGuardedSource(t *testing.T) {
	live := newLive(t)
	if got := live.Inspect(t.Context()); len(got.Tools) == 0 || len(got.Guardrails.Disabled) != 0 {
		t.Fatalf("inspection did not assemble guarded tools: tools=%d disabled=%v errors=%v", len(got.Tools), got.Guardrails.Disabled, got.Errors)
	}
}

func TestLiveRunnerProgrammaticToolsSetting(t *testing.T) {
	ctx := t.Context()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithSettings(settings))

	if !inspectionHasTool(live.Inspect(ctx), programtools.ToolName) {
		t.Fatal("program tool should default on")
	}
	if err := settings.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProgrammaticTools, "off"); err != nil {
		t.Fatalf("disable programmatic tools: %v", err)
	}
	ins := live.Inspect(ctx)
	if inspectionHasTool(ins, programtools.ToolName) {
		t.Fatal("program tool should be absent when disabled")
	}
	for _, name := range []tools.ToolName{code.ToolNameWrite, code.ToolNameEdit, code.ToolNameBash, code.ToolNameRead, code.ToolNameGrep, code.ToolNameGlob, code.ToolNameLs, code.ToolNameFileMap, code.ToolNameRetrieveCode} {
		if !inspectionHasTool(ins, name) {
			t.Fatalf("expected direct tool %q when programmatic tools disabled", name)
		}
	}
}

func TestLiveRunnerAppliesCodexEffortOnNextTurn(t *testing.T) {
	ctx := t.Context()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, ws.Root())
	provider := &requestRecordingProvider{}
	live := engine.NewLiveRunner(provider, ws, "gpt-5.6", engine.WithSettings(settings))
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai-codex", Model: "gpt-5.6"}})

	for _, effort := range []string{"high", "max"} {
		if err := settings.Svc.SetSetting(ctx, prefs.ScopeWorkspace, prefs.KeyCodexEffort, effort); err != nil {
			t.Fatal(err)
		}
		if err := live.RunTurn(ctx, effort); err != nil {
			t.Fatal(err)
		}
		if got := provider.requests[len(provider.requests)-1].Options["reasoning_effort"]; got != effort {
			t.Fatalf("next turn effort = %v, want %s", got, effort)
		}
	}
}

func TestLiveRunnerWebSearchRegistration(t *testing.T) {
	live := newLive(t)
	if inspectionHasTool(live.Inspect(t.Context()), tools.ToolNameWebSearch) {
		t.Fatal("web_search should be absent without configuration")
	}
	live.SetWebSearch(search.NewSearxng("http://127.0.0.1:8080"))
	ins := live.Inspect(t.Context())
	if !inspectionHasTool(ins, programtools.ToolName) || inspectionHasTool(ins, tools.ToolNameWebSearch) {
		t.Fatal("program should expose configured web_search while direct web_search stays hidden")
	}
}

func TestLiveRunnerComputerToolsRegistered(t *testing.T) {
	ins := newLive(t).Inspect(t.Context())
	for _, name := range []tools.ToolName{computertools.ToolNameComputerObserve, computertools.ToolNameComputerAct} {
		if !inspectionHasTool(ins, name) {
			t.Errorf("computer tool %q should be registered", name)
		}
	}
}

func TestLiveRunnerCloseCancelsActiveTurn(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	prov := &blockingProvider{started: make(chan struct{})}
	live := engine.NewLiveRunner(prov, ws, "local")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = live.RunTurn(t.Context(), "wait")
	}()

	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := live.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("turn did not return after Close")
	}
}

func TestLiveRunnerCloseDeadlineDoesNotAbandonDrain(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prov := &stubbornProvider{started: make(chan struct{}), release: make(chan struct{})}
	live := engine.NewLiveRunner(prov, ws, "local")
	turnCtx, stopTurn := context.WithCancel(t.Context())
	turnDone := make(chan struct{})
	release := sync.OnceFunc(func() { close(prov.release) })
	defer func() {
		release()
		stopTurn()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Second)
		defer cancel()
		if err := live.Close(cleanupCtx); err != nil {
			t.Errorf("cleanup Close: %v", err)
		}
		select {
		case <-turnDone:
		case <-cleanupCtx.Done():
			t.Error("cleanup: turn did not drain")
		}
	}()
	go func() {
		defer close(turnDone)
		_ = live.RunTurn(turnCtx, "wait")
	}()

	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	waitCtx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	err = live.Close(waitCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v; want deadline exceeded", err)
	}

	release()
	drainCtx, drainCancel := context.WithTimeout(t.Context(), time.Second)
	defer drainCancel()
	if err := live.Close(drainCtx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	select {
	case <-turnDone:
	case <-time.After(time.Second):
		t.Fatal("turn did not drain after provider release")
	}
	if err := live.Close(t.Context()); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if err := live.RunTurn(t.Context(), "after close"); err == nil {
		t.Fatal("RunTurn after Close succeeded")
	}
}

func TestLiveRunnerCloseReportsComputerCleanupError(t *testing.T) {
	want := errors.New("browser close")
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	fake := &fakeComputerSession{closeErr: want}
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return fake, nil }))
	if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	for i := range 2 {
		if err := live.Close(t.Context()); !errors.Is(err, want) {
			t.Fatalf("Close %d error = %v, want wrapped browser error", i+1, err)
		}
	}
	if fake.closeCalls != 1 {
		t.Fatalf("computer close calls = %d, want 1", fake.closeCalls)
	}
}

func TestLiveRunnerConcurrentCloseSharesCleanup(t *testing.T) {
	want := errors.New("browser close")
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeComputerSession{closeErr: want}
	live := engine.NewLiveRunner(nil, ws, "local", engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) { return fake, nil }))
	if _, err := live.ComputerObserve(t.Context(), model.ObserveRequest{}); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	ctx := t.Context()
	const callers = 8
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = live.Close(ctx)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, want) {
			t.Errorf("Close caller %d error = %v, want wrapped browser error", i, err)
		}
	}
	if fake.closeCalls != 1 {
		t.Fatalf("computer close calls = %d, want 1", fake.closeCalls)
	}
}

func TestLiveRunnerPlanModeGatesMidTurnDispatch(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	provider := &gatedToolProvider{ready: make(chan struct{}), release: make(chan struct{})}
	live := engine.NewLiveRunner(provider, ws, "local")
	release := sync.OnceFunc(func() { close(provider.release) })
	t.Cleanup(func() {
		release()
		_ = live.Close(context.WithoutCancel(t.Context()))
	})

	done := make(chan error, 1)
	ctx := t.Context()
	go func() { done <- live.RunTurn(ctx, "write the file") }()
	select {
	case <-provider.ready:
	case <-t.Context().Done():
		t.Fatal("provider did not reach first dispatch")
	}
	live.SetPlanMode(true)
	release()
	if err := <-done; err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked write stat error = %v, want not exist", err)
	}
}

func newLive(t *testing.T) *engine.LiveRunner {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return engine.NewLiveRunner(nil, ws, "local")
}

func inspectionHasTool(ins engine.Inspection, name tools.ToolName) bool {
	for _, tool := range ins.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
