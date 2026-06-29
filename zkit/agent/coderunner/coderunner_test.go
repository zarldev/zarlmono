package coderunner_test

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/sourcechain"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRegisterStandardToolsRegistersTheToolSet(t *testing.T) {
	reg := tools.NewRegistry()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	// pm nil exercises the degraded path; the standard set must still
	// register the file + search tools, which is what eval relies on.
	coderunner.RegisterStandardTools(reg, ws, nil)

	got := map[tools.ToolName]bool{}
	for tool := range reg.Tools(t.Context()) {
		got[tool.Definition().Name] = true
	}
	want := []tools.ToolName{
		code.ToolNameBash,
		code.ToolNameRead, code.ToolNameWrite, code.ToolNameEdit,
		code.ToolNameGrep, code.ToolNameLs, code.ToolNameGlob,
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("standard tool %q not registered", name)
		}
	}

	readTool, ok := reg.Tool(code.ToolNameRead)
	if !ok {
		t.Fatal("standard read tool not found")
	}
	if _, ok := readTool.(*code.ReadFileHLTool); !ok {
		t.Fatalf("standard read is %T, want *code.ReadFileHLTool", readTool)
	}
	editTool, ok := reg.Tool(code.ToolNameEdit)
	if !ok {
		t.Fatal("standard edit tool not found")
	}
	if _, ok := editTool.(*code.EditFileHLTool); !ok {
		t.Fatalf("standard edit is %T, want *code.EditFileHLTool", editTool)
	}
}

func TestRegisterSpawnToolHonorsDepth(t *testing.T) {
	// The parent runner is only consulted when a child actually spawns;
	// these cases only assert registration, so an empty runner suffices.
	parent := runner.New(runnertest.NewClient(nil), runner.WithTools(tools.NewRegistry()))

	tests := []struct {
		name     string
		maxDepth int
		want     bool // is spawn_agent registered?
	}{
		// 0 is the load-bearing case the settings pane relies on: a
		// spawn-depth of zero must surface NO tool, not a tool that
		// always refuses.
		{"zero disables", 0, false},
		{"positive registers", 2, true},
		// Negative defers to spawn's built-in default depth, so the tool
		// is still registered.
		{"negative keeps default", -1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			coderunner.RegisterSpawnTool(reg, parent, tt.maxDepth, 0)

			got := false
			for tool := range reg.Tools(t.Context()) {
				if tool.Definition().Name == spawn.ToolNameSpawnAgent {
					got = true
				}
			}
			if got != tt.want {
				t.Errorf("spawn_agent registered = %v, want %v (maxDepth=%d)", got, tt.want, tt.maxDepth)
			}
		})
	}
}

func TestStandardOptionsBakeInTheTunedDefaults(t *testing.T) {
	// A model-less tuning still yields the shared invariants (adaptive
	// keep-recent, empty-response detector, finalize-warn, iteration +
	// stream-idle watchdogs, max-tokens ceiling, thinking-only budget) —
	// seven options regardless of the per-run knobs.
	const invariants = 7
	bare := coderunner.StandardOptions(coderunner.Tuning{})
	if len(bare) != invariants {
		t.Fatalf("bare StandardOptions = %d options, want %d invariants", len(bare), invariants)
	}
	// Each per-run knob adds exactly one option when set.
	full := coderunner.StandardOptions(coderunner.Tuning{
		Model:           "qwen3.6-35b",
		MaxIterations:   20,
		ToolConcurrency: 4,
		ContextWindow:   131072,
	})
	if len(full) != invariants+4 {
		t.Fatalf("full StandardOptions = %d options, want %d", len(full), invariants+4)
	}
}

func TestStandardOptionsDriveARunToCompletion(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(runnertest.Tool{
		Name:   "noop",
		Result: "ok",
	})
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", "noop", "{}"), runnertest.ChunkDone()},
		{runnertest.ChunkText("done"), runnertest.ChunkDone()},
	})

	opts := coderunner.StandardOptions(coderunner.Tuning{MaxIterations: 5})
	r := runner.New(client, append(opts, runner.WithTools(reg))...)

	res := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("reason = %q, want completed", res.Reason)
	}
	if res.FinalContent != "done" {
		t.Fatalf("final = %q, want %q", res.FinalContent, "done")
	}
}

func TestGuardedSourceArmsTheProductionChain(t *testing.T) {
	reg := tools.NewRegistry()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	coderunner.RegisterStandardTools(reg, ws, nil)

	src, names, err := coderunner.GuardedSource(reg, guardrails.Deps{
		WorkspaceRoot: ws.Root(),
		Verifiers:     []guardrails.Verifier{&guardrails.GoVerifier{}},
		TestEdit:      guardrails.NewTestEditStrict(),
	}, sourcechain.Pipeline{})
	if err != nil {
		t.Fatalf("guarded source: %v", err)
	}

	// The chain must carry the production guardrails in the documented
	// shape: schema first, then the post-schema set, then test-edit +
	// improvement. Names are how /tools and the eval report describe
	// the active rails, so pin the load-bearing ones.
	for _, want := range []string{"schema", "decompose", "fanout", "test_edit_strict", "improvement_loop"} {
		if !slices.Contains(names, want) {
			t.Errorf("guardrail %q missing from chain %v", want, names)
		}
	}

	// The wrapped source still enumerates the underlying tools.
	saw := false
	for tool := range src.Tools(t.Context()) {
		if tool.Definition().Name == code.ToolNameRead {
			saw = true
		}
	}
	if !saw {
		t.Error("guarded source did not surface the read tool")
	}
}
