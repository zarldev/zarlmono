package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestLiveRunnerComputerToolsSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	live := newMCPTestLiveWithoutMCP(t, nil, settings)
	var enabledSchema []byte
	for _, step := range []struct {
		name  string
		scope prefs.Scope
		value string
		want  bool
	}{
		{name: "default", want: true},
		{name: "global off", scope: prefs.ScopeGlobal, value: "off"},
		{name: "workspace on", scope: prefs.ScopeWorkspace, value: "on", want: true},
		{name: "workspace off", scope: prefs.ScopeWorkspace, value: "off"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.value != "" {
				if err := settings.Svc.SetSetting(t.Context(), step.scope, prefs.KeyEnableComputer, step.value); err != nil {
					t.Fatal(err)
				}
			}
			if got := settings.EnableComputer(t.Context()); got != step.want {
				t.Fatalf("EnableComputer = %v, want %v", got, step.want)
			}
			var buildSchema []byte
			for _, plan := range []bool{false, true} {
				live.SetPlanMode(plan)
				inspection := live.Inspect(t.Context())
				assertNoInspectionErrors(t, inspection)
				for _, name := range []tools.ToolName{"computer_act", "computer_observe"} {
					if inspectionHasTool(inspection, name) != step.want {
						t.Fatalf("plan=%v: membership of %s != %v", plan, name, step.want)
					}
				}
				wire, err := json.Marshal(inspection.Tools)
				if err != nil {
					t.Fatal(err)
				}
				if !plan {
					buildSchema = wire
				} else if !bytes.Equal(buildSchema, wire) {
					t.Error("computer setting changed schema parity across modes")
				}
				if step.want {
					if enabledSchema == nil {
						enabledSchema = wire
					} else if !bytes.Equal(enabledSchema, wire) {
						t.Error("re-enabling did not restore default schema")
					}
				}
				if !plan {
					total := 0
					for _, spec := range inspection.Tools {
						encoded, err := json.Marshal(llm.Tool{Type: "function", Function: llm.ToolFunction{Name: spec.Name.String(), Description: spec.Description, Parameters: spec.Parameters}})
						if err != nil {
							t.Fatal(err)
						}
						total += len(encoded)
					}
					t.Logf("neutral schema: %d bytes (%d tools)", total, len(inspection.Tools))
					if !step.want && total > 8_320 {
						t.Errorf("computer-off schema bytes = %d, want <= 8320", total)
					}
				}
			}
		})
	}
	if engine.PlanAllows("computer_act") {
		t.Fatal("host reservation must not allow computer_act in Plan")
	}
}

func TestDisabledComputerToolsDoNotDispatch(t *testing.T) {
	for _, plan := range []bool{false, true} {
		t.Run(map[bool]string{false: "build", true: "plan"}[plan], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			settings := newMCPSettings(t)
			setMCPSetting(t, settings, prefs.KeyEnableComputer, "off")
			requests := 0
			provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
				requests++
				for _, name := range []string{"computer_act", "computer_observe"} {
					if requestHasTool(req, name) {
						t.Fatalf("disabled tool advertised: %s", name)
					}
				}
				if requests == 1 {
					return modeCalls(
						namedModeCall("observe", "computer_observe", `{}`),
						namedModeCall("act", "computer_act", `{"action":{"kind":"navigate","url":"https://example.com"}}`),
					)
				}
				for _, id := range []string{"observe", "act"} {
					result := modeResult(req.Messages, id)
					if !strings.Contains(result, "not found") && !strings.Contains(result, "not callable in PLAN") {
						t.Errorf("disabled %s result = %q", id, result)
					}
				}
				return modeCalls()
			})
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			live := engine.NewLiveRunner(provider, ws, "fixture", engine.WithSettings(settings), engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) {
				t.Error("disabled model tool opened a browser")
				return &fakeComputerSession{}, nil
			}))
			t.Cleanup(func() { _ = live.Close(context.WithoutCancel(t.Context())) })
			live.SetPlanMode(plan)
			if err := live.RunTurn(t.Context(), "try disabled computer tools"); err != nil {
				t.Fatal(err)
			}
			if requests != 2 {
				t.Fatalf("requests = %d, want 2", requests)
			}
		})
	}
}
