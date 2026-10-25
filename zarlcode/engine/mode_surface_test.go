package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestModeToolSchemasStayStableWithPlanShellDispatch(t *testing.T) {
	for _, programmatic := range []string{"off", "on"} {
		t.Run(programmatic, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			settings := newMCPSettings(t)
			setMCPSetting(t, settings, prefs.KeyProgrammaticTools, programmatic)
			request := 0
			var firstSchema []byte
			provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
				request++
				schema, err := json.Marshal(req.Tools)
				if err != nil {
					t.Fatal(err)
				}
				if request == 1 {
					firstSchema = schema
				} else if !bytes.Equal(schema, firstSchema) {
					t.Errorf("request %d changed ordered tool schemas across mode transitions", request)
				}
				assertModeRequest(t, req, request != 3)
				if requestHasTool(req, "program") != (programmatic == "on") {
					t.Errorf("programmatic=%s: unexpected program exposure", programmatic)
				}
				switch request {
				case 1:
					return modeCalls(namedModeCall("shell", "bash", `{"command":"printf plan-cli-ok","timeout_seconds":10}`))
				case 2:
					if result := modeResult(req.Messages, "shell"); !strings.Contains(result, "plan-cli-ok") {
						t.Errorf("Plan shell did not execute: %s", result)
					}
					return modeCalls(modeCall("build", "build"))
				case 3:
					return modeCalls(modeCall("plan", "plan"))
				default:
					return modeCalls()
				}
			})
			live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
			live.SetPlanMode(true)
			if err := live.RunTurn(t.Context(), "inspect the CLI and compare workflow tool schemas"); err != nil {
				t.Fatal(err)
			}
			if request != 4 {
				t.Fatalf("requests = %d, want 4", request)
			}
		})
	}
}

func TestReadOnlyAuthorityStillBlocksShellAndProcessControl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	request := 0
	provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		request++
		assertModeRequest(t, req, true)
		if request == 1 {
			return modeCalls(
				namedModeCall("shell", "bash", `{"command":"printf forbidden-shell"}`),
				namedModeCall("stop", "stop_process", `{"process_id":"not-owned"}`),
			)
		}
		for _, id := range []string{"shell", "stop"} {
			if result := modeResult(req.Messages, id); !strings.Contains(result, "inspect-only authority ceiling") {
				t.Errorf("%s bypassed read-only ceiling: %s", id, result)
			}
		}
		return modeCalls()
	})
	live := modeLive(t, t.TempDir(), provider, engine.WithReadOnlyTasks())
	if err := live.RunTurn(t.Context(), "inspect only"); err != nil {
		t.Fatal(err)
	}
	if request != 2 {
		t.Fatalf("requests = %d, want 2", request)
	}
}
