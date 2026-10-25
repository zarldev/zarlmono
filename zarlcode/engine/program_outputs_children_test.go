package engine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProgramOutputRecoverySharedWithChildren(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := newMCPSettings(t)
	setMCPSetting(t, settings, prefs.KeyProgramOutputRecovery, "on")
	rootRequests, childRequests := 0, 0
	rootReceipt := make(chan string, 1)
	childReceipt := make(chan string, 1)
	provider := modeProvider(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		if taskscope.DepthFrom(ctx) > 0 {
			childRequests++
			switch childRequests {
			case 1:
				return modeCalls(namedModeCall("child-read", "program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, <-rootReceipt)))
			case 2:
				assertRecoveryPage(t, modeResult(req.Messages, "child-read"))
				return modeCalls(namedModeCall("child-emit", "program", `{"script":"emit(\"x\" * 70000)"}`))
			default:
				childReceipt <- recoveryReceipt(t, modeResult(req.Messages, "child-emit"))
				return modeCalls()
			}
		}
		rootRequests++
		switch rootRequests {
		case 1:
			return modeCalls(namedModeCall("root-emit", "program", `{"script":"emit(\"x\" * 70000)"}`))
		case 2:
			rootReceipt <- recoveryReceipt(t, modeResult(req.Messages, "root-emit"))
			return modeCalls(namedModeCall("spawn", "agent_spawn", `{"prompt":"recover the fixture","mode":"explore"}`))
		case 3:
			return modeCalls(namedModeCall("await", "agent_await", `{"timeout_seconds":60}`))
		case 4:
			return modeCalls(namedModeCall("root-read", "program_output_read", fmt.Sprintf(`{"receipt":%q,"limit":4}`, <-childReceipt)))
		default:
			assertRecoveryPage(t, modeResult(req.Messages, "root-read"))
			return modeCalls()
		}
	})
	live := modeLive(t, t.TempDir(), provider, engine.WithSettings(settings))
	live.SetLimits(0, 10, 5, 1)
	if err := live.RunTurn(t.Context(), "offline child recovery fixture"); err != nil {
		t.Fatal(err)
	}
	if rootRequests != 5 || childRequests != 3 {
		t.Fatalf("requests: root=%d child=%d", rootRequests, childRequests)
	}
}
