package tui_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestClearRejectsReservedRuntimeWithoutLosingSession(t *testing.T) {
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	connections := dynamic.NewMCPRegistry(registry, nil)
	t.Cleanup(func() { _ = connections.CloseAll() })
	live := engine.NewLiveRunner(nil, workspace, "test", engine.WithMCP(connections, registry))
	encoded := fmt.Sprintf(`{"version":1,"tools":[{"connection":"scene","name":"inspect_scene","definition_hash":"v1:%s"}]}`, strings.Repeat("a", 64))
	state, err := engine.DecodeMCPActivations([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if err := reservation.RestoreSessionContext([]llm.Message{{Role: llm.RoleUser, Content: "old session"}}, state); err != nil {
		t.Fatal(err)
	}
	model := tui.New()
	step(t, model, window(120, 30))
	model.SetLiveRunner(live)
	model.SetSessionIdentity("old-session", "label", true, time.Now())
	typeAndEnter(t, model, "/clear")
	if model.SessionIdentity() != "old-session" || len(live.ContextSnapshot()) != 1 {
		t.Fatal("busy clear partially replaced session identity/context")
	}
	got, _ := live.MCPActivations().Encode()
	if !strings.Contains(string(got), "inspect_scene") {
		t.Fatal("busy clear changed selection hints")
	}
	reservation.Release()
	typeAndEnter(t, model, "/clear")
	got, _ = live.MCPActivations().Encode()
	if model.SessionIdentity() != "" || len(live.ContextSnapshot()) != 0 || strings.Contains(string(got), "inspect_scene") {
		t.Fatalf("successful clear did not isolate new session: %s", got)
	}
}
