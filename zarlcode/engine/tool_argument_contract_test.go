package engine_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// This fixture predates the description reduction. Update only for intentional
// argument/authority changes, never merely to accommodate shorter prose.
func TestDefaultToolArgumentContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inspection := engine.NewLiveRunner(nil, ws, "codex").Inspect(t.Context())
	if len(inspection.Errors) != 0 {
		t.Fatal(inspection.Errors)
	}
	type contract struct {
		Spec            tools.ToolSpec `json:"spec"`
		ScopePaths      []string       `json:"scope_paths"`
		DispatchBarrier bool           `json:"dispatch_barrier"`
	}
	contracts := make([]contract, 0, len(inspection.Tools))
	for _, spec := range inspection.Tools {
		assertEssentialToolDescriptions(t, spec)
		if spec.Name == "program" {
			_, signatures, found := strings.Cut(spec.Description, "\n\nInner tool signatures:")
			if !found {
				t.Fatal("program signatures missing")
			}
			assertToolContractFixture(t, "program_signatures.txt", []byte(signatures+"\n"))
		}
		spec.Description = ""
		spec.Parameters = withoutSchemaDescriptions(spec.Parameters)
		contracts = append(contracts, contract{
			Spec: spec,
			ScopePaths: spec.WorkspaceScope.Paths(tools.ToolParameters{
				"path": "contract.go", "root": "contract-root",
			}),
			DispatchBarrier: spec.DispatchBarrier,
		})
	}
	got, err := json.MarshalIndent(contracts, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assertToolContractFixture(t, "default_tool_arguments.json", append(got, '\n'))
}

func assertToolContractFixture(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if os.Getenv("UPDATE_TOOL_CONTRACT") == "1" {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed; review argument, signature and authority compatibility", path)
	}
}

// Clone the schema containers before stripping docs so inspection stays intact.
// Keep Extra unchanged: these default tools have no description-bearing unions.
func withoutSchemaDescriptions(schema llm.Schema) llm.Schema {
	schema.Description = ""
	schema.Properties = maps.Clone(schema.Properties)
	for name, property := range schema.Properties {
		schema.Properties[name] = withoutSchemaDescriptions(property)
	}
	if schema.Items != nil {
		item := withoutSchemaDescriptions(*schema.Items)
		schema.Items = &item
	}
	if additional, ok := schema.AdditionalProperties.(llm.Schema); ok {
		schema.AdditionalProperties = withoutSchemaDescriptions(additional)
	}
	return schema
}

// These fragments guard the safety/recovery guidance retained by this slice,
// independently of the argument fixture (which deliberately excludes prose).
func assertEssentialToolDescriptions(t *testing.T, spec tools.ToolSpec) {
	t.Helper()
	want := map[tools.ToolName][]string{
		"computer_act": {
			"Wait before acting.", "Wait after acting.", "Target id from observe; preferred.",
			"Backend locator", "Fallback coordinates.", "Movement/scroll delta.",
			"Expected URL substring.", "resulting observation",
		},
		"computer_observe": {"4 MiB encoded", "only for visual context", "non-portable escape hatch"},
		"save_plan":        {"end of Plan work", ".zarlcode/plans/", "Writes only within that directory", "no extension/slashes", "Empty = timestamp slug"},
		"save_plan_append": {"only within .zarlcode/plans/", "non-empty first chunk", "No default", "per-call cap", "256KB default"},
		"write":            {"parent directories", "Refuses existing paths; use edit instead"},
		"update_plan":      {"complete ordered steps", "keep statuses truthful"},
	}
	fragments, changed := want[spec.Name]
	if !changed {
		return
	}
	wire, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(string(wire), fragment) {
			t.Errorf("%s missing essential guidance %q", spec.Name, fragment)
		}
	}
}
