package tools_test

import (
	"testing"

	ztools "github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/tools"
)

// schemaProps returns the JSON-schema "properties" map of a tool spec.
func schemaProps(t *testing.T, spec ztools.ToolSpec) map[string]any {
	t.Helper()
	props, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("spec %s has no properties map", spec.Name)
	}
	return props
}

// schemaRequired returns the set of required parameter names.
func schemaRequired(spec ztools.ToolSpec) map[string]bool {
	out := map[string]bool{}
	req, _ := spec.Parameters.Map()["required"].([]string)
	for _, r := range req {
		out[r] = true
	}
	return out
}

func TestStartTaskToolDefinition(t *testing.T) {
	tool := tools.NewStartTaskTool(nil, nil, nil)
	def := tool.Definition()

	if def.Name != "start_task" {
		t.Errorf("name = %q, want %q", def.Name, "start_task")
	}
	if def.Description == "" {
		t.Error("description is empty")
	}

	props := schemaProps(t, def)
	required := schemaRequired(def)

	if !required["prompt"] {
		t.Error("prompt must be required")
	}
	if _, ok := props["max_iterations"]; !ok {
		t.Error("missing parameter: max_iterations")
	}
	profile, ok := props["profile"].(map[string]any)
	if !ok {
		t.Fatal("missing parameter: profile")
	}
	if required["profile"] {
		t.Error("profile must not be required")
	}
	wantEnum := []string{"default", "researcher", "coder"}
	enum, _ := profile["enum"].([]any)
	if len(enum) != len(wantEnum) {
		t.Errorf("profile enum = %v, want %v", enum, wantEnum)
	}
}

func TestTaskStatusToolDefinition(t *testing.T) {
	tool := tools.NewTaskStatusTool(nil)
	def := tool.Definition()

	if def.Name != "task_status" {
		t.Errorf("name = %q, want %q", def.Name, "task_status")
	}
	if def.Description == "" {
		t.Error("description is empty")
	}
}

func TestScheduleTaskToolDefinition(t *testing.T) {
	tool := tools.NewScheduleTaskTool(nil, nil)
	def := tool.Definition()

	if def.Name != "schedule_task" {
		t.Errorf("name = %q, want %q", def.Name, "schedule_task")
	}
	if def.Description == "" {
		t.Error("description is empty")
	}

	props := schemaProps(t, def)
	required := schemaRequired(def)

	if _, ok := props["prompt"]; !ok {
		t.Error("missing parameter: prompt")
	}
	if _, ok := props["schedule"]; !ok {
		t.Error("missing parameter: schedule")
	}
	profile, ok := props["profile"].(map[string]any)
	if !ok {
		t.Fatal("missing parameter: profile")
	}
	if required["profile"] {
		t.Error("profile must not be required")
	}
	wantEnum := []string{"default", "researcher"}
	enum, _ := profile["enum"].([]any)
	if len(enum) != len(wantEnum) {
		t.Errorf("profile enum = %v, want %v", enum, wantEnum)
	}
}
