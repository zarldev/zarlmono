package engine_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func targetFactsFixture() *engine.TargetFacts {
	return &engine.TargetFacts{
		RequestedAlias: "alias",
		Resolution: backends.ModelResolution{
			Descriptor: llm.ModelDescriptor{
				Surfaces: map[llm.ModelProtocol]llm.ModelSurface{llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS: {Limits: llm.ModelLimits{InputTokens: 123}}},
				Evidence: []llm.MetadataProvenance{{Snapshot: "original"}},
			},
			Issues: []backends.ModelResolutionIssue{{Field: "original", Evidence: []llm.MetadataProvenance{{Snapshot: "original"}}}},
		},
	}
}

func mutateTargetFacts(facts *engine.TargetFacts) {
	facts.RequestedAlias = "mutated"
	facts.Resolution.Descriptor.Surfaces[llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS] = llm.ModelSurface{}
	facts.Resolution.Descriptor.Evidence[0].Snapshot = "mutated"
	facts.Resolution.Issues[0].Field = "mutated"
	facts.Resolution.Issues[0].Evidence[0].Snapshot = "mutated"
}

func TestRunTargetClonePreservesGenerationAndIsolatesPureFacts(t *testing.T) {
	t.Parallel()
	settings := preparedGatewaySettings("http://127.0.0.1:1")
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "alias", APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	update.Facts.Resolution = targetFactsFixture().Resolution
	original := engine.RunTarget{Provider: update.Provider, Spec: update.Spec, Model: update.Spec.Model, Window: update.Window, Facts: update.Facts}
	clone := original.Clone()
	if clone.Facts == original.Facts || !reflect.DeepEqual(clone, original) {
		t.Fatal("clone changed the target or retained the original facts pointer")
	}
	if clone.Facts.Generation == (engine.TargetGeneration{}) || clone.Facts.Generation != original.Facts.Generation {
		t.Fatal("clone lost construction identity")
	}
	want := original.Clone()
	mutateTargetFacts(clone.Facts)
	if !reflect.DeepEqual(original, want) {
		t.Fatal("clone mutation changed original pure facts")
	}
	mutated := clone.Clone()
	original.Facts.Resolution.Descriptor.Evidence[0].Snapshot = "changed-original"
	original.Facts.Resolution.Issues[0].Evidence[0].Snapshot = "changed-original"
	if !reflect.DeepEqual(clone, mutated) {
		t.Fatal("original mutation changed cloned evidence")
	}
	legacy := engine.RunTarget{Provider: update.Provider, Model: "legacy"}
	if got := legacy.Clone(); got.Facts != nil || got.Provider != legacy.Provider || got.Model != legacy.Model {
		t.Fatal("legacy clone changed borrowed provider or absent facts")
	}
}

func TestTargetFactsPublicationAndSnapshotClone(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-apply", true: "reserved-apply"}[reserved], func(t *testing.T) {
			t.Parallel()
			live := reservationRunner(t, provider)
			update := engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "alias"}, Window: 12345, Facts: targetFactsFixture()}
			if reserved {
				r, err := live.ReserveRuntime()
				if err != nil {
					t.Fatal(err)
				}
				if err := r.ApplyTarget(update); err != nil {
					r.Release()
					t.Fatal(err)
				}
				r.Release()
			} else {
				live.ApplyTarget(update)
			}
			want := live.RunTarget()
			mutateTargetFacts(update.Facts)
			if !reflect.DeepEqual(live.RunTarget(), want) {
				t.Fatal("publication retained caller's facts collections")
			}
			snapshot := live.RunTarget()
			mutateTargetFacts(snapshot.Facts)
			if !reflect.DeepEqual(live.RunTarget(), want) {
				t.Fatal("snapshot exposed runtime facts collections")
			}
			clone := want.Clone()
			mutateTargetFacts(clone.Facts)
			if !reflect.DeepEqual(live.RunTarget(), want) {
				t.Fatal("RunTarget.Clone borrowed pure facts")
			}
		})
	}
}

func TestTargetFactsLegacyMutationsClearPreviousFacts(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	cases := []struct {
		name  string
		apply func(*engine.LiveRunner)
	}{
		{"apply", func(l *engine.LiveRunner) {
			l.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "other"}, Window: 100})
		}},
		{"provider", func(l *engine.LiveRunner) { l.SetProvider(provider, "other") }},
		{"spec", func(l *engine.LiveRunner) {
			l.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "other"})
		}},
		{"model", func(l *engine.LiveRunner) { l.SetModel("other") }},
		{"window", func(l *engine.LiveRunner) { l.SetContextWindow(100) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			live := reservationRunner(t, provider)
			live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "alias"}, Window: 12345, Facts: targetFactsFixture()})
			tc.apply(live)
			if live.RunTarget().Facts != nil {
				t.Fatal("legacy mutation inherited incompatible facts")
			}
		})
	}
	live := reservationRunner(t, provider)
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "alias"}, Window: 12345, Facts: targetFactsFixture()})
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Release)
	if err := r.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "other"}}); err != nil {
		t.Fatal(err)
	}
	if got := live.RunTarget(); got.Facts != nil || got.Window != engine.LiveContextWindow {
		t.Fatal("reserved legacy update inherited previous facts/window")
	}
}

func TestTargetFactsRestoreConversationClonesAndClears(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	target := live.RunTarget()
	target.Facts = targetFactsFixture()
	saved := rewind.Target{Provider: target.Spec.Name, Model: target.Model, Window: target.Window}
	if err := r.RestoreConversation(nil, saved, code.Plan{}, target); err != nil {
		t.Fatal(err)
	}
	want := live.RunTarget()
	mutateTargetFacts(target.Facts)
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("conversation restore retained facts aliases")
	}
	target.Facts = nil
	if err := r.RestoreConversation(nil, saved, code.Plan{}, target); err != nil {
		t.Fatal(err)
	}
	if live.RunTarget().Facts != nil {
		t.Fatal("legacy exact restore inherited previous facts")
	}
}

func TestTargetFactsStoredSessionRestoreClonesAndClears(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), t.TempDir()+"/preferences.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	session := engine.StoredSessionRestore{ID: "session", UseSavedTarget: true, Target: engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "alias"}, Window: 54321, Facts: targetFactsFixture()}}
	if err := r.RestoreStoredSession(t.Context(), session, settings.Svc); err != nil {
		t.Fatal(err)
	}
	want := live.RunTarget()
	mutateTargetFacts(session.Target.Facts)
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("stored restore retained facts aliases")
	}
	if err := r.RestoreSessionContext(nil, engine.MCPActivations{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("context-only restore changed same-route facts")
	}
	session.Target.Facts = nil
	if err := r.RestoreStoredSession(t.Context(), session, settings.Svc); err != nil {
		t.Fatal(err)
	}
	if live.RunTarget().Facts != nil {
		t.Fatal("stored legacy target inherited previous facts")
	}
}

func TestTargetFactsFailedTransitionsPreserveWholeValue(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.ApplyTarget(engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "alias"}, Window: 12345, Facts: targetFactsFixture()})
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}})
	want, history := live.RunTarget(), live.ContextSnapshot()
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	rejected := engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "claude-code", Model: "sonnet"}, Window: 99999, Facts: targetFactsFixture()}
	if err := r.ApplyTarget(rejected); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("admission error = %v", err)
	}
	if !reflect.DeepEqual(live.RunTarget(), want) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("rejected target partially published")
	}
	store, err := db.Open(t.Context(), t.TempDir()+"/preferences.db")
	if err != nil {
		t.Fatal(err)
	}
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	update := engine.TargetUpdate{Provider: provider, Spec: engine.ProviderSpec{Name: "openai", Model: "other"}, Window: 99999, Facts: targetFactsFixture()}
	if err := r.ApplyTargetSelection(t.Context(), update, settings.Svc, prefs.ModelSelection{Provider: "openai", Model: "other"}); err == nil {
		t.Fatal("closed store accepted preference write")
	}
	if !reflect.DeepEqual(live.RunTarget(), want) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("persistence failure partially published target")
	}
	if err := r.RestoreStoredSession(t.Context(), engine.StoredSessionRestore{ID: "new", UseSavedTarget: true, Target: update}, settings.Svc); err == nil {
		t.Fatal("closed store accepted session selection")
	}
	if !reflect.DeepEqual(live.RunTarget(), want) || !reflect.DeepEqual(live.ContextSnapshot(), history) {
		t.Fatal("session persistence failure partially published target")
	}
	r.Release()
	dispatch, err := live.ReserveTurn()
	if err != nil {
		t.Fatal(err)
	}
	defer dispatch.Release()
	if err := dispatch.ApplyTarget(update); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatal(err)
	}
	live.ApplyTarget(update)
	if !reflect.DeepEqual(live.RunTarget(), want) {
		t.Fatal("busy legacy publication changed facts")
	}
}

func TestTargetFactsAtomicPublication(t *testing.T) {
	t.Parallel()
	settings := preparedGatewaySettings("http://127.0.0.1:1")
	first, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "first", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "gateway-route", Model: "second", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	live := reservationRunner(t, first.Provider)
	live.ApplyTarget(first)
	var group sync.WaitGroup
	group.Go(func() {
		for range 100 {
			live.ApplyTarget(second)
			live.ApplyTarget(first)
		}
	})
	for range 100 {
		target := live.RunTarget()
		want := first
		if target.Facts.Generation == second.Facts.Generation {
			want = second
		}
		if target.Provider != want.Provider || target.Spec != want.Spec || target.Model != want.Spec.Model || target.Window != want.Window || !reflect.DeepEqual(target.Facts, want.Facts) {
			t.Error("partially published target")
		}
	}
	group.Wait()
}

func TestTargetFactsSelectionPersistsBeforePublication(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), t.TempDir()+"/preferences.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings := engine.NewSettings(store, nil, nil, t.TempDir())
	update, err := settings.PrepareTargetCached(t.Context(), engine.ProviderSpec{Name: "litellm", Model: "alias", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	live := reservationRunner(t, update.Provider)
	r, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	if err := r.ApplyTargetSelection(t.Context(), update, settings.Svc, prefs.ModelSelection{Provider: update.Spec.Name, Model: update.Spec.Model}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{prefs.KeyProvider: update.Spec.Name, prefs.KeyModel: update.Spec.Model} {
		got, err := settings.Svc.GetSetting(t.Context(), prefs.ScopeWorkspace, key)
		if err != nil || got.Value != want {
			t.Fatalf("persisted %s = %+v, %v", key, got, err)
		}
	}
	target := live.RunTarget()
	if target.Spec != update.Spec || target.Provider != update.Provider || target.Window != update.Window || target.Facts.Generation != update.Facts.Generation {
		t.Fatal("committed selection did not publish prepared target")
	}
}
