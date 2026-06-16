package taskrunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
)

func TestCoderToolFactory_producesExpectedTools(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	factory := taskrunner.NewCoderToolFactory()
	tools := factory(ws)

	wantNames := map[string]bool{
		"read": false, "write": false, "edit": false,
		"grep": false, "ls": false, "bash": false,
	}
	for _, tool := range tools {
		name := tool.Definition().Name.String()
		if _, ok := wantNames[name]; !ok {
			t.Errorf("unexpected tool %q", name)
			continue
		}
		wantNames[name] = true
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("missing tool %q", name)
		}
	}
}
