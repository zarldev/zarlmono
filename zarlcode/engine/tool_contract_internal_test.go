package engine

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type toolContractSize struct {
	name  string
	bytes int
}

func TestDefaultBuildToolContractSizes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	inspection := NewLiveRunner(nil, ws, "codex").Inspect(t.Context())
	if len(inspection.Errors) != 0 {
		t.Fatalf("inspect errors: %v", inspection.Errors)
	}

	sizes := make([]toolContractSize, 0, len(inspection.Tools))
	names := make([]string, 0, len(inspection.Tools))
	total := 0
	for _, spec := range inspection.Tools {
		wire, err := json.Marshal(llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        spec.Name.String(),
				Description: spec.Description,
				Parameters:  spec.Parameters,
			},
		})
		if err != nil {
			t.Fatalf("marshal %s: %v", spec.Name, err)
		}
		name := spec.Name.String()
		names = append(names, name)
		sizes = append(sizes, toolContractSize{name: name, bytes: len(wire)})
		total += len(wire)
	}
	slices.Sort(names)
	wantNames := []string{
		"bash", "computer_act", "computer_observe", "edit", "instruction_load", "program",
		"save_plan", "save_plan_append", "skill_create", "skill_load", "update_plan", "write",
	}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("BUILD membership changed:\ngot  %v\nwant %v", names, wantNames)
	}
	const maxCompactBytes = 13_143 // 25% below the 17,524-byte baseline.
	if total > maxCompactBytes {
		t.Errorf("tool contract bytes = %d, want <= %d", total, maxCompactBytes)
	}

	slices.SortFunc(sizes, func(a, b toolContractSize) int { return b.bytes - a.bytes })
	for _, size := range sizes {
		t.Logf("%5d %s", size.bytes, size.name)
	}
	t.Logf("%5d total (%d tools)", total, len(sizes))
}
