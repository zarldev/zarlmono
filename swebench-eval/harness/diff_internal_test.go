package harness

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRenderSystemPromptUsesLeanEmbeddedPrompt(t *testing.T) {
	reg := tools.NewRegistry()
	prompt, err := renderSystemPrompt("/repo", reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Workspace: /repo", "through the tool interface", "# Workspace instructions"} {
		if want == "# Workspace instructions" {
			if strings.Contains(prompt, want) {
				t.Fatalf("SWE-bench prompt unexpectedly included host workspace docs:\n%s", prompt)
			}
			continue
		}
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, leak := range []string{"# User preferences", "~/.zarlcode/preferences.md", "prompt.override.md", "new_tool"} {
		if strings.Contains(prompt, leak) {
			t.Fatalf("lean eval prompt leaked %q:\n%s", leak, prompt)
		}
	}
}
