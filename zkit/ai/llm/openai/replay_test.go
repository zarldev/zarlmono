package openai_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/options"
)

func TestEndpointAliasReplayCompatibility(t *testing.T) {
	t.Parallel()

	for _, model := range []struct {
		id               string
		defaultResponses bool
	}{
		{id: " Company/Production Alias-V7 "},
		{id: "gpt-6-unknown"},
		{id: "gpt-4o-mini"},
		{id: "gpt-5.6", defaultResponses: true},
	} {
		if got := openai.SupportsResponsesReplay(model.id); got != model.defaultResponses {
			t.Errorf("SupportsResponsesReplay(%q) = %t, want %t", model.id, got, model.defaultResponses)
		}
		for _, customURL := range []bool{false, true} {
			for _, policy := range []struct {
				name              string
				explicit, enabled bool
			}{
				{name: "default"},
				{name: "explicit Responses", explicit: true, enabled: true},
				{name: "explicit Chat", explicit: true},
			} {
				for _, reverse := range []bool{false, true} {
					name := fmt.Sprintf("%s/custom=%t/%s/reverse=%t", model.id, customURL, policy.name, reverse)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						opts := []options.Option[openai.Provider]{openai.WithModel(model.id)}
						if customURL {
							opts = append(opts, openai.WithBaseURL("https://provider.invalid/v1"))
						} else {
							opts = append(opts, openai.WithBaseURL("https://api.openai.com/v1"))
						}
						if policy.explicit {
							opts = append(opts, openai.WithResponsesAPI(policy.enabled))
						}
						if reverse {
							slices.Reverse(opts)
						}
						provider := openai.NewProvider("test-key", opts...)
						want := !customURL && (!policy.explicit || policy.enabled == model.defaultResponses)
						if got := provider.DefaultReplayCompatible(model.id); got != want {
							t.Errorf("DefaultReplayCompatible(%q) = %t, want %t", model.id, got, want)
						}
						if provider.DefaultReplayCompatible("different-model") {
							t.Error("DefaultReplayCompatible accepted a model mismatch")
						}
					})
				}
			}
		}
	}
}
