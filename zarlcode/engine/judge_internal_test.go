package engine

import (
	"context"
	"iter"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// fakeJudgeProvider satisfies llm.Provider for identity checks — the tests
// only care whether resolution hands back this instance or builds a new one.
type fakeJudgeProvider struct{}

func (fakeJudgeProvider) Complete(context.Context, llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(func(llm.CompletionChunk, error) bool) {}, nil
}
func (fakeJudgeProvider) Name() string { return "fake" }

func newJudgeTestSettings(t *testing.T) *Settings {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewSettings(t.Context(), store, nil, t.TempDir())
}

func TestDecomposeJudgeProvider(t *testing.T) {
	ctx := t.Context()
	s := newJudgeTestSettings(t)
	active := fakeJudgeProvider{}
	spec := ProviderSpec{Name: "llamacpp", Model: "local"}

	set := func(key, val string) {
		t.Helper()
		if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, key, val); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	// Off (default) → nil: the guardrail keeps its deterministic path.
	if got := s.DecomposeJudgeProvider(ctx, active, spec); got != nil {
		t.Fatalf("judge off: got %T, want nil", got)
	}

	// On with no overrides → the active provider instance is reused.
	set(prefs.KeyDecomposeJudge, "on")
	if got := s.DecomposeJudgeProvider(ctx, active, spec); got != llm.Provider(active) {
		t.Fatalf("judge on, no overrides: got %T, want the active provider", got)
	}

	// Provider override → a dedicated provider is built (not the active one).
	set(prefs.KeyJudgeProvider, "llamacpp")
	set(prefs.KeyJudgeModel, "qwen3-small")
	if got := s.DecomposeJudgeProvider(ctx, active, spec); got == nil || got == llm.Provider(active) {
		t.Fatalf("judge override: got %T (active reused=%v), want a dedicated provider", got, got == llm.Provider(active))
	}

	// Unbuildable override → falls back to the active provider rather than
	// disabling a judge the user enabled.
	set(prefs.KeyJudgeProvider, "no-such-backend")
	if got := s.DecomposeJudgeProvider(ctx, active, spec); got != llm.Provider(active) {
		t.Fatalf("unbuildable override: got %T, want active-provider fallback", got)
	}
}

// The live runner arms the judge into guardrail deps only when settings turn
// it on AND a provider resolves; everything else keeps DecomposeJudge nil so
// the decompose guardrail stays deterministic.
func TestGuardrailDepsDecomposeJudge(t *testing.T) {
	ctx := t.Context()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}

	// No settings handle → nil.
	live := NewLiveRunner(fakeJudgeProvider{}, ws, nil, "local")
	if live.guardrailDeps().DecomposeJudge != nil {
		t.Fatal("no settings handle: judge armed, want nil")
	}

	// Settings present but decompose_judge off (default) → still nil.
	s := newJudgeTestSettings(t)
	live.SetSettingsHandle(s)
	if live.guardrailDeps().DecomposeJudge != nil {
		t.Fatal("judge off: judge armed, want nil")
	}

	// On → armed, for both interactive and headless deps.
	if err := s.Svc.SetSetting(ctx, prefs.ScopeGlobal, prefs.KeyDecomposeJudge, "on"); err != nil {
		t.Fatalf("set decompose_judge: %v", err)
	}
	if live.guardrailDeps().DecomposeJudge == nil {
		t.Fatal("judge on: interactive deps missing judge")
	}
	if live.headlessGuardrailDeps().DecomposeJudge == nil {
		t.Fatal("judge on: headless deps missing judge")
	}

	// On with no provider to run it on (nil active, no override) → nil, not
	// a judge that would fail every verdict.
	bare := NewLiveRunner(nil, ws, nil, "local")
	bare.SetSettingsHandle(s)
	if bare.guardrailDeps().DecomposeJudge != nil {
		t.Fatal("judge on without any provider: judge armed, want nil")
	}
}
