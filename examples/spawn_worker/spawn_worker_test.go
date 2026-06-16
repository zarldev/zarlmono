package main

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestModePolicy_ExploreBlocksWrite verifies that explore mode
// prevents file modification tools from executing.
func TestModePolicy_ExploreBlocksWrite(t *testing.T) {
	policy := ModePolicy()

	// Mutating tool should be blocked in explore mode
	mutatingSpec := tools.ToolSpec{Name: "write_file", Mutates: true}
	if policy(spawn.SpawnModeExplore, mutatingSpec) {
		t.Error("explore mode should block mutating tools")
	}

	// Non-mutating tool should be allowed
	readSpec := tools.ToolSpec{Name: "read_file", Mutates: false}
	if !policy(spawn.SpawnModeExplore, readSpec) {
		t.Error("explore mode should allow non-mutating tools")
	}
}

// TestModePolicy_ImplementAllowsAll verifies that implement mode
// allows all tools including mutating ones.
func TestModePolicy_ImplementAllowsAll(t *testing.T) {
	policy := ModePolicy()

	mutatingSpec := tools.ToolSpec{Name: "write_file", Mutates: true}
	if !policy(spawn.SpawnModeImplement, mutatingSpec) {
		t.Error("implement mode should allow mutating tools")
	}

	readSpec := tools.ToolSpec{Name: "read_file", Mutates: false}
	if !policy(spawn.SpawnModeImplement, readSpec) {
		t.Error("implement mode should allow non-mutating tools")
	}
}

// TestWorkerRegistry_ExploreHasOnlyReadTools verifies that the
// researcher registry only contains read/list tools.
func TestWorkerRegistry_ExploreHasOnlyReadTools(t *testing.T) {
	fs := NewFileSystem("/tmp/test")
	reg := BuildWorkerRegistry(fs, spawn.SpawnModeExplore)

	// Should have read and list
	if _, ok := reg.Tool(ToolReadFile); !ok {
		t.Error("explore registry should have read_file")
	}
	if _, ok := reg.Tool(ToolListFiles); !ok {
		t.Error("explore registry should have list_files")
	}

	// Should NOT have write or edit
	if _, ok := reg.Tool(ToolWriteFile); ok {
		t.Error("explore registry should NOT have write_file")
	}
	if _, ok := reg.Tool(ToolEditFile); ok {
		t.Error("explore registry should NOT have edit_file")
	}
}

// TestWorkerRegistry_ImplementHasAllTools verifies that the
// coder registry has full tool access.
func TestWorkerRegistry_ImplementHasAllTools(t *testing.T) {
	fs := NewFileSystem("/tmp/test")
	reg := BuildWorkerRegistry(fs, spawn.SpawnModeImplement)

	// Should have all tools
	if _, ok := reg.Tool(ToolReadFile); !ok {
		t.Error("implement registry should have read_file")
	}
	if _, ok := reg.Tool(ToolWriteFile); !ok {
		t.Error("implement registry should have write_file")
	}
	if _, ok := reg.Tool(ToolEditFile); !ok {
		t.Error("implement registry should have edit_file")
	}
}

// TestAgentResolver_KnownWorkers verifies that the resolver returns
// runners for known worker names.
func TestAgentResolver_KnownWorkers(t *testing.T) {
	fs := NewFileSystem("/tmp/test")
	client := runnertest.NewClient(nil)
	resolver := BuildAgentResolver(fs, client)

	for _, name := range []string{"researcher", "reviewer", "coder"} {
		runner, err := resolver(name)
		if err != nil {
			t.Errorf("resolver(%q) error: %v", name, err)
		}
		if runner == nil {
			t.Errorf("resolver(%q) returned nil runner", name)
		}
	}
}

// TestAgentResolver_UnknownWorker returns nil for unknown agents.
func TestAgentResolver_UnknownWorker(t *testing.T) {
	fs := NewFileSystem("/tmp/test")
	client := runnertest.NewClient(nil)
	resolver := BuildAgentResolver(fs, client)

	runner, err := resolver("unknown_agent")
	if err != nil {
		t.Errorf("resolver(unknown) error: %v", err)
	}
	if runner != nil {
		t.Error("resolver(unknown) should return nil")
	}
}

// TestFileSystem_RefactorComplete checks the completion detection.
func TestFileSystem_RefactorComplete(t *testing.T) {
	fs := NewFileSystem("/tmp/test")

	// Initially not complete
	if fs.RefactorComplete() {
		t.Error("fresh filesystem should not show refactor complete")
	}

	// Add jwt.go with JWT content
	fs.Write("jwt.go", "package auth\n\nfunc ValidateJWT(token string) bool {\n\treturn true\n}")

	// Still not complete (auth.go not modified)
	if fs.RefactorComplete() {
		t.Error("jwt.go alone should not be complete")
	}

	// Modify auth.go with JWT reference
	fs.Write("auth.go", "package auth\n\n// Now uses JWT")

	// Now complete
	if !fs.RefactorComplete() {
		t.Error("with jwt.go and modified auth.go, refactor should be complete")
	}
}

// TestSpawnWorker_Integration drives the full parent→child stack: the
// parent spawns a coder worker, the child (implement mode) writes the
// files on the shared filesystem and reports done, then the parent
// completes. The single scripted client is consumed in execution order —
// parent's spawn turn, the child's turns while spawn_agent runs
// synchronously, then the parent's final turn — proving real
// parent-child coordination, not just the unit-level mechanisms above.
func TestSpawnWorker_Integration(t *testing.T) {
	fs := NewFileSystem("/tmp/test")

	client := runnertest.NewClient([][]llm.CompletionChunk{
		// Parent: delegate the whole refactor to the coder worker.
		{
			runnertest.ChunkToolCall("p1", "spawn_agent",
				`{"prompt": "Create jwt.go and switch auth.go to JWT.", "agent": "coder", "mode": "implement"}`),
			runnertest.ChunkDone(),
		},
		// Child coder: create jwt.go.
		{
			runnertest.ChunkToolCall("c1", string(ToolWriteFile),
				`{"path": "jwt.go", "content": "package auth\n\nfunc ValidateJWT(t string) bool { return true }"}`),
			runnertest.ChunkDone(),
		},
		// Child coder: rewrite auth.go to use JWT.
		{
			runnertest.ChunkToolCall("c2", string(ToolWriteFile),
				`{"path": "auth.go", "content": "package auth\n\n// Authentication now uses JWT."}`),
			runnertest.ChunkDone(),
		},
		// Child coder: report completion (terminates the child run).
		{
			runnertest.ChunkText("Created jwt.go and migrated auth.go to JWT."),
			runnertest.ChunkDone(),
		},
		// Parent: all workers done (terminates the parent run).
		{
			runnertest.ChunkText("Refactor complete."),
			runnertest.ChunkDone(),
		},
	})

	out := RunSpawnWorker(t.Context(), client, fs, 2)

	if !fs.RefactorComplete() {
		t.Errorf("child coder did not complete the refactor on the shared fs: %s", fs.Summary())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Errorf("workflow not resolved: status=%v reason=%v", out.Status(), out.Result.Reason)
	}
}
