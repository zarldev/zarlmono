package spawn_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Note: depth-cap behaviour at depth>0 is exercised in
// zkit/agent/runner/runner_test.go (TestRun_SpawnAgentRespectsDepthCap)
// because planting depth on ctx requires a real runner.Run dispatch
// — runner.withDepth is unexported. This file covers the parts of
// spawn that don't need a runner.

func TestNew_Definition(t *testing.T) {
	t.Parallel()
	tool := spawn.New(nil) // Definition doesn't touch parent
	def := tool.Definition()
	if def.Name != spawn.ToolNameSpawnAgent {
		t.Errorf("Name = %q, want %q", def.Name, spawn.ToolNameSpawnAgent)
	}
	if !strings.Contains(strings.ToLower(def.Description), "sub-agent") {
		t.Errorf("Description should mention sub-agent: %q", def.Description)
	}
	props, ok := def.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties missing or wrong type")
	}
	if _, ok := props["prompt"]; !ok {
		t.Error("Parameters.properties.prompt missing")
	}
	required, ok := def.Parameters.Map()["required"].([]string)
	if !ok || len(required) == 0 || required[0] != "prompt" {
		t.Errorf("required should be [prompt], got %v", def.Parameters.Map()["required"])
	}
}

func TestExecute_RefusesEmptyPrompt(t *testing.T) {
	t.Parallel()
	tool := spawn.New(nil)
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "c1",
		Arguments: tools.ToolParameters{},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Error("expected Success=false for empty prompt")
	}
	if !strings.Contains(res.Error, "prompt is required") {
		t.Errorf("Error = %q, want it to mention 'prompt is required'", res.Error)
	}
}

func TestExecute_AtZeroMaxDepthRefusesAlways(t *testing.T) {
	t.Parallel()
	tool := spawn.New(nil, spawn.WithMaxDepth(0))
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "c1",
		Arguments: tools.ToolParameters{"prompt": "anything"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Error("WithMaxDepth(0) should refuse every call")
	}
	if !strings.Contains(res.Error, "max recursion depth") {
		t.Errorf("Error = %q, want it to mention 'max recursion depth'", res.Error)
	}
}

func TestExecute_RefusesNilParentRunner(t *testing.T) {
	t.Parallel()
	tool := spawn.New(nil)
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "c1",
		Arguments: tools.ToolParameters{"prompt": "do a thing"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Error("expected Success=false for nil parent runner")
	}
	if !strings.Contains(res.Error, "parent runner is nil") {
		t.Errorf("Error = %q, want it to mention nil parent runner", res.Error)
	}
}

func TestWithMaxDepth_NegativeIgnored(t *testing.T) {
	t.Parallel()
	// Negative input ignored; default (1) kicks in. With ctx depth=0
	// (the test ctx, no withDepth applied), 0 >= 1 is false, so the
	// recursion-depth gate passes — we'll fail on the empty-prompt
	// gate instead, which is the proof that the depth gate didn't
	// fire.
	tool := spawn.New(nil, spawn.WithMaxDepth(-5))
	res, _ := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "c1",
		Arguments: tools.ToolParameters{},
	})
	if strings.Contains(res.Error, "max recursion depth") {
		t.Errorf("negative max-depth should be ignored; got recursion-depth error: %q", res.Error)
	}
	if !strings.Contains(res.Error, "prompt is required") {
		t.Errorf("expected to fall through to prompt-required error; got %q", res.Error)
	}
}
