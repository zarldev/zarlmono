package runner

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestKeepPolicyDecide(t *testing.T) {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}

	tests := []struct {
		name      string
		policy    keepPolicy
		usage     *llm.Usage
		wantKeep  int
		wantForce bool
	}{
		{
			name:     "static only, no pressure config",
			policy:   keepPolicy{static: 4},
			usage:    &llm.Usage{PromptTokens: 999_999},
			wantKeep: 4, wantForce: false,
		},
		{
			name:     "adaptive overrides static",
			policy:   keepPolicy{static: 4, adaptive: func([]llm.Message) int { return 9 }},
			wantKeep: 9, wantForce: false,
		},
		{
			name:     "pressure under threshold leaves keep, no force",
			policy:   keepPolicy{static: 6, budget: 100, fraction: 0.8},
			usage:    &llm.Usage{PromptTokens: 50}, // 0.50 < 0.80
			wantKeep: 6, wantForce: false,
		},
		{
			name:     "pressure at/over threshold forces keep=1",
			policy:   keepPolicy{static: 6, budget: 100, fraction: 0.8},
			usage:    &llm.Usage{PromptTokens: 85}, // 0.85 >= 0.80
			wantKeep: 1, wantForce: true,
		},
		{
			name:     "pressure falls back to TotalTokens when PromptTokens is zero",
			policy:   keepPolicy{static: 6, budget: 100, fraction: 0.8},
			usage:    &llm.Usage{TotalTokens: 90},
			wantKeep: 1, wantForce: true,
		},
		{
			name:     "nil usage never forces",
			policy:   keepPolicy{static: 6, budget: 100, fraction: 0.8},
			usage:    nil,
			wantKeep: 6, wantForce: false,
		},
		{
			name:     "budget zero disables force-path",
			policy:   keepPolicy{static: 6, fraction: 0.8},
			usage:    &llm.Usage{PromptTokens: 999_999},
			wantKeep: 6, wantForce: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			keep, force := tc.policy.decide(msgs, tc.usage)
			if keep != tc.wantKeep || force != tc.wantForce {
				t.Errorf("decide = (keep=%d, force=%v), want (keep=%d, force=%v)",
					keep, force, tc.wantKeep, tc.wantForce)
			}
		})
	}
}
