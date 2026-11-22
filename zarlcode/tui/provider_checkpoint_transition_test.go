package tui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

func TestExactSessionRejectsCustomTargetBeforePersistence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
		if err := settings.Registry.UpsertProvider(t.Context(), backends.ProviderDefinition{
			Name: "custom", DisplayName: "Custom", AdapterType: backends.AdapterTypes.OPENAICOMPATIBLE,
			BaseURL: "http://127.0.0.1:1/v1", DefaultModel: "custom-model", ContextWindow: 64000, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		f.ui.SetSettings(settings)
		selection := prefs.ModelSelection{Provider: "openai", Model: "saved-model"}
		requireSetSelection(t, settings, prefs.ScopeWorkspace, selection)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "exact history")
		id := f.ui.SessionIdentity()
		before, err := f.store.SessionVersion(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		target := f.live.RunTarget()
		driveBeforeCommand(f, f.ui.SwitchTarget(prefs.ModelSelection{Provider: "custom", Model: "custom-model"}))
		if !strings.Contains(f.ui.ToastText(), "start a new conversation") {
			t.Fatalf("missing immediate target restriction: %s", f.ui.ToastText())
		}
		requireSelection(t, settings, prefs.ScopeWorkspace, selection)
		after, err := f.store.SessionVersion(t.Context(), id)
		if err != nil || before != after || f.ui.SessionIdentity() != id {
			t.Fatalf("rejected switch altered the saved exact head: %v", err)
		}
		if got := f.live.RunTarget(); got.Provider != target.Provider || got.Spec != target.Spec {
			t.Fatal("rejected switch changed live route")
		}
		settleRewindTurn(t, f, "still works on original route")
	})
}

func TestFreshExactSessionSwitchesModelAndPreservesCheckpoints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
		settings.Registry = backends.NewRegistry(backends.WithSettingsService(sessionFixtureKeys{}))
		f.ui.SetSettings(settings)
		requireSetSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "openai", Model: "saved-model"})
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "fresh exact history")

		sessionID := f.ui.SessionIdentity()
		beforeContext := f.live.ContextSnapshot()
		beforeHead, err := f.store.GetSessionResumeState(t.Context(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), sessionID)
		if err != nil || len(checkpoints) != 1 {
			t.Fatalf("initial checkpoints: %v, %v", checkpoints, err)
		}
		original, err := f.store.GetSessionCheckpoint(t.Context(), sessionID, checkpoints[0].ID)
		if err != nil {
			t.Fatal(err)
		}

		selection := prefs.ModelSelection{Provider: "openai", Model: "other-supported-model"}
		driveBeforeCommand(f, f.ui.SwitchTarget(selection))
		if !strings.Contains(f.ui.ToastText(), "switched to") {
			t.Fatalf("model switch rejected: %s", f.ui.ToastText())
		}
		requireSelection(t, settings, prefs.ScopeWorkspace, selection)
		if f.ui.SessionIdentity() != sessionID || !reflect.DeepEqual(f.live.ContextSnapshot(), beforeContext) {
			t.Fatal("switch changed conversation identity or context")
		}
		target := f.live.RunTarget()
		if target.Spec.Name != selection.Provider || target.Model != selection.Model || f.ui.ActiveProviderSpec() != target.Spec {
			t.Fatalf("live/UI target not updated: %+v", target.Spec)
		}
		afterHead, err := f.store.GetSessionResumeState(t.Context(), sessionID)
		if err != nil || !reflect.DeepEqual(afterHead, beforeHead) {
			t.Fatalf("switch rewrote settled history: %v", err)
		}

		// Supply the fixture adapter for the selected target; do not contact an API.
		settings.Registry = nil
		f.ui.RepointProvider(f.provider, target.Spec, target.Window, nil)
		var request llm.CompletionRequest
		f.provider.request = func(got llm.CompletionRequest) { request = got }
		settleRewindTurn(t, f, "continue with selected model")
		if len(request.Messages) < len(beforeContext)+1 {
			t.Fatal("continued request omitted retained history")
		}
		completed := f.live.ContextSnapshot()
		if len(completed) <= len(beforeContext) || !reflect.DeepEqual(completed[:len(beforeContext)], beforeContext) {
			t.Fatal("continued turn lost historical context")
		}
		if err := f.ui.ResumeSavedSession(t.Context(), sessionID); err != nil {
			t.Fatal(err)
		}
		if f.live.RunTarget().Model != selection.Model || !reflect.DeepEqual(f.live.ContextSnapshot(), completed) {
			t.Fatal("resume lost selected model or context")
		}
		retained, err := f.store.GetSessionCheckpoint(t.Context(), sessionID, original.ID)
		if err != nil || !reflect.DeepEqual(retained, original) {
			t.Fatalf("switch rewrote original checkpoint: %v", err)
		}
		checkpoint, err := rewind.Load(t.Context(), f.store, retained)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := checkpoint.Snapshot()
		if err != nil || snapshot.Target.Model != "saved-model" {
			t.Fatalf("original checkpoint target changed: %+v, %v", snapshot.Target, err)
		}
		changed := snapshot.Target
		changed.Model = selection.Model
		if _, err := checkpoint.PrepareBranch("child", "branch", 99, changed); !errors.Is(err, rewind.ErrTarget) {
			t.Fatalf("checkpoint admitted a different replay target: %v", err)
		}
	})
}

func TestOrdinaryProviderQualifiedSwitchDoesNotPromoteHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) {}
		f.ui.RepointProvider(f.provider, engine.ProviderSpec{Name: "custom", Model: "saved-model"}, 64000, nil)
		settleRewindTurn(t, f, "ordinary history")
		f.ui.RepointProvider(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"}, 64000, nil)
		settleRewindTurn(t, f, "qualified route with ordinary history")
		checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), f.ui.SessionIdentity())
		if err != nil || len(checkpoints) != 0 {
			t.Fatalf("mixed history falsely promoted to exact: %d, %v", len(checkpoints), err)
		}
		f.live.RestoreContext(nil)
		if err := f.ui.ResumeSavedSession(t.Context(), f.ui.SessionIdentity()); err != nil {
			t.Fatal(err)
		}
		if len(f.live.ContextSnapshot()) < 4 {
			t.Fatal("mixed-route context not restored")
		}
	})
}
