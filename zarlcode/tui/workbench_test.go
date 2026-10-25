package tui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/agent/computer/browser"
)

func startWorkbench(t *testing.T, f *beforeFixture) *tui.Workbench {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	w := tui.NewWorkbench(ctx, f.ui, f.sink)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		w.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return w
}
func awaitWorkbench(t *testing.T, w *tui.Workbench, predicate func(tui.WorkbenchSnapshot) bool) tui.WorkbenchSnapshot {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var snapshot tui.WorkbenchSnapshot
	for {
		var err error
		snapshot, err = w.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
		if err != nil {
			t.Fatal(err)
		}
		if predicate(snapshot) {
			return snapshot
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("session did not settle: %+v", snapshot)
		}
	}
}
func TestWorkbenchLiveTurns(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	w := startWorkbench(t, f)
	for _, input := range []string{"first task", "follow up"} {
		snap, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: input})
		if err != nil {
			t.Fatal(err)
		}
		if snap.State != "running" {
			t.Fatalf("admission state %q", snap.State)
		}
		snap = awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
		var found bool
		for _, entry := range snap.Entries {
			if entry.Payload.Text == "answer" {
				found = true
			}
		}
		if !found || snap.SessionID == "" || snap.Error != "" {
			t.Fatalf("missing real provider result: %+v", snap)
		}
	}
	for _, command := range []tui.WorkbenchCommand{{Action: "unknown"}, {Action: "submit", Text: " "}, {Action: "mode", Text: "invalid"}, {Action: "submit", Text: strings.Repeat("x", 256*1024+1)}} {
		if _, err := w.Request(t.Context(), command); err == nil {
			t.Fatalf("accepted invalid command %q", command.Action)
		}
	}
	snap, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "mode", Text: "plan"})
	if err != nil || snap.Mode != "plan" {
		t.Fatalf("mode: %+v %v", snap, err)
	}
}
func TestWorkbenchCancelRetainsAdmissionUntilDrained(t *testing.T) {
	f := newBeforeFixture(t)
	p := &interruptedProvider{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	f.live.SetProviderSpec(p, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	w := startWorkbench(t, f)
	// Release must precede Workbench.Close even if an assertion fails.
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(p.release) }) })
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "long task"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "second"}); !errors.Is(err, tui.ErrWorkbenchBusy) {
		t.Fatalf("busy: %v", err)
	}
	snap, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "stop"})
	if err != nil || snap.State != "cancelling" {
		t.Fatalf("cancel: %+v %v", snap, err)
	}
	select {
	case <-p.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not cancelled")
	}
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "too early"}); !errors.Is(err, tui.ErrWorkbenchBusy) {
		t.Fatalf("admitted before drain: %v", err)
	}
	release.Do(func() { close(p.release) })
	awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
}

func TestWorkbenchToolExecutionAndResume(t *testing.T) {
	f := newBeforeFixture(t, engine.WithComputerSessionFactory(func(context.Context, ...browser.Option) (engine.ComputerSession, error) {
		return screenshotSettlementSession{}, nil
	}))
	f.live.SetProviderSpec(&screenshotSettlementProvider{}, engine.ProviderSpec{Name: "openai-codex", Model: "gpt-5.6"})
	w := startWorkbench(t, f)
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "inspect the screen"}); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
	found := false
	for _, entry := range snapshot.Entries {
		if entry.Payload.ToolName == "computer_observe" && entry.Payload.ToolState == "succeeded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool evidence missing: %+v", snapshot)
	}
	saved, err := f.store.GetSessionResumeState(t.Context(), snapshot.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(saved.Session.ContextJSON)
	if err != nil || head.SettledTurnID == "" || head.EventWatermark != saved.Transcript.Revision {
		t.Fatalf("unsaved settlement: %+v %v", head, err)
	}
	cached, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot", Since: snapshot.Revision})
	if err != nil || !cached.Unchanged || len(cached.Entries) != 0 {
		t.Fatalf("snapshot cache: %+v %v", cached, err)
	}
	w.Close()
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	restarted := tui.New()
	restarted.SetLiveRunner(f.live)
	restarted.SetLiveEventSink(f.sink)
	restarted.SetSettings(settings)
	if err := restarted.ResumeSavedSession(t.Context(), snapshot.SessionID); err != nil {
		t.Fatal(err)
	}
	f.ui = restarted
	next := startWorkbench(t, f)
	restored, err := next.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil || len(restored.Entries) != len(snapshot.Entries) {
		t.Fatalf("resume: %+v %v", restored, err)
	}
	if _, err := next.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: "continue"}); err != nil {
		t.Fatal(err)
	}
	awaitWorkbench(t, next, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
}

func TestWorkbenchDraftSurvivesCloseAndResume(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	w := startWorkbench(t, f)
	if _, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "draft", Text: "unsent task"}); err != nil {
		t.Fatal(err)
	}
	// Close before the debounce fires: final flush must still save the draft.
	w.Close()
	records, err := f.store.ListSessions(t.Context(), f.ws.Root())
	if err != nil || len(records) != 1 {
		t.Fatalf("draft session: %v %v", records, err)
	}
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	ui := tui.New()
	ui.SetLiveRunner(f.live)
	ui.SetLiveEventSink(f.sink)
	ui.SetSettings(settings)
	if err := ui.ResumeSavedSession(t.Context(), records[0].ID); err != nil {
		t.Fatal(err)
	}
	f.ui = ui
	next := startWorkbench(t, f)
	snapshot, err := next.Request(t.Context(), tui.WorkbenchCommand{Action: "snapshot"})
	if err != nil || snapshot.Draft != "unsent task" {
		t.Fatalf("restored draft: %+v %v", snapshot, err)
	}
	snapshot, err = next.Request(t.Context(), tui.WorkbenchCommand{Action: "submit", Text: snapshot.Draft})
	if err != nil || snapshot.Draft != "" {
		t.Fatalf("accepted draft not cleared: %+v %v", snapshot, err)
	}
	awaitWorkbench(t, next, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
}

func TestWorkbenchModelSwitchReportsConfiguringUntilApplied(t *testing.T) {
	f := newBeforeFixture(t)
	w := startWorkbench(t, f)
	snapshot, err := w.Request(t.Context(), tui.WorkbenchCommand{Action: "model", Provider: "missing-test-provider", Model: "model"})
	if err != nil || snapshot.State != "configuring" {
		t.Fatalf("missing target barrier: %+v %v", snapshot, err)
	}
	snapshot = awaitWorkbench(t, w, func(s tui.WorkbenchSnapshot) bool { return s.State == "ready" })
	if snapshot.Error == "" {
		t.Fatal("provider construction failure was hidden")
	}
}
