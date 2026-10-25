package engine_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestMCPActivationSessionRestore(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	connections := dynamic.NewMCPRegistry(registry, nil)
	t.Cleanup(func() { _ = connections.CloseAll() })
	workspace, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(&requestRecordingProvider{}, workspace, "test", engine.WithMCP(connections, registry))
	ref := dynamic.MCPToolRef{Connection: "scene", Name: "inspect_scene", DefinitionHash: "v1:" + strings.Repeat("a", 64)}
	encoded, err := json.Marshal(map[string]any{"version": 1, "tools": []dynamic.MCPToolRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.DecodeMCPActivations(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.RestoreSessionContext([]llm.Message{{Role: llm.RoleUser, Content: "session A"}}, state); err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	if err := reservation.RestoreMCPActivations(engine.MCPActivations{}); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("released reservation accepted replacement: %v", err)
	}
	got, err := live.MCPActivations().Encode()
	if err != nil || !strings.Contains(string(got), "inspect_scene") {
		t.Fatalf("dormant selection lost: %s, %v", got, err)
	}
	for tool := range registry.Tools(t.Context()) {
		t.Fatalf("restoring hints registered executable tool %s", tool.Definition().Name)
	}
	reservation, err = live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.RestoreSessionContext([]llm.Message{{Role: llm.RoleUser, Content: "session B"}}, engine.MCPActivations{}); err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	got, _ = live.MCPActivations().Encode()
	if strings.Contains(string(got), "inspect_scene") {
		t.Fatalf("session A hints leaked into B: %s", got)
	}
	reservation, err = live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.RestoreMCPActivations(state); err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	live.ClearContext()
	got, _ = live.MCPActivations().Encode()
	if strings.Contains(string(got), "inspect_scene") || len(live.ContextSnapshot()) != 0 {
		t.Fatalf("new session retained old state: %s", got)
	}
}

func TestDecodeMCPActivationsRejectsInvalidStorage(t *testing.T) {
	for name, data := range map[string]string{
		"version":       `{"version":2,"tools":[]}`,
		"unknown":       `{"version":1,"tools":[],"credentials":"secret"}`,
		"trailing":      `{"version":1,"tools":[]} {}`,
		"null":          `null`,
		"bad reference": `{"version":1,"tools":[{"connection":"scene","name":"inspect","definition_hash":"bad"}]}`,
		"size":          strings.Repeat(" ", 32*1024+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := engine.DecodeMCPActivations([]byte(data)); !errors.Is(err, engine.ErrMCPActivations) {
				t.Fatalf("invalid state accepted: %v", err)
			}
		})
	}
	if _, err := engine.DecodeMCPActivations(nil); err != nil {
		t.Fatalf("legacy state rejected: %v", err)
	}
}
