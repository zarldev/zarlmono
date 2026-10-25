package tui_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestWorkbenchRejectedCheckpointExportsWithoutMutatingHead(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetWorkspace(f.ws.Root(), "saved-model")
	provider := nativeSettlementProvider{name: "openai", chunk: llm.CompletionChunk{Content: "visible rejected answer", CompletedItems: []llm.ContinuationItem{{Provider: "openai", Format: "private-canary", Data: []byte(`{"secret":"not-for-export"}`)}}}}
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	settleRewindTurn(t, f, "preserve this question")
	before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithoutCancel(t.Context())
	w := tui.NewWorkbench(ctx, f.ui, f.sink)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { w.Close(); <-done }) // rejected exact state may still fail final flush
	snapshot, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil || snapshot.State != "unsaved" || snapshot.RetrySaveReason == "" {
		t.Fatalf("recovery state: %+v %v", snapshot, err)
	}
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "retry-save"}); err == nil {
		t.Fatal("unavailable retry was reported as successful")
	}
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "draft", Text: "unsent recovery draft"}); err != nil {
		t.Fatal(err)
	}
	awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "unsaved" })
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "export-recovery"}); err != nil {
		t.Fatal(err)
	}
	snapshot = awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.ExportPath != "" || s.RecoveryError != "" })
	if snapshot.RecoveryError != "" {
		t.Fatal(snapshot.RecoveryError)
	}
	content, err := os.ReadFile(snapshot.ExportPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"preserve this question", "visible rejected answer", "unsent recovery draft"} {
		if !strings.Contains(string(content), text) {
			t.Fatalf("export omitted %q", text)
		}
	}
	if strings.Contains(string(content), "not-for-export") || strings.Contains(string(content), "private-canary") {
		t.Fatal("export disclosed native continuation payload")
	}
	after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("recovery changed durable checkpoint: %v", err)
	}
	if snapshot.State != "unsaved" {
		t.Fatalf("export falsely marked checkpoint saved: %s", snapshot.State)
	}
}

func TestWorkbenchTransientSaveCanRetry(t *testing.T) {
	f, _ := failedRecoveryFixture(t)
	removeRecoveryFailure(t, f)
	w := startWorkbench(t, f)
	snapshot, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil || snapshot.State != "unsaved" || snapshot.RetrySaveReason != "" {
		t.Fatalf("retry eligibility: %+v %v", snapshot, err)
	}
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "retry-save"}); err != nil {
		t.Fatal(err)
	}
	awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
}
