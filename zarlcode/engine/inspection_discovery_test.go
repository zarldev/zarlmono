package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestInspectionRetainsDiscoveryErrorsUntilTurnRefresh(t *testing.T) {
	root := t.TempDir()
	instructionPath := filepath.Join(root, "AGENTS.md")
	agentPath := filepath.Join(root, ".zarlcode", "agents", "cached.md")
	mustWrite(t, instructionPath, "\x00")
	mustWrite(t, agentPath, "not frontmatter")
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(&requestRecordingProvider{}, ws, "local")
	defer live.Close(t.Context())
	first := live.Inspect(t.Context())
	assertDiscoveryDiagnostics(t, first)

	mustWrite(t, instructionPath, "Repaired instructions.")
	mustWrite(t, agentPath, "---\nname: cached\ndescription: repaired agent\n---\nReview code.\n")
	cached := live.Inspect(t.Context())
	assertDiscoveryDiagnostics(t, cached)
	for _, agent := range cached.Agents {
		if agent.Name == "cached" {
			t.Fatal("inspection reread the repaired agent")
		}
	}
	if err := live.RunTurn(t.Context(), "Reply done."); err != nil {
		t.Fatal(err)
	}
	refreshed := live.Inspect(t.Context())
	for _, diagnostic := range refreshed.Errors {
		if strings.HasPrefix(diagnostic, "instructions:") || strings.HasPrefix(diagnostic, "catalog:") {
			t.Fatalf("stale discovery diagnostic after turn: %s", diagnostic)
		}
	}
	assertInstructionSnapshotContains(t, refreshed.PromptSystem, "Repaired instructions.")
	for _, agent := range refreshed.Agents {
		if agent.Name == "cached" && agent.Description == "repaired agent" {
			return
		}
	}
	t.Fatal("turn did not publish the repaired agent")
}

func assertDiscoveryDiagnostics(t *testing.T, inspection engine.Inspection) {
	t.Helper()
	var instruction, agent bool
	for _, diagnostic := range inspection.Errors {
		instruction = instruction || (strings.HasPrefix(diagnostic, "instructions:") && strings.Contains(diagnostic, "AGENTS.md"))
		agent = agent || (strings.HasPrefix(diagnostic, "catalog:") && strings.Contains(diagnostic, "cached.md"))
	}
	if !instruction || !agent {
		t.Fatalf("missing published discovery diagnostics: %v", inspection.Errors)
	}
}
