package compact_test

import (
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
)

func TestDefaultSystemPromptsAreEmbeddedExactly(t *testing.T) {
	t.Parallel()

	prompts := map[string]string{
		"summary.md":   compact.SummaryDefaultSystemPrompt,
		"executive.md": compact.ExecutiveDefaultSystemPrompt,
		"handover.md":  compact.HandoverDefaultSystemPrompt,
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("read prompt file: %v", err)
			}
			if prompt != string(want) {
				t.Errorf("embedded prompt differs from %s", name)
			}
			if strings.HasSuffix(prompt, "\n") {
				t.Error("embedded prompt gained a trailing newline")
			}
		})
	}
}
