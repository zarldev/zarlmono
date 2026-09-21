package engine_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestHeadlessThreadsRestoredContext(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	provider := &requestRecordingProvider{}
	live := engine.NewLiveRunner(provider, ws, "local")
	testCtx := t.Context()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(testCtx), time.Second)
		defer cancel()
		if err := live.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "CANARY-restored"}, {Role: llm.RoleAssistant, Content: "prior answer"}})
	result := live.RunHeadless(t.Context(), "next", 1)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	found := false
	for _, m := range provider.requests[0].Messages {
		if m.Content == "CANARY-restored" {
			found = true
		}
	}
	if !found {
		t.Fatal("restored context absent from headless request")
	}
	if !reflect.DeepEqual(live.ContextSnapshot(), result.Messages) {
		t.Fatal("headless transcript not committed")
	}
	result2 := live.RunHeadless(t.Context(), "third", 1)
	if result2.Err != nil {
		t.Fatal(result2.Err)
	}
	found = false
	for _, m := range provider.requests[1].Messages {
		if m.Content == "next" {
			found = true
		}
	}
	if !found {
		t.Fatal("prior headless turn absent from next request")
	}
}
