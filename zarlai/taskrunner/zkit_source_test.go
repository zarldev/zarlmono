package taskrunner

import (
	"context"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// stubTool is a minimal tools.Tool whose Execute echoes a marker so a
// name-clash winner can be identified.
type stubTool struct {
	name   string
	marker string
}

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:       tools.ToolName(s.name),
		Parameters: service.Parameters{}.ToJSONSchema(),
	}
}

func (s stubTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, s.marker), nil
}

func sourceNames(t *testing.T, src *tools.Registry) []string {
	t.Helper()
	var names []string
	for tool := range src.Tools(t.Context()) {
		names = append(names, tool.Definition().Name.String())
	}
	slices.Sort(names)
	return names
}

func TestBuildTaskSourceComposesAndExcludes(t *testing.T) {
	resolved := ResolvedProfile{
		Tools: []tools.Tool{
			stubTool{name: "web_search"},
			stubTool{name: "start_task"}, // excluded
			stubTool{name: "read"},
		},
	}
	exclude := map[string]bool{"start_task": true, "schedule_task": true}

	src := buildTaskSource(resolved, RunnerTools(), exclude)

	got := sourceNames(t, src)
	want := []string{"complete_task", "pause_task", "read", "report_progress", "web_search"}
	if !slices.Equal(got, want) {
		t.Fatalf("source tools = %v, want %v", got, want)
	}
}

// A profile tool that collides with a lifecycle tool name must not
// shadow the lifecycle tool — loop control can't be overridden.
func TestBuildTaskSourceLifecycleWinsClash(t *testing.T) {
	resolved := ResolvedProfile{
		Tools: []tools.Tool{stubTool{name: ToolCompleteTask, marker: "PROFILE"}},
	}

	src := buildTaskSource(resolved, RunnerTools(), nil)

	res, err := src.Execute(t.Context(), tools.ToolCall{
		ToolName:  ToolCompleteTask,
		Arguments: tools.ToolParameters{"summary": "done"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The real completeTaskTool reports "Task marked complete: ...", not
	// the profile stub's "PROFILE" marker.
	if res == nil || res.Data == "PROFILE" {
		t.Fatalf("profile tool shadowed lifecycle tool: %+v", res)
	}
}

func TestBuildTaskSourceDispatches(t *testing.T) {
	resolved := ResolvedProfile{Tools: []tools.Tool{stubTool{name: "echo", marker: "hit"}}}
	src := buildTaskSource(resolved, nil, nil)

	res, err := src.Execute(t.Context(), tools.ToolCall{ToolName: "echo"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || !res.Success || res.Data != "hit" {
		t.Fatalf("Execute result = %+v, want success with data %q", res, "hit")
	}
}
