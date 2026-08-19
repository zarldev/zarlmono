package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolSpecWorkspaceAccessJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec tools.ToolSpec
		want string
	}{
		{name: "unspecified omitted", spec: tools.ToolSpec{Name: "x"}, want: `{"name":"x","description":"","parameters":{}}`},
		{name: "explicit none", spec: tools.ToolSpec{Name: "x", WorkspaceAccess: tools.WorkspaceAccesses.NONE}, want: `{"name":"x","description":"","parameters":{},"workspace_access":"none"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("json = %s, want %s", got, tt.want)
			}
		})
	}
}
