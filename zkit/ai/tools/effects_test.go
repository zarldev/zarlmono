package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolResultEffects_OmittedWhenEmpty(t *testing.T) {
	body, err := json.Marshal(tools.ToolResult{Success: true, Data: "ok"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "effects") {
		t.Fatalf("empty effects should be omitted, got %s", body)
	}
}

func TestToolResultEffects_FileAndProcessHelpers(t *testing.T) {
	res := &tools.ToolResult{Success: true}
	res.AddEffect(tools.NewFileEffect(tools.FileModify, "pkg/foo.go"))
	res.AddEffect(tools.NewProcessEffect("go test ./...", 1))

	files := res.FileEffects()
	if len(files) != 1 {
		t.Fatalf("FileEffects len = %d, want 1", len(files))
	}
	if files[0].Path != "pkg/foo.go" || files[0].Op != tools.FileModify {
		t.Fatalf("FileEffects[0] = %+v, want pkg/foo.go modify", files[0])
	}

	processes := res.ProcessEffects()
	if len(processes) != 1 {
		t.Fatalf("ProcessEffects len = %d, want 1", len(processes))
	}
	if processes[0].Command != "go test ./..." || processes[0].ExitCode != 1 {
		t.Fatalf("ProcessEffects[0] = %+v, want go test exit 1", processes[0])
	}
}

func TestToolResultEffects_JSONShape(t *testing.T) {
	res := &tools.ToolResult{Success: true}
	res.AddEffect(tools.NewFileEffect(tools.FileCreate, "new.go"))

	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(body)
	for _, want := range []string{`"effects"`, `"kind":"file"`, `"path":"new.go"`, `"op":"create"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json %s missing %s", got, want)
		}
	}
	if strings.Contains(got, `"process"`) {
		t.Fatalf("file effect should not render process payload: %s", got)
	}
}
