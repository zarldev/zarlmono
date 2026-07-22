package engine

import (
	"context"
	"iter"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	computertools "github.com/zarldev/zarlmono/zkit/ai/tools/computer"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

type blockingProvider struct {
	started chan struct{}
}

func (p *blockingProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		close(p.started)
		<-ctx.Done()
		yield(llm.CompletionChunk{Done: true}, ctx.Err())
	}, nil
}

func (*blockingProvider) Name() string { return "blocking" }

// TestLiveRunner_BuildsGuardedSource verifies the guardrail chain
// assembles with the interactive Deps shape (catches a bad Deps before a
// live run would). prov/sink are unused by source(), so nil is fine.
func TestLiveRunner_BuildsGuardedSource(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	src, _, err := NewLiveRunner(nil, ws, nil, "local").source("")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if src == nil {
		t.Fatal("guarded source must not be nil")
	}
}

// TestLiveRunner_EarlyStopWatcher checks the headless early-stop wiring: no
// watcher until a command is configured, one once it is, and nil again after
// it's cleared. Building the watcher has no side effects (the probe goroutine
// only starts when Drive calls the Watcher), so this stays hermetic.
func TestLiveRunner_EarlyStopWatcher(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	l := NewLiveRunner(nil, ws, nil, "local")

	if w := l.earlyStopWatcher(); w != nil {
		t.Fatal("no command configured → watcher must be nil")
	}
	l.SetEarlyStopCommand([]string{"go", "test", "./..."})
	if w := l.earlyStopWatcher(); w == nil {
		t.Fatal("command configured → watcher must be built")
	}
	l.SetEarlyStopCommand(nil)
	if w := l.earlyStopWatcher(); w != nil {
		t.Fatal("cleared command → watcher must be nil again")
	}
}

func TestLiveRunner_ProgrammaticToolsSetting(t *testing.T) {
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
	settings := NewSettings(ctx, store, nil, t.TempDir())
	l := NewLiveRunner(nil, ws, nil, "local")
	l.SetSettingsHandle(settings)

	src, _, err := l.source("")
	if err != nil {
		t.Fatalf("source default: %v", err)
	}
	if hasTool(src, programtools.ToolName) {
		t.Fatal("program tool should default off")
	}
	if err := settings.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyProgrammaticTools, "on"); err != nil {
		t.Fatalf("set programmatic tools: %v", err)
	}
	src, _, err = l.source("")
	if err != nil {
		t.Fatalf("source enabled: %v", err)
	}
	if !hasTool(src, programtools.ToolName) {
		t.Fatal("program tool should be present when enabled")
		for _, name := range []tools.ToolName{code.ToolNameWrite, code.ToolNameEdit, code.ToolNameBash} {
			if !hasTool(src, name) {
				t.Fatalf("programmatic tools must not hide mutating/shell tool %q", name)
			}
		}
		for _, name := range []tools.ToolName{code.ToolNameRead, code.ToolNameGrep, code.ToolNameGlob, code.ToolNameLs, code.ToolNameFileMap, code.ToolNameRetrieveCode} {
			if hasTool(src, name) {
				t.Fatalf("programmatic tools should hide direct read/search tool %q", name)
			}
		}
	}
}

// TestLiveRunner_WebSearchRegistration verifies web_search is present exactly
// when a SearXNG URL is configured, and absent otherwise.
func TestLiveRunner_WebSearchRegistration(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	l := NewLiveRunner(nil, ws, nil, "local")

	srcOff, _, err := l.source("")
	if err != nil {
		t.Fatalf("source off: %v", err)
	}
	if hasTool(srcOff, tools.ToolNameWebSearch) {
		t.Error("web_search should be absent when no SearXNG URL is set")
	}
	srcOn, _, err := l.source("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("source on: %v", err)
	}
	if !hasTool(srcOn, tools.ToolNameWebSearch) {
		t.Error("web_search should be registered when a SearXNG URL is set")
	}
}

func TestLiveRunner_ComputerToolsRegistered(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	l := NewLiveRunner(nil, ws, nil, "local")

	src, _, err := l.source("")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if !hasTool(src, computertools.ToolNameComputerObserve) {
		t.Error("computer_observe should be registered")
	}
	if !hasTool(src, computertools.ToolNameComputerAct) {
		t.Error("computer_act should be registered")
	}
}

func TestLiveRunner_CloseCancelsActiveTurn(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	prov := &blockingProvider{started: make(chan struct{})}
	l := NewLiveRunner(prov, ws, nil, "local")
	l.SetContext(t.Context())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = l.RunTurn("wait")
	}()

	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := l.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunFn command did not return after Close")
	}
}

// buildLiveCompactor maps the engine name to a compactor; the no-LLM engines
// build directly and the LLM engines fall back to tiered without a provider,
// so a misconfigured engine never breaks compaction.
func TestBuildLiveCompactor_EngineSelection(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	l := NewLiveRunner(nil, ws, nil, "local") // satisfies compact.StateProvider

	if _, ok := buildLiveCompactor("structural", 32768, nil, "", l, "").(compact.Structural); !ok {
		t.Error("structural should build a Structural compactor")
	}
	if _, ok := buildLiveCompactor("tiered", 32768, nil, "", l, "").(*compact.Tiered); !ok {
		t.Error("tiered should build a *Tiered compactor")
	}
	for _, eng := range []string{"summary", "executive", "handover", "bogus", ""} {
		if _, ok := buildLiveCompactor(eng, 32768, nil, "", l, "").(*compact.Tiered); !ok {
			t.Errorf("%q without a provider should fall back to tiered", eng)
		}
	}
	// With a provider, handover builds the clear-and-reseed compactor.
	if _, ok := buildLiveCompactor("handover", 32768, fakeJudgeProvider{}, "m", l, t.TempDir()).(*compact.Handover); !ok {
		t.Error("handover with a provider should build a *Handover compactor")
	}
}

func hasTool(src tools.Source, name tools.ToolName) bool {
	for tool := range src.Tools(context.Background()) {
		if tool.Definition().Name == name {
			return true
		}
	}
	return false
}
