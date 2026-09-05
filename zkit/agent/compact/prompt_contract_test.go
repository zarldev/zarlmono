package compact_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
)

func TestCompactionPromptsPreserveOperationalContract(t *testing.T) {
	t.Parallel()

	prompts := map[string]string{
		"summary":   compact.SummaryDefaultSystemPrompt,
		"executive": compact.ExecutiveDefaultSystemPrompt,
		"handover":  compact.HandoverDefaultSystemPrompt,
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, required := range []string{
				"exact current request",
				"definition of done",
				"constraints",
				"preferences",
				"approval boundaries",
				"rejected approaches",
				"verification evidence",
				"Problems encountered",
				"current state",
				"blockers",
				"promises",
				"next actions",
				"paths",
				"symbols",
				"commands",
				"errors",
				"identifiers",
				"values",
				"dates",
				"links",
			} {
				if !strings.Contains(strings.ToLower(prompt), strings.ToLower(required)) {
					t.Errorf("prompt missing %q", required)
				}
			}
			if !strings.Contains(strings.ToLower(prompt), "soft target") {
				t.Error("prompt does not make concision target soft")
			}
		})
	}
}
