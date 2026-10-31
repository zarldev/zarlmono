package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/deepseek"
)

func TestOrdinaryPersistenceSnapshotDoesNotQualifyExactReplay(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "custom-model", BaseURL: "https://model.example.test/v1"})
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "owned context"}})
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	messages, target, exact, err := reservation.PersistenceSnapshot()
	if err != nil || exact || target.Model != "custom-model" || len(messages) != 1 {
		t.Fatalf("ordinary snapshot = %v, %v, %v, %v", messages, target, exact, err)
	}
	messages[0].Content = "modified copy"
	if live.ContextSnapshot()[0].Content != "owned context" {
		t.Fatal("persistence snapshot aliases live context")
	}
	if _, _, err := reservation.Snapshot(); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("custom route qualified for exact replay: %v", err)
	}
	reservation.Release()
	if _, _, _, err := reservation.PersistenceSnapshot(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("released reservation returned context: %v", err)
	}
}

func TestDeepSeekRoutesRemainOrdinaryUntilQualified(t *testing.T) {
	t.Parallel()
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	registryProvider, err := backends.NewRegistry().BuildWithConfig(t.Context(), "deepseek", backends.BuildConfig{Model: deepseek.DefaultModel, APIKey: "fixture-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		provider llm.Provider
		baseURL  string
	}{
		{"name-only fake", provider, ""},
		{"stock direct", deepseek.NewProvider("fixture-key"), ""},
		{"stock registry", registryProvider, ""},
		{"custom endpoint", deepseek.NewProvider("fixture-key", deepseek.WithBaseURL("https://proxy.example.test/v1")), "https://proxy.example.test/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := engine.ProviderSpec{Name: "deepseek", Model: deepseek.DefaultModel, BaseURL: tc.baseURL}
			if live.SupportsExactTarget(engine.TargetUpdate{Provider: tc.provider, Spec: spec}) {
				t.Fatal("ordinary DeepSeek route qualified for unproven exact replay")
			}
		})
	}
}
