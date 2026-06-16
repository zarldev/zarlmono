package coderunner_test

import (
	"maps"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestGuardrailRejectionCounts(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: `please run guardrail "fanout" by hand`}, // non-tool roles don't count
		{Role: llm.RoleAssistant, Content: "running tools"},
		{Role: llm.RoleTool, Content: `guardrail "fanout": tool read exceeded its repeat cap`},
		{Role: llm.RoleTool, Content: `guardrail "fanout": tool grep exceeded its repeat cap`},
		{Role: llm.RoleTool, Content: `guardrail "shell_policy": command mutates outside the workspace`},
		{Role: llm.RoleTool, Content: "wrote 42 bytes to main.go"}, // success — no count
	}
	got := coderunner.GuardrailRejectionCounts(messages)
	want := map[string]int{"fanout": 2, "shell_policy": 1}
	if !maps.Equal(got, want) {
		t.Errorf("GuardrailRejectionCounts = %v, want %v", got, want)
	}
}

func TestGuardrailRejectionCountsCleanTranscript(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleTool, Content: "ok"},
	}
	if got := coderunner.GuardrailRejectionCounts(messages); got != nil {
		t.Errorf("clean transcript: got %v, want nil", got)
	}
}
