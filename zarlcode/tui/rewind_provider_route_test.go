package tui_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestCheckpointCaptureBindsConstructedOpenAIRoute(t *testing.T) {
	registryProvider, err := backends.NewRegistry().BuildWithConfig(t.Context(), "openai", backends.BuildConfig{Model: "gpt-5.6", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		provider llm.Provider
		err      error
	}{
		{"stock registry", registryProvider, nil},
		{"stock Responses", openai.NewProvider("test-key", openai.WithModel("gpt-5.6")), nil},
		{"Responses disabled", openai.NewProvider("test-key", openai.WithModel("gpt-5.6"), openai.WithResponsesAPI(false)), rewind.ErrTarget},
		{"custom endpoint", openai.NewProvider("test-key", openai.WithModel("gpt-5.6"), openai.WithBaseURL("https://custom.invalid/v1"), openai.WithResponsesAPI(true)), rewind.ErrTarget},
		{"different actual model", openai.NewProvider("test-key", openai.WithModel("gpt-4o")), rewind.ErrTarget},
		{"unproven adapter", nativeSettlementProvider{name: "openai"}, rewind.ErrTarget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBeforeFixture(t)
			// Deliberately stock-looking metadata must not override the actual route.
			f.live.SetProviderSpec(tc.provider, engine.ProviderSpec{Name: "openai", Model: "gpt-5.6"})
			reservation, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			defer reservation.Release()
			_, _, err = reservation.Snapshot()
			if !errors.Is(err, tc.err) {
				t.Fatalf("capture=%v, want %v", err, tc.err)
			}
		})
	}
}
