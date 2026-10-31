package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type toolContractSize struct {
	name       string
	bytes      int
	structural int
}

func TestDefaultBuildToolContractSizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	inspection := engine.NewLiveRunner(nil, ws, "codex").Inspect(t.Context())
	if len(inspection.Errors) != 0 {
		t.Fatalf("inspect errors: %v", inspection.Errors)
	}

	sizes := make([]toolContractSize, 0, len(inspection.Tools))
	names := make([]string, 0, len(inspection.Tools))
	total := 0
	for _, spec := range inspection.Tools {
		tool := llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        spec.Name.String(),
				Description: spec.Description,
				Parameters:  spec.Parameters,
			},
		}
		wire, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal %s: %v", spec.Name, err)
		}
		name := spec.Name.String()
		names = append(names, name)
		tool.Function.Description = ""
		tool.Function.Parameters = withoutSchemaDescriptions(spec.Parameters)
		structural, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, toolContractSize{name: name, bytes: len(wire), structural: len(structural)})
		total += len(wire)
	}
	slices.Sort(names)
	wantNames := []string{
		"bash", "computer_act", "computer_observe", "edit", "instruction_load", "program",
		"save_plan", "save_plan_append", "set_mode", "skill_create", "skill_load", "update_plan", "write",
	}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("BUILD membership changed:\ngot  %v\nwant %v", names, wantNames)
	}
	const maxCompactBytes = 13_143 + 602 // Existing compact budget plus the typed set_mode control contract.
	if total > maxCompactBytes {
		t.Errorf("tool contract bytes = %d, want <= %d", total, maxCompactBytes)
	}

	slices.SortFunc(sizes, func(a, b toolContractSize) int { return b.bytes - a.bytes })
	for _, size := range sizes {
		t.Logf("%5d %s (without descriptions: %d)", size.bytes, size.name, size.structural)
	}
	t.Logf("%5d total (%d tools)", total, len(sizes))
}

func TestDelegationToolContractStableAcrossModes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var firstSchema []byte
	requests := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		requests++
		wire, err := json.Marshal(req.Tools)
		if err != nil {
			t.Fatal(err)
		}
		const maxDelegationBytes = 16_200 // Current 16,171-byte full array, with bounded headroom.
		if len(wire) > maxDelegationBytes {
			t.Errorf("delegation schema bytes = %d, want <= %d", len(wire), maxDelegationBytes)
		}
		// The roster is shared across workflows; no Plan-only shell authority is implied.
		if requests == 1 {
			firstSchema = wire
			t.Logf("delegation-enabled schema: %d bytes (%d tools)", len(wire), len(req.Tools))
		} else if !bytes.Equal(firstSchema, wire) {
			t.Error("delegation schema changed across modes")
		}
		assertModeRequest(t, req, requests == 2)
		for _, name := range []string{"agent_spawn", "agent_await", "agent_status", "agent_stop", "list_agent_tasks"} {
			if !requestHasTool(req, name) {
				t.Errorf("missing delegation tool %s", name)
			}
		}
		for _, tool := range req.Tools {
			if tool.Function.Name != "agent_status" {
				continue
			}
			parameter, present := tool.Function.Parameters.Properties["include_summary"]
			if !present || parameter.Type != "boolean" || slices.Contains(tool.Function.Parameters.Required, "include_summary") {
				t.Errorf("include_summary must be an optional Boolean: %#v", tool.Function.Parameters)
			}
			statusWire, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			const maxStatusBytes = 640 // Current 632-byte metadata-aware status contract.
			if len(statusWire) > maxStatusBytes {
				t.Errorf("status schema bytes = %d, want <= %d", len(statusWire), maxStatusBytes)
			}
			t.Logf("status schema: %d bytes", len(statusWire))
		}
		switch requests {
		case 1:
			return modeCalls(modeCall("plan", "plan"))
		case 2:
			return modeCalls(modeCall("build", "build"))
		default:
			return modeCalls()
		}
	})
	live := modeLive(t, t.TempDir(), provider)
	live.SetLimits(5120, 5, 2, 1)
	if err := live.RunTurn(t.Context(), "compare delegation contracts across workflows"); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}
