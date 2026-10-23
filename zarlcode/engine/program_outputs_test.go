package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestProgramOutputRecoverySetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	live := newMCPTestLiveWithoutMCP(t, nil, settings)
	for _, step := range []struct {
		name, value string
		scope       prefs.Scope
		want        bool
	}{
		{name: "default"},
		{name: "global on", value: "on", scope: prefs.ScopeGlobal, want: true},
		{name: "workspace off", value: "off", scope: prefs.ScopeWorkspace},
		{name: "workspace on", value: "on", scope: prefs.ScopeWorkspace, want: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.value != "" {
				if err := settings.Svc.SetSetting(t.Context(), step.scope, prefs.KeyProgramOutputRecovery, step.value); err != nil {
					t.Fatal(err)
				}
			}
			if settings.ProgramOutputRecovery(t.Context()) != step.want {
				t.Fatal("incorrect effective preference")
			}
			var build []byte
			for _, plan := range []bool{false, true} {
				live.SetPlanMode(plan)
				inspection := live.Inspect(t.Context())
				assertNoInspectionErrors(t, inspection)
				if inspectionHasTool(inspection, program.ToolNameOutputRead) != step.want {
					t.Fatal("incorrect reader exposure")
				}
				wire, err := json.Marshal(inspection.Tools)
				if err != nil {
					t.Fatal(err)
				}
				if !plan {
					build = wire
				} else if !bytes.Equal(build, wire) {
					t.Fatal("Plan/Build schema mismatch")
				}
				if !plan {
					t.Logf("inspection schema: %d bytes / %d tools", len(wire), len(inspection.Tools))
				}
			}
		})
	}
	setMCPSetting(t, settings, prefs.KeyProgrammaticTools, "off")
	if inspectionHasTool(live.Inspect(t.Context()), program.ToolNameOutputRead) {
		t.Fatal("reader exposed without program")
	}
}

func TestProgramOutputRecoveryConversationLifetime(t *testing.T) {
	for _, name := range []string{"turn and mode", "clear", "legacy restore", "session restore", "exact restore", "exact session restore", "failed target", "failed session", "failed exact session", "rejected close"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			settings := newMCPSettings(t)
			setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
			var call llm.ToolCall
			var result string
			requests, serial := 0, 0
			provider := modeProvider(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
				requests++
				if requests == 1 {
					return modeCalls(call)
				}
				result = modeResult(req.Messages, call.ID)
				return modeCalls()
			})
			live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
			live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "fixture"})
			invoke := func(tool, args string) string {
				t.Helper()
				serial++
				call = namedModeCall(fmt.Sprintf("recovery-%d", serial), tool, args)
				requests, result = 0, ""
				if err := live.RunTurn(t.Context(), "offline recovery fixture"); err != nil {
					t.Fatal(err)
				}
				if requests != 2 {
					t.Fatalf("requests = %d", requests)
				}
				return result
			}
			receipt := recoveryReceipt(t, invoke("program", `{"script":"emit(\"x\" * 70000)"}`))
			readArgs := fmt.Sprintf(`{"receipt":%q,"limit":4}`, receipt)
			assertRecoveryPage(t, invoke("program_output_read", readArgs))
			preserved := false
			switch name {
			case "turn and mode":
				live.SetPlanMode(true)
				assertNoInspectionErrors(t, live.Inspect(t.Context()))
				preserved = true
			case "clear":
				live.ClearContext()
			case "legacy restore":
				live.RestoreContext(nil)
			default:
				r, err := live.ReserveRuntime()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Release()
				_, target, err := r.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				invalidState, err := engine.DecodeMCPActivations([]byte(`{"version":1,"tools":[{"connection":"absent","name":"read_remote","definition_hash":"v1:` + strings.Repeat("a", 64) + `"}]}`))
				if err != nil {
					t.Fatal(err)
				}
				switch name {
				case "session restore":
					err = r.RestoreSessionContext(nil, engine.MCPActivations{})
				case "exact restore":
					err = r.RestoreConversation(nil, target, code.Plan{}, live.RunTarget())
				case "exact session restore":
					err = r.RestoreSessionConversation(nil, target, code.Plan{}, live.RunTarget(), engine.MCPActivations{})
				case "failed target":
					target.Model = "wrong"
					err = r.RestoreConversation(nil, target, code.Plan{}, live.RunTarget())
					if !errors.Is(err, rewind.ErrTarget) {
						t.Fatalf("restore: %v", err)
					}
					preserved, err = true, nil
				case "failed session", "failed exact session":
					if name == "failed session" {
						err = r.RestoreSessionContext(nil, invalidState)
					} else {
						err = r.RestoreSessionConversation(nil, target, code.Plan{}, live.RunTarget(), invalidState)
					}
					if !errors.Is(err, engine.ErrMCPActivations) {
						t.Fatalf("restore: %v", err)
					}
					preserved, err = true, nil
				case "rejected close":
					err = live.Close(t.Context())
					if !errors.Is(err, engine.ErrRuntimeBusy) {
						t.Fatalf("close: %v", err)
					}
					preserved, err = true, nil
				}
				if err != nil {
					t.Fatal(err)
				}
				r.Release()
			}
			got := invoke("program_output_read", readArgs)
			if preserved {
				assertRecoveryPage(t, got)
			} else if !strings.Contains(got, "output unavailable") {
				t.Fatalf("old receipt survived replacement: %s", got)
			}
			// A replacement must grant a fresh working capability, not reopen the old one.
			next := recoveryReceipt(t, invoke("program", `{"script":"emit(\"x\" * 70000)"}`))
			if next == receipt {
				t.Fatal("receipt reused")
			}
			assertRecoveryPage(t, invoke("program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, next)))
		})
	}
}

func recoveryReceipt(t *testing.T, message string) string {
	t.Helper()
	_, tail, ok := strings.Cut(message, "receipt=")
	if !ok {
		t.Fatalf("missing receipt: %s", message)
	}
	quoted, _, ok := strings.Cut(tail, ", offset=")
	if !ok {
		t.Fatalf("missing recipe: %s", message)
	}
	receipt, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func assertRecoveryPage(t *testing.T, result string) {
	t.Helper()
	var page program.OutputPage
	if err := json.Unmarshal([]byte(result), &page); err != nil {
		t.Fatalf("page: %s: %v", result, err)
	}
	if page.Content != `"xxx` || page.TotalBytes != 70002 || page.NextOffset != 4 || page.Done {
		t.Fatalf("page: %+v", page)
	}
}
