package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestExactTargetSelectionPersistenceFailureRetainsState(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), t.TempDir()+"/settings.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	selection := prefs.ModelSelection{Provider: "openai", Model: "saved-model"}
	if err := settings.Svc.SetModelSelection(t.Context(), prefs.ScopeWorkspace, selection); err != nil {
		t.Fatal(err)
	}
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	before := live.RunTarget()
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	_, saved, err := reservation.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	history := []llm.Message{{Role: llm.RoleUser, Content: "retained prompt"}, {Role: llm.RoleAssistant, Content: "retained answer"}}
	if err := reservation.RestoreConversation(history, saved, code.Plan{}, before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = reservation.ApplyTargetSelection(ctx, engine.TargetUpdate{
		Provider: provider, Spec: engine.ProviderSpec{Name: "anthropic", Model: "new-model"}, Window: 128000,
	}, settings.Svc, prefs.ModelSelection{Provider: "anthropic", Model: "new-model"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("switch error = %v, want canceled persistence", err)
	}
	if !reflect.DeepEqual(live.RunTarget(), before) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("failed persistence changed live target or history")
	}
	for key, want := range map[string]string{prefs.KeyProvider: selection.Provider, prefs.KeyModel: selection.Model} {
		got, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, key)
		if err != nil || got.Value != want {
			t.Fatalf("saved %s = %q, %v, want %q", key, got.Value, err, want)
		}
	}
}
