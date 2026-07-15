package coderunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestProgrammaticReadPolicy(t *testing.T) {
	policy := coderunner.ProgrammaticReadPolicy()
	allowed := []tools.ToolName{code.ToolNameRead, code.ToolNameGrep, code.ToolNameGlob, code.ToolNameLs, code.ToolNameFileMap, code.ToolNameRetrieveCode, tools.ToolNameWebSearch, tools.ToolNameWebFetch, "list_skills", "list_agents", "list_instructions"}
	for _, name := range allowed {
		if !policy(tools.ToolSpec{Name: name}) {
			t.Fatalf("%s denied", name)
		}
	}
	denied := []tools.ToolSpec{{Name: code.ToolNameWrite}, {Name: code.ToolNameBash}, {Name: programtools.ToolName}, {Name: code.ToolNameRead, Mutates: true}, {Name: code.ToolNameGrep, AffectsWorkspace: true}, {Name: "mcp_tool"}}
	for _, spec := range denied {
		if policy(spec) {
			t.Fatalf("%+v allowed", spec)
		}
	}
}
