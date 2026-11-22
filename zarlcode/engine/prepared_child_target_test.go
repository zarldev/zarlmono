package engine_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestPreparedChildTargetConstructionScopeAndBudget(t *testing.T) {
	t.Parallel()
	settings := newJudgeTestSettings(t)
	def, _ := backends.Builtin("litellm")
	def.BaseURL = "http://127.0.0.1:1"
	settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{def}))
	prepared, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "litellm", Model: "parent/Alias", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	// A larger parent operational budget must not leak to unknown child models.
	prepared.Window = 900000
	prepared.Facts.Budget.Tokens = prepared.Window
	ws, err := code.NewWorkspace(settings.WorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	live := engine.NewLiveRunner(prepared.Provider, ws, prepared.Spec.Model, engine.WithSettings(settings))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.ApplyTarget(prepared)
	before := live.RunTarget()
	unchanged, err := live.PrepareChildTarget(t.Context(), catalog.Agent{Name: "unchanged"})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Provider != before.Provider || unchanged.Facts.Generation != before.Facts.Generation || unchanged.Window != before.Window || unchanged.Facts == before.Facts {
		t.Fatal("unchanged child lost actual construction or clone isolation")
	}
	unchanged.Facts.Resolution.Issues[0].Field = "mutated-child"
	if !reflect.DeepEqual(live.RunTarget(), before) {
		t.Fatal("child facts mutation escaped to parent")
	}
	changed, err := live.PrepareChildTarget(t.Context(), catalog.Agent{Name: "changed", Model: "unknown/Child Alias"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Provider == before.Provider || changed.Facts.Generation == before.Facts.Generation || changed.Model != "unknown/Child Alias" || changed.Spec.Model != changed.Model || changed.Facts.RequestedAlias != llm.ModelID(changed.Model) {
		t.Fatal("changed child inherited parent construction facts")
	}
	if changed.Window != engine.LiveContextWindow || changed.Facts.Budget.Tokens != changed.Window || changed.Facts.Budget.Source != engine.OperationalBudgetSources.BUDGETAPPLICATIONFALLBACK {
		t.Fatalf("unknown child budget = %d, %+v", changed.Window, changed.Facts.Budget)
	}
	if changed.Spec.Protocol != llm.ModelProtocols.PROTOCOLUNKNOWN || changed.Facts.SelectedProtocol != llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		t.Fatal("child selected protocol changed override intent")
	}
	if _, err := live.PrepareChildTarget(t.Context(), catalog.Agent{Name: "bad", Provider: "missing-route"}); err == nil {
		t.Fatal("missing child route accepted")
	}
	if !reflect.DeepEqual(live.RunTarget(), before) {
		t.Fatal("child preparation changed active parent")
	}
}
