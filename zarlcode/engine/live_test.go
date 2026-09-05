package engine_test

import (
	"context"
	"errors"
	"path/filepath"
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
	if err := live.Close(t.Context()); !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want wrapped browser error", err)
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
